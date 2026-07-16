//go:build darwin || linux

package agentadapters

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func allowACPPromptStageSubstitutionForTest(t *testing.T, _ *acpPromptStage) {
	t.Helper()
}

func TestACPPromptStagePOSIXCreateDoesNotFollowSubstitutedParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "prompt-parent")
	moved := filepath.Join(root, "prompt-parent-retained")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatalf("create prompt parent: %v", err)
	}

	dir, identity, err := createPrivateACPPromptStageDirAt(parent, func() error {
		if err := os.Rename(parent, moved); err != nil {
			return err
		}
		return os.Mkdir(parent, 0o700)
	})
	if err == nil {
		stage := &acpPromptStage{dir: dir, identity: identity}
		_ = stage.cleanup()
		t.Fatal("stage creation accepted a substituted parent path")
	}

	assertDirectoryEmpty(t, parent)
	assertDirectoryEmpty(t, moved)
}

func TestACPPromptStagePOSIXRejectsWritableNonStickyParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "unsafe-parent")
	if err := os.Mkdir(parent, 0o777); err != nil {
		t.Fatalf("create unsafe parent: %v", err)
	}
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatalf("set unsafe parent mode: %v", err)
	}
	if _, _, err := createPrivateACPPromptStageDirAt(parent, nil); err == nil {
		t.Fatal("stage creation accepted a writable non-sticky parent")
	}
	assertDirectoryEmpty(t, parent)
}

func TestACPPromptStagePOSIXRemovalEvidenceFailsClosedOnStatError(t *testing.T) {
	t.Parallel()

	parentLookupCalled := false
	removed := privateACPPromptStageRemovalEvidence(nil, errors.New("transient stat failure"), func() error {
		parentLookupCalled = true
		return unix.ENOENT
	})
	if removed {
		t.Fatal("retained directory Stat error was treated as removal")
	}
	if parentLookupCalled {
		t.Fatal("parent lookup was used after retained directory Stat failed")
	}

	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open temporary directory: %v", err)
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil {
		t.Fatalf("stat temporary directory: %v", err)
	}
	if privateACPPromptStageRemovalEvidence(info, nil, func() error { return unix.EIO }) {
		t.Fatal("parent lookup I/O error was treated as removal")
	}
	if !privateACPPromptStageRemovalEvidence(info, nil, func() error { return unix.ENOENT }) {
		t.Fatal("parent-relative ENOENT did not confirm removal")
	}
}

func assertDirectoryEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read directory %q: %v", path, err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory %q contains unexpected entries: %#v", path, entries)
	}
}
