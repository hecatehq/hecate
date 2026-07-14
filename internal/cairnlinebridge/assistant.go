package cairnlinebridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func UpsertAssistantProposalRecord(ctx context.Context, service *cairnline.Service, record projectassistant.ProposalRecord) (cairnline.AssistantProposalRecord, bool, error) {
	if service == nil {
		return cairnline.AssistantProposalRecord{}, false, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item, ok := AssistantProposalRecord(record)
	if !ok {
		return cairnline.AssistantProposalRecord{}, false, nil
	}
	imported, err := service.ImportAssistantProposalRecord(ctx, item)
	return imported, true, err
}

func AssistantProposalRecord(record projectassistant.ProposalRecord) (cairnline.AssistantProposalRecord, bool) {
	actions := make([]cairnline.AssistantAction, 0, len(record.Proposal.Actions))
	for _, action := range record.Proposal.Actions {
		item, ok := AssistantAction(action)
		if !ok {
			if strings.TrimSpace(action.Kind) == projectassistant.ActionUpdateHandoff {
				return cairnline.AssistantProposalRecord{}, false
			}
			continue
		}
		// Hecate update_handoff actions are sparse patches, while Cairnline's
		// assistant contract currently applies a full Handoff replacement. A
		// revision-bearing sparse row could fail after earlier proposal actions
		// have committed, so reject the record instead of importing a partially
		// applicable proposal. Zero-token actions remain importable as historical
		// ledger entries; Cairnline rejects them before applying any action.
		if item.Kind == cairnline.AssistantActionUpdateHandoff && item.Handoff != nil && !item.Handoff.UpdatedAt.IsZero() {
			return cairnline.AssistantProposalRecord{}, false
		}
		actions = append(actions, item)
	}
	if len(actions) == 0 {
		return cairnline.AssistantProposalRecord{}, false
	}
	latestResult := AssistantApplyResultPtr(record.LatestResult)
	attempts := make([]cairnline.AssistantApplyAttempt, 0, len(record.ApplyAttempts))
	for _, attempt := range record.ApplyAttempts {
		attempts = append(attempts, AssistantApplyAttempt(attempt))
	}
	return cairnline.AssistantProposalRecord{
		ID:        strings.TrimSpace(record.ID),
		ProjectID: strings.TrimSpace(record.ProjectID),
		Source:    AssistantProposalSource(record.Source),
		SourceID:  strings.TrimSpace(record.SourceID),
		Proposal: cairnline.AssistantProposal{
			ID:                   strings.TrimSpace(record.Proposal.ID),
			ProjectID:            strings.TrimSpace(record.ProjectID),
			Title:                strings.TrimSpace(record.Proposal.Title),
			Summary:              strings.TrimSpace(record.Proposal.Summary),
			Warnings:             compactStrings(record.Proposal.Warnings),
			Source:               AssistantProposalSource(record.Source),
			RequiresConfirmation: record.Proposal.RequiresConfirmation,
			Actions:              actions,
			CreatedAt:            record.CreatedAt,
		},
		Status:        AssistantProposalStatus(record.Status),
		LatestResult:  latestResult,
		ApplyAttempts: attempts,
		CreatedAt:     record.CreatedAt,
		UpdatedAt:     record.UpdatedAt,
		AppliedAt:     record.AppliedAt,
	}, true
}

func ProjectAssistantProposalRecord(record cairnline.AssistantProposalRecord) (projectassistant.ProposalRecord, bool) {
	actions := make([]projectassistant.Action, 0, len(record.Proposal.Actions))
	for _, action := range record.Proposal.Actions {
		// Cairnline update_handoff actions are full replacements. Hecate's
		// Project Assistant contract only has a sparse CAS patch, so narrowing
		// one would make the proposal impossible to write back to the portable
		// ledger after apply. Reject the whole record before any action can run.
		if action.Kind == cairnline.AssistantActionUpdateHandoff {
			return projectassistant.ProposalRecord{}, false
		}
		item, ok := ProjectAssistantAction(action)
		if !ok {
			continue
		}
		actions = append(actions, item)
	}
	if len(actions) == 0 {
		return projectassistant.ProposalRecord{}, false
	}
	latestResult := ProjectAssistantApplyResultPtr(record.LatestResult)
	attempts := make([]projectassistant.ApplyAttempt, 0, len(record.ApplyAttempts))
	for _, attempt := range record.ApplyAttempts {
		attempts = append(attempts, ProjectAssistantApplyAttempt(attempt))
	}
	return projectassistant.ProposalRecord{
		ID:        strings.TrimSpace(record.ID),
		ProjectID: strings.TrimSpace(record.ProjectID),
		Source:    ProjectAssistantProposalSource(record.Source),
		SourceID:  strings.TrimSpace(record.SourceID),
		Proposal: projectassistant.Proposal{
			ID:                   strings.TrimSpace(record.Proposal.ID),
			Title:                strings.TrimSpace(record.Proposal.Title),
			Summary:              strings.TrimSpace(record.Proposal.Summary),
			Actions:              actions,
			Warnings:             compactStrings(record.Proposal.Warnings),
			RequiresConfirmation: record.Proposal.RequiresConfirmation,
		},
		Status:        ProjectAssistantProposalStatus(record.Status),
		LatestResult:  latestResult,
		ApplyAttempts: attempts,
		CreatedAt:     record.CreatedAt,
		UpdatedAt:     record.UpdatedAt,
		AppliedAt:     record.AppliedAt,
	}, true
}

func AssistantAction(action projectassistant.Action) (cairnline.AssistantAction, bool) {
	item := cairnline.AssistantAction{
		Kind:    AssistantActionKind(action.Kind),
		Summary: strings.TrimSpace(action.Reason),
		Target:  AssistantTarget(action.Target),
	}
	if item.Kind == "" {
		return cairnline.AssistantAction{}, false
	}
	switch strings.TrimSpace(action.Kind) {
	case projectassistant.ActionCreateProject:
		patch, ok := decodeAssistantPatch[assistantProjectPatch](action.Patch)
		if !ok {
			return cairnline.AssistantAction{}, false
		}
		item.Project = assistantProject(patch)
	case projectassistant.ActionUpdateProject:
		projectID := firstNonEmpty(targetValue(action.Target, "project_id"), targetValue(action.Target, "id"))
		if projectID == "" {
			return cairnline.AssistantAction{}, false
		}
		patch, ok := decodeAssistantPatch[assistantUpdateProjectPatch](action.Patch)
		if !ok {
			return cairnline.AssistantAction{}, false
		}
		item.Project = &cairnline.Project{
			ID:          projectID,
			Name:        pointerValue(patch.Name),
			Description: pointerValue(patch.Description),
		}
	case projectassistant.ActionAttachProjectRoot:
		patch, ok := decodeAssistantPatch[assistantRootPatch](action.Patch)
		if !ok {
			return cairnline.AssistantAction{}, false
		}
		if item.Target.ProjectID == "" {
			return cairnline.AssistantAction{}, false
		}
		root := assistantRoot(patch)
		item.Root = &root
	case projectassistant.ActionRemoveProjectRoot:
		if item.Target.ProjectID == "" || item.Target.RootID == "" {
			return cairnline.AssistantAction{}, false
		}
	case projectassistant.ActionSetProjectDefaults:
		projectID := firstNonEmpty(targetValue(action.Target, "project_id"), targetValue(action.Target, "id"))
		if projectID == "" {
			return cairnline.AssistantAction{}, false
		}
		patch, ok := decodeAssistantPatch[assistantDefaultsPatch](action.Patch)
		if !ok || patch.DefaultRootID == nil {
			return cairnline.AssistantAction{}, false
		}
		item.Project = &cairnline.Project{
			ID:            projectID,
			DefaultRootID: strings.TrimSpace(*patch.DefaultRootID),
		}
	case projectassistant.ActionCreateRole:
		patch, ok := decodeAssistantPatch[assistantRolePatch](action.Patch)
		if !ok {
			return cairnline.AssistantAction{}, false
		}
		projectID := firstNonEmpty(strings.TrimSpace(patch.ProjectID), targetValue(action.Target, "project_id"))
		item.Role = &cairnline.Role{
			ID:                   strings.TrimSpace(patch.ID),
			ProjectID:            projectID,
			Name:                 strings.TrimSpace(patch.Name),
			Description:          strings.TrimSpace(patch.Description),
			Instructions:         strings.TrimSpace(patch.Instructions),
			DefaultSkillIDs:      compactStrings(patch.SkillIDs),
			DefaultExecutionMode: ExecutionMode(patch.DefaultDriverKind),
		}
	case projectassistant.ActionCreateWorkItem:
		patch, ok := decodeAssistantPatch[assistantWorkItemPatch](action.Patch)
		if !ok {
			return cairnline.AssistantAction{}, false
		}
		projectID := firstNonEmpty(strings.TrimSpace(patch.ProjectID), targetValue(action.Target, "project_id"))
		item.WorkItem = &cairnline.WorkItem{
			ID:              strings.TrimSpace(patch.ID),
			ProjectID:       projectID,
			Title:           strings.TrimSpace(patch.Title),
			Brief:           strings.TrimSpace(patch.Brief),
			Status:          WorkItemStatus(patch.Status),
			Priority:        strings.TrimSpace(patch.Priority),
			OwnerRoleID:     strings.TrimSpace(patch.OwnerRoleID),
			ReviewerRoleIDs: compactStrings(patch.ReviewerRoleIDs),
			RootID:          strings.TrimSpace(patch.RootID),
		}
	case projectassistant.ActionUpdateWorkItem:
		projectID := targetValue(action.Target, "project_id")
		workItemID := targetValue(action.Target, "work_item_id")
		if projectID == "" || workItemID == "" {
			return cairnline.AssistantAction{}, false
		}
		patch, ok := decodeAssistantPatch[assistantUpdateWorkItemPatch](action.Patch)
		if !ok {
			return cairnline.AssistantAction{}, false
		}
		item.WorkItem = &cairnline.WorkItem{
			ID:              workItemID,
			ProjectID:       projectID,
			Title:           pointerValue(patch.Title),
			Brief:           pointerValue(patch.Brief),
			Status:          WorkItemStatus(pointerValue(patch.Status)),
			Priority:        pointerValue(patch.Priority),
			OwnerRoleID:     pointerValue(patch.OwnerRoleID),
			ReviewerRoleIDs: compactStrings(patch.ReviewerRoleIDs),
		}
	case projectassistant.ActionCreateAssignment:
		patch, ok := decodeAssistantPatch[assistantAssignmentPatch](action.Patch)
		if !ok {
			return cairnline.AssistantAction{}, false
		}
		projectID := firstNonEmpty(strings.TrimSpace(patch.ProjectID), targetValue(action.Target, "project_id"))
		item.Assignment = &cairnline.Assignment{
			ID:            strings.TrimSpace(patch.ID),
			ProjectID:     projectID,
			WorkItemID:    strings.TrimSpace(patch.WorkItemID),
			RoleID:        strings.TrimSpace(patch.RoleID),
			RootID:        strings.TrimSpace(patch.RootID),
			ExecutionMode: ExecutionMode(patch.DriverKind),
			Status:        cairnline.AssignmentQueued,
			DesiredAgent: cairnline.DesiredAgent{
				Kind: DesiredAgentKind(patch.DriverKind),
			},
		}
	case projectassistant.ActionCreateHandoff:
		patch, ok := decodeAssistantPatch[assistantHandoffPatch](action.Patch)
		if !ok {
			return cairnline.AssistantAction{}, false
		}
		projectID := firstNonEmpty(strings.TrimSpace(patch.ProjectID), targetValue(action.Target, "project_id"))
		item.Handoff = &cairnline.Handoff{
			ID:                    strings.TrimSpace(patch.ID),
			ProjectID:             projectID,
			WorkItemID:            strings.TrimSpace(patch.WorkItemID),
			SourceAssignmentID:    strings.TrimSpace(patch.SourceAssignmentID),
			SourceRunID:           strings.TrimSpace(patch.SourceRunID),
			SourceChatSessionID:   strings.TrimSpace(patch.SourceChatSessionID),
			SourceMessageID:       strings.TrimSpace(patch.SourceMessageID),
			FromRoleID:            strings.TrimSpace(patch.CreatedByRoleID),
			ToRoleID:              strings.TrimSpace(patch.TargetRoleID),
			TargetAssignmentID:    strings.TrimSpace(patch.TargetAssignmentID),
			TargetWorkItemID:      strings.TrimSpace(patch.TargetWorkItemID),
			Title:                 strings.TrimSpace(patch.Title),
			Body:                  strings.TrimSpace(patch.Summary),
			RecommendedNextAction: strings.TrimSpace(patch.RecommendedNextAction),
			LinkedArtifactIDs:     compactStrings(patch.LinkedArtifactIDs),
			LinkedMemoryIDs:       compactStrings(patch.LinkedMemoryIDs),
			ContextRefs:           compactStrings(patch.ContextRefs),
			Status:                HandoffStatus(patch.Status),
			ProvenanceKind:        strings.TrimSpace(patch.ProvenanceKind),
			TrustLabel:            strings.TrimSpace(patch.TrustLabel),
		}
	case projectassistant.ActionUpdateHandoff:
		projectID := targetValue(action.Target, "project_id")
		workItemID := targetValue(action.Target, "work_item_id")
		handoffID := targetValue(action.Target, "handoff_id")
		if projectID == "" || workItemID == "" || handoffID == "" {
			return cairnline.AssistantAction{}, false
		}
		patch, ok := decodeAssistantPatch[assistantUpdateHandoffPatch](action.Patch)
		if !ok {
			return cairnline.AssistantAction{}, false
		}
		expectedUpdatedAt := time.Time{}
		if patch.ExpectedUpdatedAt != nil {
			expectedUpdatedAt = *patch.ExpectedUpdatedAt
		}
		item.Handoff = &cairnline.Handoff{
			ID:                 handoffID,
			ProjectID:          projectID,
			WorkItemID:         workItemID,
			TargetAssignmentID: pointerValue(patch.TargetAssignmentID),
			ToRoleID:           pointerValue(patch.TargetRoleID),
			Status:             HandoffStatus(pointerValue(patch.Status)),
			UpdatedAt:          expectedUpdatedAt,
		}
	case projectassistant.ActionCreateMemoryCandidate:
		patch, ok := decodeAssistantPatch[assistantMemoryCandidatePatch](action.Patch)
		if !ok {
			return cairnline.AssistantAction{}, false
		}
		projectID := firstNonEmpty(strings.TrimSpace(patch.ProjectID), targetValue(action.Target, "project_id"))
		item.MemoryCandidate = &cairnline.MemoryCandidate{
			ID:                  strings.TrimSpace(patch.ID),
			ProjectID:           projectID,
			Title:               strings.TrimSpace(patch.Title),
			Body:                strings.TrimSpace(patch.Body),
			SuggestedKind:       strings.TrimSpace(patch.SuggestedKind),
			SuggestedTrustLabel: strings.TrimSpace(patch.SuggestedTrustLabel),
			SuggestedSourceKind: strings.TrimSpace(patch.SuggestedSourceKind),
			SuggestedSourceID:   strings.TrimSpace(patch.SuggestedSourceID),
			SourceRefs:          MemoryCandidateSourceRefs(patch.SourceRefs),
			Status:              cairnline.MemoryCandidatePending,
		}
	default:
		return cairnline.AssistantAction{}, false
	}
	return item, true
}

func ProjectAssistantAction(action cairnline.AssistantAction) (projectassistant.Action, bool) {
	item := projectassistant.Action{
		Kind:   ProjectAssistantActionKind(action.Kind),
		Target: ProjectAssistantTarget(action.Target),
		Reason: strings.TrimSpace(firstNonEmpty(action.Summary, action.Title)),
	}
	if item.Kind == "" {
		return projectassistant.Action{}, false
	}
	var patch json.RawMessage
	var ok bool
	switch strings.TrimSpace(action.Kind) {
	case cairnline.AssistantActionCreateProject:
		if action.Project == nil {
			return projectassistant.Action{}, false
		}
		patch, ok = projectAssistantRawPatch(assistantProjectPatch{
			ID:          strings.TrimSpace(action.Project.ID),
			Name:        strings.TrimSpace(action.Project.Name),
			Description: strings.TrimSpace(action.Project.Description),
			Roots:       projectAssistantRootPatches(action.Project.Roots),
		})
	case cairnline.AssistantActionUpdateProject:
		if action.Project == nil {
			return projectassistant.Action{}, false
		}
		patch, ok = projectAssistantRawPatch(assistantUpdateProjectPatch{
			Name:        stringPtrIfNotEmpty(action.Project.Name),
			Description: stringPtrIfNotEmpty(action.Project.Description),
		})
	case cairnline.AssistantActionAttachProjectRoot:
		if action.Root == nil {
			return projectassistant.Action{}, false
		}
		patch, ok = projectAssistantRawPatch(projectAssistantRootPatch(*action.Root))
	case cairnline.AssistantActionRemoveProjectRoot:
		ok = true
	case cairnline.AssistantActionSetProjectDefaults:
		if action.Project == nil {
			return projectassistant.Action{}, false
		}
		patch, ok = projectAssistantRawPatch(assistantDefaultsPatch{
			DefaultRootID: stringPtrIfNotEmpty(action.Project.DefaultRootID),
		})
	case cairnline.AssistantActionCreateRole:
		if action.Role == nil {
			return projectassistant.Action{}, false
		}
		patch, ok = projectAssistantRawPatch(assistantRolePatch{
			ID:                strings.TrimSpace(action.Role.ID),
			ProjectID:         strings.TrimSpace(action.Role.ProjectID),
			Name:              strings.TrimSpace(action.Role.Name),
			Description:       strings.TrimSpace(action.Role.Description),
			Instructions:      strings.TrimSpace(action.Role.Instructions),
			DefaultDriverKind: DriverKind(action.Role.DefaultExecutionMode),
			SkillIDs:          compactStrings(action.Role.DefaultSkillIDs),
		})
	case cairnline.AssistantActionCreateWorkItem:
		if action.WorkItem == nil {
			return projectassistant.Action{}, false
		}
		patch, ok = projectAssistantRawPatch(assistantWorkItemPatch{
			ID:              strings.TrimSpace(action.WorkItem.ID),
			ProjectID:       strings.TrimSpace(action.WorkItem.ProjectID),
			Title:           strings.TrimSpace(action.WorkItem.Title),
			Brief:           strings.TrimSpace(action.WorkItem.Brief),
			Status:          projectWorkItemStatus(action.WorkItem.Status),
			Priority:        strings.TrimSpace(action.WorkItem.Priority),
			OwnerRoleID:     strings.TrimSpace(action.WorkItem.OwnerRoleID),
			ReviewerRoleIDs: compactStrings(action.WorkItem.ReviewerRoleIDs),
			RootID:          strings.TrimSpace(action.WorkItem.RootID),
		})
	case cairnline.AssistantActionUpdateWorkItem:
		if action.WorkItem == nil {
			return projectassistant.Action{}, false
		}
		patch, ok = projectAssistantRawPatch(assistantUpdateWorkItemPatch{
			Title:           stringPtrIfNotEmpty(action.WorkItem.Title),
			Brief:           stringPtrIfNotEmpty(action.WorkItem.Brief),
			Status:          stringPtrIfNotEmpty(projectWorkItemStatus(action.WorkItem.Status)),
			Priority:        stringPtrIfNotEmpty(action.WorkItem.Priority),
			OwnerRoleID:     stringPtrIfNotEmpty(action.WorkItem.OwnerRoleID),
			ReviewerRoleIDs: compactStrings(action.WorkItem.ReviewerRoleIDs),
		})
	case cairnline.AssistantActionCreateAssignment:
		if action.Assignment == nil {
			return projectassistant.Action{}, false
		}
		patch, ok = projectAssistantRawPatch(assistantAssignmentPatch{
			ID:         strings.TrimSpace(action.Assignment.ID),
			ProjectID:  strings.TrimSpace(action.Assignment.ProjectID),
			WorkItemID: strings.TrimSpace(action.Assignment.WorkItemID),
			RoleID:     strings.TrimSpace(action.Assignment.RoleID),
			RootID:     strings.TrimSpace(action.Assignment.RootID),
			DriverKind: DriverKind(action.Assignment.ExecutionMode),
		})
	case cairnline.AssistantActionCreateHandoff:
		if action.Handoff == nil {
			return projectassistant.Action{}, false
		}
		patch, ok = projectAssistantRawPatch(projectAssistantHandoffPatch(*action.Handoff))
	case cairnline.AssistantActionCreateMemoryCandidate:
		if action.MemoryCandidate == nil {
			return projectassistant.Action{}, false
		}
		patch, ok = projectAssistantRawPatch(projectAssistantMemoryCandidatePatch(*action.MemoryCandidate))
	default:
		return projectassistant.Action{}, false
	}
	if !ok {
		return projectassistant.Action{}, false
	}
	item.Patch = patch
	return item, true
}

func AssistantProposalSource(source string) string {
	switch strings.TrimSpace(source) {
	case projectassistant.ProposalSourceAPI:
		return cairnline.AssistantProposalSourceAPI
	default:
		return cairnline.AssistantProposalSourceAssistant
	}
}

func ProjectAssistantProposalSource(source string) string {
	switch strings.TrimSpace(source) {
	case cairnline.AssistantProposalSourceAPI:
		return projectassistant.ProposalSourceAPI
	default:
		return projectassistant.ProposalSourceDraft
	}
}

func AssistantProposalStatus(status string) string {
	switch strings.TrimSpace(status) {
	case projectassistant.ProposalStatusProposed, projectassistant.ProposalStatusApplying:
		return strings.TrimSpace(status)
	case projectassistant.ApplyStatusApplied:
		return cairnline.AssistantProposalStatusApplied
	case projectassistant.ApplyStatusPartialDueToRuntimeFailure:
		return cairnline.AssistantProposalStatusPartial
	case projectassistant.ApplyStatusBlockedBeforeApply:
		return cairnline.AssistantProposalStatusRejected
	default:
		return cairnline.AssistantProposalStatusProposed
	}
}

func ProjectAssistantProposalStatus(status string) string {
	switch strings.TrimSpace(status) {
	case cairnline.AssistantProposalStatusApplied:
		return projectassistant.ApplyStatusApplied
	case cairnline.AssistantProposalStatusPartial:
		return projectassistant.ApplyStatusPartialDueToRuntimeFailure
	case cairnline.AssistantProposalStatusRejected:
		return projectassistant.ApplyStatusBlockedBeforeApply
	case cairnline.AssistantProposalStatusNeedsConfirm:
		return projectassistant.ProposalStatusProposed
	default:
		return projectassistant.ProposalStatusProposed
	}
}

func AssistantApplyStatus(status string) string {
	switch strings.TrimSpace(status) {
	case projectassistant.ApplyStatusApplied:
		return cairnline.AssistantApplyStatusApplied
	case projectassistant.ApplyStatusPartialDueToRuntimeFailure:
		return cairnline.AssistantApplyStatusPartial
	case projectassistant.ApplyStatusBlockedBeforeApply:
		return cairnline.AssistantApplyStatusRejected
	default:
		return firstNonEmpty(strings.TrimSpace(status), cairnline.AssistantApplyStatusRejected)
	}
}

func ProjectAssistantApplyStatus(status string) string {
	switch strings.TrimSpace(status) {
	case cairnline.AssistantApplyStatusApplied:
		return projectassistant.ApplyStatusApplied
	case cairnline.AssistantApplyStatusPartial:
		return projectassistant.ApplyStatusPartialDueToRuntimeFailure
	case cairnline.AssistantApplyStatusRejected, cairnline.AssistantApplyStatusNeedsConfirm:
		return projectassistant.ApplyStatusBlockedBeforeApply
	default:
		return projectassistant.ApplyStatusBlockedBeforeApply
	}
}

func AssistantActionKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case projectassistant.ActionCreateProject:
		return cairnline.AssistantActionCreateProject
	case projectassistant.ActionUpdateProject:
		return cairnline.AssistantActionUpdateProject
	case projectassistant.ActionAttachProjectRoot:
		return cairnline.AssistantActionAttachProjectRoot
	case projectassistant.ActionRemoveProjectRoot:
		return cairnline.AssistantActionRemoveProjectRoot
	case projectassistant.ActionSetProjectDefaults:
		return cairnline.AssistantActionSetProjectDefaults
	case projectassistant.ActionCreateRole:
		return cairnline.AssistantActionCreateRole
	case projectassistant.ActionCreateWorkItem:
		return cairnline.AssistantActionCreateWorkItem
	case projectassistant.ActionUpdateWorkItem:
		return cairnline.AssistantActionUpdateWorkItem
	case projectassistant.ActionCreateAssignment:
		return cairnline.AssistantActionCreateAssignment
	case projectassistant.ActionCreateHandoff:
		return cairnline.AssistantActionCreateHandoff
	case projectassistant.ActionUpdateHandoff:
		return cairnline.AssistantActionUpdateHandoff
	case projectassistant.ActionCreateMemoryCandidate:
		return cairnline.AssistantActionCreateMemoryCandidate
	default:
		return ""
	}
}

func ProjectAssistantActionKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case cairnline.AssistantActionCreateProject:
		return projectassistant.ActionCreateProject
	case cairnline.AssistantActionUpdateProject:
		return projectassistant.ActionUpdateProject
	case cairnline.AssistantActionAttachProjectRoot:
		return projectassistant.ActionAttachProjectRoot
	case cairnline.AssistantActionRemoveProjectRoot:
		return projectassistant.ActionRemoveProjectRoot
	case cairnline.AssistantActionSetProjectDefaults:
		return projectassistant.ActionSetProjectDefaults
	case cairnline.AssistantActionCreateRole:
		return projectassistant.ActionCreateRole
	case cairnline.AssistantActionCreateWorkItem:
		return projectassistant.ActionCreateWorkItem
	case cairnline.AssistantActionUpdateWorkItem:
		return projectassistant.ActionUpdateWorkItem
	case cairnline.AssistantActionCreateAssignment:
		return projectassistant.ActionCreateAssignment
	case cairnline.AssistantActionCreateHandoff:
		return projectassistant.ActionCreateHandoff
	case cairnline.AssistantActionUpdateHandoff:
		return projectassistant.ActionUpdateHandoff
	case cairnline.AssistantActionCreateMemoryCandidate:
		return projectassistant.ActionCreateMemoryCandidate
	default:
		return ""
	}
}

