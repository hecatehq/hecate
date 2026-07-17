//go:build real_cli

package agentadapters

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// This opt-in smoke reaches both Hecate's built-in adapters and direct ACP modes
// through vendor CLIs. The default suite stays hermetic; run it only with
// disposable prompts and operator-owned authentication because turns consume quota.
func TestRealACPCLIsSmoke(t *testing.T) {
	if os.Getenv("HECATE_ACP_REAL_CLI_SMOKE") != "1" {
		t.Skip("set HECATE_ACP_REAL_CLI_SMOKE=1 to exercise authenticated direct ACP CLIs")
	}

	cases := map[string]struct {
		token     string
		fileToken string
	}{
		"codex":        {token: "HECATE_CODEX_EMBEDDED_OK", fileToken: "HECATE_CODEX_FILE_OK"},
		"claude_code":  {token: "HECATE_CLAUDE_EMBEDDED_OK", fileToken: "HECATE_CLAUDE_FILE_OK"},
		"cursor_agent": {token: "HECATE_CURSOR_ACP_OK", fileToken: "HECATE_CURSOR_FILE_OK"},
		"grok_build":   {token: "HECATE_GROK_ACP_OK", fileToken: "HECATE_GROK_FILE_OK"},
	}
	requested := strings.TrimSpace(os.Getenv("HECATE_ACP_REAL_ADAPTERS"))
	if requested == "" {
		requested = "codex,claude_code,cursor_agent,grok_build"
	}

	adapterIDs := make([]string, 0, len(cases))
	seen := make(map[string]struct{}, len(cases))
	for _, adapterID := range strings.Split(requested, ",") {
		adapterID = strings.TrimSpace(adapterID)
		_, ok := cases[adapterID]
		if !ok {
			t.Fatal("HECATE_ACP_REAL_ADAPTERS contains an unsupported adapter")
		}
		if _, duplicate := seen[adapterID]; duplicate {
			continue
		}
		seen[adapterID] = struct{}{}
		adapterIDs = append(adapterIDs, adapterID)
	}

	for _, adapterID := range adapterIDs {
		testCase := cases[adapterID]
		t.Run(adapterID, func(t *testing.T) {
			probeCtx, cancelProbe := context.WithTimeout(t.Context(), 30*time.Second)
			probe := Probe(probeCtx, adapterID)
			cancelProbe()
			if probe.Status != ProbeStatusReady || probe.Stage != ProbeStageReady {
				t.Fatalf("Probe(%s) = status %q at stage %q", adapterID, probe.Status, probe.Stage)
			}

			workspace := t.TempDir()
			manager := NewSessionManager()
			t.Cleanup(func() {
				shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancelShutdown()
				if err := manager.Shutdown(shutdownCtx); err != nil {
					t.Errorf("Shutdown(%s) failed", adapterID)
				}
			})

			sessionID := "real_cli_" + adapterID
			prepareCtx, cancelPrepare := context.WithTimeout(t.Context(), 60*time.Second)
			prepared, err := manager.PrepareSession(prepareCtx, PrepareSessionRequest{
				SessionID: sessionID,
				AdapterID: adapterID,
				Workspace: workspace,
			})
			cancelPrepare()
			if err != nil {
				t.Fatalf("PrepareSession(%s) failed", adapterID)
			}
			if prepared.DriverKind != DriverKindACP || prepared.NativeSessionID == "" || !prepared.SessionStarted {
				t.Fatalf("PrepareSession(%s) did not return a new ACP session", adapterID)
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
				t.Fatalf("Run(%s) failed", adapterID)
			}
			if result.DriverKind != DriverKindACP || result.SessionStarted || result.NativeSessionID != prepared.NativeSessionID {
				t.Fatalf("Run(%s) did not reuse the prepared ACP session", adapterID)
			}
			if strings.TrimSpace(result.Output) != testCase.token {
				t.Fatalf("Run(%s) returned unexpected output", adapterID)
			}

			fileRunCtx, cancelFileRun := context.WithTimeout(t.Context(), 4*time.Minute)
			fileResult, err := manager.Run(fileRunCtx, RunRequest{
				SessionID: sessionID,
				AdapterID: adapterID,
				Workspace: workspace,
				Prompt: PromptInput{
					Text: "Read the attached input.txt file. Reply with exactly the token it contains and nothing else.",
					Files: []PromptFile{
						promptTestFile("input.txt", "text/plain", []byte(testCase.fileToken+"\n")),
					},
				},
				Timeout:        3 * time.Minute,
				MaxOutputBytes: 64 * 1024,
			})
			cancelFileRun()
			if err != nil {
				t.Fatalf("Run(%s) with file failed", adapterID)
			}
			if fileResult.DriverKind != DriverKindACP || fileResult.SessionStarted || fileResult.NativeSessionID != prepared.NativeSessionID {
				t.Fatalf("Run(%s) with file did not reuse the prepared ACP session", adapterID)
			}
			if strings.TrimSpace(fileResult.Output) != testCase.fileToken {
				t.Fatalf("Run(%s) with file returned %q, want %q", adapterID, strings.TrimSpace(fileResult.Output), testCase.fileToken)
			}
		})
	}
}
