package gateway

import (
	"errors"
	"fmt"
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestIsDeniedAndIsClientError(t *testing.T) {
	if !IsDeniedError(errDenied) {
		t.Error("errDenied should match IsDeniedError")
	}
	wrapped := fmt.Errorf("context: %w", errDenied)
	if !IsDeniedError(wrapped) {
		t.Error("wrapped errDenied should still match IsDeniedError")
	}
	if IsDeniedError(errors.New("unrelated")) {
		t.Error("unrelated error should not match IsDeniedError")
	}
	if IsDeniedError(nil) {
		t.Error("nil should not match IsDeniedError")
	}

	if !IsClientError(errClient) {
		t.Error("errClient should match IsClientError")
	}
	if !IsClientError(fmt.Errorf("wrap: %w", errClient)) {
		t.Error("wrapped errClient should match IsClientError")
	}
	if IsClientError(errDenied) {
		t.Error("errDenied should not match IsClientError")
	}
}

func TestFirstNonEmptyString(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"all empty", []string{"", "", ""}, ""},
		{"first wins", []string{"first", "second"}, "first"},
		{"skips empty until first non-empty", []string{"", "", "third"}, "third"},
		{"empty input", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstNonEmptyString(tc.in...); got != tc.want {
				t.Errorf("firstNonEmptyString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStringAttrCoercesNonStringValues(t *testing.T) {
	attrs := map[string]any{
		"str":  "hello",
		"num":  42,
		"bool": true,
		"nil":  nil,
	}
	if got := stringAttr(attrs, "str"); got != "hello" {
		t.Errorf("stringAttr(str) = %q, want hello", got)
	}
	if got := stringAttr(attrs, "num"); got != "42" {
		t.Errorf("stringAttr(num) = %q, want 42 (fmt.Sprint coercion)", got)
	}
	if got := stringAttr(attrs, "bool"); got != "true" {
		t.Errorf("stringAttr(bool) = %q, want true", got)
	}
	if got := stringAttr(attrs, "nil"); got != "" {
		t.Errorf("stringAttr(nil) = %q, want empty", got)
	}
	if got := stringAttr(attrs, "missing"); got != "" {
		t.Errorf("stringAttr(missing key) = %q, want empty", got)
	}
	if got := stringAttr(nil, "any"); got != "" {
		t.Errorf("stringAttr(nil map) = %q, want empty", got)
	}
}

func TestInt64AttrAcceptsNumericKinds(t *testing.T) {
	attrs := map[string]any{
		"int":     42,
		"int64":   int64(43),
		"float64": float64(44.7),
		"float32": float32(45.3),
		"string":  "not a number",
		"nil":     nil,
	}
	cases := []struct {
		key  string
		want int64
	}{
		{"int", 42},
		{"int64", 43},
		{"float64", 44}, // truncated
		{"float32", 45},
		{"string", 0}, // unsupported type returns zero
		{"nil", 0},
		{"missing", 0},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			if got := int64Attr(attrs, tc.key); got != tc.want {
				t.Errorf("int64Attr(%q) = %d, want %d", tc.key, got, tc.want)
			}
		})
	}
	if got := int64Attr(nil, "any"); got != 0 {
		t.Errorf("int64Attr(nil map) = %d, want 0", got)
	}
}

func TestBoolAttrOnlyAcceptsBoolType(t *testing.T) {
	attrs := map[string]any{
		"true":   true,
		"false":  false,
		"string": "true",
		"nil":    nil,
	}
	if !boolAttr(attrs, "true") {
		t.Error("boolAttr(true) = false, want true")
	}
	if boolAttr(attrs, "false") {
		t.Error("boolAttr(false) = true, want false")
	}
	if boolAttr(attrs, "string") {
		t.Error(`boolAttr("true" string) should be false (not a bool)`)
	}
	if boolAttr(attrs, "nil") {
		t.Error("boolAttr(nil) should be false")
	}
	if boolAttr(nil, "any") {
		t.Error("boolAttr(nil map) should be false")
	}
}

func TestShouldReplaceOutcomeRanking(t *testing.T) {
	cases := []struct {
		current, incoming string
		want              bool
	}{
		{"", "selected", true},
		{"unknown", "considered", true},
		{"considered", "selected", true},  // 1 → 3
		{"selected", "considered", false}, // 3 → 1
		{"selected", "completed", true},   // 3 → 5
		{"failed", "completed", true},     // 4 → 5
		{"completed", "failed", false},    // 5 → 4
		{"selected", "", false},           // empty incoming
		{"selected", "unknown", false},    // unknown incoming
	}
	for _, tc := range cases {
		t.Run(tc.current+"→"+tc.incoming, func(t *testing.T) {
			if got := shouldReplaceOutcome(tc.current, tc.incoming); got != tc.want {
				t.Errorf("shouldReplaceOutcome(%q, %q) = %v, want %v", tc.current, tc.incoming, got, tc.want)
			}
		})
	}
}

func TestNormalizeRouteCandidate(t *testing.T) {
	cases := []struct {
		name              string
		in                types.RouteCandidateReport
		wantOutcome       string
		wantSkipReasonSet bool
	}{
		{"selected stays selected", types.RouteCandidateReport{Outcome: "selected"}, "selected", false},
		{"failed stays failed", types.RouteCandidateReport{Outcome: "failed"}, "failed", false},
		{"empty becomes skipped/not_selected", types.RouteCandidateReport{Outcome: ""}, "skipped", true},
		{"unknown becomes skipped/not_selected", types.RouteCandidateReport{Outcome: "unknown"}, "skipped", true},
		{"considered becomes skipped/not_selected", types.RouteCandidateReport{Outcome: "considered"}, "skipped", true},
		{"unrecognized outcome becomes skipped with the outcome as skip reason",
			types.RouteCandidateReport{Outcome: "weird-state"}, "skipped", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in
			normalizeRouteCandidate(&got)
			if got.Outcome != tc.wantOutcome {
				t.Errorf("Outcome = %q, want %q", got.Outcome, tc.wantOutcome)
			}
			if tc.wantSkipReasonSet && got.SkipReason == "" {
				t.Error("SkipReason should be populated, got empty")
			}
		})
	}

	t.Run("preserves caller-supplied skip reason", func(t *testing.T) {
		c := types.RouteCandidateReport{Outcome: "", SkipReason: "explicit"}
		normalizeRouteCandidate(&c)
		if c.SkipReason != "explicit" {
			t.Errorf("SkipReason overwritten: got %q, want %q", c.SkipReason, "explicit")
		}
	})
}

