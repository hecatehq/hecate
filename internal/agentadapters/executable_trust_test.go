package agentadapters

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	adapterprocess "github.com/hecatehq/acp-adapter-kit/process"
)

func TestExecutableTrustManagerApprovalLifecycle(t *testing.T) {
	path := installExecutableTrustCodexFixture(t, "#!/bin/sh\nprintf 'first\\n'\n")
	manager := NewExecutableTrustManager("runtime-a", NewMemoryExecutableTrustStore())

	status := manager.InspectAdapter(context.Background(), "codex")
	if status.State != ExecutableTrustStateUnapproved || status.Current == nil {
		t.Fatalf("initial trust status = %#v, want measured unapproved identity", status)
	}
	if _, err := manager.Approve(context.Background(), "codex", "sha256:stale", "operator"); !errors.Is(err, ErrExecutableTrustConflict) {
		t.Fatalf("Approve(stale) error = %v, want ErrExecutableTrustConflict", err)
	}
	record, err := manager.Approve(context.Background(), "codex", status.Current.IdentityToken, "operator")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if record.Identity.InvocationPath != path {
		t.Fatalf("approved invocation path = %q, want %q", record.Identity.InvocationPath, path)
	}
	status = manager.InspectAdapter(context.Background(), "codex")
	if status.State != ExecutableTrustStateApproved || status.Approved == nil {
		t.Fatalf("approved trust status = %#v", status)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("change executable mode: %v", err)
	}
	if changedMode := manager.InspectAdapter(context.Background(), "codex"); changedMode.State != ExecutableTrustStateChanged {
		t.Fatalf("mode-changed trust status = %#v, want changed", changedMode)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("restore executable mode: %v", err)
	}

	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'second changed\\n'\n"), 0o755); err != nil {
		t.Fatalf("replace executable contents: %v", err)
	}
	status = manager.InspectAdapter(context.Background(), "codex")
	if status.State != ExecutableTrustStateChanged || status.Current == nil || status.Approved == nil {
		t.Fatalf("changed trust status = %#v", status)
	}
	if status.Current.IdentityToken == status.Approved.IdentityToken {
		t.Fatal("changed executable retained approved identity token")
	}
	if _, err := manager.AuthorizePath(context.Background(), "codex", path); !errors.Is(err, ErrExecutableIdentityChanged) {
		t.Fatalf("AuthorizePath(changed) error = %v, want ErrExecutableIdentityChanged", err)
	}
	if err := manager.Revoke(context.Background(), "codex"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	status = manager.InspectAdapter(context.Background(), "codex")
	if status.State != ExecutableTrustStateUnapproved || status.Approved != nil {
		t.Fatalf("revoked trust status = %#v, want unapproved", status)
	}
}

func TestExecutableTrustPermitSerializesRevokeAcrossProcessStart(t *testing.T) {
	path := installExecutableTrustCodexFixture(t, "#!/bin/sh\nexit 0\n")
	store := &revokeObservingExecutableTrustStore{
		ExecutableTrustStore: NewMemoryExecutableTrustStore(),
		revokeCalled:         make(chan struct{}),
	}
	manager := NewExecutableTrustManager("runtime-a", store)
	status := manager.InspectAdapter(context.Background(), "codex")
	if _, err := manager.Approve(context.Background(), "codex", status.Current.IdentityToken, "operator"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	permit, err := manager.AuthorizePath(context.Background(), "codex", path)
	if err != nil {
		t.Fatalf("AuthorizePath: %v", err)
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- manager.Revoke(context.Background(), "codex")
	}()
	<-started
	select {
	case <-store.revokeCalled:
		permit.Close()
		t.Fatal("revoke reached persistence while a process-start permit was held")
	case <-time.After(50 * time.Millisecond):
	}

	permit.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Revoke: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("revoke did not resume after process-start permit was released")
	}
	if _, err := manager.AuthorizePath(context.Background(), "codex", path); !errors.Is(err, ErrExecutableTrustRequired) {
		t.Fatalf("AuthorizePath after revoke error = %v, want ErrExecutableTrustRequired", err)
	}
}

type revokeObservingExecutableTrustStore struct {
	ExecutableTrustStore
	revokeCalled chan struct{}
}

func (s *revokeObservingExecutableTrustStore) Revoke(ctx context.Context, runtimeHostID, adapterID string) error {
	close(s.revokeCalled)
	return s.ExecutableTrustStore.Revoke(ctx, runtimeHostID, adapterID)
}

