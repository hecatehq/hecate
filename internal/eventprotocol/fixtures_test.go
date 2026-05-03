package eventprotocol

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

type fixtureEvent struct {
	SchemaVersion string         `json:"schema_version"`
	EventID       string         `json:"event_id"`
	RunID         string         `json:"run_id"`
	TaskID        string         `json:"task_id"`
	SessionID     string         `json:"session_id"`
	Sequence      int64          `json:"sequence"`
	OccurredAt    string         `json:"occurred_at"`
	Type          string         `json:"type"`
	Data          map[string]any `json:"data"`
}

var (
	candidateCoreEventTypes = map[string]struct{}{
		"run.queued":                     {},
		"run.started":                    {},
		"run.finished":                   {},
		"run.failed":                     {},
		"run.cancelled":                  {},
		"run.resumed_from_event":         {},
		"run.checkpoint_saved":           {},
		"turn.started":                   {},
		"turn.completed":                 {},
		"turn.failed":                    {},
		"assistant.text_delta":           {},
		"assistant.text_complete":        {},
		"assistant.tool_call_proposed":   {},
		"assistant.final_answer":         {},
		"tool.invoked":                   {},
		"tool.started":                   {},
		"tool.completed":                 {},
		"tool.failed":                    {},
		"tool.cancelled":                 {},
		"tool.timed_out":                 {},
		"tool.shell.command":             {},
		"tool.shell.output_chunk":        {},
		"tool.shell.exited":              {},
		"tool.file.patch":                {},
		"tool.file.reverted":             {},
		"approval.requested":             {},
		"approval.resolved":              {},
		"approval.timed_out":             {},
		"cost.tick":                      {},
		"cost.budget_warning":            {},
		"cost.budget_exceeded":           {},
		"policy.tool_blocked":            {},
		"policy.model_rewrote":           {},
		"error.tool_unavailable":         {},
		"error.model_capability_missing": {},
		"error.upstream":                 {},
		"gap.events_pruned":              {},
		"gap.run_disconnected":           {},
	}

	candidateRunLifecycleEventTypes = []string{
		"run.queued",
		"run.started",
		"run.finished",
		"run.failed",
		"run.cancelled",
		"run.resumed_from_event",
		"run.checkpoint_saved",
	}

	forbiddenEventTypeReplacements = map[string]string{
		"approval.approved":    "approval.resolved",
		"approval.rejected":    "approval.resolved",
		"run.claimed":          "run.started",
		"run.completed":        "run.finished",
		"run.resumed":          "run.resumed_from_event",
		"run.resume_requested": "run.resumed_from_event",
		"run.running":          "run.started",
	}

	candidateCoreGroups = map[string]func(string) bool{
		"run":             func(eventType string) bool { return strings.HasPrefix(eventType, "run.") },
		"turn":            func(eventType string) bool { return strings.HasPrefix(eventType, "turn.") },
		"assistant_text":  func(eventType string) bool { return strings.HasPrefix(eventType, "assistant.text_") },
		"assistant_final": func(eventType string) bool { return eventType == "assistant.final_answer" },
		"tool":            func(eventType string) bool { return strings.HasPrefix(eventType, "tool.") },
		"tool_shell":      func(eventType string) bool { return strings.HasPrefix(eventType, "tool.shell.") },
		"approval":        func(eventType string) bool { return strings.HasPrefix(eventType, "approval.") },
		"cost":            func(eventType string) bool { return strings.HasPrefix(eventType, "cost.") },
		"policy":          func(eventType string) bool { return strings.HasPrefix(eventType, "policy.") },
		"error":           func(eventType string) bool { return strings.HasPrefix(eventType, "error.") },
		"gap":             func(eventType string) bool { return strings.HasPrefix(eventType, "gap.") },
	}

	eventIDPattern = regexp.MustCompile(`^evt_[0-9A-HJKMNP-TV-Z]{26}$`)
	runIDPattern   = regexp.MustCompile(`^run_[0-9A-HJKMNP-TV-Z]{26}$`)
	taskIDPattern  = regexp.MustCompile(`^task_[0-9A-HJKMNP-TV-Z]{26}$`)
	chatIDPattern  = regexp.MustCompile(`^chat_[0-9A-HJKMNP-TV-Z]{26}$`)
	typePattern    = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
)

