package just_test

import (
	"os/exec"
	"strings"
	"testing"
)

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
