package runtimehost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewIDProducesValidDistinctRuntimeIDs(t *testing.T) {
	first, err := NewID()
	if err != nil {
		t.Fatalf("NewID() first error = %v", err)
	}
	second, err := NewID()
	if err != nil {
		t.Fatalf("NewID() second error = %v", err)
	}
	if first == second {
		t.Fatalf("NewID() returned duplicate id %q", first)
	}
	if err := ValidateID(first); err != nil {
		t.Fatalf("ValidateID(%q) error = %v", first, err)
	}
}

func TestValidateIDRejectsMalformedRuntimeIDs(t *testing.T) {
	for _, id := range []string{"", "runtime_", "host_001122", "runtime_not-hex", " runtime_00112233445566778899aabb "} {
		if err := ValidateID(id); err == nil {
			t.Fatalf("ValidateID(%q) error = nil, want invalid format", id)
		}
	}
}

func TestResolvePersistsRuntimeIdentityAcrossRestarts(t *testing.T) {
	dataDir := t.TempDir()

	first, err := Resolve(dataDir, "MacBook")
	if err != nil {
		t.Fatalf("Resolve() first error = %v", err)
	}
	second, err := Resolve(dataDir, "Renamed MacBook")
	if err != nil {
		t.Fatalf("Resolve() second error = %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("runtime id changed across resolves: first=%q second=%q", first.ID, second.ID)
	}
	if second.Label != "Renamed MacBook" {
		t.Fatalf("label = %q, want Renamed MacBook", second.Label)
	}

	raw, err := os.ReadFile(filepath.Join(dataDir, StateFilename))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var state persistedIdentity
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.RuntimeHostID != first.ID {
		t.Fatalf("persisted runtime id = %q, want %q", state.RuntimeHostID, first.ID)
	}
}

func TestResolveRejectsCorruptOrInvalidIdentity(t *testing.T) {
	for name, raw := range map[string]string{
		"corrupt json": "{not json",
		"invalid id":   `{"runtime_host_id":"runtime_invalid"}`,
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			path := filepath.Join(dataDir, StateFilename)
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			if _, err := Resolve(dataDir, "MacBook"); err == nil {
				t.Fatal("Resolve() error = nil, want invalid state error")
			}
		})
	}
}

func TestValidateLabelAllowsOperatorNamesAndRejectsUnsafeDisplayText(t *testing.T) {
	for _, label := range []string{"", "MacBook", "Build host 2"} {
		if err := ValidateLabel(label); err != nil {
			t.Fatalf("ValidateLabel(%q) error = %v", label, err)
		}
	}
	for _, label := range []string{"MacBook\nspoofed", strings.Repeat("a", maxLabelRunes+1)} {
		if err := ValidateLabel(label); err == nil {
			t.Fatalf("ValidateLabel(%q) error = nil, want rejection", label)
		}
	}
}

func TestResolveLabelPrefersConfiguredValue(t *testing.T) {
	if got := ResolveLabel("  MacBook  "); got != "MacBook" {
		t.Fatalf("ResolveLabel() = %q, want MacBook", got)
	}
}
