//go:build !windows

package workspacefs

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestOpenStableRegularReadRejectsSymlinkAndFIFO(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "events.pipe"), 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	fsys, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, path := range []string{"link.txt", "events.pipe"} {
		file, _, _, openErr := fsys.OpenStableRegularRead(path)
		if file != nil {
			file.Close()
		}
		if openErr == nil {
			t.Fatalf("OpenStableRegularRead(%q) succeeded, want refusal", path)
		}
		if path == "events.pipe" && !errors.Is(openErr, ErrNotStableRegularFile) {
			t.Fatalf("OpenStableRegularRead(%q) error = %v, want ErrNotStableRegularFile", path, openErr)
		}
	}
}

func TestOpenStableRegularReadRejectsHardlinksDirectoriesAndSockets(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "hws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, "source.txt"), filepath.Join(root, "linked.txt")); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}
	socketPath := filepath.Join(root, "agent.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Skipf("Unix sockets unavailable: %v", err)
	}
	defer listener.Close()
	fSys, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"source.txt", "linked.txt", ".", "agent.sock"} {
		file, _, _, openErr := fSys.OpenStableRegularRead(path)
		if file != nil {
			file.Close()
		}
		if !errors.Is(openErr, ErrNotStableRegularFile) {
			t.Fatalf("OpenStableRegularRead(%q) error = %v, want ErrNotStableRegularFile", path, openErr)
		}
	}
}

func TestOpenStableRegularReadRejectsIntermediateSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "private", "note.txt"), []byte("must not leak\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("private", filepath.Join(root, "redirect")); err != nil {
		t.Fatal(err)
	}
	fsys, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	file, _, _, err := fsys.OpenStableRegularRead("redirect/note.txt")
	if file != nil {
		file.Close()
	}
	if err == nil {
		t.Fatal("OpenStableRegularRead followed an intermediate symlink")
	}
}

func TestWalkDirContextRejectsIntermediateSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "private", "note.txt"), []byte("must not leak\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("private", filepath.Join(root, "redirect")); err != nil {
		t.Fatal(err)
	}
	fsys, err := NewPinned(root)
	if err != nil {
		t.Fatalf("NewPinned: %v", err)
	}
	err = fsys.WalkDirContext(context.Background(), "redirect", func(string, string, DirEntry) error { return nil })
	if err == nil {
		t.Fatal("WalkDirContext followed an intermediate symlink")
	}
}

func TestOpenStableDirectoryReadRejectsPathReplacementBetweenWalks(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	file, _, _, err := openStableDirectoryReadWithHook(root, "nested", func() {
		if renameErr := os.Rename(nested, filepath.Join(root, "old")); renameErr != nil {
			t.Fatalf("rename original directory: %v", renameErr)
		}
		if mkdirErr := os.Mkdir(nested, 0o700); mkdirErr != nil {
			t.Fatalf("create replacement directory: %v", mkdirErr)
		}
	})
	if file != nil {
		_ = file.Close()
	}
	if !errors.Is(err, ErrNotStableRegularFile) {
		t.Fatalf("openStableDirectoryReadWithHook error = %v, want ErrNotStableRegularFile", err)
	}
}

func TestWalkDirContextRejectsDirectoryReplacementDuringEnumeration(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "old.txt"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fsys, err := NewPinned(root)
	if err != nil {
		t.Fatalf("NewPinned: %v", err)
	}
	var once sync.Once
	fsys.beforeStableDirectoryPostVerify = func(relativePath string) {
		if relativePath != "nested" {
			return
		}
		once.Do(func() {
			if renameErr := os.Rename(nested, filepath.Join(root, "old")); renameErr != nil {
				t.Fatalf("rename enumerated directory: %v", renameErr)
			}
			if mkdirErr := os.Mkdir(nested, 0o700); mkdirErr != nil {
				t.Fatalf("create replacement directory: %v", mkdirErr)
			}
		})
	}
	err = fsys.WalkDirContext(context.Background(), ".", func(string, string, DirEntry) error { return nil })
	if !errors.Is(err, ErrNotStableRegularFile) {
		t.Fatalf("WalkDirContext error = %v, want ErrNotStableRegularFile", err)
	}
}

