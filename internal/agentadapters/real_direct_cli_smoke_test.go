//go:build real_cli

package agentadapters

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// This opt-in smoke reaches the direct ACP modes shipped by vendor CLIs. The
// default suite stays hermetic; run it only with disposable prompts and an
// operator-owned authenticated CLI because a successful turn may consume quota.
func TestRealDirectACPCLIsSmoke(t *testing.T) {
	if os.Getenv("HECATE_ACP_REAL_CLI_SMOKE") != "1" {
		t.Skip("set HECATE_ACP_REAL_CLI_SMOKE=1 to exercise authenticated direct ACP CLIs")
	}

	cases := map[string]struct {
		token string
	}{
		"cursor_agent": {token: "HECATE_CURSOR_ACP_OK"},
		"grok_build":   {token: "HECATE_GROK_ACP_OK"},
	}
	requested := strings.TrimSpace(os.Getenv("HECATE_ACP_REAL_ADAPTERS"))
	if requested == "" {
		requested = "cursor_agent,grok_build"
	}

	for _, adapterID := range strings.Split(requested, ",") {
		adapterID = strings.TrimSpace(adapterID)
		testCase, ok := cases[adapterID]
		if !ok {
			t.Fatalf("HECATE_ACP_REAL_ADAPTERS contains unsupported direct adapter %q", adapterID)
		}
		t.Run(adapterID, func(t *testing.T) {
			probeCtx, cancelProbe := context.WithTimeout(t.Context(), 30*time.Second)
			probe := Probe(probeCtx, adapterID)
			cancelProbe()
			if probe.Status != ProbeStatusReady || probe.Stage != ProbeStageReady {
				t.Fatalf("Probe(%s) = status %q at stage %q: %s\n%s", adapterID, probe.Status, probe.Stage, probe.Error, probe.Stderr)
			}

			manager := NewSessionManager()
			t.Cleanup(func() {
				shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancelShutdown()
				if err := manager.Shutdown(shutdownCtx); err != nil {
					t.Errorf("Shutdown(%s): %v", adapterID, err)
				}
			})

			workspace := t.TempDir()
			sessionID := "real_direct_" + adapterID
			prepareCtx, cancelPrepare := context.WithTimeout(t.Context(), 60*time.Second)
			prepared, err := manager.PrepareSession(prepareCtx, PrepareSessionRequest{
				SessionID: sessionID,
				AdapterID: adapterID,
				Workspace: workspace,
			})
			cancelPrepare()
			if err != nil {
				t.Fatalf("PrepareSession(%s): %v", adapterID, err)
			}
			if prepared.DriverKind != DriverKindACP || prepared.NativeSessionID == "" || !prepared.SessionStarted {
				t.Fatalf("PrepareSession(%s) = %#v, want a new ACP session", adapterID, prepared)
			}

			runCtx, cancelRun := context.WithTimeout(t.Context(), 4*time.Minute)
			result, err := manager.Run(runCtx, RunRequest{
				SessionID: sessionID,
				AdapterID: adapterID,
				Workspace: workspace,
				Prompt: PromptInput{Text: "Reply with exactly " + testCase.token +
					" and nothing else. Do not inspect files or run tools."},
				Timeout:        3 * time.Minute,
				MaxOutputBytes: 64 * 1024,
			})
			cancelRun()
			if err != nil {
				t.Fatalf("Run(%s): %v", adapterID, err)
			}
			if result.DriverKind != DriverKindACP || result.SessionStarted || result.NativeSessionID != prepared.NativeSessionID {
				t.Fatalf("Run(%s) = %#v, want prepared ACP session %q reused", adapterID, result, prepared.NativeSessionID)
			}
			if !strings.Contains(result.Output, testCase.token) {
				t.Fatalf("Run(%s) output = %q, want token %q", adapterID, result.Output, testCase.token)
			}
		})
	}
}