func AssistantApplyResultPtr(result *projectassistant.ApplyResult) *cairnline.AssistantApplyResult {
	if result == nil {
		return nil
	}
	out := AssistantApplyResult(*result)
	return &out
}

func AssistantApplyResult(result projectassistant.ApplyResult) cairnline.AssistantApplyResult {
	actions := make([]cairnline.AssistantActionResult, 0, len(result.Actions))
	for _, action := range result.Actions {
		actions = append(actions, AssistantActionResult(action))
	}
	return cairnline.AssistantApplyResult{
		ProposalID:         strings.TrimSpace(result.ProposalID),
		Status:             AssistantApplyStatus(result.Status),
		Applied:            result.Applied,
		TotalActionCount:   result.TotalActionCount,
		AppliedActionCount: result.CommittedActionCount,
		FailedActionIndex:  result.FailedActionIndex,
		Actions:            actions,
	}
}

func ProjectAssistantApplyResultPtr(result *cairnline.AssistantApplyResult) *projectassistant.ApplyResult {
	if result == nil {
		return nil
	}
	out := ProjectAssistantApplyResult(*result)
	return &out
}

func ProjectAssistantApplyResult(result cairnline.AssistantApplyResult) projectassistant.ApplyResult {
	actions := make([]projectassistant.ActionResult, 0, len(result.Actions))
	for _, action := range result.Actions {
		actions = append(actions, ProjectAssistantActionResult(action))
	}
	return projectassistant.ApplyResult{
		ProposalID:           strings.TrimSpace(result.ProposalID),
		Status:               ProjectAssistantApplyStatus(result.Status),
		Applied:              result.Applied,
		Actions:              actions,
		TotalActionCount:     result.TotalActionCount,
		CommittedActionCount: result.AppliedActionCount,
		FailedActionIndex:    result.FailedActionIndex,
		ResumeActionIndex:    result.AppliedActionCount,
	}
}

