package agentadapters

import (
	"context"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/hecatehq/hecate/internal/telemetry"
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

	t.Run("timeout includes operator hint", func(t *testing.T) {
		t.Parallel()
		status, hint := classifyAdapterError("context deadline exceeded", "")
		if status != ProbeStatusError {
			t.Fatalf("status = %q, want %q", status, ProbeStatusError)
		}
		if !strings.Contains(hint, "retry from Connections") {
			t.Fatalf("hint = %q, want retry guidance", hint)
		}
	})
}

func TestClaudeCodeErrorNeedsAdapterVisibleAuth(t *testing.T) {
	t.Parallel()

	authCases := []string{
		"Authentication required",
		"missing credentials",
		"request returned 401 unauthorized",
	}
	for _, tc := range authCases {
		if !claudeCodeErrorNeedsAdapterVisibleAuth(tc, "") {
			t.Fatalf("claudeCodeErrorNeedsAdapterVisibleAuth(%q) = false, want true", tc)
		}
	}

	billingCases := []string{
		"Credit balance is too low",
		"payment required",
		"subscription required",
	}
	for _, tc := range billingCases {
		if claudeCodeErrorNeedsAdapterVisibleAuth(tc, "") {
			t.Fatalf("claudeCodeErrorNeedsAdapterVisibleAuth(%q) = true, want false", tc)
		}
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

func TestProbeHonorsDevOverrideMatrix(t *testing.T) {
	cases := []struct {
		name       string
		override   string
		wantStatus string
		wantStage  string
		wantHint   string
	}{
		{
			name:       "connector missing",
			override:   "codex=connector_missing",
			wantStatus: ProbeStatusNotInstalled,
			wantStage:  ProbeStageLookup,
			wantHint:   "@zed-industries/codex-acp",
		},
		{
			name:       "app missing",
			override:   "codex=app_missing",
			wantStatus: ProbeStatusError,
			wantStage:  ProbeStageReady,
			wantHint:   "Install Codex CLI",
		},
		{
			name:       "no auth",
			override:   "codex=no_auth",
			wantStatus: ProbeStatusAuthRequired,
			wantStage:  ProbeStageReady,
			wantHint:   "codex login",
		},
		{
			name:       "ready",
			override:   "codex=ready",
			wantStatus: ProbeStatusReady,
			wantStage:  ProbeStageReady,
		},
		{
			name:       "billing",
			override:   "codex=billing",
			wantStatus: ProbeStatusError,
			wantStage:  ProbeStageReady,
			wantHint:   "Billing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(adapterDevOverrideEnv, tc.override)
			res := Probe(context.Background(), "codex")
			if res.Status != tc.wantStatus {
				t.Fatalf("Status = %q, want %q; result = %#v", res.Status, tc.wantStatus, res)
			}
			if res.Stage != tc.wantStage {
				t.Fatalf("Stage = %q, want %q", res.Stage, tc.wantStage)
			}
			if tc.wantHint != "" && !strings.Contains(res.Hint, tc.wantHint) {
				t.Fatalf("Hint = %q, want substring %q", res.Hint, tc.wantHint)
			}
		})
	}
}

// TestProbeRecordsCounterWithFinalStatus pins the probe-counter
// contract: every Probe call fires exactly once, labeled with the
// adapter id and the final classification — even on the
// short-circuit path where the adapter isn't in the registry. The
// test injects a metrics sink via SetProbeMetrics, runs Probe
// against a non-existent adapter (so the test never spawns any real
// binary), and asserts the counter saw exactly one
// (codex-fake, error) increment.
//
// Cannot run in parallel — SetProbeMetrics installs a process-wide
// hook that other Probe-driven tests in this package would observe.
func TestProbeRecordsCounterWithFinalStatus(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := telemetry.NewAgentAdapterMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewAgentAdapterMetricsWithMeterProvider: %v", err)
	}

	SetProbeMetrics(metrics)
	t.Cleanup(func() { SetProbeMetrics(nil) })

	res := Probe(context.Background(), "no-such-adapter")
	if res.Status != ProbeStatusError {
		t.Fatalf("res.Status = %q, want %q", res.Status, ProbeStatusError)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	totalByStatus := make(map[string]int64)
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != telemetry.MetricAgentAdapterProbeTotal {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("probe metric data type = %T, want Sum[int64]", m.Data)
			}
			for _, dp := range sum.DataPoints {
				status, _ := dp.Attributes.Value(telemetry.AttrHecateAgentProbeStatus)
				totalByStatus[status.AsString()] += dp.Value
			}
		}
	}
	if got := totalByStatus[ProbeStatusError]; got != 1 {
		t.Errorf("probe counter for status=%q = %d, want exactly 1; full map = %v",
			ProbeStatusError, got, totalByStatus)
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
