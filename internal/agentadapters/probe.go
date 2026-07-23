package agentadapters

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/telemetry"
)

// Probe statuses. "unverified" is reserved for passive catalog/health reads;
// every other status is an operator-facing classification from an explicit
// probe or a lookup failure. Stable strings — once exported on the wire they
// are part of the frontend contract.
const (
	ProbeStatusUnverified   = "unverified"
	ProbeStatusReady        = "ready"
	ProbeStatusNotInstalled = "not_installed"
	ProbeStatusAuthRequired = "auth_required"
	ProbeStatusError        = "error"
)

// Probe stages. Track which step in the runtime-and-handshake sequence
// failed so the UI can give a concrete next-action hint without the
// operator having to read the raw error.
const (
	ProbeStageLookup     = "lookup"
	ProbeStageSpawn      = "spawn"
	ProbeStageInitialize = "initialize"
	ProbeStageNewSession = "new_session"
	ProbeStageReady      = "ready"
)

// ProbeResult is the typed outcome of a single Probe call. Stage names
// the step that completed (on success) or that failed (on error). When
// Status == "auth_required" or "error", Error and Stderr carry the raw
// diagnostic; the UI surfaces them verbatim because adapters phrase
// auth failures inconsistently and any forced normalization would
// destroy operator-actionable detail.
type ProbeResult struct {
	AdapterID            string            `json:"adapter_id"`
	Status               string            `json:"status"`
	Stage                string            `json:"stage"`
	Path                 string            `json:"path,omitempty"`
	Error                string            `json:"error,omitempty"`
	Stderr               string            `json:"stderr,omitempty"`
	Hint                 string            `json:"hint,omitempty"`
	AgentInfo            *ProbeAgentInfo   `json:"agent_info,omitempty"`
	CapabilitiesKnown    bool              `json:"capabilities_known,omitempty"`
	SupportsAuthenticate bool              `json:"supports_authenticate"`
	SupportsLogout       bool              `json:"supports_logout"`
	SupportsLoadSession  bool              `json:"supports_load_session"`
	AuthMethods          []ProbeAuthMethod `json:"auth_methods,omitempty"`
	DurationMS           int64             `json:"duration_ms"`
}

// ProbeAgentInfo keeps the health-probe name for the shared ACP implementation
// metadata projection.
type ProbeAgentInfo = agentcontrols.ImplementationInfo

// ProbeAuthMethod is the non-secret subset of ACP Initialize authMethods that
// Hecate can safely surface in health responses. Env var names and terminal env
// payloads intentionally stay inside the adapter runtime boundary.
type ProbeAuthMethod struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// probeTimeout caps runtime startup + Initialize + NewSession + cleanup
// round-trip. Real ACP startup uses 20s per call; we run two calls
// back-to-back and want to surface a "stuck adapter" failure in
// roughly the time an operator will wait staring at a button.
const probeTimeout = 15 * time.Second

// probeMetrics holds the optional AgentAdapterMetrics sink used by
// every Probe call. atomic.Pointer to avoid a lock on the hot path
// — the handler installs this once at startup and Probe only ever
// reads. Nil is safe (every Record* method is nil-tolerant).
var probeMetrics atomic.Pointer[telemetry.AgentAdapterMetrics]

// SetProbeMetrics installs the AgentAdapterMetrics used by every
// subsequent Probe call. Pass nil to disable. Callers wire this once
// at startup; Probe uses an atomic load so the setter is safe to
// call after handlers are already serving.
func SetProbeMetrics(metrics *telemetry.AgentAdapterMetrics) {
	probeMetrics.Store(metrics)
}

