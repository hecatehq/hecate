package agentadapters

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/hecatehq/hecate/internal/remoteruntime"
)

const (
	AuthenticateStatusAuthenticated = "authenticated"
	LogoutStatusLoggedOut           = "logged_out"
	ACPAuthMethodAgentLogin         = "agent-login"
)

var (
	acpAuthenticateActionTimeout = 5 * time.Minute
	acpLogoutActionTimeout       = 30 * time.Second
	acpAuthInitializeTimeout     = 10 * time.Second
)

type LogoutResult struct {
	AdapterID  string `json:"adapter_id"`
	Status     string `json:"status"`
	Path       string `json:"path,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type AuthenticateResult struct {
	AdapterID  string `json:"adapter_id"`
	Status     string `json:"status"`
	MethodID   string `json:"method_id"`
	Path       string `json:"path,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type acpAuthActionResult struct {
	Adapter    Adapter
	Path       string
	DurationMS int64
}

type acpAuthAction func(context.Context, *acp.ClientSideConnection, acp.InitializeResponse) error

func Authenticate(ctx context.Context, adapterID string) (AuthenticateResult, error) {
	if _, ok := remoteruntime.FromContext(ctx); ok {
		return AuthenticateResult{AdapterID: strings.TrimSpace(adapterID), MethodID: ACPAuthMethodAgentLogin}, fmt.Errorf("ACP authenticate is local-only in remote runtime mode; configure a remote-safe credential environment variable instead")
	}
	action, err := runACPAuthAction(ctx, adapterID, "authenticate", "hecate-adapter-authenticate-*", "hecate-authenticate", acpAuthenticateActionTimeout, func(ctx context.Context, conn *acp.ClientSideConnection, initResp acp.InitializeResponse) error {
		if !initializeSupportsHecateAuthenticate(initResp) {
			return fmt.Errorf("adapter %q does not advertise ACP auth method %q", adapterID, ACPAuthMethodAgentLogin)
		}
		_, err := conn.Authenticate(ctx, acp.AuthenticateRequest{MethodId: ACPAuthMethodAgentLogin})
		return err
	})
	res := AuthenticateResult{
		AdapterID:  action.Adapter.ID,
		MethodID:   ACPAuthMethodAgentLogin,
		Path:       action.Path,
		DurationMS: action.DurationMS,
	}
	if err != nil {
		return res, err
	}
	res.Status = AuthenticateStatusAuthenticated
	return res, nil
}

func Logout(ctx context.Context, adapterID string) (LogoutResult, error) {
	action, err := runACPAuthAction(ctx, adapterID, "logout", "hecate-adapter-logout-*", "hecate-logout", acpLogoutActionTimeout, func(ctx context.Context, conn *acp.ClientSideConnection, initResp acp.InitializeResponse) error {
		if initResp.AgentCapabilities.Auth.Logout == nil {
			return fmt.Errorf("adapter %q does not advertise ACP logout", adapterID)
		}
		_, err := conn.Logout(ctx, acp.LogoutRequest{})
		return err
	})
	res := LogoutResult{
		AdapterID:  action.Adapter.ID,
		Path:       action.Path,
		DurationMS: action.DurationMS,
	}
	if err != nil {
		return res, err
	}
	res.Status = LogoutStatusLoggedOut
	return res, nil
}

func runACPAuthAction(ctx context.Context, adapterID, operation, workspacePattern, clientName string, actionTimeout time.Duration, action acpAuthAction) (acpAuthActionResult, error) {
	start := time.Now()
	adapter, ok := BuiltInByID(adapterID)
	if !ok {
		return acpAuthActionResult{}, fmt.Errorf("agent adapter %q not found", strings.TrimSpace(adapterID))
	}
	res := acpAuthActionResult{Adapter: adapter}

	path, err := resolveAdapterPeerExecutable(ctx, adapter, nil)
	if err != nil {
		res.DurationMS = elapsedMS(start)
		return res, err
	}
	res.Path = path

	if actionTimeout <= 0 {
		actionTimeout = probeTimeout
	}
	workspace, err := os.MkdirTemp("", workspacePattern)
	if err != nil {
		res.DurationMS = elapsedMS(start)
		return res, fmt.Errorf("create %s workspace: %w", operation, err)
	}
	defer func() { _ = os.RemoveAll(workspace) }()

	peer, err := launchACPAdapterPeer(ctx, adapter, workspace, path)
	if err != nil {
		res.DurationMS = elapsedMS(start)
		return res, fmt.Errorf("start ACP adapter runtime %q: %w", adapter.ID, err)
	}
	cleanupPeer := func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), acpShutdownCloseTimeout)
		_ = peer.Close(closeCtx)
		closeCancel()
	}
	defer cleanupPeer()

	conn := newGuardedACPProbeConnection(workspace, peer.stdin, peer.stdout)
	initCtx, initCancel := context.WithTimeout(ctx, acpAuthInitializeTimeout)
	initResp, err := conn.Initialize(initCtx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo: &acp.Implementation{
			Name:    clientName,
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
		cleanupPeer()
		res.DurationMS = elapsedMS(start)
		return res, fmt.Errorf("initialize ACP adapter %q: %w%s", adapter.ID, err, stderrSuffix(peer.Stderr()))
	}

	callCtx, callCancel := context.WithTimeout(ctx, actionTimeout)
	err = action(callCtx, conn, initResp)
	callCancel()
	if err != nil {
		cleanupPeer()
		res.DurationMS = elapsedMS(start)
		return res, fmt.Errorf("%s ACP adapter %q: %w%s", operation, adapter.ID, err, stderrSuffix(peer.Stderr()))
	}
	res.DurationMS = elapsedMS(start)
	return res, nil
}

func initializeSupportsHecateAuthenticate(initResp acp.InitializeResponse) bool {
	for _, method := range initResp.AuthMethods {
		if authMethodSupportsHecateAuthenticate(method) {
			return true
		}
	}
	return false
}
