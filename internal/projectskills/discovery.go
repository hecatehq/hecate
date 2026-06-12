package projectskills

import (
	"context"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"

	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/workspacefs"
)

const (
	guidanceMaxBytes = 64 * 1024
	skillMaxBytes    = 64 * 1024
)

type skillBaseDir struct {
	Path                   string
	SourceContextSourceIDs []string
}

// Discover returns project skill metadata only. It may read bounded local
// guidance and SKILL.md files, but it never returns or stores skill bodies.
func Discover(ctx context.Context, project projects.Project) ([]Skill, []string) {
	var out []Skill
	var warnings []string
	byID := make(map[string]Skill)
	for _, root := range project.Roots {
		if !root.Active || strings.TrimSpace(root.Path) == "" {
			continue
		}
		select {
		case <-ctx.Done():
			return sortedSkillMap(byID), append(warnings, ctx.Err().Error())
		default:
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
		for _, base := range skillBaseDirs(fsys, root, project.ContextSources, &warnings) {
			discovered := discoverInBaseDir(fsys, root.ID, base, &warnings)
			for _, skill := range discovered {
				if existing, ok := byID[skill.ID]; ok {
					existing.Status = StatusConflict
					existing.Warnings = appendUniqueStrings(existing.Warnings,
						fmt.Sprintf("Skill id %q is declared by multiple paths: %s and %s.", skill.ID, existing.Path, skill.Path),
					)
					existing.SourceContextSourceIDs = appendUniqueStrings(existing.SourceContextSourceIDs, skill.SourceContextSourceIDs...)
					byID[skill.ID] = existing
					continue
				}
				byID[skill.ID] = skill
			}
		}
	}
	out = sortedSkillMap(byID)
	return out, warnings
}

func sortedSkillMap(items map[string]Skill) []Skill {
	if len(items) == 0 {
		return nil
	}
	out := make([]Skill, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	sortSkills(out)
	return out
}

func skillBaseDirs(fsys *workspacefs.FS, root projects.Root, sources []projects.ContextSource, warnings *[]string) []skillBaseDir {
	dirs := []skillBaseDir{
		{Path: ".agents/skills"},
		{Path: ".hecate/skills"},
	}
	seen := map[string]int{
		".agents/skills": 0,
		".hecate/skills": 1,
	}
	for _, source := range sources {
		if !guidanceSourceForRoot(source, root.ID) {
			continue
		}
		body, ok := readGuidanceSource(fsys, source, warnings)
		if !ok {
			continue
		}
		for _, dir := range skillBaseDirsFromGuidance(source.Path, body) {
			if shouldSkipSkillDiscoveryPath(dir) {
				continue
			}
			if idx, ok := seen[dir]; ok {
				dirs[idx].SourceContextSourceIDs = appendUniqueStrings(dirs[idx].SourceContextSourceIDs, source.ID)
				continue
			}
			seen[dir] = len(dirs)
			dirs = append(dirs, skillBaseDir{
				Path:                   dir,
				SourceContextSourceIDs: normalizeStringSlice([]string{source.ID}),
			})
		}
	}
	return dirs
}

func guidanceSourceForRoot(source projects.ContextSource, rootID string) bool {
	if !source.Enabled || strings.TrimSpace(source.Path) == "" {
		return false
	}
	if shouldSkipSkillDiscoveryPath(source.Path) {
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

func readGuidanceSource(fsys *workspacefs.FS, source projects.ContextSource, warnings *[]string) (string, bool) {
	info, _, err := fsys.Stat(source.Path)
	if err != nil || info.IsDir() {
		return "", false
	}
	if info.Size() > guidanceMaxBytes {
		if warnings != nil {
			*warnings = append(*warnings, fmt.Sprintf("Skipped skill-reference discovery from %s because it is larger than %d bytes.", source.Path, guidanceMaxBytes))
		}
		return "", false
	}
	raw, _, err := fsys.ReadFile(source.Path)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func discoverInBaseDir(fsys *workspacefs.FS, rootID string, base skillBaseDir, warnings *[]string) []Skill {
	if shouldSkipSkillDiscoveryPath(base.Path) {
		return nil
	}
	entries, _, err := fsys.ReadDir(base.Path)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, entry := range entries {
		if !entry.IsDir || strings.HasPrefix(entry.Name, ".") {
			continue
		}
		skillID := normalizeID(entry.Name)
		if skillID == "" {
			continue
		}
		skillPath := path.Join(base.Path, entry.Name, "SKILL.md")
		info, _, err := fsys.Stat(skillPath)
		if err != nil || info.IsDir() {
			continue
		}
		title, description, skillWarnings, status := readSkillMetadata(fsys, skillID, skillPath, info.Size())
		if status == StatusInvalid && warnings != nil {
			*warnings = append(*warnings, skillWarnings...)
		}
		out = append(out, Skill{
			ID:                     skillID,
			Title:                  title,
			Description:            description,
			Path:                   skillPath,
			RootID:                 rootID,
			Format:                 FormatSkillMD,
			Enabled:                true,
			Status:                 status,
			TrustLabel:             TrustWorkspaceSkill,
			SourceContextSourceIDs: base.SourceContextSourceIDs,
			Warnings:               skillWarnings,
		})
	}
	return out
}

func readSkillMetadata(fsys *workspacefs.FS, skillID, skillPath string, size int64) (string, string, []string, string) {
	title := titleFromID(skillID)
	var warnings []string
	status := StatusAvailable
	if size > skillMaxBytes {
		warnings = append(warnings, fmt.Sprintf("Skipped metadata parsing for %s because it is larger than %d bytes.", skillPath, skillMaxBytes))
		return title, "", warnings, StatusInvalid
	}
	file, _, err := fsys.Open(skillPath)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("Failed to read skill metadata from %s: %v.", skillPath, err))
		return title, "", warnings, StatusInvalid
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, skillMaxBytes+1))
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("Failed to read skill metadata from %s: %v.", skillPath, err))
		return title, "", warnings, StatusInvalid
	}
	body := string(raw)
	if name, description := parseFrontmatterMetadata(body); name != "" || description != "" {
		if name != "" {
			title = name
		}
		return title, description, warnings, status
	}
	if heading := firstMarkdownHeading(body); heading != "" {
		title = heading
	}
	return title, "", warnings, status
}

func parseFrontmatterMetadata(body string) (string, string) {
	body = strings.TrimPrefix(body, "\ufeff")
	if !strings.HasPrefix(body, "---\n") && !strings.HasPrefix(body, "---\r\n") {
		return "", ""
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var name, description string
	for idx := 1; idx < len(lines); idx++ {
		line := strings.TrimSpace(lines[idx])
		if line == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name", "title":
			name = value
		case "description":
			description = value
		}
	}
	return name, description
}

func firstMarkdownHeading(body string) string {
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
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

func shouldSkipSkillDiscoveryPath(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	rel = strings.TrimPrefix(rel, "./")
	if rel == "" {
		return false
	}
	rel = path.Clean(rel)
	return rel == ".worktrees" || strings.HasPrefix(rel, ".worktrees/") ||
		rel == ".claude/worktrees" || strings.HasPrefix(rel, ".claude/worktrees/")
}
