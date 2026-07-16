//go:build darwin

package agentadapters

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestACPPromptStageDarwinRequiresLocalFilesystem(t *testing.T) {
	t.Parallel()

	if localACPPromptDarwinFilesystem(uint32(unix.MNT_RDONLY)) {
		t.Fatal("Darwin filesystem flags without MNT_LOCAL were accepted")
	}
	if !localACPPromptDarwinFilesystem(uint32(unix.MNT_LOCAL)) {
		t.Fatal("Darwin MNT_LOCAL filesystem flags were rejected")
	}

	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open temporary directory: %v", err)
	}
	defer dir.Close()
	if err := verifyPrivateACPPromptDarwinFilesystem(dir); err != nil {
		t.Fatalf("temporary filesystem rejected: %v", err)
	}
}

func TestACPPromptStageDarwinRejectsParentExtendedACL(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "acl-parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatalf("create ACL parent: %v", err)
	}
	if output, err := exec.Command("/bin/chmod", "+a", "everyone allow read", parent).CombinedOutput(); err != nil {
		t.Fatalf("add parent ACL: %v: %s", err, output)
	}
	if _, _, err := createPrivateACPPromptStageDirAt(parent, nil); err == nil {
		t.Fatal("stage creation accepted a parent with an extended ACL")
	}
	assertDirectoryEmpty(t, parent)
}
