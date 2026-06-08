package api

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hecatehq/hecate/internal/projects"
)

const maxProjectInstructionDiscoveryDepth = 8

func (h *Handler) HandleDiscoverProjectContextSources(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	project, ok, err := h.projects.Get(r.Context(), projectID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	discovered, err := discoverProjectInstructionSources(project)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	project, err = h.projects.Update(r.Context(), projectID, func(item *projects.Project) {
		item.ContextSources = mergeDiscoveredContextSources(item.ContextSources, discovered)
	})
	if errors.Is(err, projects.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectResponse{Object: "project", Data: renderProject(project)})
}

func discoverProjectInstructionSources(project projects.Project) ([]projects.ContextSource, error) {
	var out []projects.ContextSource
	for _, root := range project.Roots {
		if !root.Active {
			continue
		}
		rootPath := strings.TrimSpace(root.Path)
		if rootPath == "" {
			continue
		}
		if !filepath.IsAbs(rootPath) {
			return nil, errors.New("project context discovery requires absolute project root paths")
		}
		info, err := os.Stat(rootPath)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, errors.New("project context discovery requires directory roots")
		}
		rootID := strings.TrimSpace(root.ID)
		err = filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, relErr := filepath.Rel(rootPath, path)
			if relErr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if rel == "." {
				return nil
			}
			depth := pathDepth(rel)
			if d.IsDir() {
				if shouldSkipInstructionDiscoveryDir(d.Name()) || depth > maxProjectInstructionDiscoveryDepth {
					return filepath.SkipDir
				}
				return nil
			}
			if depth > maxProjectInstructionDiscoveryDepth+1 {
				return nil
			}
			if source, ok := classifyInstructionSource(rel, rootID); ok {
				out = append(out, source)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

func classifyInstructionSource(rel, rootID string) (projects.ContextSource, bool) {
	base := pathBase(rel)
	lower := strings.ToLower(rel)
	source := projects.ContextSource{
		ID:             newOpaqueTaskResourceID("ctxsrc"),
		Path:           rel,
		Title:          rel,
		Enabled:        true,
		TrustLabel:     contextTrustWorkspaceGuidance,
		SourceCategory: "workspace_guidance",
		Metadata: map[string]string{
			"root_id": rootID,
		},
	}
	switch {
	case base == "AGENTS.md":
		source.Kind = "workspace_instruction"
		source.Format = "agents_md"
		source.Scope = instructionScopeForPath(rel)
	case rel == "CLAUDE.md" || rel == ".claude/CLAUDE.md" || base == "CLAUDE.local.md":
		source.Kind = "host_instruction"
		source.Format = "claude_md"
		source.Scope = instructionScopeForPath(rel)
		source.Metadata["host"] = "claude"
	case rel == "GEMINI.md":
		source.Kind = "host_instruction"
		source.Format = "gemini_md"
		source.Scope = "workspace"
		source.Metadata["host"] = "gemini"
	case strings.HasPrefix(lower, ".cursor/rules/"):
		source.Kind = "host_rule"
		source.Format = "cursor_rule"
		source.Scope = "metadata_only"
		source.Metadata["host"] = "cursor"
	case lower == ".github/copilot-instructions.md":
		source.Kind = "host_instruction"
		source.Format = "copilot_instruction"
		source.Scope = "metadata_only"
		source.Metadata["host"] = "github_copilot"
	case strings.HasPrefix(lower, ".github/instructions/") && strings.HasSuffix(lower, ".instructions.md"):
		source.Kind = "path_instruction"
		source.Format = "copilot_instruction"
		source.Scope = "metadata_only"
		source.Metadata["host"] = "github_copilot"
	case strings.HasPrefix(lower, ".devin/rules/"):
		source.Kind = "host_rule"
		source.Format = "devin_rule"
		source.Scope = "metadata_only"
		source.Metadata["host"] = "devin"
	case strings.HasPrefix(lower, ".windsurf/rules/"):
		source.Kind = "host_rule"
		source.Format = "windsurf_rule"
		source.Scope = "metadata_only"
		source.Metadata["host"] = "windsurf"
	case strings.HasPrefix(lower, ".gemini/commands/") && strings.HasSuffix(lower, ".toml"):
		source.Kind = "host_command"
		source.Format = "gemini_command"
		source.Scope = "metadata_only"
		source.Metadata["host"] = "gemini"
	default:
		return projects.ContextSource{}, false
	}
	return source, true
}

func mergeDiscoveredContextSources(existing, discovered []projects.ContextSource) []projects.ContextSource {
	byKey := make(map[string]projects.ContextSource, len(existing)+len(discovered))
	order := make([]string, 0, len(existing)+len(discovered))
	add := func(source projects.ContextSource) {
		key := contextSourceMergeKey(source)
		if key == "\x00\x00" {
			return
		}
		if _, ok := byKey[key]; !ok {
			legacyKey := contextSourceLegacyMergeKey(source)
			if legacyKey != key {
				if previous, ok := byKey[legacyKey]; ok {
					delete(byKey, legacyKey)
					for i, item := range order {
						if item == legacyKey {
							order[i] = key
							break
						}
					}
					source.ID = firstNonEmptyString(previous.ID, source.ID)
					source.Enabled = previous.Enabled
					source.CreatedAt = previous.CreatedAt
				}
			}
		}
		if previous, ok := byKey[key]; ok {
			source.ID = firstNonEmptyString(previous.ID, source.ID)
			source.Enabled = previous.Enabled
			source.CreatedAt = previous.CreatedAt
		} else {
			order = append(order, key)
		}
		byKey[key] = source
	}
	for _, source := range existing {
		add(source)
	}
	for _, source := range discovered {
		add(source)
	}
	out := make([]projects.ContextSource, 0, len(order))
	for _, key := range order {
		out = append(out, byKey[key])
	}
	return out
}

func contextSourceMergeKey(source projects.ContextSource) string {
	rootID := ""
	if source.Metadata != nil {
		rootID = strings.TrimSpace(source.Metadata["root_id"])
	}
	return contextSourceLegacyMergeKey(source) + "\x00" + rootID
}

func contextSourceLegacyMergeKey(source projects.ContextSource) string {
	return strings.TrimSpace(source.Kind) + "\x00" + strings.TrimSpace(source.Path)
}

func instructionScopeForPath(rel string) string {
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "." || dir == "" {
		return "workspace"
	}
	return "path:" + dir
}

func shouldSkipInstructionDiscoveryDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", ".next", ".turbo", ".cache", ".gomodcache", "target", "coverage":
		return true
	default:
		return false
	}
}

func pathDepth(rel string) int {
	if rel == "" || rel == "." {
		return 0
	}
	return strings.Count(filepath.ToSlash(rel), "/")
}

func pathBase(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return parts[len(parts)-1]
}