func AssistantApplyAttempt(attempt projectassistant.ApplyAttempt) cairnline.AssistantApplyAttempt {
	result := AssistantApplyResult(attempt.Result)
	result.Confirmed = attempt.Confirmed
	return cairnline.AssistantApplyAttempt{
		ID:           strings.TrimSpace(attempt.ID),
		ProposalID:   strings.TrimSpace(attempt.ProposalID),
		Status:       AssistantApplyStatus(attempt.Status),
		Confirmed:    attempt.Confirmed,
		Result:       result,
		ErrorMessage: strings.TrimSpace(firstNonEmpty(attempt.ErrorMessage, attempt.ErrorType)),
		CreatedAt:    attempt.CreatedAt,
	}
}

func ProjectAssistantApplyAttempt(attempt cairnline.AssistantApplyAttempt) projectassistant.ApplyAttempt {
	return projectassistant.ApplyAttempt{
		ID:           strings.TrimSpace(attempt.ID),
		ProposalID:   strings.TrimSpace(attempt.ProposalID),
		Status:       ProjectAssistantApplyStatus(attempt.Status),
		Confirmed:    attempt.Confirmed,
		Result:       ProjectAssistantApplyResult(attempt.Result),
		ErrorMessage: strings.TrimSpace(attempt.ErrorMessage),
		CreatedAt:    attempt.CreatedAt,
	}
}

