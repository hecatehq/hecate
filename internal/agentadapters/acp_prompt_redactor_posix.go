//go:build !windows

package agentadapters

import (
	"path/filepath"
	"strings"
)

func acpPromptAliasesCaseInsensitive() bool { return false }

func acpPromptAliasEqual(left, right string) bool { return left == right }

func acpPromptPathKey(path string) string { return filepath.Clean(path) }

func acpPromptPathWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
