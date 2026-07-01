package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

const cairnlineAssignmentContextReason = "Inspectable Cairnline read-model launch packet; Hecate has not created a task/chat execution snapshot for this assignment"
const cairnlineAssignmentLaunchEvidenceReason = "Portable Cairnline launch packet evidence for replacement-readiness review"

func (h *Handler) contextPacketForCairnlineProjectAssignment(ctx context.Context, assignment projectwork.Assignment) (chat.ContextPacket, bool, error) {
	if h == nil || !h.projectReadRoutesUseCairnlineReadModel() {
		return chat.ContextPacket{}, false, nil
	}
	launch, err := h.cairnlineAssignmentLaunchPacket(ctx, assignment)
	if errors.Is(err, cairnline.ErrNotFound) {
		return chat.ContextPacket{}, false, nil
	}
	if err != nil {
		return chat.ContextPacket{}, false, err
	}
	return cairnlineAssignmentLaunchContextPacket(launch), true, nil
}

func (h *Handler) cairnlineAssignmentLaunchPacket(ctx context.Context, assignment projectwork.Assignment) (cairnline.AssignmentLaunchPacket, error) {
	view, err := h.cairnlineProjectWorkView(ctx, assignment.ProjectID)
	if err != nil {
		return cairnline.AssignmentLaunchPacket{}, err
	}
	defer view.Close()
	return view.service.AssignmentLaunchPacket(ctx, view.snapshot.Project.ID, assignment.ID)
}

func (h *Handler) contextPacketForStrictEmbeddedCairnlineProjectAssignment(ctx context.Context, projectID, workItemID, assignmentID string) (chat.ContextPacket, bool, error) {
	projectID = strings.TrimSpace(projectID)
	workItemID = strings.TrimSpace(workItemID)
	assignmentID = strings.TrimSpace(assignmentID)
	_, service, store, err := h.openCairnlineEmbeddedService(ctx)
	if err != nil {
		return chat.ContextPacket{}, false, err
	}
	defer store.Close()
	context, err := service.AssignmentContext(ctx, projectID, assignmentID)
	if errors.Is(err, cairnline.ErrNotFound) {
		return chat.ContextPacket{}, false, nil
	}
	if err != nil {
		return chat.ContextPacket{}, false, err
	}
	if cairnlineSidecarAssignmentContextRouteMismatch(context, projectID, workItemID, assignmentID) {
		return chat.ContextPacket{}, false, nil
	}
	return cairnlineAssignmentContextPacket(context), true, nil
}

func (h *Handler) contextPacketForCairnlineSidecarProjectAssignment(ctx context.Context, projectID, workItemID, assignmentID string) (chat.ContextPacket, bool, error) {
	projectID = strings.TrimSpace(projectID)
	workItemID = strings.TrimSpace(workItemID)
	assignmentID = strings.TrimSpace(assignmentID)
	projectItem, ok, err := h.cairnlineSidecarProject(ctx, projectID)
	if err != nil {
		return chat.ContextPacket{}, false, err
	}
	if !ok || strings.TrimSpace(projectItem.ID) != projectID {
		return chat.ContextPacket{}, false, nil
	}
	context, ok, err := h.cairnlineSidecarAssignmentContext(ctx, projectID, assignmentID)
	if err != nil {
		return chat.ContextPacket{}, false, err
	}
	if !ok {
		return chat.ContextPacket{}, false, nil
	}
	if cairnlineSidecarAssignmentContextRouteMismatch(context, projectID, workItemID, assignmentID) {
		return chat.ContextPacket{}, false, nil
	}
	return cairnlineAssignmentContextPacket(context), true, nil
}

func cairnlineSidecarAssignmentContextRouteMismatch(context cairnline.AssignmentContext, projectID, workItemID, assignmentID string) bool {
	projectID = strings.TrimSpace(projectID)
	workItemID = strings.TrimSpace(workItemID)
	assignmentID = strings.TrimSpace(assignmentID)
	if strings.TrimSpace(context.Project.ID) != projectID {
		return true
	}
	if strings.TrimSpace(context.WorkItem.ID) != workItemID {
		return true
	}
	if strings.TrimSpace(context.WorkItem.ProjectID) != projectID {
		return true
	}
	if strings.TrimSpace(context.Assignment.ID) != assignmentID {
		return true
	}
	if strings.TrimSpace(context.Assignment.ProjectID) != projectID {
		return true
	}
	if strings.TrimSpace(context.Assignment.WorkItemID) != workItemID {
		return true
	}
	if context.Role != nil {
		if roleID := strings.TrimSpace(context.Role.ID); strings.TrimSpace(context.Assignment.RoleID) != "" && roleID != strings.TrimSpace(context.Assignment.RoleID) {
			return true
		}
		if roleProjectID := strings.TrimSpace(context.Role.ProjectID); roleProjectID != "" && roleProjectID != projectID {
			return true
		}
	}
	return false
}

