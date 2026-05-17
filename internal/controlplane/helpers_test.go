package controlplane

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/config"
)

func TestCanonicalIDLowercasesAndReplacesNonAlphanum(t *testing.T) {
	cases := []struct {
		id, name, want string
	}{
		{"", "Tenant Name", "tenant-name"},
		{"  ", "Hello WORLD", "hello-world"},
		{"explicit-id", "ignored", "explicit-id"},
		{"My ID!", "fallback", "my-id"},
		{"with...multiple.,dots", "n", "with-multiple-dots"},
		{"---trimmed---", "n", "trimmed"},
		{"User@Example.com", "n", "user-example-com"},
		{"unicode-é-test", "n", "unicode-test"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := canonicalID(tc.id, tc.name); got != tc.want {
				t.Errorf("canonicalID(%q, %q) = %q, want %q", tc.id, tc.name, got, tc.want)
			}
		})
	}
}

func TestNormalizeStringListDedupesAndTrims(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty input", nil, []string{}},
		{"deduplicates", []string{"a", "a", "b"}, []string{"a", "b"}},
		{"trims and skips empty", []string{"  a  ", "", "  "}, []string{"a"}},
		{"preserves first-seen order", []string{"b", "a", "c", "a"}, []string{"b", "a", "c"}},
		{"dedup is case-sensitive", []string{"A", "a"}, []string{"A", "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeStringList(tc.in)
			if !equalStringsCP(got, tc.want) {
				t.Errorf("normalizeStringList(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestClonePolicyRuleIsolatesAllSlices(t *testing.T) {
	original := config.PolicyRuleConfig{
		ID:            "r1",
		Providers:     []string{"openai"},
		ProviderKinds: []string{"cloud"},
		Models:        []string{"gpt-4o"},
		RouteReasons:  []string{"explicit"},
	}
	clone := clonePolicyRule(original)

	// Mutate every original slice; the clone must not see the changes.
	original.Providers[0] = "MUTATED"
	original.ProviderKinds[0] = "MUTATED"
	original.Models[0] = "MUTATED"
	original.RouteReasons[0] = "MUTATED"

	if clone.Providers[0] == "MUTATED" {
		t.Error("Providers slice not cloned")
	}
	if clone.ProviderKinds[0] == "MUTATED" {
		t.Error("ProviderKinds slice not cloned")
	}
	if clone.Models[0] == "MUTATED" {
		t.Error("Models slice not cloned")
	}
	if clone.RouteReasons[0] == "MUTATED" {
		t.Error("RouteReasons slice not cloned")
	}
}

func TestActorFromContextDefaults(t *testing.T) {
	if got := actorFromContext(context.Background()); got != "system" {
		t.Errorf("plain context → %q, want system", got)
	}

	ctx := WithActor(context.Background(), "alice")
	if got := actorFromContext(ctx); got != "alice" {
		t.Errorf("WithActor(alice) → %q, want alice", got)
	}

	// Whitespace-only actor should fall back to system, not leak the spaces.
	ctx = WithActor(context.Background(), "   ")
	if got := actorFromContext(ctx); got != "system" {
		t.Errorf("blank actor → %q, want system", got)
	}
}

func TestNewAuditEventPopulatesFieldsAndTimestamp(t *testing.T) {
	ctx := WithActor(context.Background(), "alice")
	before := time.Now().UTC()
	got := newAuditEvent(ctx, "create", "tenant", "t1", "first run")
	after := time.Now().UTC()

	if got.Actor != "alice" {
		t.Errorf("Actor = %q, want alice", got.Actor)
	}
	if got.Action != "create" {
		t.Errorf("Action = %q, want create", got.Action)
	}
	if got.TargetType != "tenant" {
		t.Errorf("TargetType = %q, want tenant", got.TargetType)
	}
	if got.TargetID != "t1" {
		t.Errorf("TargetID = %q, want t1", got.TargetID)
	}
	if got.Detail != "first run" {
		t.Errorf("Detail = %q, want first run", got.Detail)
	}
	if got.Timestamp.Before(before) || got.Timestamp.After(after) {
		t.Errorf("Timestamp %v not within [%v, %v]", got.Timestamp, before, after)
	}
}

func TestPruneByMaxAge(t *testing.T) {
	now := time.Now()
	state := &State{
		Events: []AuditEvent{
			{Action: "old", Timestamp: now.Add(-2 * time.Hour)},
			{Action: "old", Timestamp: now.Add(-90 * time.Minute)},
			{Action: "fresh", Timestamp: now.Add(-5 * time.Minute)},
			{Action: "fresh", Timestamp: now},
		},
	}
	deleted := pruneAuditEvents(state, time.Hour, 0)
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}
	if len(state.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(state.Events))
	}
	for _, e := range state.Events {
		if e.Action != "fresh" {
			t.Errorf("kept event with action %q, want only fresh", e.Action)
		}
	}
}

func TestPruneByMaxCount(t *testing.T) {
	now := time.Now()
	state := &State{
		Events: []AuditEvent{
			{Action: "1", Timestamp: now.Add(-4 * time.Minute)},
			{Action: "2", Timestamp: now.Add(-3 * time.Minute)},
			{Action: "3", Timestamp: now.Add(-2 * time.Minute)},
			{Action: "4", Timestamp: now.Add(-1 * time.Minute)},
		},
	}
	deleted := pruneAuditEvents(state, 0, 2)
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}
	// Most-recent should win — the trailing "3" and "4".
	if len(state.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(state.Events))
	}
	if state.Events[0].Action != "3" || state.Events[1].Action != "4" {
		t.Errorf("kept events = %v, want ['3', '4']", []string{state.Events[0].Action, state.Events[1].Action})
	}
}

func TestPruneHandlesNilState(t *testing.T) {
	if got := pruneAuditEvents(nil, time.Hour, 10); got != 0 {
		t.Errorf("pruneAuditEvents(nil) = %d, want 0", got)
	}
}

func TestCloneStateIsolatesNestedSlices(t *testing.T) {
	original := State{
		Providers:   []Provider{{ID: "p1", Name: "OpenAI"}},
		PolicyRules: []config.PolicyRuleConfig{{ID: "r1", Providers: []string{"openai"}}},
		Events:      []AuditEvent{{Action: "create"}},
	}
	clone := cloneState(original)

	original.Providers[0].Name = "MUTATED"
	original.PolicyRules[0].Providers[0] = "MUTATED"

	if clone.Providers[0].Name == "MUTATED" {
		t.Error("Providers slice shared with original")
	}
	if clone.PolicyRules[0].Providers[0] == "MUTATED" {
		t.Error("PolicyRules.Providers slice shared with original")
	}
}

// equalStringsCP avoids name conflict with helpers in other test files.
func equalStringsCP(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Sanity-check our test relies on State carrying expected fields.
var _ = strings.TrimSpace