// Probe attempts a minimal start-and-handshake against the named adapter to
// determine whether it can prepare an ACP session. It does not prove that an
// embedded bridge can launch its vendor CLI, authenticate, or serve a turn.
//
// Sequence: resolve provider/runtime command → start runtime → ACP Initialize → ACP NewSession
// (against a temporary workspace) → terminate. On any failure, the
// stage that failed is recorded and the error + captured stderr are
// surfaced as-is. We deliberately do NOT issue a chat prompt — the
// goal is "would startACPSession succeed?", not "does the adapter launch its
// deferred vendor process or produce useful output?".
//
// Side effects: a fresh ACP session is created and immediately
// abandoned. Adapters that bill on session-create will see a single
// no-op session per probe; adapters that bill on prompt completion
// will see no charge. The `cwd` passed to NewSession is a temporary
// directory that's removed before Probe returns.
func Probe(ctx context.Context, adapterID string) (res ProbeResult) {
	defer func() {
		// Fire the probe counter at the very end, after every return
		// path has converged on res.Status. Inline-defer rather than
		// a wrapper so existing call sites keep their ergonomic
		// `result := agentadapters.Probe(...)` signature.
		if metrics := probeMetrics.Load(); metrics != nil {
			metrics.RecordProbe(ctx, telemetry.AgentAdapterProbeRecord{
				AdapterID: res.AdapterID,
				Status:    res.Status,
			})
		}
	}()

	start := time.Now()
	res = ProbeResult{AdapterID: adapterID, Stage: ProbeStageLookup}

	adapter, ok := FindAdapter(adapterID)
	if !ok {
		res.Status = ProbeStatusError
		res.Error = fmt.Sprintf("unknown adapter %q", adapterID)
		res.DurationMS = elapsedMS(start)
		return res
	}
	if _, err := validateRemoteCredentialForRequest(ctx, adapter); err != nil {
		res.Status = ProbeStatusAuthRequired
		res.Error = err.Error()
		res.Hint = remoteCredentialHint(adapter)
		res.DurationMS = elapsedMS(start)
		return res
	}
	if override, ok := adapterDevOverride(adapterID); ok {
		return probeResultForDevOverride(adapter, override, start)
	}

	path, err := resolveAdapterPeerExecutable(ctx, adapter, nil)
	if err != nil {
		res.Status = ProbeStatusNotInstalled
		res.Error = err.Error()
		res.Hint = lookupHint(adapter)
		res.DurationMS = elapsedMS(start)
		return res
	}
	res.Path = path

	// Bound the whole probe — the caller's ctx still wins, but we
	// also self-cap so a hung adapter doesn't tie up the HTTP handler.
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	workspace, err := os.MkdirTemp("", "hecate-adapter-probe-*")
	if err != nil {
		res.Stage = ProbeStageSpawn
		res.Status = ProbeStatusError
		res.Error = fmt.Sprintf("create probe workspace: %v", err)
		res.DurationMS = elapsedMS(start)
		return res
	}
	defer func() { _ = os.RemoveAll(workspace) }()

	res.Stage = ProbeStageSpawn
	peer, err := launchACPAdapterPeer(ctx, adapter, workspace, path)
	if err != nil {
		res.Status = ProbeStatusError
		if errors.Is(err, ErrRemoteCredentialRequired) {
			res.Status = ProbeStatusAuthRequired
			res.Hint = remoteCredentialHint(adapter)
		}
		res.Error = fmt.Sprintf("start adapter runtime: %v", err)
		res.DurationMS = elapsedMS(start)
		return res
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), acpShutdownCloseTimeout)
		_ = peer.Close(closeCtx)
		closeCancel()
	}()

	conn := newGuardedACPProbeConnection(workspace, peer.stdin, peer.stdout)

	res.Stage = ProbeStageInitialize
	initCtx, initCancel := context.WithTimeout(probeCtx, 10*time.Second)
	initResp, err := conn.Initialize(initCtx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo: &acp.Implementation{
			Name:    "hecate-probe",
			Version: "alpha",
		},
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapabilities{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
			Terminal: false,
		},
	})
	initCancel()
	if err != nil {
		res.Stderr = strings.TrimSpace(peer.Stderr())
		res.Status, res.Hint = classifyAdapterError(err.Error(), res.Stderr)
		if adapterID == "claude_code" && claudeCodeErrorNeedsAdapterVisibleAuth(err.Error(), res.Stderr) {
			res.Hint = adapterSignInHint(adapter)
		}
		res.Error = err.Error()
		res.DurationMS = elapsedMS(start)
		return res
	}
	applyInitializeCapabilities(&res, initResp)

	res.Stage = ProbeStageNewSession
	newCtx, newCancel := context.WithTimeout(probeCtx, 10*time.Second)
	_, err = conn.NewSession(newCtx, acp.NewSessionRequest{
		Cwd:        workspace,
		McpServers: []acp.McpServer{},
	})
	newCancel()
	if err != nil {
		res.Stderr = strings.TrimSpace(peer.Stderr())
		res.Status, res.Hint = classifyAdapterError(err.Error(), res.Stderr)
		if adapterID == "claude_code" && claudeCodeErrorNeedsAdapterVisibleAuth(err.Error(), res.Stderr) {
			res.Hint = adapterSignInHint(adapter)
		}
		res.Error = err.Error()
		res.DurationMS = elapsedMS(start)
		return res
	}

	res.Stage = ProbeStageReady
	res.Status = ProbeStatusReady
	res.Stderr = strings.TrimSpace(peer.Stderr())
	res.DurationMS = elapsedMS(start)
	return res
}

