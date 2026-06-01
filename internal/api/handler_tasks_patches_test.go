package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPatchWorkspaceTarget_AllowsAbsolutePathInsideWorkspace(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	target := filepath.Join(workspace, "nested", "file.txt")

	fsys, rel, err := patchWorkspaceTarget(workspace, target)
	if err != nil {
		t.Fatalf("patchWorkspaceTarget: %v", err)
	}
	if rel != filepath.Join("nested", "file.txt") {
		t.Fatalf("rel = %q, want nested/file.txt", rel)
	}
	if _, err := fsys.WriteFile(rel, []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile through resolved target: %v", err)
	}
}

func TestPatchWorkspaceTarget_RejectsAbsolutePathOutsideWorkspace(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape.txt")

	_, _, err := patchWorkspaceTarget(workspace, outside)
	if err == nil || !strings.Contains(err.Error(), "outside the run workspace") {
		t.Fatalf("error = %v, want outside workspace rejection", err)
	}
}

func TestPatchWorkspaceTarget_RejectsSymlinkComponentEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires elevated privileges on Windows")
	}
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(workspace, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	for _, path := range []string{
		filepath.Join("linked", "escape.txt"),
		filepath.Join(workspace, "linked", "escape.txt"),
	} {
		_, _, err := patchWorkspaceTarget(workspace, path)
		if err == nil || !strings.Contains(err.Error(), "outside the run workspace") {
			t.Fatalf("path %q error = %v, want outside workspace rejection", path, err)
		}
	}
}