func TestEventProtocolV1CoreFixtures(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "docs", "fixtures", "events", "v1", "core")
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("no event protocol fixtures found in %s", fixtureDir)
	}
	slices.Sort(paths)

	seenGroups := make(map[string]bool)
	seenTypes := make(map[string]bool)
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			events := readFixtureEvents(t, path)
			validateFixtureSequence(t, events)
			for i, event := range events {
				validateFixtureEvent(t, path, i, event)
				seenTypes[event.Type] = true
				for group, matches := range candidateCoreGroups {
					if matches(event.Type) {
						seenGroups[group] = true
					}
				}
			}
		})
	}

	missing := make([]string, 0)
	for group := range candidateCoreGroups {
		if !seenGroups[group] {
			missing = append(missing, group)
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Fatalf("candidate-core fixture coverage missing groups: %s", strings.Join(missing, ", "))
	}

	missing = missingFixtureCoverage(candidateRunLifecycleEventTypes, seenTypes)
	if len(missing) > 0 {
		t.Fatalf("candidate-core fixture coverage missing run lifecycle events: %s", strings.Join(missing, ", "))
	}
}

func TestEventProtocolV1EnvelopeSchemaStub(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "schemas", "events", "v1", "envelope.schema.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read envelope schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode envelope schema: %v", err)
	}
	requiredRaw, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("schema required must be an array")
	}
	required := make(map[string]bool, len(requiredRaw))
	for _, field := range requiredRaw {
		name, ok := field.(string)
		if !ok {
			t.Fatalf("schema required field must be string, got %T", field)
		}
		required[name] = true
	}
	for _, field := range []string{"schema_version", "event_id", "run_id", "sequence", "occurred_at", "type", "data"} {
		if !required[field] {
			t.Fatalf("schema required fields missing %q", field)
		}
	}
}

func readFixtureEvents(t *testing.T, path string) []fixtureEvent {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var events []fixtureEvent
	if err := json.Unmarshal(raw, &events); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if len(events) == 0 {
		t.Fatalf("%s: fixture must contain at least one event", path)
	}
	return events
}

func validateFixtureEvent(t *testing.T, path string, index int, event fixtureEvent) {
	t.Helper()

	where := fmt.Sprintf("%s[%d]", path, index)
	if event.SchemaVersion != "1" {
		t.Fatalf("%s: schema_version = %q, want 1", where, event.SchemaVersion)
	}
	if !eventIDPattern.MatchString(event.EventID) {
		t.Fatalf("%s: invalid event_id %q", where, event.EventID)
	}
	if !runIDPattern.MatchString(event.RunID) {
		t.Fatalf("%s: invalid run_id %q", where, event.RunID)
	}
	if event.TaskID != "" && !taskIDPattern.MatchString(event.TaskID) {
		t.Fatalf("%s: invalid task_id %q", where, event.TaskID)
	}
	if event.SessionID != "" && !chatIDPattern.MatchString(event.SessionID) {
		t.Fatalf("%s: invalid session_id %q", where, event.SessionID)
	}
	if event.Sequence < 0 {
		t.Fatalf("%s: sequence must be non-negative, got %d", where, event.Sequence)
	}
	if _, err := time.Parse(time.RFC3339Nano, event.OccurredAt); err != nil {
		t.Fatalf("%s: occurred_at is not RFC3339Nano: %v", where, err)
	}
	if !typePattern.MatchString(event.Type) {
		t.Fatalf("%s: invalid event type format %q", where, event.Type)
	}
	if replacement, ok := forbiddenEventTypeReplacements[event.Type]; ok {
		t.Fatalf("%s: unsupported event type %q used; use %q", where, event.Type, replacement)
	}
	if _, ok := candidateCoreEventTypes[event.Type]; !ok {
		t.Fatalf("%s: %q is not a candidate-core v1 event type", where, event.Type)
	}
	if event.Data == nil {
		t.Fatalf("%s: data must be a JSON object, got null or missing", where)
	}
	validateRunLifecycleData(t, where, event)
	if strings.HasPrefix(event.Type, "assistant.thinking_") || strings.HasPrefix(event.Type, "artifact.") || strings.HasPrefix(event.Type, "tool.edit.") || strings.HasPrefix(event.Type, "tool.multi_edit.") {
		t.Fatalf("%s: %q belongs in experimental or artifact-dependent fixtures, not core", where, event.Type)
	}
}