func TestRouteCandidateKeyIsStable(t *testing.T) {
	if got := routeCandidateKey("openai", "gpt-4o", 2); got != "2:openai:gpt-4o" {
		t.Errorf("routeCandidateKey = %q, want 2:openai:gpt-4o", got)
	}
}

// customErrorType is a named exported type so traceErrorType can extract its
// name. The reflection path strips pointer levels and reports the underlying
// type's exported name.
type customErrorType struct{ msg string }

func (e *customErrorType) Error() string { return e.msg }

func TestTraceErrorType(t *testing.T) {
	if got := traceErrorType(nil); got != "" {
		t.Errorf("traceErrorType(nil) = %q, want empty", got)
	}
	if got := traceErrorType(&customErrorType{msg: "x"}); got != "customErrorType" {
		t.Errorf("traceErrorType(*customErrorType) = %q, want customErrorType", got)
	}
	// errors.New returns *errors.errorString, which has a non-empty type name.
	if got := traceErrorType(errors.New("std")); got == "" {
		t.Error("traceErrorType(stdlib error) returned empty string")
	}
}

func TestCloneTraceAttrsIsolatesMap(t *testing.T) {
	original := map[string]any{"a": 1, "b": "two"}
	clone := cloneTraceAttrs(original)
	clone["a"] = 999
	clone["c"] = "added"
	if original["a"] != 1 {
		t.Errorf("original[a] mutated to %v after modifying clone", original["a"])
	}
	if _, ok := original["c"]; ok {
		t.Error("original gained key 'c' from modifying clone")
	}

	// nil/empty input should yield an empty (writable) map, not nil.
	empty := cloneTraceAttrs(nil)
	if empty == nil {
		t.Fatal("cloneTraceAttrs(nil) returned nil map")
	}
	empty["x"] = 1 // must be writable without panic
}
