package agentadapters

import (
	"context"
	"strings"
	"testing"
)

// TestClassifyAdapterError pins the auth-detection heuristic. Adapter
// auth failures are phrased differently across vendors; the heuristic
// has to catch the patterns we've actually seen in the wild without
// false-positive-tagging plain process errors as auth issues.
func TestClassifyAdapterError(t *testing.T) {
	t.Parallel()

	authCases := []struct {
		name   string
		err    string
		stderr string
	}{
		{"cursor authentication required", "Authentication required", ""},
		{"explicit unauthenticated", "user is unauthenticated", ""},
		{"unauthorized verb", "request was unauthorized", ""},
		{"please log in", "Please log in to continue", ""},
		{"please sign in", "Please sign in before using this command", ""},
		{"login required", "Login required: run `cursor-agent login`", ""},
		{"sign in required", "Sign in required to continue", ""},
		{"not logged in", "you are not logged in", ""},
		{"missing api key", "missing api key for provider anthropic", ""},
		{"apikey jammed together", "no apikey configured", ""},
		{"missing credentials", "missing credentials for adapter", ""},
		{"invalid credentials", "invalid credentials provided", ""},
		{"credit balance", "Credit balance is too low to start a session", ""},
		{"payment required", "402 payment required", ""},
		{"subscription required", "Active subscription required for this model", ""},
		{"401 unauthorized", "request returned 401 unauthorized", ""},
		{"bare 403", "got status 403 forbidden from upstream", ""},
		{"stderr-only signal", "initialize failed", "Error: Authentication required\n"},
		{"case-insensitive", "ERROR: PLEASE LOG IN", ""},
	}
	for _, tc := range authCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, hint := classifyAdapterError(tc.err, tc.stderr)
			if status != ProbeStatusAuthRequired {
				t.Fatalf("status = %q, want %q\n  err: %q\n  stderr: %q",
					status, ProbeStatusAuthRequired, tc.err, tc.stderr)
			}
			if hint == "" {
				t.Fatalf("auth_required without a hint — operators rely on the suggestion")
			}
		})
	}

	nonAuthCases := []struct {
		name   string
		err    string
		stderr string
	}{
		{"plain crash", "process exited with code 1", ""},
		{"protocol mismatch", "unexpected ACP protocol version", ""},
		{"ENOENT", "exec: command not found", ""},
		{"context deadline", "context deadline exceeded", ""},
		{"network error", "dial tcp: connection refused", ""},
		// "401" embedded in a request id mustn't trigger — the heuristic
		// looks for word-bordered 401/403, not arbitrary digits.
		{"401 in unrelated id", "request id req_4012ab failed", ""},
	}
	for _, tc := range nonAuthCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, _ := classifyAdapterError(tc.err, tc.stderr)
			if status != ProbeStatusError {
				t.Fatalf("status = %q, want %q\n  err: %q\n  stderr: %q",
					status, ProbeStatusError, tc.err, tc.stderr)
			}
		})
	}
}

// TestProbeUnknownAdapter ensures we surface a clean error rather than
// crashing or attempting to spawn a phantom binary.
func TestProbeUnknownAdapter(t *testing.T) {
	t.Parallel()
	res := Probe(context.Background(), "no-such-adapter")
	if res.Status != ProbeStatusError {
		t.Fatalf("Status = %q, want %q", res.Status, ProbeStatusError)
	}
	if res.Stage != ProbeStageLookup {
		t.Fatalf("Stage = %q, want %q (failure happens before any spawn)", res.Stage, ProbeStageLookup)
	}
	if !strings.Contains(res.Error, "unknown adapter") {
		t.Fatalf("Error = %q, want substring %q", res.Error, "unknown adapter")
	}
	if res.AdapterID != "no-such-adapter" {
		t.Fatalf("AdapterID = %q, want round-tripped id", res.AdapterID)
	}
}

// TestFindAdapter pins the lookup helper; the health handler relies on
// it to 404 cleanly before we kick off any probe work.
func TestFindAdapter(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"codex", "claude_code", "cursor_agent"} {
		if a, ok := FindAdapter(id); !ok || a.ID != id {
			t.Fatalf("FindAdapter(%q) = (%+v, %v), want hit", id, a, ok)
		}
	}

	if _, ok := FindAdapter("not-real"); ok {
		t.Fatalf("FindAdapter(\"not-real\") = ok, want miss")
	}
}

// TestLookupHint covers the not-installed hint generator. It must
// always produce *some* operator-actionable string; an empty hint
// would render an empty banner cell in the UI.
func TestLookupHint(t *testing.T) {
	t.Parallel()

	for _, a := range BuiltIns() {
		hint := lookupHint(a)
		if hint == "" {
			t.Fatalf("lookupHint(%s) = empty — every adapter must surface a suggestion", a.ID)
		}
		// Hint should reference the adapter by name or by managed
		// package — never just "install something".
		if !strings.Contains(hint, a.Name) && !strings.Contains(hint, a.Managed.Package) {
			t.Errorf("lookupHint(%s) = %q, want it to reference the adapter or its managed package", a.ID, hint)
		}
	}
}
