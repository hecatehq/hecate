//go:build !windows

package workspacefs

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

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
		if got.err != nil {
			t.Fatalf("OpenReadNonBlocking() error = %v", got.err)
		}
		if got.mode.IsRegular() || got.mode.IsDir() {
			t.Fatalf("FIFO mode = %v, want non-regular non-directory", got.mode)
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