func cairnlineAssignmentContextPacket(context cairnline.AssignmentContext) chat.ContextPacket {
	root, rootOK, rootSelection := selectedCairnlineRoot(context.Project, context.WorkItem, context.Assignment)
	workspace := ""
	if rootOK {
		workspace = strings.TrimSpace(root.Path)
	}
	packet := baseChatContextPacket(firstNonEmptyString(strings.TrimSpace(context.Assignment.ExecutionMode), cairnline.ExecutionMCPPull), "", "", workspace)
	packet.ID = firstNonEmptyString(strings.TrimSpace(context.Assignment.ContextSnapshotID), strings.TrimSpace(context.ID), "cairnline_assignment_context_"+strings.TrimSpace(context.Assignment.ID))
	packet.ExecutionProfile = strings.TrimSpace(context.Assignment.ExecutionProfileID)
	packet.SystemPromptIncluded = false
	packet.Refs = &chat.ContextRefs{
		ProjectID:    strings.TrimSpace(context.Project.ID),
		WorkItemID:   strings.TrimSpace(context.WorkItem.ID),
		AssignmentID: strings.TrimSpace(context.Assignment.ID),
		RoleID:       strings.TrimSpace(context.Assignment.RoleID),
	}

	appendCairnlineProjectSummary(&packet, context.Project)
	appendCairnlineWorkItem(&packet, context.WorkItem)
	appendCairnlineAssignment(&packet, context.Assignment)
	if rootOK {
		appendCairnlineRoot(&packet, root, rootSelection)
	}
	if context.Role != nil {
		appendCairnlineRole(&packet, *context.Role)
	}
	appendCairnlineAssignmentContextRuntime(&packet, context)
	return packet
}

func cairnlineAssignmentLaunchContextPacket(launch cairnline.AssignmentLaunchPacket) chat.ContextPacket {
	root, rootOK, rootSelection := selectedCairnlineRoot(launch.Project, launch.WorkItem, launch.Assignment)
	workspace := ""
	if rootOK {
		workspace = strings.TrimSpace(root.Path)
	}
	provider, model, executionProfile := "", "", strings.TrimSpace(launch.Assignment.ExecutionProfileID)
	if launch.ExecutionProfile != nil {
		provider = strings.TrimSpace(launch.ExecutionProfile.ProviderHint)
		model = strings.TrimSpace(launch.ExecutionProfile.ModelHint)
		executionProfile = firstNonEmptyString(strings.TrimSpace(launch.ExecutionProfile.ID), executionProfile)
	}
	packet := baseChatContextPacket(firstNonEmptyString(strings.TrimSpace(launch.Assignment.ExecutionMode), cairnline.ExecutionMCPPull), provider, model, workspace)
	packet.ID = firstNonEmptyString(strings.TrimSpace(launch.Assignment.ContextSnapshotID), "cairnline_assignment_context_"+strings.TrimSpace(launch.Assignment.ID))
	packet.ExecutionProfile = executionProfile
	packet.SystemPromptIncluded = false
	packet.Refs = &chat.ContextRefs{
		ProjectID:    strings.TrimSpace(launch.Project.ID),
		WorkItemID:   strings.TrimSpace(launch.WorkItem.ID),
		AssignmentID: strings.TrimSpace(launch.Assignment.ID),
		RoleID:       strings.TrimSpace(launch.Assignment.RoleID),
	}

	appendCairnlineProjectSummary(&packet, launch.Project)
	appendCairnlineWorkItem(&packet, launch.WorkItem)
	appendCairnlineAssignment(&packet, launch.Assignment)
	if rootOK {
		appendCairnlineRoot(&packet, root, rootSelection)
	}
	if launch.Role != nil {
		appendCairnlineRole(&packet, *launch.Role)
	}
	if launch.Profile != nil {
		appendCairnlineAgentProfile(&packet, *launch.Profile)
	}
	if launch.ExecutionProfile != nil {
		appendCairnlineExecutionProfile(&packet, *launch.ExecutionProfile)
	}
	appendCairnlineProjectSkills(&packet, launch)
	appendCairnlineMemory(&packet, launch.Memory)
	appendCairnlineMemoryCandidates(&packet, launch.MemoryCandidates)
	appendCairnlineProjectSources(&packet, launch.Project.ContextSources)
	appendCairnlineAssignmentArtifacts(&packet, launch)
	appendCairnlineAssignmentHandoffs(&packet, launch)
	appendCairnlineLaunchRuntime(&packet, launch)
	return packet
}

func (h *Handler) appendCairnlineAssignmentLaunchPacketEvidence(ctx context.Context, packet *chat.ContextPacket, assignment projectwork.Assignment) {
	if h == nil || packet == nil || !h.projectReadRoutesUseCairnlineReadModel() {
		return
	}
	launch, err := h.cairnlineAssignmentLaunchPacket(ctx, assignment)
	if err != nil {
		appendCairnlineAssignmentLaunchPacketEvidenceError(packet, assignment, err)
		return
	}
	appendCairnlineAssignmentLaunchPacketEvidenceItem(packet, launch)
}

