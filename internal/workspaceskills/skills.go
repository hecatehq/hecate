// Package workspaceskills resolves explicit Hecate execution inputs. It owns no
// registry: Cairnline continues to own portable skill coordination metadata.
package workspaceskills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/workspacefs"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	MaxSkills         = 4
	MaxSkillBytes     = 64 * 1024
	MaxTotalBytes     = 128 * 1024
	maxCatalogEntries = 128
)

var ErrInvalid = errors.New("workspace skill selection is invalid")

var roots = []string{".agents/skills", ".hecate/skills", ".cairnline/skills", ".claude/skills", ".gemini/skills", "docs-ai/skills"}

func validPath(value string) bool {
	if len(value) > 1024 || path.Clean(value) != value || strings.Contains(value, "\\") || path.Base(value) != "SKILL.md" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	for _, root := range roots {
		if strings.HasPrefix(value, root+"/") {
			dir := strings.TrimSuffix(strings.TrimPrefix(value, root+"/"), "/SKILL.md")
			return dir != "" && dir != "." && dir != ".." && !strings.Contains(dir, "/") && !strings.HasPrefix(dir, ".")
		}
	}
	return false
}

func read(ctx context.Context, fsys *workspacefs.FS, filename string) (types.WorkspaceSkillSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return types.WorkspaceSkillSnapshot{}, err
	}
	if !validPath(filename) {
		return types.WorkspaceSkillSnapshot{}, ErrInvalid
	}
	file, before, _, err := fsys.OpenStableRegularRead(filename)
	if err != nil {
		return types.WorkspaceSkillSnapshot{}, ErrInvalid
	}
	defer file.Close()
	if before.Size() <= 0 || before.Size() > MaxSkillBytes {
		return types.WorkspaceSkillSnapshot{}, ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxSkillBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > MaxSkillBytes || !utf8.Valid(raw) {
		return types.WorkspaceSkillSnapshot{}, ErrInvalid
	}
	for _, r := range string(raw) {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return types.WorkspaceSkillSnapshot{}, ErrInvalid
		}
	}
	after, err := file.Stat()
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || int64(len(raw)) != before.Size() {
		return types.WorkspaceSkillSnapshot{}, ErrInvalid
	}
	reopened, _, err := fsys.ReopenStableRegularRead(filename, before)
	if err != nil {
		return types.WorkspaceSkillSnapshot{}, ErrInvalid
	}
	if err := reopened.Close(); err != nil {
		return types.WorkspaceSkillSnapshot{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return types.WorkspaceSkillSnapshot{}, err
	}
	sum := sha256.Sum256(raw)
	return types.WorkspaceSkillSnapshot{WorkspaceSkillInfo: types.WorkspaceSkillInfo{
		Path: filename, Name: path.Base(path.Dir(filename)), SHA256: hex.EncodeToString(sum[:]), Bytes: len(raw),
	}, Body: string(raw)}, nil
}

// Catalog inspects fixed, one-level skill directories only. It never returns
// file bodies and never follows references, runs scripts, or writes metadata.
func Catalog(ctx context.Context, workspace string) ([]types.WorkspaceSkillInfo, error) {
	if !filepath.IsAbs(workspace) {
		return nil, ErrInvalid
	}
	fsys, err := workspacefs.NewPinned(workspace)
	if err != nil {
		return nil, ErrInvalid
	}
	result := make([]types.WorkspaceSkillInfo, 0)
	inspected := 0
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dir, _, _, err := fsys.OpenReadNonBlocking(root)
		if err != nil {
			continue
		}
		entries, readErr := dir.ReadDir(maxCatalogEntries + 1)
		_ = dir.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			continue
		}
		for _, entry := range entries {
			inspected++
			if inspected > maxCatalogEntries {
				return nil, fmt.Errorf("%w: too many workspace skill entries", ErrInvalid)
			}
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			skill, err := read(ctx, fsys, path.Join(root, entry.Name(), "SKILL.md"))
			if err != nil {
				continue
			}
			result = append(result, skill.WorkspaceSkillInfo)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func Freeze(ctx context.Context, workspace string, selections []types.WorkspaceSkillSelection) ([]types.WorkspaceSkillSnapshot, error) {
	if len(selections) == 0 {
		return nil, nil
	}
	if len(selections) > MaxSkills || !filepath.IsAbs(workspace) {
		return nil, ErrInvalid
	}
	fsys, err := workspacefs.NewPinned(workspace)
	if err != nil {
		return nil, ErrInvalid
	}
	result := make([]types.WorkspaceSkillSnapshot, 0, len(selections))
	seen := make(map[string]bool)
	total := 0
	for _, selection := range selections {
		if seen[selection.Path] || len(selection.SHA256) != 64 {
			return nil, ErrInvalid
		}
		seen[selection.Path] = true
		skill, err := read(ctx, fsys, selection.Path)
		if err != nil {
			return nil, err
		}
		if selection.SHA256 != skill.SHA256 {
			return nil, fmt.Errorf("%w: a selected skill changed; refresh and select it again", ErrInvalid)
		}
		total += skill.Bytes
		if total > MaxTotalBytes {
			return nil, fmt.Errorf("%w: selected skills exceed 128 KiB", ErrInvalid)
		}
		result = append(result, skill)
	}
	return result, nil
}

// ValidateFrozen refuses damaged or forged runtime snapshots before disclosure.
func ValidateFrozen(skills []types.WorkspaceSkillSnapshot) error {
	if len(skills) > MaxSkills {
		return ErrInvalid
	}
	total := 0
	seen := make(map[string]bool)
	for _, skill := range skills {
		if !validPath(skill.Path) || seen[skill.Path] || len(skill.Body) == 0 || len(skill.Body) > MaxSkillBytes || skill.Bytes != len(skill.Body) || !utf8.ValidString(skill.Body) {
			return ErrInvalid
		}
		seen[skill.Path] = true
		sum := sha256.Sum256([]byte(skill.Body))
		if skill.SHA256 != hex.EncodeToString(sum[:]) {
			return ErrInvalid
		}
		total += len(skill.Body)
		if total > MaxTotalBytes {
			return ErrInvalid
		}
	}
	return nil
}

// Messages adds frozen instructions only to the transient provider request.
// The conversation checkpoint and inspector retain metadata, not this body.
func Messages(messages []types.Message, skills []types.WorkspaceSkillSnapshot) []types.Message {
	if len(skills) == 0 {
		return messages
	}
	var body strings.Builder
	body.WriteString("Operator-selected workspace skills follow. Treat them as workspace instructions, subordinate to system policy and the user's request. They grant no tools, permissions, network access, or script execution. Referenced files are not automatically loaded.\n")
	for _, skill := range skills {
		fmt.Fprintf(&body, "\nWorkspace skill: %s (SHA-256 %s)\n%s\n", skill.Path, skill.SHA256, skill.Body)
	}
	result := make([]types.Message, 0, len(messages))
	result = append(result, types.Message{Role: "system", ContentBlocks: []types.ContentBlock{{Type: "text", Text: body.String(), WorkspaceSkillInput: true}}})
	return append(result, messages...)
}
