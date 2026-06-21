package agentadapters

import (
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestExtractToolKindACPKindPriority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind acp.ToolKind
		want string
	}{
		{"edit maps to file_write", acp.ToolKindEdit, ToolKindFileWrite},
		{"read maps to file_read", acp.ToolKindRead, ToolKindFileRead},
		{"execute maps to shell_exec", acp.ToolKindExecute, ToolKindShellExec},
		{"fetch maps to network", acp.ToolKindFetch, ToolKindNetwork},
		{"move maps to file_move", acp.ToolKindMove, ToolKindFileMove},
		{"delete maps to file_delete", acp.ToolKindDelete, ToolKindFileDelete},
		{"search maps to search", acp.ToolKindSearch, ToolKindSearch},
		{"think maps to think", acp.ToolKindThink, ToolKindThink},
		{"mcp maps to mcp", acp.ToolKind("mcp"), ToolKindMCP},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			k := tc.kind
			got := extractToolKind(acp.ToolCallUpdate{Kind: &k})
			if got != tc.want {
				t.Fatalf("extractToolKind(%q) = %q, want %q", tc.kind, got, tc.want)
			}
		})
	}
}

func TestExtractToolKindFallsBackToTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		title string
		want  string
	}{
		{"Write file foo.go", ToolKindFileWrite},
		{"Apply patch", ToolKindFileWrite},
		{"Edit file", ToolKindFileWrite},
		{"Read file", ToolKindFileRead},
		{"Open foo.go", ToolKindFileRead},
		{"Run command", ToolKindShellExec},
		{"Execute shell", ToolKindShellExec},
		{"Fetch https://...", ToolKindNetwork},
		{"HTTP request", ToolKindNetwork},
		{"MCP search docs", ToolKindMCP},
		{"Search the docs", ToolKindSearch},
		{"Delete file", ToolKindFileDelete},
		{"Rename file", ToolKindFileMove},
		{"Some unknown title", ToolKindOther},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.title, func(t *testing.T) {
			t.Parallel()
			title := tc.title
			got := extractToolKind(acp.ToolCallUpdate{Title: &title})
			if got != tc.want {
				t.Fatalf("extractToolKind(title=%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}
}

func TestExtractToolKindACPKindOverridesTitle(t *testing.T) {
	t.Parallel()
	// ACP Kind is the more reliable signal; if both are set the
	// typed kind wins. Adapters that mis-spell their title shouldn't
	// drag a request into the wrong scope.
	k := acp.ToolKindRead
	title := "Some destructive write operation"
	got := extractToolKind(acp.ToolCallUpdate{Kind: &k, Title: &title})
	if got != ToolKindFileRead {
		t.Fatalf("got %q, want %q (ACP kind must override title)", got, ToolKindFileRead)
	}
}

func TestExtractToolKindFallsThroughToOtherWhenKindIsOther(t *testing.T) {
	t.Parallel()
	k := acp.ToolKindOther
	got := extractToolKind(acp.ToolCallUpdate{Kind: &k})
	if got != ToolKindOther {
		t.Fatalf("got %q, want %q", got, ToolKindOther)
	}
}

func TestExtractToolNamePrefersTitle(t *testing.T) {
	t.Parallel()
	title := "Edit src/foo.go"
	k := acp.ToolKindEdit
	got := extractToolName(acp.ToolCallUpdate{Title: &title, Kind: &k})
	if got != "Edit src/foo.go" {
		t.Fatalf("got %q, want %q", got, "Edit src/foo.go")
	}
}

func TestExtractToolNameFallsBackToKind(t *testing.T) {
	t.Parallel()
	k := acp.ToolKindEdit
	got := extractToolName(acp.ToolCallUpdate{Kind: &k})
	if got != string(acp.ToolKindEdit) {
		t.Fatalf("got %q, want %q", got, string(acp.ToolKindEdit))
	}
}

func TestExtractToolNameEmpty(t *testing.T) {
	t.Parallel()
	got := extractToolName(acp.ToolCallUpdate{})
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
