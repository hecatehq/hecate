package projectassistant

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/workspacefs"
)

const (
	bootstrapGuidanceActionLimit = 8
	bootstrapSkillActionLimit    = 8
	bootstrapGuidanceMaxBytes    = 64 * 1024
)

type bootstrapSkillSource struct {
	ID   string
	Path string
}

func (s *Service) draftBootstrap(ctx context.Context, input DraftInput, draftContext DraftContext) (Proposal, error) {
	projectID := draftContext.Project.ID
	actions, warnings := bootstrapGuidanceActions(projectID, draftContext)

	skillActions, skillWarnings := bootstrapSkillRoleActions(projectID, draftContext)
	actions = append(actions, skillActions...)
	warnings = append(warnings, skillWarnings...)
	if draftContext.SelectedWork != nil {
		warnings = append(warnings, "Bootstrap drafts are project-scoped; selected work context was ignored.")
	}
	if len(actions) == 0 {
		return Proposal{}, fmt.Errorf("%w: no enabled guidance sources or local skill files found for bootstrap", ErrInvalid)
	}

	proposal, err := s.Propose(ctx, ProposalInput{
		Title:   fmt.Sprintf("Bootstrap %s guidance", firstNonEmpty(draftContext.Project.Name, projectID)),
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
			warnings = append(warnings, fmt.Sprintf("Skipped additional guidance sources after %d bootstrap memory candidates.", bootstrapGuidanceActionLimit))
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
	skills, warnings := discoverBootstrapSkills(draftContext.Project, existingRoles)
	var actions []Action
	for _, skill := range skills {
		if len(actions) >= bootstrapSkillActionLimit {
			warnings = append(warnings, fmt.Sprintf("Skipped additional local skills after %d bootstrap role suggestions.", bootstrapSkillActionLimit))
			break
		}
		actions = append(actions, bootstrapSkillRoleAction(projectID, skill))
	}
	return actions, warnings
}

func bootstrapSkillRoleAction(projectID string, skill bootstrapSkillSource) Action {
	roleID := "skill_" + skill.ID
	roleName := bootstrapTitle(skill.ID)
	return Action{
		Kind:   ActionCreateRole,
		Target: map[string]string{"project_id": projectID},
		Patch: mustRawJSON(rolePatch{
			ID:                roleID,
			ProjectID:         projectID,
			Name:              roleName,
			Description:       fmt.Sprintf("Suggested from local skill metadata at %s.", skill.Path),
			Instructions:      fmt.Sprintf("Use local skill guidance at %s when this project role owns work. This role is a suggestion only; it does not install skills, execute scripts, change approvals, or grant tools.", skill.Path),
			DefaultDriverKind: projectwork.AssignmentDriverHecateTask,
		}),
		Reason: fmt.Sprintf("Suggest a project role from local skill metadata at %s.", skill.Path),
	}
}

func discoverBootstrapSkills(project ProjectContext, existingRoles map[string]bool) ([]bootstrapSkillSource, []string) {
	var out []bootstrapSkillSource
	var warnings []string
	seen := make(map[string]bool)
	for _, root := range project.Roots {
		if !root.Active || strings.TrimSpace(root.Path) == "" {
			continue
		}
		if !filepath.IsAbs(root.Path) {
			warnings = append(warnings, fmt.Sprintf("Skipped skill discovery for non-absolute project root %s.", root.ID))
			continue
		}
		fsys, err := workspacefs.New(root.Path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Skipped skill discovery for root %s: %v.", root.ID, err))
			continue
		}
		baseDirs := bootstrapSkillBaseDirs(fsys, root, project.ContextSources, &warnings)
		for _, base := range baseDirs {
			skills := discoverBootstrapSkillsInDir(fsys, base, seen, existingRoles)
			out = append(out, skills...)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Path < out[j].Path
	})
	return out, warnings
}

func bootstrapSkillBaseDirs(fsys *workspacefs.FS, root ProjectRootContext, sources []ContextSource, warnings *[]string) []string {
	dirs := []string{".agents/skills", ".hecate/skills"}
	seen := map[string]bool{
		".agents/skills": true,
		".hecate/skills": true,
	}
	for _, source := range sources {
		if !bootstrapGuidanceSourceForRoot(source, root.ID) {
			continue
		}
		body, ok := readBootstrapGuidanceSource(fsys, source, warnings)
		if !ok {
			continue
		}
		for _, dir := range skillBaseDirsFromGuidance(source.Path, body) {
			if seen[dir] {
				continue
			}
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func bootstrapGuidanceSourceForRoot(source ContextSource, rootID string) bool {
	if !source.Enabled || strings.TrimSpace(source.Path) == "" {
		return false
	}
	if source.Metadata != nil {
		sourceRootID := strings.TrimSpace(source.Metadata["root_id"])
		if sourceRootID != "" && rootID != "" && sourceRootID != rootID {
			return false
		}
	}
	if source.Kind == "workspace_instruction" || source.Format == "agents_md" || source.Format == "claude_md" {
		return true
	}
	base := strings.ToLower(path.Base(filepath.ToSlash(source.Path)))
	return base == "agents.md" || base == "claude.md" || base == "claude.local.md"
}

func readBootstrapGuidanceSource(fsys *workspacefs.FS, source ContextSource, warnings *[]string) (string, bool) {
	info, _, err := fsys.Stat(source.Path)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		return "", false
	}
	if info.Size() > bootstrapGuidanceMaxBytes {
		if warnings != nil {
			*warnings = append(*warnings, fmt.Sprintf("Skipped skill-reference discovery from %s because it is larger than %d bytes.", source.Path, bootstrapGuidanceMaxBytes))
		}
		return "", false
	}
	raw, _, err := fsys.ReadFile(source.Path)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func discoverBootstrapSkillsInDir(fsys *workspacefs.FS, base string, seen, existingRoles map[string]bool) []bootstrapSkillSource {
	entries, _, err := fsys.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []bootstrapSkillSource
	for _, entry := range entries {
		if !entry.IsDir || strings.HasPrefix(entry.Name, ".") {
			continue
		}
		skillID := normalizeBootstrapID(entry.Name)
		roleID := "skill_" + skillID
		if skillID == "" || seen[skillID] || existingRoles[skillID] || existingRoles[roleID] {
			continue
		}
		skillPath := path.Join(base, entry.Name, "SKILL.md")
		info, _, err := fsys.Stat(skillPath)
		if err != nil || info.IsDir() {
			continue
		}
		seen[skillID] = true
		out = append(out, bootstrapSkillSource{
			ID:   skillID,
			Path: skillPath,
		})
	}
	return out
}

func skillBaseDirsFromGuidance(sourcePath, body string) []string {
	sourceDir := path.Dir(filepath.ToSlash(strings.TrimSpace(sourcePath)))
	if sourceDir == "." {
		sourceDir = ""
	}
	seen := make(map[string]bool)
	var out []string
	for _, token := range guidancePathTokens(body) {
		for _, dir := range skillBaseDirsFromToken(sourceDir, token) {
			if seen[dir] {
				continue
			}
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return out
}

func guidancePathTokens(body string) []string {
	var out []string
	var builder strings.Builder
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		token := strings.Trim(builder.String(), "`'\"()[]{}<>.,;:")
		builder.Reset()
		if token != "" {
			out = append(out, token)
		}
	}
	for _, r := range body {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-', r == '/', r == '*', r == '@':
			builder.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}

func skillBaseDirsFromToken(sourceDir, token string) []string {
	token = strings.TrimSpace(strings.TrimPrefix(token, "@"))
	if token == "" || strings.Contains(token, "://") || strings.HasPrefix(token, "#") {
		return nil
	}
	token = filepath.ToSlash(token)
	token = strings.TrimPrefix(token, "./")
	if path.IsAbs(token) {
		return nil
	}
	cleaned := path.Clean(token)
	if sourceDir != "" && !strings.HasPrefix(cleaned, ".agents/") && !strings.HasPrefix(cleaned, ".hecate/") {
		cleaned = path.Clean(path.Join(sourceDir, cleaned))
	}
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return nil
	}
	lower := strings.ToLower(cleaned)
	switch {
	case strings.Contains(lower, "/*/skill.md"):
		idx := strings.Index(lower, "/*/skill.md")
		base := cleaned[:idx]
		if base != "" && base != "." {
			return []string{base}
		}
	case strings.HasSuffix(lower, "/skill.md"):
		skillDir := path.Dir(cleaned)
		base := path.Dir(skillDir)
		if base != "." && base != "/" {
			return []string{base}
		}
	case strings.HasSuffix(lower, "/skills/readme.md"):
		return []string{path.Dir(cleaned)}
	case strings.HasSuffix(lower, "/skills"):
		return []string{cleaned}
	default:
		if idx := strings.Index(lower, "/skills/"); idx >= 0 {
			return []string{cleaned[:idx+len("/skills")]}
		}
	}
	return nil
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
