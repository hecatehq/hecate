package codeintel

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFormatResultEnforcesCompleteUTF8OutputBudget(t *testing.T) {
	items := make([]Item, absoluteMaxResults)
	for index := range items {
		items[index] = Item{
			Path:    strings.Repeat("nested/", 300) + "source.go",
			Detail:  strings.Repeat("🙂 detail ", 500),
			Preview: strings.Repeat("🙂 preview ", 100),
		}
	}
	text := formatResult(Result{
		Operation: OpWorkspaceSymbols,
		Provider:  "fixture",
		Items:     items,
		Truncated: true,
	})
	if len(text) > maxResultTextBytes {
		t.Fatalf("formatted result bytes = %d, want at most %d", len(text), maxResultTextBytes)
	}
	if !utf8.ValidString(text) || !strings.HasSuffix(text, "... output truncated") {
		t.Fatalf("formatted result must end with a valid UTF-8 truncation marker")
	}
}

func TestFormatCapabilitiesIncludesVersionOperationsAndVerificationStage(t *testing.T) {
	text := formatResult(Result{
		Operation: OpCapabilities,
		Capabilities: []Capability{{
			Language:   "go",
			Provider:   "gopls",
			Version:    "0.20.0",
			Available:  true,
			Status:     "installed_unverified",
			Operations: []Operation{OpDefinition, OpReferences},
			Detail:     "trusted executable and version probe passed; LSP initialization is verified on query",
		}},
	})
	for _, want := range []string{
		"go: available via gopls version=0.20.0",
		"[installed_unverified]",
		"operations=definition,references",
		"LSP initialization is verified on query",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("formatted capabilities = %q, want %q", text, want)
		}
	}
}
