//go:build !windows

package gitrunner

import "testing"

func ensureStatCheckFixtureCTimeChanged(t *testing.T, _ string) {
	t.Helper()
	// The content rewrite already changes ctime on Unix.
}
