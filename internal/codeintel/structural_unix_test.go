//go:build !windows

package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/hecatehq/hecate/internal/processrunner"
	"github.com/hecatehq/hecate/internal/sandbox"
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

func TestService_StructuralSearchPreservesWhitespaceInProviderResultPath(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	name := "sample.go "
	sourcePath := filepath.Join(workspace, name)
	if err := os.WriteFile(sourcePath, []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write whitespace path: %v", err)
	}
	canonical, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		t.Fatalf("canonicalize source: %v", err)
	}
	stdout := `{"file":` + quoteJSON(canonical) + `,"text":"package sample","range":{"start":{"line":0,"column":0},"end":{"line":0,"column":7}}}`
	service := NewService()
	service.runner = &recordingRunner{result: processrunner.Result{Stdout: stdout}}
	setProviderPath(service, "ast-grep", executableFixture(t, t.TempDir(), "ast-grep"))

	result, err := service.Query(context.Background(), workspace, Request{
		Operation: OpStructuralSearch,
		Path:      ".",
		Language:  "go",
		Query:     "$X",
	})
	if err != nil {
		t.Fatalf("structural search: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Path != name {
		t.Fatalf("items = %+v, want exact whitespace path %q", result.Items, name)
	}
}