func AssistantActionResult(result projectassistant.ActionResult) cairnline.AssistantActionResult {
	return cairnline.AssistantActionResult{
		Kind:              AssistantActionKind(result.Kind),
		Status:            cairnline.AssistantApplyStatusApplied,
		ProjectID:         result.Data["project_id"],
		RootID:            result.Data["root_id"],
		RoleID:            result.Data["role_id"],
		WorkItemID:        result.Data["work_item_id"],
		AssignmentID:      result.Data["assignment_id"],
		ArtifactID:        result.Data["artifact_id"],
		HandoffID:         result.Data["handoff_id"],
		MemoryCandidateID: firstNonEmpty(result.Data["memory_candidate_id"], result.Data["candidate_id"]),
	}
}

func ProjectAssistantActionResult(result cairnline.AssistantActionResult) projectassistant.ActionResult {
	data := map[string]string{}
	add := func(key, value string) {
		if value = strings.TrimSpace(value); value != "" {
			data[key] = value
		}
	}
	add("project_id", result.ProjectID)
	add("root_id", result.RootID)
	add("role_id", result.RoleID)
	add("work_item_id", result.WorkItemID)
	add("assignment_id", result.AssignmentID)
	add("artifact_id", result.ArtifactID)
	add("handoff_id", result.HandoffID)
	add("memory_candidate_id", result.MemoryCandidateID)
	return projectassistant.ActionResult{
		Kind: ProjectAssistantActionKind(result.Kind),
		ID: firstNonEmpty(
			result.ProjectID,
			result.RootID,
			result.RoleID,
			result.WorkItemID,
			result.AssignmentID,
			result.ArtifactID,
			result.HandoffID,
			result.MemoryCandidateID,
		),
		Data: data,
	}
}

