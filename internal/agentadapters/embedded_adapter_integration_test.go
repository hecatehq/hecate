package agentadapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fakeEmbeddedProviderConfigSuffix = ".hecate-provider.json"

type fakeEmbeddedProviderConfig struct {
	Command       string            `json:"command"`
	Capture       string            `json:"capture"`
	Output        string            `json:"output"`
	ExpectedFiles map[string]string `json:"expectedFiles"`
}

func TestMain(m *testing.M) {
	configPath := os.Args[0] + fakeEmbeddedProviderConfigSuffix
	if _, err := os.Stat(configPath); err == nil {
		os.Exit(runFakeEmbeddedProviderCLI(configPath, os.Args[1:]))
	}
	os.Exit(m.Run())
}

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
			for _, want := range []string{"screen.png", "image/png", "notes.txt", "text/plain"} {
				if !strings.Contains(promptText, want) {
					t.Fatalf("provider prompt = %q, want %q", promptText, want)
				}
			}
			if strings.Contains(promptText, "file://") {
				t.Fatalf("provider prompt leaked source resource URI: %q", promptText)
			}
			var stagedPaths []string
			for _, line := range strings.Split(promptText, "\n") {
				var manifest struct {
					Kind string `json:"kind"`
					Path string `json:"path"`
				}
				if err := json.Unmarshal([]byte(line), &manifest); err != nil || manifest.Kind != "resource_link" || manifest.Path == "" {
					continue
				}
				if !filepath.IsAbs(manifest.Path) {
					t.Fatalf("provider prompt private path = %q, want absolute path", manifest.Path)
				}
				stagedPaths = append(stagedPaths, manifest.Path)
				if _, err := os.Stat(manifest.Path); !os.IsNotExist(err) {
					t.Fatalf("private prompt stage input %q still exists after provider command: %v", manifest.Path, err)
				}
			}
			if len(stagedPaths) != 2 {
				t.Fatalf("provider prompt staged paths = %#v, want two private inputs", stagedPaths)
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
			for _, stagedPath := range stagedPaths {
				if strings.Contains(followUpText, stagedPath) {
					t.Fatalf("follow-up provider prompt replayed private stage path %q", stagedPath)
				}
			}
			for _, want := range []string{"Inspect both attached inputs.", tt.output, "Summarize what you found."} {
				if !strings.Contains(followUpText, want) {
					t.Fatalf("follow-up provider prompt = %q, want transcript text %q", followUpText, want)
				}
			}
			for _, privateMetadata := range []string{"screen.png", "image/png", "notes.txt", "text/plain", "acp-commandbridge-private-"} {
				if strings.Contains(followUpText, privateMetadata) {
					t.Fatalf("follow-up provider prompt retained private resource metadata %q", privateMetadata)
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
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	executable := filepath.Join(bin, command+suffix)
	copyExecutable(t, executable)
	config := fakeEmbeddedProviderConfig{
		Command: command,
		Capture: capture,
		Output:  output,
		ExpectedFiles: map[string]string{
			"screen.png": "private-image-bytes",
			"notes.txt":  "private notes",
		},
	}
	rawConfig, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal provider config: %v", err)
	}
	if err := os.WriteFile(executable+fakeEmbeddedProviderConfigSuffix, rawConfig, 0o600); err != nil {
		t.Fatalf("write provider config: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return executable
}

func copyExecutable(t *testing.T, destination string) {
	t.Helper()
	sourcePath, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatalf("open test executable: %v", err)
	}
	defer source.Close()
	destinationFile, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatalf("create provider executable: %v", err)
	}
	if _, err := io.Copy(destinationFile, source); err != nil {
		_ = destinationFile.Close()
		t.Fatalf("copy provider executable: %v", err)
	}
	if err := destinationFile.Close(); err != nil {
		t.Fatalf("close provider executable: %v", err)
	}
	if err := os.Chmod(destination, 0o700); err != nil {
		t.Fatalf("make provider executable: %v", err)
	}
}

func runFakeEmbeddedProviderCLI(configPath string, args []string) int {
	rawConfig, err := os.ReadFile(configPath)
	if err != nil {
		return 92
	}
	var config fakeEmbeddedProviderConfig
	if err := json.Unmarshal(rawConfig, &config); err != nil {
		return 92
	}
	if os.Getenv("HECATE_EMBEDDED_TEST_SECRET") != "" {
		_, _ = fmt.Fprintln(os.Stderr, "unexpected inherited environment")
		return 90
	}
	if len(args) == 1 && args[0] == "--version" {
		_, _ = fmt.Fprintf(os.Stdout, "%s 1.2.3\n", config.Command)
		return 0
	}
	switch strings.Join(args, " ") {
	case "login", "logout", "/login", "auth logout":
		return 0
	}
	prompt := ""
	if len(args) > 0 {
		prompt = args[len(args)-1]
	}
	if err := os.WriteFile(config.Capture, []byte(prompt), 0o600); err != nil {
		return 92
	}
	for _, line := range strings.Split(prompt, "\n") {
		var manifest struct {
			Kind string `json:"kind"`
			Path string `json:"path"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(line), &manifest); err != nil || manifest.Kind != "resource_link" || manifest.Path == "" {
			continue
		}
		data, err := os.ReadFile(manifest.Path)
		expected, known := config.ExpectedFiles[manifest.Name]
		if err != nil || !known || string(data) != expected {
			return 91
		}
	}
	if config.Command == "codex" {
		writeFakeProviderJSON(map[string]any{
			"method": "item/completed",
			"params": map[string]any{"item": map[string]any{
				"type": "agent_message", "id": "msg-1", "text": config.Output,
			}},
		})
		writeFakeProviderJSON(map[string]any{
			"method": "turn/completed",
			"params": map[string]any{"usage": map[string]any{
				"input_tokens": 2, "output_tokens": 3, "context_window": 128,
			}},
		})
		return 0
	}
	writeFakeProviderJSON(map[string]any{
		"type": "assistant",
		"message": map[string]any{"content": []map[string]any{{
			"type": "text", "text": config.Output,
		}}},
	})
	writeFakeProviderJSON(map[string]any{
		"type": "result", "subtype": "success",
		"usage": map[string]any{"input_tokens": 2, "output_tokens": 3, "context_window": 128},
	})
	return 0
}

func writeFakeProviderJSON(value any) {
	raw, err := json.Marshal(value)
	if err == nil {
		_, _ = fmt.Fprintln(os.Stdout, string(raw))
	}
}