func applyInitializeCapabilities(res *ProbeResult, initResp acp.InitializeResponse) {
	if res == nil {
		return
	}
	res.CapabilitiesKnown = true
	res.AgentInfo = agentcontrols.FromACPImplementation(initResp.AgentInfo)
	res.SupportsLoadSession = initResp.AgentCapabilities.LoadSession
	res.SupportsLogout = initResp.AgentCapabilities.Auth.Logout != nil
	res.AuthMethods = probeAuthMethods(initResp.AuthMethods)
	for _, method := range initResp.AuthMethods {
		if authMethodSupportsHecateAuthenticate(method) {
			res.SupportsAuthenticate = true
			break
		}
	}
}

func probeAuthMethods(methods []acp.AuthMethod) []ProbeAuthMethod {
	if len(methods) == 0 {
		return nil
	}
	out := make([]ProbeAuthMethod, 0, len(methods))
	for _, method := range methods {
		switch {
		case method.Agent != nil:
			out = append(out, ProbeAuthMethod{
				ID:          strings.TrimSpace(method.Agent.Id),
				Kind:        "agent",
				Name:        strings.TrimSpace(method.Agent.Name),
				Description: trimStringPtr(method.Agent.Description),
			})
		case method.EnvVar != nil:
			out = append(out, ProbeAuthMethod{
				ID:          strings.TrimSpace(method.EnvVar.Id),
				Kind:        "env_var",
				Name:        strings.TrimSpace(method.EnvVar.Name),
				Description: trimStringPtr(method.EnvVar.Description),
			})
		case method.Terminal != nil:
			out = append(out, ProbeAuthMethod{
				ID:          strings.TrimSpace(method.Terminal.Id),
				Kind:        "terminal",
				Name:        strings.TrimSpace(method.Terminal.Name),
				Description: trimStringPtr(method.Terminal.Description),
			})
		}
	}
	return out
}

func authMethodSupportsHecateAuthenticate(method acp.AuthMethod) bool {
	return method.Agent != nil && strings.TrimSpace(method.Agent.Id) == ACPAuthMethodAgentLogin
}

func trimStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// ApplyProbeCapabilities makes live ACP Initialize capabilities authoritative
// for a freshly probed adapter row. Static registry flags remain the fallback
// before a probe has successfully completed Initialize.
func ApplyProbeCapabilities(status Status, result ProbeResult) Status {
	if !result.CapabilitiesKnown {
		return status
	}
	status.SupportsAuthenticate = result.SupportsAuthenticate
	status.SupportsLogout = result.SupportsLogout
	return status
}

func claudeCodeErrorNeedsAdapterVisibleAuth(errText, stderr string) bool {
	combined := strings.ToLower(errText + "\n" + stderr)
	if matchesAny(combined, "credit balance", "payment required", "subscription required", "billing") {
		return false
	}
	return matchesAny(combined,
		"authentication required",
		"unauthenticated",
		"unauthorized",
		"please log in",
		"please login",
		"please sign in",
		"login required",
		"sign in required",
		"not logged in",
		"api key",
		"apikey",
		"missing credentials",
		"invalid credentials",
		"credential",
		"401 unauthorized",
		" 401 ",
		"403 forbidden",
		" 403 ",
	)
}