func appendCairnlineAssignmentLaunchPacketEvidenceItem(packet *chat.ContextPacket, launch cairnline.AssignmentLaunchPacket) {
	root, rootOK, rootSelection := selectedCairnlineRoot(launch.Project, launch.WorkItem, launch.Assignment)
	rootID, rootPath := "", ""
	rootDetail := "none"
	if rootOK {
		rootID = strings.TrimSpace(root.ID)
		rootPath = strings.TrimSpace(root.Path)
		rootDetail = firstNonEmptyString(rootID, "unresolved") + " (" + firstNonEmptyString(rootPath, "no path") + "; " + firstNonEmptyString(rootSelection, "fallback") + ")"
	}
	profileID := strings.TrimSpace(launch.Assignment.ProfileID)
	if launch.Profile != nil {
		profileID = firstNonEmptyString(strings.TrimSpace(launch.Profile.ID), profileID)
	}
	executionProfileID := strings.TrimSpace(launch.Assignment.ExecutionProfileID)
	if launch.ExecutionProfile != nil {
		executionProfileID = firstNonEmptyString(strings.TrimSpace(launch.ExecutionProfile.ID), executionProfileID)
	}
	roleLabel := strings.TrimSpace(launch.Assignment.RoleID)
	if launch.Role != nil && strings.TrimSpace(launch.Role.Name) != "" {
		roleLabel = firstNonEmptyString(roleLabel, strings.TrimSpace(launch.Role.ID)) + " (" + strings.TrimSpace(launch.Role.Name) + ")"
	}
	body := []string{
		"Ready: true",
		"Project: " + firstNonEmptyString(strings.TrimSpace(launch.Project.ID), "unknown"),
		"Work item: " + firstNonEmptyString(strings.TrimSpace(launch.WorkItem.ID), "unknown"),
		"Assignment: " + firstNonEmptyString(strings.TrimSpace(launch.Assignment.ID), "unknown"),
		"Execution mode: " + firstNonEmptyString(strings.TrimSpace(launch.Assignment.ExecutionMode), cairnline.ExecutionMCPPull),
		"Role: " + firstNonEmptyString(roleLabel, "none"),
		"Desired agent: " + firstNonEmptyString(strings.TrimSpace(launch.Assignment.DesiredAgent.Kind), cairnline.DesiredAgentAny),
		"Profile: " + firstNonEmptyString(profileID, "inherit"),
		"Execution profile: " + firstNonEmptyString(executionProfileID, "inherit"),
		"Root: " + rootDetail,
		fmt.Sprintf("Skills: %d; artifacts: %d; evidence: %d; reviews: %d; handoffs: %d; memory: %d; memory candidates: %d",
			len(launch.Skills),
			len(launch.Artifacts),
			len(launch.Evidence),
			len(launch.Reviews),
			len(launch.Handoffs),
			len(launch.Memory),
			len(launch.MemoryCandidates),
		),
	}
	if launchID := strings.TrimSpace(launch.ID); launchID != "" {
		body = append(body, "Launch packet: "+launchID)
	}
	if len(launch.Warnings) > 0 {
		body = append(body, "Warnings: "+strings.Join(compactContextIDs(launch.Warnings), " "))
	}
	metadata := cairnlineAssignmentLaunchPacketEvidenceMetadata(launch, rootID, rootPath, true, "")
	appendCairnlineAssignmentLaunchPacketEvidenceSource(packet, strings.Join(body, "\n"), metadata)
}

func appendCairnlineAssignmentLaunchPacketEvidenceError(packet *chat.ContextPacket, assignment projectwork.Assignment, err error) {
	message := strings.TrimSpace(err.Error())
	if errors.Is(err, cairnline.ErrNotFound) {
		message = "Cairnline launch packet not found for assignment " + firstNonEmptyString(strings.TrimSpace(assignment.ID), "unknown")
	}
	body := strings.Join([]string{
		"Ready: false",
		"Assignment: " + firstNonEmptyString(strings.TrimSpace(assignment.ID), "unknown"),
		"Error: " + firstNonEmptyString(message, "Cairnline launch packet could not be built."),
	}, "\n")
	metadata := map[string]string{
		"read_backend":  "cairnline",
		"ready":         "false",
		"project_id":    strings.TrimSpace(assignment.ProjectID),
		"work_item_id":  strings.TrimSpace(assignment.WorkItemID),
		"assignment_id": strings.TrimSpace(assignment.ID),
		"role_id":       strings.TrimSpace(assignment.RoleID),
		"error":         firstNonEmptyString(message, "Cairnline launch packet could not be built."),
	}
	appendCairnlineAssignmentLaunchPacketEvidenceSource(packet, body, metadata)
}

func appendCairnlineAssignmentLaunchPacketEvidenceSource(packet *chat.ContextPacket, body string, metadata map[string]string) {
	appendContextPacketSourceWithSection(packet, contextSectionRuntime, chat.ContextSource{
		Kind:   "cairnline_launch_packet",
		Label:  "Cairnline launch packet",
		Detail: strings.TrimSpace(metadata["assignment_id"]),
		Trust:  contextTrustRuntimeState,
	}, chat.ContextItem{
		Kind:            "cairnline_launch_packet",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          "cairnline.assignment_launch_packet",
		Title:           "Cairnline launch packet",
		Body:            body,
		Included:        false,
		InclusionReason: cairnlineAssignmentLaunchEvidenceReason,
		Metadata:        metadata,
	})
}

