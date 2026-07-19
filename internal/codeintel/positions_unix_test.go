//go:build !windows

package codeintel

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/workspacefs"
)

func TestSourceCacheRejectsFIFOWithoutBlocking(t *testing.T) {
	workspace := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(workspace, "source.go"), 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	fsys, err := workspacefs.New(workspace)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, openErr := newSourceCache(fsys).openRelative("source.go")
		done <- openErr
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("FIFO error = %v, want regular-file rejection", err)
		}
	case <-time.After(time.Second):
		t.Fatal("source cache blocked while opening a FIFO")
	}
}
