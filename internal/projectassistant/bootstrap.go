package projectassistant

import (
	"context"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

const (
	bootstrapGuidanceActionLimit = 8
	bootstrapSkillActionLimit    = 8
)

func (s *Service) draftBootstrap(ctx context.Context, input DraftInput, draftContext DraftContext) (Proposal, error) {
	projectID := draftContext.Project.ID
	actions, warnings := bootstrapGuidanceActions(projectID, draftContext)

	skillActions, skillWarnings := bootstrapSkillRoleActions(projectID, draftContext)
	actions = append(actions, skillActions...)
	warnings = append(warnings, skillWarnings...)
	if draftContext.SelectedWork != nil {
		warnings = append(warnings, "Project setup proposals are project-scoped; selected work context was ignored.")
	}
	if len(actions) == 0 {
		return Proposal{}, fmt.Errorf("%w: no enabled guidance sources or local skill files found for project setup", ErrInvalid)
	}

	proposal, err := s.Propose(ctx, ProposalInput{
		Title:   fmt.Sprintf("Set up %s guidance", firstNonEmpty(draftContext.Project.Name, projectID)),
		Summary: "Create reviewable memory candidates from discovered guidance metadata and suggest project roles from local skill files.",
		Actions: actions,
		TraceID: strings.TrimSpace(input.TraceID),
	})
	if err != nil {
		return Proposal{}, err
	}
	proposal.Warnings = warnings
	return proposal, nil
}

func bootstrapGuidanceActions(projectID string, draftContext DraftContext) ([]Action, []string) {
	existing := existingMemorySourceRefs(draftContext)
	var actions []Action
	var warnings []string
	for _, source := range draftContext.Project.ContextSources {
		if !source.Enabled || strings.TrimSpace(source.ID) == "" {
			continue
		}
		if strings.TrimSpace(source.SourceCategory) != "workspace_guidance" {
			continue
		}
		refKey := memorySourceRefKey("context_source", source.ID)
		if existing[refKey] {
			continue
		}
		if len(actions) >= bootstrapGuidanceActionLimit {
			warnings = append(warnings, fmt.Sprintf("Skipped additional guidance sources after %d setup memory candidates.", bootstrapGuidanceActionLimit))
			break
		}
		actions = append(actions, bootstrapGuidanceAction(projectID, source))
	}
	return actions, warnings
}

func bootstrapGuidanceAction(projectID string, source ContextSource) Action {
	trustLabel := firstNonEmpty(source.TrustLabel, "workspace_guidance")
	title := firstNonEmpty(source.Title, source.Path, source.ID)
	reason := "Prepare a reviewable memory candidate from discovered workspace guidance metadata."
	if source.Kind != "workspace_instruction" {
		reason = "Record host-specific guidance as reviewable metadata without importing host policy."
	}
	return Action{
		Kind:   ActionCreateMemoryCandidate,
		Target: map[string]string{"project_id": projectID},
		Patch: mustRawJSON(memoryCandidatePatch{
			ProjectID:           projectID,
			Title:               "Guidance source: " + title,
			Body:                bootstrapGuidanceBody(source),
			SuggestedKind:       "workspace_guidance",
			SuggestedTrustLabel: trustLabel,
			SuggestedSourceKind: "context_source",
			SuggestedSourceID:   source.ID,
			SourceRefs: []memory.CandidateSourceRef{{
				Kind:  "context_source",
				ID:    source.ID,
				Title: title,
			}},
		}),
		Reason: reason,
	}
}

func bootstrapGuidanceBody(source ContextSource) string {
	var lines []string
	lines = append(lines,
		"Discovered project guidance source.",
		"",
		"Source: "+firstNonEmpty(source.Path, source.ID),
		"Kind: "+firstNonEmpty(source.Kind, "unknown"),
		"Format: "+firstNonEmpty(source.Format, "unknown"),
		"Scope: "+firstNonEmpty(source.Scope, "unknown"),
		"Trust label: "+firstNonEmpty(source.TrustLabel, "workspace_guidance"),
	)
	if rootID := strings.TrimSpace(source.Metadata["root_id"]); rootID != "" {
		lines = append(lines, "Root: "+rootID)
	}
	if host := strings.TrimSpace(source.Metadata["host"]); host != "" {
		lines = append(lines, "Host: "+host)
	}
	lines = append(lines,
		"",
		"This candidate records source provenance only. Review the source file before promoting or editing durable project memory from it.",
	)
	if source.Kind != "workspace_instruction" {
		lines = append(lines, "Host-specific guidance is labelled for visibility only; it does not override Hecate policy, approvals, sandboxing, or project profile settings.")
	}
	return strings.Join(lines, "\n")
}

func existingMemorySourceRefs(draftContext DraftContext) map[string]bool {
	out := make(map[string]bool)
	for _, item := range draftContext.Memory {
		if item.SourceKind != "" && item.SourceID != "" {
			out[memorySourceRefKey(item.SourceKind, item.SourceID)] = true
		}
	}
	for _, item := range draftContext.MemoryCandidates {
		if item.SuggestedSourceKind != "" && item.SuggestedSourceID != "" {
			out[memorySourceRefKey(item.SuggestedSourceKind, item.SuggestedSourceID)] = true
		}
		for _, ref := range item.SourceRefs {
			if ref.Kind != "" && ref.ID != "" {
				out[memorySourceRefKey(ref.Kind, ref.ID)] = true
			}
		}
	}
	return out
}

func memorySourceRefKey(kind, id string) string {
	return strings.TrimSpace(kind) + "\x00" + strings.TrimSpace(id)
}

func bootstrapSkillRoleActions(projectID string, draftContext DraftContext) ([]Action, []string) {
	existingRoles := existingRoleIDs(draftContext.Roles)
	var actions []Action
	var warnings []string
	if len(draftContext.Skills) == 0 {
		warnings = append(warnings, "No project skills are registered yet; run project skill discovery before setup can suggest skill roles.")
		return actions, warnings
	}
	for _, skill := range draftContext.Skills {
		skillID := strings.TrimSpace(skill.ID)
		if skillID == "" {
			continue
		}
		roleID := "skill_" + skillID
		switch {
		case !skill.Enabled:
			warnings = append(warnings, fmt.Sprintf("Skipped disabled project skill %s.", skillID))
			continue
		case skill.Status != projectskills.StatusAvailable:
			warnings = append(warnings, fmt.Sprintf("Skipped project skill %s because status is %s.", skillID, firstNonEmpty(skill.Status, "unknown")))
			continue
		case existingRoles[skillID] || existingRoles[roleID]:
			continue
		}
		if len(actions) >= bootstrapSkillActionLimit {
			warnings = append(warnings, fmt.Sprintf("Skipped additional project skills after %d bootstrap role suggestions.", bootstrapSkillActionLimit))
			break
		}
		actions = append(actions, bootstrapSkillRoleAction(projectID, skill))
	}
	return actions, warnings
}

func bootstrapSkillRoleAction(projectID string, skill ProjectSkillContext) Action {
	roleID := "skill_" + skill.ID
	roleName := firstNonEmpty(skill.Title, bootstrapTitle(skill.ID))
	return Action{
		Kind:   ActionCreateRole,
		Target: map[string]string{"project_id": projectID},
		Patch: mustRawJSON(rolePatch{
			ID:                roleID,
			ProjectID:         projectID,
			Name:              roleName,
			Description:       fmt.Sprintf("Suggested from project skill metadata at %s.", skill.Path),
			Instructions:      fmt.Sprintf("Use project skill reference %s (%s) when this project role owns work. This role is a suggestion only; it does not inject skill bodies, install skills, execute scripts, change approvals, or grant tools.", skill.ID, skill.Path),
			DefaultDriverKind: projectwork.AssignmentDriverHecateTask,
			SkillIDs:          []string{skill.ID},
		}),
		Reason: fmt.Sprintf("Suggest a project role from project skill metadata at %s.", skill.Path),
	}
}

func existingRoleIDs(roles []RoleContext) map[string]bool {
	out := make(map[string]bool, len(roles))
	for _, role := range roles {
		if role.ID != "" {
			out[role.ID] = true
		}
	}
	return out
}

func normalizeBootstrapID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastUnderscore = false
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if !lastUnderscore && builder.Len() > 0 {
				builder.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(builder.String(), "_")
}

func bootstrapTitle(id string) string {
	parts := strings.Split(normalizeBootstrapID(id), "_")
	for idx, part := range parts {
		if part == "" {
			continue
		}
		parts[idx] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}