func cairnlineAssignmentLaunchPacketEvidenceMetadata(launch cairnline.AssignmentLaunchPacket, rootID, rootPath string, ready bool, errorMessage string) map[string]string {
	metadata := map[string]string{
		"read_backend":           "cairnline",
		"ready":                  fmt.Sprintf("%t", ready),
		"project_id":             strings.TrimSpace(launch.Project.ID),
		"work_item_id":           strings.TrimSpace(launch.WorkItem.ID),
		"assignment_id":          strings.TrimSpace(launch.Assignment.ID),
		"role_id":                strings.TrimSpace(launch.Assignment.RoleID),
		"root_id":                strings.TrimSpace(rootID),
		"root_path":              strings.TrimSpace(rootPath),
		"execution_mode":         firstNonEmptyString(strings.TrimSpace(launch.Assignment.ExecutionMode), cairnline.ExecutionMCPPull),
		"desired_agent":          firstNonEmptyString(strings.TrimSpace(launch.Assignment.DesiredAgent.Kind), cairnline.DesiredAgentAny),
		"profile_id":             strings.TrimSpace(launch.Assignment.ProfileID),
		"execution_profile_id":   strings.TrimSpace(launch.Assignment.ExecutionProfileID),
		"skill_count":            fmt.Sprintf("%d", len(launch.Skills)),
		"artifact_count":         fmt.Sprintf("%d", len(launch.Artifacts)),
		"evidence_count":         fmt.Sprintf("%d", len(launch.Evidence)),
		"review_count":           fmt.Sprintf("%d", len(launch.Reviews)),
		"handoff_count":          fmt.Sprintf("%d", len(launch.Handoffs)),
		"memory_count":           fmt.Sprintf("%d", len(launch.Memory)),
		"memory_candidate_count": fmt.Sprintf("%d", len(launch.MemoryCandidates)),
	}
	if launch.Profile != nil {
		metadata["profile_id"] = firstNonEmptyString(strings.TrimSpace(launch.Profile.ID), metadata["profile_id"])
	}
	if launch.ExecutionProfile != nil {
		metadata["execution_profile_id"] = firstNonEmptyString(strings.TrimSpace(launch.ExecutionProfile.ID), metadata["execution_profile_id"])
	}
	if launchID := strings.TrimSpace(launch.ID); launchID != "" {
		metadata["launch_packet_id"] = launchID
	}
	if len(launch.Warnings) > 0 {
		metadata["warnings"] = strings.Join(compactContextIDs(launch.Warnings), " ")
	}
	if errorMessage = strings.TrimSpace(errorMessage); errorMessage != "" {
		metadata["error"] = errorMessage
	}
	return metadata
}

func appendCairnlineProjectSummary(packet *chat.ContextPacket, project cairnline.Project) {
	label := firstNonEmptyString(strings.TrimSpace(project.Name), strings.TrimSpace(project.ID), "Project")
	body := "Description: " + firstNonEmptyString(strings.TrimSpace(project.Description), "No description recorded.")
	if rootID := strings.TrimSpace(project.DefaultRootID); rootID != "" {
		body += "\nDefault root: " + rootID
	}
	appendContextPacketSourceWithSection(packet, contextSectionProject, chat.ContextSource{
		Kind:   "project",
		Label:  label,
		Detail: strings.TrimSpace(project.ID),
		Trust:  contextTrustProject,
	}, chat.ContextItem{
		Kind:            "project",
		TrustLevel:      contextTrustProject,
		Origin:          strings.TrimSpace(project.ID),
		Title:           label,
		Body:            body,
		Included:        true,
		InclusionReason: cairnlineAssignmentContextReason,
	})
}

func appendCairnlineWorkItem(packet *chat.ContextPacket, item cairnline.WorkItem) {
	body := []string{
		"Status: " + firstNonEmptyString(strings.TrimSpace(item.Status), "unknown"),
		"Priority: " + firstNonEmptyString(strings.TrimSpace(item.Priority), "normal"),
		"Brief: " + firstNonEmptyString(strings.TrimSpace(item.Brief), "No brief recorded."),
	}
	if owner := strings.TrimSpace(item.OwnerRoleID); owner != "" {
		body = append(body, "Owner role: "+owner)
	}
	if reviewers := compactContextIDs(item.ReviewerRoleIDs); len(reviewers) > 0 {
		body = append(body, "Reviewer roles: "+strings.Join(reviewers, ", "))
	}
	if rootID := strings.TrimSpace(item.RootID); rootID != "" {
		body = append(body, "Root: "+rootID)
	}
	appendContextPacketSourceWithSection(packet, contextSectionProjectWork, chat.ContextSource{
		Kind:   "work_item",
		Label:  firstNonEmptyString(strings.TrimSpace(item.Title), strings.TrimSpace(item.ID)),
		Detail: strings.TrimSpace(item.ID),
		Trust:  contextTrustProject,
	}, chat.ContextItem{
		Kind:            "work_item",
		TrustLevel:      contextTrustProject,
		Origin:          strings.TrimSpace(item.ID),
		Title:           firstNonEmptyString(strings.TrimSpace(item.Title), strings.TrimSpace(item.ID)),
		Body:            strings.Join(body, "\n"),
		Included:        true,
		InclusionReason: cairnlineAssignmentContextReason,
	})
}

func appendCairnlineAssignment(packet *chat.ContextPacket, item cairnline.Assignment) {
	body := []string{
		"Status: " + firstNonEmptyString(strings.TrimSpace(item.Status), "unknown"),
		"Execution mode: " + firstNonEmptyString(strings.TrimSpace(item.ExecutionMode), cairnline.ExecutionMCPPull),
		"Role: " + firstNonEmptyString(strings.TrimSpace(item.RoleID), "none"),
		"Desired agent: " + firstNonEmptyString(strings.TrimSpace(item.DesiredAgent.Kind), cairnline.DesiredAgentAny),
		"Profile: " + firstNonEmptyString(strings.TrimSpace(item.ProfileID), "inherit"),
		"Execution profile: " + firstNonEmptyString(strings.TrimSpace(item.ExecutionProfileID), "inherit"),
	}
	if rootID := strings.TrimSpace(item.RootID); rootID != "" {
		body = append(body, "Root override: "+rootID)
	}
	if skills := compactContextIDs(item.DesiredAgent.SkillIDs); len(skills) > 0 {
		body = append(body, "Desired skills: "+strings.Join(skills, ", "))
	}
	if claimedBy := strings.TrimSpace(item.ClaimedBy); claimedBy != "" {
		body = append(body, "Claimed by: "+claimedBy)
	}
	if executionRef := strings.TrimSpace(item.ExecutionRef); executionRef != "" {
		body = append(body, "Execution ref: "+executionRef)
	}
	if contextSnapshotID := strings.TrimSpace(item.ContextSnapshotID); contextSnapshotID != "" {
		body = append(body, "Context snapshot: "+contextSnapshotID)
	}
	appendContextPacketSourceWithSection(packet, contextSectionProjectWork, chat.ContextSource{
		Kind:   "assignment",
		Label:  firstNonEmptyString(strings.TrimSpace(item.ID), "Assignment"),
		Detail: strings.TrimSpace(item.ExecutionMode),
		Trust:  contextTrustRuntimeState,
	}, chat.ContextItem{
		Kind:            "assignment",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          strings.TrimSpace(item.ID),
		Title:           "Assignment",
		Body:            strings.Join(body, "\n"),
		Included:        true,
		InclusionReason: cairnlineAssignmentContextReason,
	})
}

