package just_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDocsEnvRecipeFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX rg fixture")
	}
	just, err := exec.LookPath("just")
	if err != nil {
		t.Skip("just binary not available")
	}
	for _, tc := range []struct {
		name        string
		status      string
		wantSuccess bool
	}{
		{"no matches", "1", true},
		{"obsolete variables found", "0", false},
		{"search error", "2", false},
		{"missing executable", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			if tc.status != "" {
				if err := os.WriteFile(filepath.Join(bin, "rg"), []byte("#!/bin/sh\nexit "+tc.status+"\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(just, "--shell", "/bin/sh", "docs-env-check")
			cmd.Dir = ".."
			cmd.Env = append(os.Environ(), "PATH="+bin)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.wantSuccess {
				t.Fatalf("success=%v, want %v: %s", err == nil, tc.wantSuccess, out)
			}
		})
	}
}

func TestPostgresRecipeRequiresDatabase(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just binary not available")
	}
	for _, value := range []string{"", " \t "} {
		cmd := exec.Command("just", "test-postgres")
		cmd.Dir = ".."
		cmd.Env = append(os.Environ(), "HECATE_POSTGRES_TEST_URL="+value)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "Set HECATE_POSTGRES_TEST_URL") || strings.Contains(string(out), "go test") {
			t.Fatalf("missing URL must fail before tests: err=%v output=%s", err, out)
		}
	}
}

func TestPostgresRecipeRunsStoreAndBinaryChecks(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just binary not available")
	}
	cmd := exec.Command("just", "--dry-run", "test-postgres")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	for _, want := range []string{"go test -race -count=1", "./cmd/hecate", "./internal/projectruntime", "./internal/chatattachments", "go test -tags e2e -count=1", "-run '^TestPostgres'"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("Postgres recipe missing %q: %s", want, out)
		}
	}
}

func TestRuntimeRecipesStartServeCommand(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just binary not available")
	}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "run",
			args: []string{"--dry-run", "run"},
			want: "go run ./cmd/hecate serve",
		},
		{
			name: "dev",
			args: []string{"--dry-run", "dev"},
			want: "go run ./cmd/hecate serve",
		},
		{
			name: "dev agent adapters",
			args: []string{"--dry-run", "dev-agent-adapters", "all=missing"},
			want: `HECATE_AGENT_ADAPTER_DEV_OVERRIDES="all=missing" GOCACHE="$PWD/.gocache" go run ./cmd/hecate serve`,
		},
		{
			name: "prebuilt serve",
			args: []string{"--dry-run", "serve"},
			want: "./hecate serve",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("just", tt.args...)
			cmd.Dir = ".."
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("just %s failed: %v\n%s", strings.Join(tt.args, " "), err, out)
			}
			if !strings.Contains(string(out), tt.want) {
				t.Fatalf("just %s output missing %q:\n%s", strings.Join(tt.args, " "), tt.want, out)
			}
		})
	}
}
