//go:build !windows

package codeintel

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/hecatehq/hecate/internal/processrunner"
)

func TestService_StructuralSearchRejectsFIFOWithoutRunningProvider(t *testing.T) {
	workspace := t.TempDir()
	fifoPath := filepath.Join(workspace, "events.go")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	runner := &recordingRunner{run: func(processrunner.Request) (processrunner.Result, error) {
		t.Fatal("structural provider must not run for a FIFO target")
		return processrunner.Result{}, nil
	}}
	service := NewService()
	service.runner = runner
	setProviderPath(service, "ast-grep", executableFixture(t, t.TempDir(), "ast-grep"))

	_, err := service.Query(context.Background(), workspace, Request{
		Operation: OpStructuralSearch,
		Path:      "events.go",
		Query:     "$X",
	})
	if err == nil || !strings.Contains(err.Error(), "regular file or directory") {
		t.Fatalf("FIFO error = %v, want special-file rejection", err)
	}
	if len(runner.requests) != 0 {
		t.Fatalf("runner requests = %d, want 0", len(runner.requests))
	}
}