func appendCairnlineRoot(packet *chat.ContextPacket, root cairnline.Root, selection string) {
	body := []string{
		"Root ID: " + firstNonEmptyString(strings.TrimSpace(root.ID), "unresolved"),
		"Path: " + firstNonEmptyString(strings.TrimSpace(root.Path), "none"),
		"Kind: " + firstNonEmptyString(strings.TrimSpace(root.Kind), "local"),
		"Active: " + boolLabel(root.Active),
		"Selection: " + firstNonEmptyString(strings.TrimSpace(selection), "project root fallback"),
	}
	if branch := strings.TrimSpace(root.GitBranch); branch != "" {
		body = append(body, "Git branch: "+branch)
	}
	if remote := strings.TrimSpace(root.GitRemote); remote != "" {
		body = append(body, "Git remote: "+remote)
	}
	appendContextPacketSourceWithSection(packet, contextSectionProjectWork, chat.ContextSource{
		Kind:   "project_root",
		Label:  firstNonEmptyString(strings.TrimSpace(root.ID), "Project root"),
		Detail: strings.TrimSpace(root.Path),
		Trust:  contextTrustRuntimeState,
	}, chat.ContextItem{
		Kind:            "project_root",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          firstNonEmptyString(strings.TrimSpace(root.ID), "project_root"),
		Title:           "Project root",
		Body:            strings.Join(body, "\n"),
		Included:        true,
		InclusionReason: cairnlineAssignmentContextReason,
	})
}

func appendCairnlineRole(packet *chat.ContextPacket, role cairnline.Role) {
	body := []string{
		"Description: " + firstNonEmptyString(strings.TrimSpace(role.Description), "No description recorded."),
		"Instructions: " + firstNonEmptyString(strings.TrimSpace(role.Instructions), "No role instructions recorded."),
		"Default profile: " + firstNonEmptyString(strings.TrimSpace(role.DefaultProfileID), "none"),
		"Default execution profile: " + firstNonEmptyString(strings.TrimSpace(role.DefaultExecutionProfileID), "none"),
		"Default execution mode: " + firstNonEmptyString(strings.TrimSpace(role.DefaultExecutionMode), "inherit"),
	}
	if skills := compactContextIDs(role.DefaultSkillIDs); len(skills) > 0 {
		body = append(body, "Default skills: "+strings.Join(skills, ", "))
	}
	appendContextPacketSourceWithSection(packet, contextSectionProjectWork, chat.ContextSource{
		Kind:   "role",
		Label:  firstNonEmptyString(strings.TrimSpace(role.Name), strings.TrimSpace(role.ID)),
		Detail: strings.TrimSpace(role.ID),
		Trust:  contextTrustRuntimeState,
	}, chat.ContextItem{
		Kind:            "role",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          strings.TrimSpace(role.ID),
		Title:           firstNonEmptyString(strings.TrimSpace(role.Name), strings.TrimSpace(role.ID)),
		Body:            strings.Join(body, "\n"),
		Included:        true,
		InclusionReason: cairnlineAssignmentContextReason,
	})
}

func appendCairnlineAgentProfile(packet *chat.ContextPacket, profile cairnline.AgentProfile) {
	body := []string{
		"ID: " + strings.TrimSpace(profile.ID),
		"Name: " + firstNonEmptyString(strings.TrimSpace(profile.Name), strings.TrimSpace(profile.ID)),
		"Context policy: " + firstNonEmptyString(strings.TrimSpace(profile.ContextPolicy), "inherit"),
		"Memory policy: " + firstNonEmptyString(strings.TrimSpace(profile.MemoryPolicy), "inherit"),
		"Source policy: " + firstNonEmptyString(strings.TrimSpace(profile.SourcePolicy), "inherit"),
	}
	if description := strings.TrimSpace(profile.Description); description != "" {
		body = append(body, "Description: "+description)
	}
	if instructions := strings.TrimSpace(profile.Instructions); instructions != "" {
		body = append(body, "Instructions:\n"+instructions)
	}
	if skills := compactContextIDs(profile.SkillIDs); len(skills) > 0 {
		body = append(body, "Skills: "+strings.Join(skills, ", "))
	}
	appendContextPacketSourceWithSection(packet, contextSectionProfile, chat.ContextSource{
		Kind:   "agent_profile",
		Label:  firstNonEmptyString(strings.TrimSpace(profile.Name), strings.TrimSpace(profile.ID)),
		Detail: strings.TrimSpace(profile.ID),
		Trust:  contextTrustRuntimeState,
	}, chat.ContextItem{
		Kind:            "agent_profile",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          strings.TrimSpace(profile.ID),
		Title:           firstNonEmptyString(strings.TrimSpace(profile.Name), strings.TrimSpace(profile.ID)),
		Body:            strings.Join(body, "\n"),
		Included:        true,
		InclusionReason: cairnlineAssignmentContextReason,
	})
}