// classifyAdapterError sorts a (raw error, stderr) pair into either
// "auth_required" or "error". The detection is heuristic — adapters
// phrase auth failures differently (Cursor: "Authentication required",
// Claude Code: "Credit balance is too low", Codex: "Please run codex
// login") — but the markers below cover what we've observed in the
// wild. Anything that doesn't match falls through to "error" and the
// raw text is shown verbatim so the operator can act on it.
//
// Returns (status, hint). hint is a one-line action suggestion or
// empty when we don't have a confident recommendation.
func classifyAdapterError(errText, stderr string) (string, string) {
	combined := strings.ToLower(errText + "\n" + stderr)
	if matchesAny(combined, "context deadline exceeded", "deadline exceeded", "timed out", "timeout") {
		return ProbeStatusError, "The adapter did not finish its ACP startup check in time. Check the CLI is responsive, signed in, and not waiting on a browser or network prompt, then retry from Connections."
	}
	if matchesAny(combined,
		"authentication required",
		"unauthenticated",
		"unauthorized",
		"please log in",
		"please login",
		"please sign in",
		"login required",
		"sign in required",
		"not logged in",
		"api key",
		"apikey",
		"missing credentials",
		"invalid credentials",
		"credential",
		"credit balance",
		"payment required",
		"subscription required",
		"401 unauthorized",
		" 401 ",
		"403 forbidden",
		" 403 ",
	) {
		return ProbeStatusAuthRequired, "Adapter started but failed authentication. Try the adapter's CLI login flow or set its API-key env var."
	}
	return ProbeStatusError, ""
}

func matchesAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

// lookupHint is the human-friendly suggestion attached to a "binary
// not on PATH" failure.
func lookupHint(adapter Adapter) string {
	if adapter.DocsURL != "" {
		return fmt.Sprintf("Install %s and ensure it's on PATH. Setup docs: %s", adapter.Name, adapter.DocsURL)
	}
	return fmt.Sprintf("Install %s and ensure it's on PATH.", adapter.Name)
}

func probeResultForDevOverride(adapter Adapter, override string, start time.Time) ProbeResult {
	res := ProbeResult{
		AdapterID:  adapter.ID,
		Stage:      ProbeStageReady,
		Path:       "dev-override://" + adapter.ID,
		DurationMS: elapsedMS(start),
	}
	switch override {
	case adapterDevOverrideMissing:
		res.Stage = ProbeStageLookup
		res.Status = ProbeStatusNotInstalled
		res.Path = ""
		res.Error = "forced ACP connector missing by " + adapterDevOverrideEnv
		res.Hint = lookupHint(adapter)
	case adapterDevOverrideAuthRequired:
		res.Status = ProbeStatusAuthRequired
		res.Error = "forced auth_required by " + adapterDevOverrideEnv
		res.Hint = adapterSignInHint(adapter)
	case adapterDevOverrideBilling:
		res.Status = ProbeStatusError
		res.Error = "forced billing by " + adapterDevOverrideEnv
		res.Hint = "Billing or usage limit requires attention."
	case adapterDevOverrideAppMissing:
		res.Status = ProbeStatusError
		res.Error = "forced app CLI missing by " + adapterDevOverrideEnv
		res.Hint = adapterAppMissingHint(adapter)
	case adapterDevOverrideError:
		res.Status = ProbeStatusError
		res.Error = "forced probe error by " + adapterDevOverrideEnv
		res.Hint = "The adapter fixture is simulating a startup or handshake failure."
	default:
		res.Status = ProbeStatusReady
	}
	return res
}

func elapsedMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

// probeClient is the minimal ACP client used during health probes.
// It implements the same filesystem callbacks advertised during
// Initialize against the temporary probe workspace; everything else
// either no-ops or fails closed because probes never drive turns.
type probeClient struct {
	workspace string
}

func (probeClient) SessionUpdate(context.Context, acp.SessionNotification) error {
	return nil
}

func (probeClient) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	// Probe doesn't drive any tool calls; if an adapter pushes a
	// RequestPermission during the handshake (none we've seen do),
	// return Cancelled — "not approved" rather than a synthesized
	// denial.
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
	}, nil
}

func (c probeClient) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return (&acpChatClient{workspace: c.workspace}).ReadTextFile(ctx, params)
}

func (c probeClient) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return (&acpChatClient{workspace: c.workspace}).WriteTextFile(ctx, params)
}

func (probeClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errors.New("probe: terminal not supported")
}

func (probeClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errors.New("probe: terminal not supported")
}

func (probeClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errors.New("probe: terminal not supported")
}

func (probeClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errors.New("probe: terminal not supported")
}

func (probeClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errors.New("probe: terminal not supported")
}