func validateRunLifecycleData(t *testing.T, where string, event fixtureEvent) {
	t.Helper()

	switch event.Type {
	case "run.queued":
		requireStringField(t, where, event.Data, "kind")
		requireStringField(t, where, event.Data, "model")
		requireStringField(t, where, event.Data, "provider")
	case "run.started":
		requireStringField(t, where, event.Data, "worker_id")
		leaseUntil := requireStringField(t, where, event.Data, "lease_until")
		if _, err := time.Parse(time.RFC3339Nano, leaseUntil); err != nil {
			t.Fatalf("%s: lease_until is not RFC3339Nano: %v", where, err)
		}
	case "run.finished":
		if got := requireStringField(t, where, event.Data, "final_status"); got != "completed" {
			t.Fatalf("%s: final_status = %q, want completed", where, got)
		}
		requireNumberField(t, where, event.Data, "duration_ms")
	case "run.failed":
		requireStringField(t, where, event.Data, "code")
		requireStringField(t, where, event.Data, "message")
		requireBoolField(t, where, event.Data, "retriable")
	case "run.cancelled":
		requireStringField(t, where, event.Data, "by")
		requireStringField(t, where, event.Data, "reason")
	case "run.resumed_from_event":
		fromRunID := requireStringField(t, where, event.Data, "from_run_id")
		if !runIDPattern.MatchString(fromRunID) {
			t.Fatalf("%s: invalid from_run_id %q", where, fromRunID)
		}
		requireNumberField(t, where, event.Data, "from_sequence")
	case "run.checkpoint_saved":
		requireStringField(t, where, event.Data, "checkpoint_id")
	}
}

func requireStringField(t *testing.T, where string, data map[string]any, field string) string {
	t.Helper()

	value, ok := data[field]
	if !ok {
		t.Fatalf("%s: missing data.%s", where, field)
	}
	text, ok := value.(string)
	if !ok || text == "" {
		t.Fatalf("%s: data.%s must be a non-empty string, got %T", where, field, value)
	}
	return text
}

func requireNumberField(t *testing.T, where string, data map[string]any, field string) float64 {
	t.Helper()

	value, ok := data[field]
	if !ok {
		t.Fatalf("%s: missing data.%s", where, field)
	}
	number, ok := value.(float64)
	if !ok {
		t.Fatalf("%s: data.%s must be a number, got %T", where, field, value)
	}
	if number < 0 {
		t.Fatalf("%s: data.%s must be non-negative, got %v", where, field, number)
	}
	return number
}

func requireBoolField(t *testing.T, where string, data map[string]any, field string) bool {
	t.Helper()

	value, ok := data[field]
	if !ok {
		t.Fatalf("%s: missing data.%s", where, field)
	}
	boolean, ok := value.(bool)
	if !ok {
		t.Fatalf("%s: data.%s must be a bool, got %T", where, field, value)
	}
	return boolean
}

func validateFixtureSequence(t *testing.T, events []fixtureEvent) {
	t.Helper()

	lastByRun := make(map[string]int64)
	seenByRun := make(map[string]bool)
	for _, event := range events {
		if !seenByRun[event.RunID] {
			seenByRun[event.RunID] = true
			lastByRun[event.RunID] = event.Sequence
			continue
		}
		last := lastByRun[event.RunID]
		if event.Sequence != last+1 {
			t.Fatalf("run %s sequence moved from %d to %d, want %d", event.RunID, last, event.Sequence, last+1)
		}
		lastByRun[event.RunID] = event.Sequence
	}
}

func missingFixtureCoverage(want []string, seen map[string]bool) []string {
	missing := make([]string, 0)
	for _, eventType := range want {
		if !seen[eventType] {
			missing = append(missing, eventType)
		}
	}
	slices.Sort(missing)
	return missing
}