func appendCairnlineExecutionProfile(packet *chat.ContextPacket, profile cairnline.ExecutionProfile) {
	body := []string{
		"ID: " + strings.TrimSpace(profile.ID),
		"Name: " + firstNonEmptyString(strings.TrimSpace(profile.Name), strings.TrimSpace(profile.ID)),
		"Agent kind: " + firstNonEmptyString(strings.TrimSpace(profile.AgentKind), cairnline.DesiredAgentAny),
		"Provider hint: " + firstNonEmptyString(strings.TrimSpace(profile.ProviderHint), "inherit"),
		"Model hint: " + firstNonEmptyString(strings.TrimSpace(profile.ModelHint), "inherit"),
		"Tools policy: " + firstNonEmptyString(strings.TrimSpace(profile.ToolsPolicy), "inherit"),
		"Writes policy: " + firstNonEmptyString(strings.TrimSpace(profile.WritesPolicy), "inherit"),
		"Network policy: " + firstNonEmptyString(strings.TrimSpace(profile.NetworkPolicy), "inherit"),
		"Approval policy: " + firstNonEmptyString(strings.TrimSpace(profile.ApprovalPolicy), "inherit"),
	}
	if description := strings.TrimSpace(profile.Description); description != "" {
		body = append(body, "Description: "+description)
	}
	if keys := cairnlineAdapterOptionKeys(profile.AdapterOptions); len(keys) > 0 {
		body = append(body, "Adapter option keys: "+strings.Join(keys, ", "))
	}
	appendContextPacketSourceWithSection(packet, contextSectionProfile, chat.ContextSource{
		Kind:   "execution_profile",
		Label:  firstNonEmptyString(strings.TrimSpace(profile.Name), strings.TrimSpace(profile.ID)),
		Detail: strings.TrimSpace(profile.ID),
		Trust:  contextTrustRuntimeState,
	}, chat.ContextItem{
		Kind:            "execution_profile",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          strings.TrimSpace(profile.ID),
		Title:           firstNonEmptyString(strings.TrimSpace(profile.Name), strings.TrimSpace(profile.ID)),
		Body:            strings.Join(body, "\n"),
		Included:        true,
		InclusionReason: cairnlineAssignmentContextReason,
	})
}

func appendCairnlineProjectSkills(packet *chat.ContextPacket, launch cairnline.AssignmentLaunchPacket) {
	requested := cairnlineRequestedSkillIDs(launch)
	if len(requested) == 0 && len(launch.Skills) == 0 {
		return
	}
	body := []string{
		"Requested: " + firstNonEmptyString(strings.Join(requested, ", "), "none"),
	}
	if len(launch.Skills) > 0 {
		resolved := make([]string, 0, len(launch.Skills))
		for _, skill := range launch.Skills {
			detail := firstNonEmptyString(strings.TrimSpace(skill.ID), strings.TrimSpace(skill.Title), "skill")
			if path := strings.TrimSpace(skill.Path); path != "" {
				detail += " (" + path + ")"
			}
			if status := strings.TrimSpace(skill.Status); status != "" {
				detail += "; status: " + status
			}
			if trust := strings.TrimSpace(skill.TrustLabel); trust != "" {
				detail += "; trust: " + trust
			}
			if len(skill.Warnings) > 0 {
				detail += "; warnings: " + strings.Join(compactContextIDs(skill.Warnings), " ")
			}
			resolved = append(resolved, detail)
		}
		body = append(body, "Resolved enabled skills: "+strings.Join(resolved, ", "))
	} else {
		body = append(body, "Resolved enabled skills: none")
	}
	appendContextPacketSourceWithSection(packet, contextSectionSkills, chat.ContextSource{
		Kind:   "project_skills",
		Label:  "Project skills",
		Detail: strings.Join(requested, ","),
		Trust:  projectskills.TrustWorkspaceSkill,
	}, chat.ContextItem{
		Kind:            "project_skills",
		TrustLevel:      projectskills.TrustWorkspaceSkill,
		Origin:          "project_skills",
		Title:           "Project skills",
		Body:            strings.Join(body, "\n"),
		Included:        len(launch.Skills) > 0,
		InclusionReason: "Cairnline resolved skill metadata for this assignment; skill bodies are not injected",
	})
}

func appendCairnlineMemory(packet *chat.ContextPacket, entries []cairnline.MemoryEntry) {
	for _, entry := range entries {
		appendProjectMemoryEntry(packet, memory.Entry{
			ID:         strings.TrimSpace(entry.ID),
			ProjectID:  strings.TrimSpace(entry.ProjectID),
			Title:      strings.TrimSpace(entry.Title),
			Body:       strings.TrimSpace(entry.Body),
			TrustLabel: strings.TrimSpace(entry.TrustLabel),
			SourceKind: strings.TrimSpace(entry.SourceKind),
			SourceID:   strings.TrimSpace(entry.SourceID),
			Enabled:    entry.Enabled,
			CreatedAt:  entry.CreatedAt,
			UpdatedAt:  entry.UpdatedAt,
		}, false, "Inspectable Cairnline memory only; this read-model endpoint does not inject memory bodies into a model prompt")
	}
}

