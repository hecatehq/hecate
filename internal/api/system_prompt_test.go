package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemPromptResolver_AllLayersConcatenated(t *testing.T) {
	// Composition order is broadest-first: global, workspace, per-task.
	// Pinning so a refactor that flips the order is caught.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("Workspace says hi."), 0o644); err != nil {
		t.Fatal(err)
	}

	resolver := buildSystemPromptResolver("Global default.")
	got := resolver(context.Background(), "", "Per-task override.", dir)

	wantParts := []string{"Global default.", "Workspace says hi.", "Per-task override."}
	for i, p := range wantParts {
		if !strings.Contains(got, p) {
			t.Errorf("layer %d %q missing from composed prompt: %s", i+1, p, got)
		}
	}
	prev := -1
	for _, p := range wantParts {
		idx := strings.Index(got, p)
		if idx <= prev {
			t.Errorf("layer %q at idx=%d, want > %d (previous): %s", p, idx, prev, got)
		}
		prev = idx
	}
}

func TestSystemPromptResolver_EmptyLayersSkipped(t *testing.T) {
	resolver := buildSystemPromptResolver("Just global.")
	got := resolver(context.Background(), "", "", "")
	if got != "Just global." {
		t.Errorf("got %q, want %q", got, "Just global.")
	}
}

func TestSystemPromptResolver_NoLayersAtAllReturnsEmpty(t *testing.T) {
	resolver := buildSystemPromptResolver("")
	got := resolver(context.Background(), "", "", "")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestSystemPromptResolver_AGENTS_md_Fallback(t *testing.T) {
	// CLAUDE.md and AGENTS.md are both honored. When CLAUDE.md is
	// absent, AGENTS.md (Codex CLI convention) is read.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("From AGENTS.md."), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := buildSystemPromptResolver("")
	got := resolver(context.Background(), "", "", dir)
	if got != "From AGENTS.md." {
		t.Errorf("got %q, want 'From AGENTS.md.'", got)
	}
}

func TestSystemPromptResolver_CLAUDE_md_TakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("From CLAUDE.md."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("From AGENTS.md."), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := buildSystemPromptResolver("")
	got := resolver(context.Background(), "", "", dir)
	if !strings.Contains(got, "CLAUDE.md") || strings.Contains(got, "AGENTS.md") {
		t.Errorf("CLAUDE.md should win; got: %q", got)
	}
}

func TestSystemPromptResolver_WorkspaceFileTooLargeIsTruncated(t *testing.T) {
	dir := t.TempDir()
	huge := strings.Repeat("x", agentWorkspacePromptMaxBytes*2)
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(huge), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := buildSystemPromptResolver("")
	got := resolver(context.Background(), "", "", dir)
	if len(got) > agentWorkspacePromptMaxBytes {
		t.Errorf("len = %d, want <= %d", len(got), agentWorkspacePromptMaxBytes)
	}
}