func AssistantTarget(target map[string]string) cairnline.AssistantTarget {
	return cairnline.AssistantTarget{
		ProjectID:    targetValue(target, "project_id"),
		RootID:       targetValue(target, "root_id"),
		RoleID:       targetValue(target, "role_id"),
		WorkItemID:   targetValue(target, "work_item_id"),
		AssignmentID: targetValue(target, "assignment_id"),
		ArtifactID:   targetValue(target, "artifact_id"),
		HandoffID:    targetValue(target, "handoff_id"),
	}
}

func ProjectAssistantTarget(target cairnline.AssistantTarget) map[string]string {
	out := map[string]string{}
	add := func(key, value string) {
		if value = strings.TrimSpace(value); value != "" {
			out[key] = value
		}
	}
	add("project_id", target.ProjectID)
	add("root_id", target.RootID)
	add("role_id", target.RoleID)
	add("work_item_id", target.WorkItemID)
	add("assignment_id", target.AssignmentID)
	add("artifact_id", target.ArtifactID)
	add("handoff_id", target.HandoffID)
	if len(out) == 0 {
		return nil
	}
	return out
}

func WorkItemStatus(status string) string {
	switch strings.TrimSpace(status) {
	case projectwork.WorkItemStatusDone:
		return cairnline.WorkStatusDone
	case "":
		return cairnline.WorkStatusReady
	default:
		return strings.TrimSpace(status)
	}
}

func DriverKind(mode string) string {
	switch strings.TrimSpace(mode) {
	case cairnline.ExecutionOrchestrated:
		return projectwork.AssignmentDriverHecateTask
	case cairnline.ExecutionExternalAdapter:
		return projectwork.AssignmentDriverExternalAgent
	case cairnline.ExecutionManual:
		return projectwork.AssignmentDriverManual
	default:
		return ""
	}
}

func projectWorkItemStatus(status string) string {
	switch strings.TrimSpace(status) {
	case cairnline.WorkStatusDone:
		return projectwork.WorkItemStatusDone
	case cairnline.WorkStatusReady, "":
		return projectwork.WorkItemStatusReady
	default:
		return strings.TrimSpace(status)
	}
}

