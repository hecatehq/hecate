//go:build !windows

package agentadapters

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedAdaptersRunProviderCLIsWithPrivateFileLinks(t *testing.T) {
	t.Setenv(adapterTestProcessOverridesEnv, "")
	t.Setenv("HECATE_EMBEDDED_TEST_SECRET", "must-not-leak")

	tests := []struct {
		adapterID string
		command   string
		output    string
	}{
		{adapterID: "codex", command: "codex", output: "embedded codex ok"},
		{adapterID: "claude_code", command: "claude", output: "embedded claude ok"},
	}
	for _, tt := range tests {
		t.Run(tt.adapterID, func(t *testing.T) {
			capture := filepath.Join(t.TempDir(), "prompt.txt")
			executable := installFakeEmbeddedProviderCLI(t, tt.command, capture, tt.output)

			probe := Probe(context.Background(), tt.adapterID)
			if probe.Status != ProbeStatusReady || probe.Path != executable {
				t.Fatalf("Probe(%s) = %#v, want ready embedded runtime at %q", tt.adapterID, probe, executable)
			}
			auth, err := Authenticate(context.Background(), tt.adapterID)
			if err != nil || auth.Status != AuthenticateStatusAuthenticated || auth.Path != executable {
				t.Fatalf("Authenticate(%s) = %#v, %v", tt.adapterID, auth, err)
			}
			logout, err := Logout(context.Background(), tt.adapterID)
			if err != nil || logout.Status != LogoutStatusLoggedOut || logout.Path != executable {
				t.Fatalf("Logout(%s) = %#v, %v", tt.adapterID, logout, err)
			}

			manager := NewSessionManager()
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := manager.Shutdown(ctx); err != nil {
					t.Errorf("Shutdown(%s): %v", tt.adapterID, err)
				}
			})
			workspace := t.TempDir()
			sessionID := "chat_embedded_" + tt.adapterID
			prepared, err := manager.PrepareSession(context.Background(), PrepareSessionRequest{
				SessionID: sessionID,
				AdapterID: tt.adapterID,
				Workspace: workspace,
			})
			if err != nil {
				t.Fatalf("PrepareSession(%s): %v", tt.adapterID, err)
			}
			if prepared.NativeSessionID == "" || manager.sessions[sessionID].peer.Kind() != acpPeerKindEmbedded {
				t.Fatalf("PrepareSession(%s) = %#v, want embedded native session", tt.adapterID, prepared)
			}

			result, err := manager.Run(context.Background(), RunRequest{
				SessionID: sessionID,
				AdapterID: tt.adapterID,
				Workspace: workspace,
				Prompt: PromptInput{
					Text: "Inspect both attached inputs.",
					Files: []PromptFile{
						promptTestFile("screen.png", "image/png", []byte("private-image-bytes")),
						promptTestFile("notes.txt", "text/plain", []byte("private notes")),
					},
				},
				Timeout:        10 * time.Second,
				MaxOutputBytes: 64 * 1024,
			})
			if err != nil {
				t.Fatalf("Run(%s): %v", tt.adapterID, err)
			}
			if !strings.Contains(result.Output, tt.output) {
				t.Fatalf("Run(%s) output = %q, want %q", tt.adapterID, result.Output, tt.output)
			}

			prompt, err := os.ReadFile(capture)
			if err != nil {
				t.Fatalf("read captured provider prompt: %v", err)
			}
			promptText := string(prompt)
			for _, want := range []string{"screen.png", "image/png", "notes.txt", "text/plain", "file://"} {
				if !strings.Contains(promptText, want) {
					t.Fatalf("provider prompt = %q, want %q", promptText, want)
				}
			}
			for _, line := range strings.Split(promptText, "\n") {
				if !strings.HasPrefix(line, "file://") {
					continue
				}
				path := strings.TrimPrefix(line, "file://")
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("private prompt stage %q still exists after provider command: %v", path, err)
				}
			}

			followUp, err := manager.Run(context.Background(), RunRequest{
				SessionID:      sessionID,
				AdapterID:      tt.adapterID,
				Workspace:      workspace,
				Prompt:         PromptInput{Text: "Summarize what you found."},
				Timeout:        10 * time.Second,
				MaxOutputBytes: 64 * 1024,
			})
			if err != nil {
				t.Fatalf("follow-up Run(%s): %v", tt.adapterID, err)
			}
			if !strings.Contains(followUp.Output, tt.output) {
				t.Fatalf("follow-up Run(%s) output = %q, want %q", tt.adapterID, followUp.Output, tt.output)
			}
			followUpPrompt, err := os.ReadFile(capture)
			if err != nil {
				t.Fatalf("read captured follow-up provider prompt: %v", err)
			}
			followUpText := string(followUpPrompt)
			if strings.Contains(followUpText, "file://") {
				t.Fatalf("follow-up provider prompt replayed ephemeral resource link: %q", followUpText)
			}
			for _, want := range []string{"screen.png", "image/png", "notes.txt", "text/plain", "Summarize what you found."} {
				if !strings.Contains(followUpText, want) {
					t.Fatalf("follow-up provider prompt = %q, want retained metadata %q", followUpText, want)
				}
			}
		})
	}
}

func installFakeEmbeddedProviderCLI(t *testing.T, command, capture, output string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatalf("mkdir provider bin: %v", err)
	}
	executable := filepath.Join(bin, command)
	providerOutput := ""
	if command == "codex" {
		providerOutput = fmt.Sprintf("printf '%%s\\n' %q\nprintf '%%s\\n' %q\n", `{"method":"item/completed","params":{"item":{"type":"agent_message","id":"msg-1","text":"`+output+`"}}}`, `{"method":"turn/completed","params":{"usage":{"input_tokens":2,"output_tokens":3,"context_window":128}}}`)
	} else {
		providerOutput = fmt.Sprintf("printf '%%s\\n' %q\nprintf '%%s\\n' %q\n", `{"type":"assistant","message":{"content":[{"type":"text","text":"`+output+`"}]}}`, `{"type":"result","subtype":"success","usage":{"input_tokens":2,"output_tokens":3,"context_window":128}}`)
	}
	script := fmt.Sprintf(`#!/bin/sh
if [ -n "$HECATE_EMBEDDED_TEST_SECRET" ]; then
  echo "unexpected inherited environment" >&2
  exit 90
fi
if [ "$1" = "--version" ]; then
  echo "%s 1.2.3"
  exit 0
fi
case "$*" in
  "login"|"logout"|"/login"|"auth logout") exit 0 ;;
esac
prompt=""
for arg in "$@"; do prompt="$arg"; done
printf '%%s' "$prompt" > %q
while IFS= read -r line; do
  case "$line" in
    file://*)
      path=${line#file://}
      [ -r "$path" ] || exit 91
      ;;
  esac
done <<HECATE_PROMPT
$prompt
HECATE_PROMPT
%s`, command, capture, providerOutput)
	if err := os.WriteFile(executable, []byte(script), 0o755); err != nil {
		t.Fatalf("write provider CLI: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return executable
}
