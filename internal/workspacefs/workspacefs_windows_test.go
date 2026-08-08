//go:build windows

package workspacefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenStableRegularReadRejectsHardlinkToOutsideWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.txt")
	if err := os.WriteFile(outside, []byte("private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked.txt")
	if err := os.Link(outside, linked); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}

	fsys, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	file, _, _, err := fsys.OpenStableRegularRead("linked.txt")
	if file != nil {
		file.Close()
	}
	if !errors.Is(err, ErrNotStableRegularFile) {
		t.Fatalf("OpenStableRegularRead() error = %v, want ErrNotStableRegularFile", err)
	}
}

func TestOpenStableRegularReadAllowsSingleLink(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fsys, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	file, _, _, err := fsys.OpenStableRegularRead("note.txt")
	if err != nil {
		t.Fatalf("OpenStableRegularRead(): %v", err)
	}
	file.Close()
}
