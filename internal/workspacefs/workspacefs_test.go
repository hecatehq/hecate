package workspacefs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSafeJoinRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	cases := []string{
		"../outside",
		filepath.Join("..", "outside"),
		filepath.Clean(filepath.Join("nested", "..", "..", "outside")),
	}
	for _, relativePath := range cases {
		t.Run(relativePath, func(t *testing.T) {
			if _, err := SafeJoin(root, relativePath); err == nil {
				t.Fatal("expected escaping path to be rejected")
			}
		})
	}
}

func TestSafeJoinRejectsExistingSymlinkComponents(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if _, err := SafeJoin(root, filepath.Join("linked", "file.txt")); err == nil {
		t.Fatal("expected symlink component to be rejected")
	}
}

func TestSafeJoinAllowsNestedLocalPaths(t *testing.T) {
	root := t.TempDir()
	got, err := SafeJoin(root, filepath.Join("nested", "file.txt"))
	if err != nil {
		t.Fatalf("SafeJoin: %v", err)
	}
	want := filepath.Join(root, "nested", "file.txt")
	if got != want {
		t.Errorf("joined path = %q, want %q", got, want)
	}
}

func TestWriteFileRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	fsys, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = fsys.WriteFile(filepath.Join("linked", "owned.txt"), []byte("owned"), 0o644)
	if err == nil {
		t.Fatal("expected write through symlink component to be rejected")
	}
	if !strings.Contains(err.Error(), "symlink component") {
		t.Fatalf("error = %q, want symlink component", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "owned.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("outside file stat error = %v, want not exist", statErr)
	}
}

func TestAppendFileRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	fsys, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = fsys.AppendFile(filepath.Join("linked", "owned.txt"), []byte("owned"), 0o644)
	if err == nil {
		t.Fatal("expected append through symlink component to be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "owned.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("outside file stat error = %v, want not exist", statErr)
	}
}

func TestOpenStatAndRemoveRejectSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	fsys, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, _, err := fsys.Open(filepath.Join("linked", "secret.txt")); err == nil {
		t.Fatal("expected open through symlink component to be rejected")
	}
	if _, _, err := fsys.Stat(filepath.Join("linked", "secret.txt")); err == nil {
		t.Fatal("expected stat through symlink component to be rejected")
	}
	if _, err := fsys.Remove(filepath.Join("linked", "secret.txt")); err == nil {
		t.Fatal("expected remove through symlink component to be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "secret.txt")); statErr != nil {
		t.Fatalf("outside file stat error = %v, want preserved", statErr)
	}
}

func TestOpenStatAndRemoveUseWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	fsys, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	info, abs, err := fsys.Stat("note.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != int64(len("hello")) {
		t.Fatalf("size = %d, want %d", info.Size(), len("hello"))
	}
	if abs != filepath.Join(root, "note.txt") {
		t.Fatalf("abs = %q", abs)
	}

	file, _, err := fsys.Open("note.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer file.Close()
	buf := make([]byte, 5)
	n, err := file.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := string(buf[:n]); got != "hello" {
		t.Fatalf("read = %q, want hello", got)
	}

	if _, err := fsys.Remove("note.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "note.txt")); !os.IsNotExist(err) {
		t.Fatalf("stat after remove = %v, want not exist", err)
	}
}

func TestReadDirRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	fsys, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, _, err := fsys.ReadDir("linked"); err == nil {
		t.Fatal("expected read dir through symlink component to be rejected")
	}
}

func TestWalkDirRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	fsys, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = fsys.WalkDir("linked", func(string, string, DirEntry) error {
		t.Fatal("visit should not be called")
		return nil
	})
	if err == nil {
		t.Fatal("expected walk through symlink component to be rejected")
	}
}

func TestWalkDirVisitsWorkspaceRelativePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "b", "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	fsys, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var got []string
	err = fsys.WalkDir("a", func(_ string, rel string, entry DirEntry) error {
		if !entry.IsDir {
			got = append(got, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	if want := []string{"a/b/file.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("walked files = %#v, want %#v", got, want)
	}
}
