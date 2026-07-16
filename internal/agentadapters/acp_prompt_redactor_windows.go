//go:build windows

package agentadapters

import (
	"path/filepath"
	"strings"
)

func acpPromptAliasesCaseInsensitive() bool { return true }

// acpPromptAliasEqual follows Windows' case-insensitive path behavior and
// treats native/backslash and URI/slash spellings as equivalent. JSON-escaped
// aliases remain separately registered because their doubled separators have a
// different length.
func acpPromptAliasEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	leftNormalized := strings.ReplaceAll(left, "\\", "/")
	rightNormalized := strings.ReplaceAll(right, "\\", "/")
	return strings.EqualFold(leftNormalized, rightNormalized)
}

func acpPromptPathKey(path string) string {
	return strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
}

func acpPromptPathWithin(path, dir string) bool {
	path = normalizeACPPromptWindowsComparisonPath(path)
	dir = normalizeACPPromptWindowsComparisonPath(dir)
	if path == "" || dir == "" {
		return false
	}
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}

func normalizeACPPromptWindowsComparisonPath(path string) string {
	path = strings.ReplaceAll(filepath.Clean(path), "/", `\`)
	upper := strings.ToUpper(path)
	switch {
	case strings.HasPrefix(upper, `\\?\UNC\`), strings.HasPrefix(upper, `\\.\`):
		return ""
	case strings.HasPrefix(upper, `\\?\`):
		path = path[len(`\\?\`):]
	}
	return strings.ToLower(filepath.Clean(path))
}
