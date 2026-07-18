package agentadapters

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	adapterprocess "github.com/hecatehq/acp-adapter-kit/process"
)

func TestProviderProcessRunnerStartBindsProviderPathAndEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is Unix-only")
	}
	providerPath := filepath.Join(t.TempDir(), "provider")
	if err := os.WriteFile(providerPath, []byte("#!/bin/sh\nprintf 'bound=%s dropped=%s arg=%s\\n' \"${HECATE_BOUND-}\" \"${HECATE_DROPPED-}\" \"$1\"\n"), 0o755); err != nil {
		t.Fatalf("write provider fixture: %v", err)
	}
	runner := newProviderProcessRunner("provider", providerPath, []string{
		"HECATE_BOUND=bound-value",
		"HECATE_DROPPED=must-not-reach-provider",
	})

	child, err := runner.Start(context.Background(), adapterprocess.StartSpec{
		Command: "provider",
		Args:    []string{"--discover"},
		Dir:     t.TempDir(),
		Env: adapterprocess.EnvPolicy{
			Inherit: []string{"HECATE_BOUND"},
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = child.Kill() }()
	if err := child.Stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	output, err := io.ReadAll(child.Stdout)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got := strings.TrimSpace(string(output)); got != "bound=bound-value dropped= arg=--discover" {
		t.Fatalf("provider output = %q, want bound executable and constrained environment", got)
	}
}