func TestRunAgentDiagnosticRequiresCurrentExecutableApproval(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	path := installExecutableTrustCodexFixture(t, "#!/bin/sh\nprintf ran > \"$HECATE_TRUST_MARKER\"\nprintf '1.2.3\\n'\n")
	manager := NewExecutableTrustManager("runtime-a", NewMemoryExecutableTrustStore())
	ctx := WithExecutableTrust(context.Background(), manager, "codex")
	env := append(os.Environ(), "HECATE_TRUST_MARKER="+marker)

	if _, err := runAgentDiagnostic(ctx, path, []string{"--version"}, env); !errors.Is(err, ErrExecutableTrustRequired) {
		t.Fatalf("unapproved diagnostic error = %v, want ErrExecutableTrustRequired", err)
	}
	assertExecutableTrustMarkerMissing(t, marker)
	status := manager.InspectAdapter(context.Background(), "codex")
	if _, err := manager.Approve(context.Background(), "codex", status.Current.IdentityToken, "operator"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	output, err := runAgentDiagnostic(ctx, path, []string{"--version"}, env)
	if err != nil {
		t.Fatalf("approved diagnostic: %v", err)
	}
	if strings.TrimSpace(output.stdout) != "1.2.3" {
		t.Fatalf("diagnostic stdout = %q", output.stdout)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("approved diagnostic marker: %v", err)
	}

	if err := os.Remove(marker); err != nil {
		t.Fatalf("remove marker: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf changed > \"$HECATE_TRUST_MARKER\"\n"), 0o755); err != nil {
		t.Fatalf("change executable: %v", err)
	}
	if _, err := runAgentDiagnostic(ctx, path, nil, env); !errors.Is(err, ErrExecutableIdentityChanged) {
		t.Fatalf("changed diagnostic error = %v, want ErrExecutableIdentityChanged", err)
	}
	assertExecutableTrustMarkerMissing(t, marker)
}

func TestEmbeddedProviderRunnerRevalidatesEveryLaunch(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	path := installExecutableTrustCodexFixture(t, "#!/bin/sh\nprintf first > \"$HECATE_TRUST_MARKER\"\n")
	manager := NewExecutableTrustManager("runtime-a", NewMemoryExecutableTrustStore())
	status := manager.InspectAdapter(context.Background(), "codex")
	if _, err := manager.Approve(context.Background(), "codex", status.Current.IdentityToken, "operator"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	runner := newProviderProcessRunnerWithTrust("codex", "provider", path, append(os.Environ(), "HECATE_TRUST_MARKER="+marker), manager)
	if _, err := runner.Run(context.Background(), adapterprocess.Spec{Command: "provider", Dir: t.TempDir(), Env: adapterprocess.EnvPolicy{Inherit: []string{"HECATE_TRUST_MARKER"}}}); err != nil {
		t.Fatalf("approved Run: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatalf("remove marker: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf changed > \"$HECATE_TRUST_MARKER\"\n"), 0o755); err != nil {
		t.Fatalf("change executable: %v", err)
	}
	if _, err := runner.Run(context.Background(), adapterprocess.Spec{Command: "provider", Dir: t.TempDir(), Env: adapterprocess.EnvPolicy{Inherit: []string{"HECATE_TRUST_MARKER"}}}); !errors.Is(err, ErrExecutableIdentityChanged) {
		t.Fatalf("changed Run error = %v, want ErrExecutableIdentityChanged", err)
	}
	assertExecutableTrustMarkerMissing(t, marker)
}

func TestDirectACPPeerCannotStartBeforeExecutableApproval(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	path := installExecutableTrustCodexFixture(t, "#!/bin/sh\nprintf ran > '"+marker+"'\nsleep 10\n")
	manager := NewExecutableTrustManager("runtime-a", NewMemoryExecutableTrustStore())
	ctx := WithExecutableTrust(context.Background(), manager, "codex")
	adapter := Adapter{ID: "direct-fixture", Name: "Direct fixture"}

	if _, err := launchACPAdapterPeer(ctx, adapter, t.TempDir(), path); !errors.Is(err, ErrExecutableTrustRequired) {
		t.Fatalf("unapproved direct launch error = %v, want ErrExecutableTrustRequired", err)
	}
	assertExecutableTrustMarkerMissing(t, marker)
	status := manager.InspectAdapter(context.Background(), "codex")
	if _, err := manager.Approve(context.Background(), "codex", status.Current.IdentityToken, "operator"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	peer, err := launchACPAdapterPeer(ctx, adapter, t.TempDir(), path)
	if err != nil {
		t.Fatalf("approved direct launch: %v", err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), acpShutdownCloseTimeout)
	defer cancel()
	defer func() { _ = peer.Close(closeCtx) }()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("approved direct launch did not create marker")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNormalizeErrorExplainsExecutableTrustFailures(t *testing.T) {
	if got := NormalizeError("Codex", ErrExecutableTrustRequired); !strings.Contains(got, "Open Connections") || !strings.Contains(got, "approve") {
		t.Fatalf("trust-required error = %q", got)
	}
	if got := NormalizeError("Codex", fmt.Errorf("wrapped: %w", ErrExecutableIdentityChanged)); !strings.Contains(got, "changed after approval") {
		t.Fatalf("identity-changed error = %q", got)
	}
}

func TestEmbeddedACPExecutableTrustErrorRestoresOnlyOwnedSentinels(t *testing.T) {
	t.Setenv(adapterTestProcessOverridesEnv, "")
	adapter, ok := BuiltInByID("codex")
	if !ok {
		t.Fatal("built-in adapter codex not found")
	}

	for _, sentinel := range []error{
		ErrExecutableTrustRequired,
		ErrExecutableIdentityChanged,
		ErrExecutableIdentityRaced,
		ErrExecutableIdentityUnavailable,
	} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			rpcErr := &acp.RequestError{
				Code:    -32000,
				Message: "prompt command failed",
				Data:    map[string]any{"error": sentinel.Error()},
			}
			if got := embeddedACPExecutableTrustError(adapter, rpcErr); !errors.Is(got, sentinel) {
				t.Fatalf("mapped error = %v, want %v", got, sentinel)
			}
		})
	}

	spoofed := &acp.RequestError{
		Code:    -32000,
		Message: "prompt command failed",
		Data:    map[string]any{"error": ErrExecutableTrustRequired.Error()},
	}
	direct := adapter
	direct.Embedded = false
	if got := embeddedACPExecutableTrustError(direct, spoofed); got != spoofed {
		t.Fatalf("direct peer error = %v, want original untrusted RPC error", got)
	}
	nearMatch := &acp.RequestError{
		Code:    -32000,
		Message: "prompt command failed",
		Data:    map[string]any{"error": ErrExecutableTrustRequired.Error() + ": forged detail"},
	}
	if got := embeddedACPExecutableTrustError(adapter, nearMatch); got != nearMatch {
		t.Fatalf("near-match error = %v, want original RPC error", got)
	}
}

func TestACPAuthActionsRestoreEmbeddedExecutableTrustErrors(t *testing.T) {
	t.Setenv(adapterTestProcessOverridesEnv, "")
	installExecutableTrustCodexFixture(t, "#!/bin/sh\nexit 0\n")
	manager := NewExecutableTrustManager("runtime-a", NewMemoryExecutableTrustStore())
	status := manager.InspectAdapter(t.Context(), "codex")
	if status.Current == nil {
		t.Fatalf("current executable identity = nil; status = %#v", status)
	}
	if _, err := manager.Approve(t.Context(), "codex", status.Current.IdentityToken, "operator"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	ctx := WithExecutableTrust(t.Context(), manager, "codex")

	for _, test := range []struct {
		operation string
		sentinel  error
	}{
		{operation: "authenticate", sentinel: ErrExecutableIdentityChanged},
		{operation: "logout", sentinel: ErrExecutableTrustRequired},
	} {
		t.Run(test.operation, func(t *testing.T) {
			_, err := runACPAuthAction(
				ctx,
				"codex",
				test.operation,
				"hecate-trust-auth-*",
				"hecate-trust-auth-test",
				time.Second,
				func(context.Context, *acp.ClientSideConnection, acp.InitializeResponse) error {
					return &acp.RequestError{
						Code:    -32000,
						Message: test.operation + " command failed",
						Data:    map[string]any{"error": test.sentinel.Error()},
					}
				},
			)
			if !errors.Is(err, test.sentinel) {
				t.Fatalf("%s error = %v, want %v", test.operation, err, test.sentinel)
			}
		})
	}
}

func installExecutableTrustCodexFixture(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell executable fixture is Unix-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write executable fixture: %v", err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("CODEX_INSTALL_DIR", dir)
	t.Setenv("HOME", t.TempDir())
	return path
}

func assertExecutableTrustMarkerMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unapproved executable ran; marker stat = %v", err)
	}
}