func appendCairnlineMemoryCandidates(packet *chat.ContextPacket, candidates []cairnline.MemoryCandidate) {
	for _, candidate := range candidates {
		trust := firstNonEmptyString(strings.TrimSpace(candidate.SuggestedTrustLabel), contextTrustRuntimeState)
		appendContextPacketSourceWithSection(packet, contextSectionMemory, chat.ContextSource{
			Kind:   "memory_candidate",
			Label:  firstNonEmptyString(strings.TrimSpace(candidate.Title), strings.TrimSpace(candidate.ID)),
			Detail: strings.TrimSpace(candidate.ID),
			Trust:  trust,
		}, chat.ContextItem{
			Kind:            "memory_candidate",
			TrustLevel:      trust,
			Origin:          strings.TrimSpace(candidate.ID),
			Title:           firstNonEmptyString(strings.TrimSpace(candidate.Title), strings.TrimSpace(candidate.ID)),
			Body:            strings.TrimSpace(candidate.Body),
			Included:        false,
			InclusionReason: "Pending memory candidate is inspectable only until the operator promotes it",
			Metadata: map[string]string{
				"status":                strings.TrimSpace(candidate.Status),
				"suggested_kind":        strings.TrimSpace(candidate.SuggestedKind),
				"suggested_source_kind": strings.TrimSpace(candidate.SuggestedSourceKind),
				"suggested_source_id":   strings.TrimSpace(candidate.SuggestedSourceID),
			},
		})
	}
}

func appendCairnlineProjectSources(packet *chat.ContextPacket, sources []cairnline.Source) {
	for _, source := range sources {
		trust := firstNonEmptyString(strings.TrimSpace(source.TrustLabel), contextTrustWorkspaceGuidance)
		appendContextPacketSourceWithSection(packet, contextSectionSources, chat.ContextSource{
			Kind:   projectContextSourceKind(source.Kind),
			Label:  firstNonEmptyString(strings.TrimSpace(source.Title), strings.TrimSpace(source.ID)),
			Detail: strings.TrimSpace(source.Locator),
			Trust:  trust,
		}, chat.ContextItem{
			Kind:            projectContextSourceKind(source.Kind),
			TrustLevel:      trust,
			Origin:          firstNonEmptyString(strings.TrimSpace(source.ID), strings.TrimSpace(source.Locator)),
			Title:           firstNonEmptyString(strings.TrimSpace(source.Title), strings.TrimSpace(source.ID)),
			BodyRef:         strings.TrimSpace(source.Locator),
			Included:        false,
			InclusionReason: "Cairnline source metadata is inspectable only; source file bodies are not loaded by this endpoint",
			Metadata: map[string]string{
				"enabled":         boolLabel(source.Enabled),
				"format":          strings.TrimSpace(source.Format),
				"scope":           strings.TrimSpace(source.Scope),
				"source_category": strings.TrimSpace(source.SourceCategory),
			},
		})
	}
}

func appendCairnlineAssignmentArtifacts(packet *chat.ContextPacket, launch cairnline.AssignmentLaunchPacket) {
	items := make([]projectwork.CollaborationArtifact, 0, len(launch.Artifacts)+len(launch.Evidence)+len(launch.Reviews))
	for _, item := range launch.Artifacts {
		items = append(items, projectWorkArtifactFromCairnline(item))
	}
	for _, item := range launch.Evidence {
		items = append(items, projectHealthEvidenceFromCairnline(item))
	}
	for _, item := range launch.Reviews {
		items = append(items, projectHealthReviewFromCairnline(item))
	}
	if len(items) == 0 {
		return
	}
	appendProjectAssignmentArtifacts(packet, filterAssignmentArtifacts(items, launch.Assignment.ID), false, "Collaboration artifact is inspectable Cairnline project evidence; not injected into a model prompt")
}

func appendCairnlineAssignmentHandoffs(packet *chat.ContextPacket, launch cairnline.AssignmentLaunchPacket) {
	items := make([]projectwork.Handoff, 0, len(launch.Handoffs))
	for _, item := range launch.Handoffs {
		items = append(items, projectHealthHandoffFromCairnline(item))
	}
	appendProjectAssignmentHandoffs(packet, filterAssignmentHandoffs(items, launch.Assignment.ID, launch.Assignment.RoleID), false, "Handoff metadata is inspectable Cairnline project evidence; not injected into a model prompt")
}

func appendCairnlineLaunchRuntime(packet *chat.ContextPacket, launch cairnline.AssignmentLaunchPacket) {
	body := []string{
		"Read backend: cairnline",
		"Launch packet kind: " + firstNonEmptyString(strings.TrimSpace(launch.Kind), "assignment"),
		"Portable execution mode: " + firstNonEmptyString(strings.TrimSpace(launch.Assignment.ExecutionMode), cairnline.ExecutionMCPPull),
		"Preview only: Hecate stores remain authoritative and no task, chat session, or external-agent run is created by this context read.",
	}
	if launchID := strings.TrimSpace(launch.ID); launchID != "" {
		body = append(body, "Cairnline launch packet: "+launchID)
	}
	if len(launch.Warnings) > 0 {
		body = append(body, "Warnings: "+strings.Join(compactContextIDs(launch.Warnings), " "))
	}
	appendContextPacketSourceWithSection(packet, contextSectionRuntime, chat.ContextSource{
		Kind:   "cairnline_assignment_context",
		Label:  "Cairnline assignment context",
		Detail: strings.TrimSpace(launch.Assignment.ID),
		Trust:  contextTrustRuntimeState,
	}, chat.ContextItem{
		Kind:            "cairnline_assignment_context",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          "cairnline.assignment_launch_packet",
		Title:           "Cairnline assignment context",
		Body:            strings.Join(body, "\n"),
		Included:        false,
		InclusionReason: "Read-model preview only",
		Metadata: map[string]string{
			"read_backend":   "cairnline",
			"launch_preview": "true",
		},
	})
}

