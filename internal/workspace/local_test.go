package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/sandbox"
)

// Step 1 of the workspace refactor delivers a pass-through wrapper.
// These tests confirm the wrapper actually routes to the underlying
// sandbox executor — same inputs, same outputs — so the orchestrator
// swap in step 2 can rely on the wrapper being a faithful proxy.

func TestLocalWorkspace_RunDelegatesToSandbox(t *testing.T) {
	t.Parallel()
	ws := NewLocalWorkspace()

	dir := t.TempDir()
	result, err := ws.Run(context.Background(), Command{
		Command:          "printf hello",
		WorkingDirectory: dir,
		Policy: Policy{
			AllowedRoot: dir,
			ReadOnly:    true,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(result.Stdout) != "hello" {
		t.Fatalf("stdout = %q; want hello", result.Stdout)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit = %d; want 0", result.ExitCode)
	}
}

func TestLocalWorkspace_WriteFileDelegatesToSandbox(t *testing.T) {
	t.Parallel()
	ws := NewLocalWorkspace()

	dir := t.TempDir()
	target := "note.txt"
	_, err := ws.WriteFile(context.Background(), FileRequest{
		Path:             target,
		Content:          "hi from workspace",
		WorkingDirectory: dir,
		Policy:           Policy{AllowedRoot: dir},
	})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, target))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "hi from workspace" {
		t.Fatalf("file content = %q", got)
	}
}

// Compile-time guard: a *sandbox.LocalExecutor isn't a Workspace
// directly (Workspace lives in this package), but a LocalWorkspace
// wrapping one is. Locks the invariant that step 2's swap from
// `sandbox.Executor` to `workspace.Workspace` won't accidentally
// accept the wrong type.
func TestLocalWorkspace_InterfaceSatisfaction(t *testing.T) {
	t.Parallel()
	var ws Workspace = NewLocalWorkspaceFromExecutor(sandbox.NewLocalExecutor())
	if ws == nil {
		t.Fatal("LocalWorkspace did not satisfy Workspace")
	}
}
