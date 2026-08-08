//go:build linux

package localfs

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestLinuxInspectorUsesMostSpecificMount(t *testing.T) {
	inspector, err := newLinuxInspector([]byte(strings.Join([]string{
		"1 0 8:1 / / rw - ext4 /dev/root rw",
		"2 1 0:42 / /workspace/remote rw - fuse.sshfs remote rw",
	}, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if err := inspector.EnsurePath("/workspace/local.txt"); err != nil {
		t.Fatalf("local parent path: %v", err)
	}
	if err := inspector.EnsurePath("/workspace/remote/file.txt"); !errors.Is(err, ErrUnboundedFilesystem) {
		t.Fatalf("nested FUSE path error = %v", err)
	}
}

func TestLinuxInspectorRejectsUnboundedDescendantMount(t *testing.T) {
	inspector, err := newLinuxInspector([]byte(strings.Join([]string{
		"1 0 8:1 / / rw - ext4 /dev/root rw",
		"2 1 0:42 / /workspace/remote rw - fuse.sshfs remote rw",
	}, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if err := inspector.EnsureTree("/workspace"); !errors.Is(err, ErrUnboundedFilesystem) {
		t.Fatalf("workspace tree error = %v, want nested FUSE refusal", err)
	}
	if err := inspector.EnsureTree("/workspace/local"); err != nil {
		t.Fatalf("unrelated local subtree error = %v", err)
	}
}

func TestLinuxInspectorRejectsTruncatedMountInventory(t *testing.T) {
	_, err := newLinuxInspector(bytes.Repeat([]byte{'x'}, mountInfoLimit+1))
	if err == nil || !strings.Contains(err.Error(), "inventory exceeds") {
		t.Fatalf("oversized mount inventory error = %v", err)
	}
}

func TestLinuxInspectorRejectsStackedMountPoint(t *testing.T) {
	inspector, err := newLinuxInspector([]byte(strings.Join([]string{
		"1 0 8:1 / / rw - ext4 /dev/root rw",
		"2 1 8:2 / /workspace rw - ext4 /dev/data rw",
		"3 1 0:42 / /workspace rw - fuse.sshfs remote rw",
	}, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if err := inspector.EnsurePath("/workspace/file.txt"); !errors.Is(err, ErrUnboundedFilesystem) {
		t.Fatalf("stacked mount error = %v", err)
	}
}