func projectHandoffStatus(status string) string {
	switch strings.TrimSpace(status) {
	case cairnline.HandoffStatusAccepted:
		return projectwork.HandoffStatusAccepted
	case cairnline.HandoffStatusSuperseded:
		return projectwork.HandoffStatusSuperseded
	case cairnline.HandoffStatusDismissed:
		return projectwork.HandoffStatusDismissed
	default:
		return projectwork.HandoffStatusPending
	}
}

func assistantProject(patch assistantProjectPatch) *cairnline.Project {
	roots := assistantRoots(patch.Roots)
	if workspacePath := strings.TrimSpace(patch.WorkspacePath); workspacePath != "" && len(roots) == 0 {
		roots = []cairnline.Root{{
			Path:   workspacePath,
			Kind:   strings.TrimSpace(patch.WorkspaceKind),
			Active: true,
		}}
	}
	return &cairnline.Project{
		ID:          strings.TrimSpace(patch.ID),
		Name:        strings.TrimSpace(patch.Name),
		Description: strings.TrimSpace(patch.Description),
		Roots:       roots,
	}
}

func assistantRoots(items []assistantRootPatch) []cairnline.Root {
	roots := make([]cairnline.Root, 0, len(items))
	for _, item := range items {
		roots = append(roots, assistantRoot(item))
	}
	return roots
}

func assistantRoot(item assistantRootPatch) cairnline.Root {
	active := true
	if item.Active != nil {
		active = *item.Active
	}
	return cairnline.Root{
		ID:        strings.TrimSpace(item.ID),
		Path:      strings.TrimSpace(item.Path),
		Kind:      strings.TrimSpace(item.Kind),
		GitRemote: strings.TrimSpace(item.GitRemote),
		GitBranch: strings.TrimSpace(item.GitBranch),
		Active:    active,
	}
}

func projectAssistantRootPatches(items []cairnline.Root) []assistantRootPatch {
	roots := make([]assistantRootPatch, 0, len(items))
	for _, item := range items {
		roots = append(roots, projectAssistantRootPatch(item))
	}
	return roots
}

func projectAssistantRootPatch(item cairnline.Root) assistantRootPatch {
	return assistantRootPatch{
		ID:        strings.TrimSpace(item.ID),
		Path:      strings.TrimSpace(item.Path),
		Kind:      strings.TrimSpace(item.Kind),
		GitRemote: strings.TrimSpace(item.GitRemote),
		GitBranch: strings.TrimSpace(item.GitBranch),
		Active:    boolPtr(item.Active),
	}
}

func projectAssistantHandoffPatch(item cairnline.Handoff) assistantHandoffPatch {
	return assistantHandoffPatch{
		ID:                    strings.TrimSpace(item.ID),
		ProjectID:             strings.TrimSpace(item.ProjectID),
		WorkItemID:            strings.TrimSpace(item.WorkItemID),
		SourceAssignmentID:    strings.TrimSpace(item.SourceAssignmentID),
		SourceRunID:           strings.TrimSpace(item.SourceRunID),
		SourceChatSessionID:   strings.TrimSpace(item.SourceChatSessionID),
		SourceMessageID:       strings.TrimSpace(item.SourceMessageID),
		TargetRoleID:          strings.TrimSpace(item.ToRoleID),
		TargetAssignmentID:    strings.TrimSpace(item.TargetAssignmentID),
		TargetWorkItemID:      strings.TrimSpace(item.TargetWorkItemID),
		Title:                 strings.TrimSpace(item.Title),
		Summary:               strings.TrimSpace(item.Body),
		RecommendedNextAction: strings.TrimSpace(item.RecommendedNextAction),
		LinkedArtifactIDs:     compactStrings(item.LinkedArtifactIDs),
		LinkedMemoryIDs:       compactStrings(item.LinkedMemoryIDs),
		ContextRefs:           compactStrings(item.ContextRefs),
		Status:                projectHandoffStatus(item.Status),
		ProvenanceKind:        strings.TrimSpace(item.ProvenanceKind),
		TrustLabel:            strings.TrimSpace(item.TrustLabel),
		CreatedByRoleID:       strings.TrimSpace(item.FromRoleID),
	}
}

func projectAssistantMemoryCandidatePatch(item cairnline.MemoryCandidate) assistantMemoryCandidatePatch {
	return assistantMemoryCandidatePatch{
		ID:                  strings.TrimSpace(item.ID),
		ProjectID:           strings.TrimSpace(item.ProjectID),
		Title:               strings.TrimSpace(item.Title),
		Body:                strings.TrimSpace(item.Body),
		SuggestedKind:       strings.TrimSpace(item.SuggestedKind),
		SuggestedTrustLabel: strings.TrimSpace(item.SuggestedTrustLabel),
		SuggestedSourceKind: strings.TrimSpace(item.SuggestedSourceKind),
		SuggestedSourceID:   strings.TrimSpace(item.SuggestedSourceID),
		SourceRefs:          projectAssistantMemoryCandidateSourceRefs(item.SourceRefs),
	}
}

func projectAssistantMemoryCandidateSourceRefs(items []cairnline.MemoryCandidateSourceRef) []memory.CandidateSourceRef {
	refs := make([]memory.CandidateSourceRef, 0, len(items))
	for _, item := range items {
		refs = append(refs, memory.CandidateSourceRef{
			Kind:  strings.TrimSpace(item.Kind),
			ID:    strings.TrimSpace(item.ID),
			Title: strings.TrimSpace(item.Title),
			URL:   strings.TrimSpace(item.URL),
		})
	}
	return refs
}