func appendCairnlineAssignmentContextRuntime(packet *chat.ContextPacket, context cairnline.AssignmentContext) {
	body := []string{
		"Read backend: cairnline",
		"Source tool: assignments.context",
		"Portable execution mode: " + firstNonEmptyString(strings.TrimSpace(context.Assignment.ExecutionMode), cairnline.ExecutionMCPPull),
		"Preview only: Hecate stores remain authoritative and no task, chat session, or external-agent run is created by this context read.",
	}
	if contextID := strings.TrimSpace(context.ID); contextID != "" {
		body = append(body, "Cairnline assignment context: "+contextID)
	}
	if len(context.Warnings) > 0 {
		body = append(body, "Warnings: "+strings.Join(compactContextIDs(context.Warnings), " "))
	}
	metadata := map[string]string{
		"read_backend":   "cairnline",
		"source_tool":    "assignments.context",
		"project_id":     strings.TrimSpace(context.Project.ID),
		"work_item_id":   strings.TrimSpace(context.WorkItem.ID),
		"assignment_id":  strings.TrimSpace(context.Assignment.ID),
		"role_id":        strings.TrimSpace(context.Assignment.RoleID),
		"execution_mode": firstNonEmptyString(strings.TrimSpace(context.Assignment.ExecutionMode), cairnline.ExecutionMCPPull),
	}
	if contextID := strings.TrimSpace(context.ID); contextID != "" {
		metadata["assignment_context_id"] = contextID
	}
	if len(context.Warnings) > 0 {
		metadata["warnings"] = strings.Join(compactContextIDs(context.Warnings), " ")
	}
	appendContextPacketSourceWithSection(packet, contextSectionRuntime, chat.ContextSource{
		Kind:   "cairnline_assignment_context",
		Label:  "Cairnline assignment context",
		Detail: strings.TrimSpace(context.Assignment.ID),
		Trust:  contextTrustRuntimeState,
	}, chat.ContextItem{
		Kind:            "cairnline_assignment_context",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          "cairnline.assignments.context",
		Title:           "Cairnline assignment context",
		Body:            strings.Join(body, "\n"),
		Included:        false,
		InclusionReason: "Read-model preview only",
		Metadata:        metadata,
	})
}

func selectedCairnlineRoot(project cairnline.Project, workItem cairnline.WorkItem, assignment cairnline.Assignment) (cairnline.Root, bool, string) {
	if root, ok := cairnlineRootByID(project.Roots, assignment.RootID); ok {
		return root, true, "assignment override"
	}
	if root, ok := cairnlineRootByID(project.Roots, workItem.RootID); ok {
		return root, true, "work item default"
	}
	if root, ok := cairnlineRootByID(project.Roots, project.DefaultRootID); ok {
		return root, true, "project default"
	}
	for _, root := range project.Roots {
		if root.Active {
			return root, true, "active project root"
		}
	}
	if len(project.Roots) > 0 {
		return project.Roots[0], true, "first project root"
	}
	return cairnline.Root{}, false, ""
}

func cairnlineRootByID(roots []cairnline.Root, id string) (cairnline.Root, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return cairnline.Root{}, false
	}
	for _, root := range roots {
		if strings.TrimSpace(root.ID) == id {
			return root, true
		}
	}
	return cairnline.Root{}, false
}

func cairnlineRequestedSkillIDs(launch cairnline.AssignmentLaunchPacket) []string {
	var ids []string
	ids = append(ids, launch.Assignment.DesiredAgent.SkillIDs...)
	if launch.Role != nil {
		ids = append(ids, launch.Role.DefaultSkillIDs...)
	}
	if launch.Profile != nil {
		ids = append(ids, launch.Profile.SkillIDs...)
	}
	for _, skill := range launch.Skills {
		ids = append(ids, skill.ID)
	}
	return compactContextIDs(ids)
}

func cairnlineAdapterOptionKeys(options map[string]any) []string {
	keys := make([]string, 0, len(options))
	for key := range options {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			keys = append(keys, trimmed)
		}
	}
	sort.Strings(keys)
	return keys
}

func compactContextIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func filterAssignmentArtifacts(items []projectwork.CollaborationArtifact, assignmentID string) []projectwork.CollaborationArtifact {
	assignmentID = strings.TrimSpace(assignmentID)
	if assignmentID == "" {
		return items
	}
	filtered := make([]projectwork.CollaborationArtifact, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.AssignmentID) == "" || strings.TrimSpace(item.AssignmentID) == assignmentID {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func filterAssignmentHandoffs(items []projectwork.Handoff, assignmentID, roleID string) []projectwork.Handoff {
	assignmentID = strings.TrimSpace(assignmentID)
	roleID = strings.TrimSpace(roleID)
	if assignmentID == "" && roleID == "" {
		return items
	}
	filtered := make([]projectwork.Handoff, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.SourceAssignmentID) == assignmentID || strings.TrimSpace(item.TargetAssignmentID) == assignmentID || strings.TrimSpace(item.TargetRoleID) == roleID {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
