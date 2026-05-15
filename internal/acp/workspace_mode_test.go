package acp

import (
	"strings"
	"testing"
)

func TestResolveWorkspaceMode(t *testing.T) {
	t.Parallel()
	fullCaps := ClientCapabilities{
		FS:       &FSCapability{ReadTextFile: true, WriteTextFile: true},
		Terminal: &TerminalCapability{},
	}
	readOnlyCaps := ClientCapabilities{
		FS:       &FSCapability{ReadTextFile: true},
		Terminal: &TerminalCapability{},
	}
	noTerminalCaps := ClientCapabilities{
		FS: &FSCapability{ReadTextFile: true, WriteTextFile: true},
	}

	tests := []struct {
		name       string
		configured string
		caps       ClientCapabilities
		want       string
		wantErr    string // substring; empty means no error
	}{
		{
			name:       "empty configured falls through to auto",
			configured: "",
			caps:       fullCaps,
			want:       WorkspaceModeEditorOwned,
		},
		{
			name:       "auto with full caps picks editor-owned",
			configured: "auto",
			caps:       fullCaps,
			want:       WorkspaceModeEditorOwned,
		},
		{
			name:       "auto without caps falls back to hecate-owned",
			configured: "auto",
			caps:       ClientCapabilities{Permissions: &PermissionCapability{}},
			want:       WorkspaceModeHecateOwned,
		},
		{
			name:       "auto with read-only fs falls back to hecate-owned",
			configured: "auto",
			caps:       readOnlyCaps,
			want:       WorkspaceModeHecateOwned,
		},
		{
			name:       "auto with fs but no terminal falls back to hecate-owned",
			configured: "auto",
			caps:       noTerminalCaps,
			want:       WorkspaceModeHecateOwned,
		},
		{
			name:       "hecate-owned ignores caps",
			configured: "hecate-owned",
			caps:       fullCaps,
			want:       WorkspaceModeHecateOwned,
		},
		{
			name:       "editor-owned with full caps",
			configured: "editor-owned",
			caps:       fullCaps,
			want:       WorkspaceModeEditorOwned,
		},
		{
			name:       "editor-owned missing writeTextFile rejects",
			configured: "editor-owned",
			caps:       readOnlyCaps,
			wantErr:    "writeTextFile",
		},
		{
			name:       "editor-owned missing terminal rejects",
			configured: "editor-owned",
			caps:       noTerminalCaps,
			wantErr:    "terminal",
		},
		{
			name:       "editor-owned with empty caps rejects",
			configured: "editor-owned",
			caps:       ClientCapabilities{Permissions: &PermissionCapability{}},
			wantErr:    "editor-owned",
		},
		{
			name:       "mode is case-insensitive",
			configured: "EDITOR-OWNED",
			caps:       fullCaps,
			want:       WorkspaceModeEditorOwned,
		},
		{
			name:       "leading whitespace tolerated",
			configured: "  hecate-owned  ",
			caps:       fullCaps,
			want:       WorkspaceModeHecateOwned,
		},
		{
			name:       "unknown mode rejected",
			configured: "shared",
			caps:       fullCaps,
			wantErr:    "invalid HECATE_WORKSPACE_MODE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveWorkspaceMode(tt.configured, tt.caps)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ResolveWorkspaceMode(%q, ...) = (%q, nil); want error containing %q", tt.configured, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q; want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveWorkspaceMode(%q, ...) error = %v; want %q", tt.configured, err, tt.want)
			}
			if got != tt.want {
				t.Fatalf("ResolveWorkspaceMode(%q, ...) = %q; want %q", tt.configured, got, tt.want)
			}
		})
	}
}
