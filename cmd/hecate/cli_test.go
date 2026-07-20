package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/version"
)

func TestCLI_VersionAliases(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"version"},
		{"--version"},
		{"-v"},
	} {
		args := args
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			cmd := newRootCommand()
			cmd.SetArgs(args)
			cmd.SetOut(&stdout)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute(%v): %v", args, err)
			}
			if got := strings.TrimSpace(stdout.String()); got != version.Version {
				t.Fatalf("stdout = %q, want %q", got, version.Version)
			}
		})
	}
}

func TestCLI_HelpListsCommandTree(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := newRootCommand()
	cmd.SetArgs([]string{"help"})
	cmd.SetOut(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(help): %v", err)
	}

	help := stdout.String()
	for _, want := range []string{"serve", "acp", "mcp", "version"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help output does not contain %q:\n%s", want, help)
		}
	}
	for _, unwanted := range []string{"completion", "mcp-server"} {
		if strings.Contains(help, unwanted) {
			t.Fatalf("help output contains %q:\n%s", unwanted, help)
		}
	}
}

func TestCLI_ACPHelpListsServeOnly(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := newRootCommand()
	cmd.SetArgs([]string{"acp", "help"})
	cmd.SetOut(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(acp help): %v", err)
	}

	help := stdout.String()
	if !strings.Contains(help, "serve") {
		t.Fatalf("acp help output does not contain serve:\n%s", help)
	}
	if strings.Contains(help, "mcp-server") {
		t.Fatalf("acp help output contains legacy MCP command:\n%s", help)
	}
}

func TestCLI_BareCommandRunsInteractiveShellUntilQuit(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := newRootCommand()
	cmd.SetArgs(nil)
	cmd.SetIn(strings.NewReader("help\nserve\nstatus\nq\n"))
	cmd.SetOut(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(bare): %v", err)
	}

	output := stdout.String()
	for _, want := range []string{
		"Hecate operator shell",
		"Commands: status, serve, ui, help, quit",
		"Start the runtime with: hecate serve",
		"Runtime status is available",
		"bye",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("interactive output does not contain %q:\n%s", want, output)
		}
	}
	if got := strings.Count(output, "hecate> "); got != 4 {
		t.Fatalf("prompt count = %d, want 4; output:\n%s", got, output)
	}
	if strings.Contains(output, "hecate ·") {
		t.Fatalf("interactive shell printed serve banner, likely started runtime:\n%s", output)
	}
}

func TestCLI_MCPHelpListsServeOnly(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := newRootCommand()
	cmd.SetArgs([]string{"mcp", "help"})
	cmd.SetOut(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(mcp help): %v", err)
	}

	help := stdout.String()
	if !strings.Contains(help, "serve") {
		t.Fatalf("mcp help output does not contain serve:\n%s", help)
	}
	if strings.Contains(help, "acp") {
		t.Fatalf("mcp help output contains acp:\n%s", help)
	}
}

func TestCLI_LegacyMCPServerCommandIsHidden(t *testing.T) {
	t.Parallel()

	cmd := newRootCommand()
	legacy, _, err := cmd.Find([]string{"mcp-server"})
	if err != nil {
		t.Fatalf("Find(mcp-server): %v", err)
	}
	if legacy == nil || legacy.Name() != "mcp-server" {
		t.Fatalf("Find(mcp-server) = %#v, want legacy command", legacy)
	}
	if !legacy.Hidden {
		t.Fatal("legacy mcp-server command should be hidden from help")
	}
}