func TestWalkDirContextVerifiesDirectoryBeforeReturningVisitorStop(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "old.txt"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fsys, err := NewPinned(root)
	if err != nil {
		t.Fatalf("NewPinned: %v", err)
	}
	fsys.beforeStableDirectoryPostVerify = func(relativePath string) {
		if relativePath != "nested" {
			return
		}
		if renameErr := os.Rename(nested, filepath.Join(root, "old")); renameErr != nil {
			t.Fatalf("rename enumerated directory: %v", renameErr)
		}
		if mkdirErr := os.Mkdir(nested, 0o700); mkdirErr != nil {
			t.Fatalf("create replacement directory: %v", mkdirErr)
		}
	}
	stop := errors.New("visitor stop")
	err = fsys.WalkDirContext(context.Background(), ".", func(_ string, relPath string, _ DirEntry) error {
		if relPath == filepath.Join("nested", "old.txt") {
			return stop
		}
		return nil
	})
	if !errors.Is(err, ErrNotStableRegularFile) {
		t.Fatalf("WalkDirContext error = %v, want post-enumeration ErrNotStableRegularFile before visitor stop", err)
	}
}

func TestOpenReadNonBlockingDoesNotWaitForFIFO(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "events.pipe"), 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	fsys, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	type result struct {
		mode os.FileMode
		err  error
	}
	done := make(chan result, 1)
	go func() {
		file, info, _, openErr := fsys.OpenReadNonBlocking("events.pipe")
		if file != nil {
			file.Close()
		}
		if openErr != nil {
			done <- result{err: openErr}
			return
		}
		done <- result{mode: info.Mode()}
	}()
	select {
	case got := <-done:
		if !errors.Is(got.err, ErrNotStableRegularFile) {
			t.Fatalf("OpenReadNonBlocking() error = %v, want special-file refusal", got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("OpenReadNonBlocking() blocked on FIFO")
	}

	walkDone := make(chan result, 1)
	go func() {
		var visitedMode os.FileMode
		walkErr := fsys.WalkDirContext(context.Background(), "events.pipe", func(_ string, _ string, entry DirEntry) error {
			visitedMode = entry.Type
			return nil
		})
		walkDone <- result{mode: visitedMode, err: walkErr}
	}()
	select {
	case got := <-walkDone:
		if got.err != nil {
			t.Fatalf("WalkDirContext() FIFO error = %v", got.err)
		}
		if got.mode&os.ModeNamedPipe == 0 {
			t.Fatalf("WalkDirContext() mode = %v, want named pipe", got.mode)
		}
	case <-time.After(time.Second):
		t.Fatal("WalkDirContext() blocked on FIFO")
	}
}

func TestWalkDirContextHandlesDeepTreeWithoutRetainingDirectoryHandles(t *testing.T) {
	fdDirectory := "/proc/self/fd"
	baselineFDs, err := os.ReadDir(fdDirectory)
	if err != nil {
		fdDirectory = "/dev/fd"
		baselineFDs, err = os.ReadDir(fdDirectory)
	}
	if err != nil {
		t.Skipf("open-descriptor view unavailable: %v", err)
	}
	root := t.TempDir()
	current := root
	const depth = 300
	for index := 0; index < depth; index++ {
		current = filepath.Join(current, "d")
		if err := os.Mkdir(current, 0o755); err != nil {
			t.Fatalf("mkdir depth %d: %v", index, err)
		}
	}
	if err := os.WriteFile(filepath.Join(current, "leaf.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fsys, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	visits := 0
	leafFDs := -1
	err = fsys.WalkDirContext(context.Background(), ".", func(_ string, _ string, entry DirEntry) error {
		visits++
		if entry.Name == "leaf.txt" {
			openFDs, readErr := os.ReadDir(fdDirectory)
			if readErr != nil {
				return readErr
			}
			leafFDs = len(openFDs)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDirContext(): %v", err)
	}
	if want := depth + 2; visits != want {
		t.Fatalf("visits = %d, want %d", visits, want)
	}
	if leafFDs < 0 {
		t.Fatal("leaf was not visited")
	}
	if delta := leafFDs - len(baselineFDs); delta > 8 {
		t.Fatalf("open descriptors grew by %d at depth %d; want at most 8", delta, depth)
	}
}