func decodeAssistantPatch[T any](payload json.RawMessage) (T, bool) {
	var out T
	if len(payload) == 0 {
		return out, true
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return out, false
	}
	return out, true
}

func targetValue(target map[string]string, key string) string {
	if len(target) == 0 {
		return ""
	}
	return strings.TrimSpace(target[key])
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func projectAssistantRawPatch(value any) (json.RawMessage, bool) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	return payload, true
}

func stringPtrIfNotEmpty(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

type assistantProjectPatch struct {
	ID            string               `json:"id,omitempty"`
	Name          string               `json:"name,omitempty"`
	Description   string               `json:"description,omitempty"`
	WorkspacePath string               `json:"workspace_path,omitempty"`
	WorkspaceKind string               `json:"workspace_kind,omitempty"`
	Roots         []assistantRootPatch `json:"roots,omitempty"`
}

type assistantUpdateProjectPatch struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type assistantRootPatch struct {
	ID        string `json:"id,omitempty"`
	Path      string `json:"path,omitempty"`
	Kind      string `json:"kind,omitempty"`
	GitRemote string `json:"git_remote,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	Active    *bool  `json:"active,omitempty"`
}

type assistantDefaultsPatch struct {
	DefaultRootID *string `json:"default_root_id,omitempty"`
}

type assistantRolePatch struct {
	ID                  string   `json:"id,omitempty"`
	ProjectID           string   `json:"project_id,omitempty"`
	Name                string   `json:"name,omitempty"`
	Description         string   `json:"description,omitempty"`
	Instructions        string   `json:"instructions,omitempty"`
	DefaultDriverKind   string   `json:"default_driver_kind,omitempty"`
	DefaultAgentProfile string   `json:"default_agent_profile,omitempty"`
	SkillIDs            []string `json:"skill_ids,omitempty"`
}

type assistantWorkItemPatch struct {
	ID              string   `json:"id,omitempty"`
	ProjectID       string   `json:"project_id,omitempty"`
	Title           string   `json:"title,omitempty"`
	Brief           string   `json:"brief,omitempty"`
	Status          string   `json:"status,omitempty"`
	Priority        string   `json:"priority,omitempty"`
	OwnerRoleID     string   `json:"owner_role_id,omitempty"`
	ReviewerRoleIDs []string `json:"reviewer_role_ids,omitempty"`
	RootID          string   `json:"root_id,omitempty"`
}

type assistantUpdateWorkItemPatch struct {
	Title           *string  `json:"title,omitempty"`
	Brief           *string  `json:"brief,omitempty"`
	Status          *string  `json:"status,omitempty"`
	Priority        *string  `json:"priority,omitempty"`
	OwnerRoleID     *string  `json:"owner_role_id,omitempty"`
	ReviewerRoleIDs []string `json:"reviewer_role_ids,omitempty"`
}

type assistantAssignmentPatch struct {
	ID         string `json:"id,omitempty"`
	ProjectID  string `json:"project_id,omitempty"`
	WorkItemID string `json:"work_item_id,omitempty"`
	RoleID     string `json:"role_id,omitempty"`
	RootID     string `json:"root_id,omitempty"`
	DriverKind string `json:"driver_kind,omitempty"`
}

type assistantHandoffPatch struct {
	ID                    string   `json:"id,omitempty"`
	ProjectID             string   `json:"project_id,omitempty"`
	WorkItemID            string   `json:"work_item_id,omitempty"`
	SourceAssignmentID    string   `json:"source_assignment_id,omitempty"`
	SourceRunID           string   `json:"source_run_id,omitempty"`
	SourceChatSessionID   string   `json:"source_chat_session_id,omitempty"`
	SourceMessageID       string   `json:"source_message_id,omitempty"`
	TargetRoleID          string   `json:"target_role_id,omitempty"`
	TargetAssignmentID    string   `json:"target_assignment_id,omitempty"`
	TargetWorkItemID      string   `json:"target_work_item_id,omitempty"`
	Title                 string   `json:"title,omitempty"`
	Summary               string   `json:"summary,omitempty"`
	RecommendedNextAction string   `json:"recommended_next_action,omitempty"`
	LinkedArtifactIDs     []string `json:"linked_artifact_ids,omitempty"`
	LinkedMemoryIDs       []string `json:"linked_memory_ids,omitempty"`
	ContextRefs           []string `json:"context_refs,omitempty"`
	Status                string   `json:"status,omitempty"`
	ProvenanceKind        string   `json:"provenance_kind,omitempty"`
	TrustLabel            string   `json:"trust_label,omitempty"`
	CreatedByRoleID       string   `json:"created_by_role_id,omitempty"`
}

type assistantUpdateHandoffPatch struct {
	TargetAssignmentID *string    `json:"target_assignment_id,omitempty"`
	TargetRoleID       *string    `json:"target_role_id,omitempty"`
	Status             *string    `json:"status,omitempty"`
	ExpectedUpdatedAt  *time.Time `json:"expected_updated_at,omitempty"`
}

type assistantMemoryCandidatePatch struct {
	ID                  string                      `json:"id,omitempty"`
	ProjectID           string                      `json:"project_id,omitempty"`
	Title               string                      `json:"title,omitempty"`
	Body                string                      `json:"body,omitempty"`
	SuggestedKind       string                      `json:"suggested_kind,omitempty"`
	SuggestedTrustLabel string                      `json:"suggested_trust_label,omitempty"`
	SuggestedSourceKind string                      `json:"suggested_source_kind,omitempty"`
	SuggestedSourceID   string                      `json:"suggested_source_id,omitempty"`
	SourceRefs          []memory.CandidateSourceRef `json:"source_refs,omitempty"`
}
