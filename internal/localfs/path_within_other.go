//go:build !darwin

package localfs

import (
	"path/filepath"
	"strings"
)

// PathWithinDirectory reports whether candidate is root or one of its
// descendants using the host platform's ordinary filepath semantics.
func PathWithinDirectory(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
