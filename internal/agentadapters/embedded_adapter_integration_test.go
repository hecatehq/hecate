package agentadapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const fakeEmbeddedProviderConfigSuffix = ".hecate-provider.json"

type fakeEmbeddedProviderConfig struct {
	Command           string                          `json:"command"`
	Capture           string                          `json:"capture"`
	Output            string                          `json:"output"`
	ExpectedFiles     map[string]string               `json:"expectedFiles"`
	DiscoveryCatalogs [][]fakeEmbeddedProviderCommand `json:"discoveryCatalogs,omitempty"`
	DiscoveryCapture  string                          `json:"discoveryCapture,omitempty"`
	DiscoveryState    string                          `json:"discoveryState,omitempty"`
}

type fakeEmbeddedProviderCommand struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	ArgumentHint string   `json:"argumentHint,omitempty"`
	Aliases      []string `json:"aliases,omitempty"`
}

type fakeEmbeddedProviderDiscoveryCapture struct {
	Args         []string `json:"args"`
	Type         string   `json:"type"`
	RequestID    string   `json:"requestID"`
	Subtype      string   `json:"subtype"`
	SystemPrompt []string `json:"systemPrompt"`
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
			if tt.adapterID == "codex" {
				if commands := prepared.AvailableCommands; !prepared.AvailableCommandsKnown || len(commands) != 2 ||
					commands[0].Name != "review" || commands[0].InputHint != "optional review instructions" ||
					commands[1].Name != "init" || commands[1].InputHint != "optional instruction focus" {
					t.Fatalf("PrepareSession(codex) command catalog = %#v (known=%t), want review/init hints", commands, prepared.AvailableCommandsKnown)
				}
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

func TestEmbeddedClaudeCommandDiscoveryPublishesProviderReplacementSnapshots(t *testing.T) {
	t.Setenv(adapterTestProcessOverridesEnv, "")
	t.Setenv("HECATE_EMBEDDED_TEST_SECRET", "must-not-leak")
	discoveryCapture := filepath.Join(t.TempDir(), "discovery.json")
	discoveryState := filepath.Join(t.TempDir(), "discovery-state")
	executable := installFakeEmbeddedProviderCLIWithConfig(t, fakeEmbeddedProviderConfig{
		Command: "claude",
		DiscoveryCatalogs: [][]fakeEmbeddedProviderCommand{
			{
				{Name: "goal", Description: "Set a durable goal.", ArgumentHint: "outcome"},
				{Name: "loop", Description: "Run a loop until complete.", ArgumentHint: "[interval]", Aliases: []string{"proactive"}},
			},
			{{Name: "review", Description: "Review the current work."}},
		},
		DiscoveryCapture: discoveryCapture,
		DiscoveryState:   discoveryState,
	})

	probe := Probe(context.Background(), "claude_code")
	if probe.Status != ProbeStatusReady || probe.Path != executable {
		t.Fatalf("Probe(claude_code) = %#v, want ready embedded runtime at %q", probe, executable)
	}

	manager := NewSessionManager()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown(claude_code): %v", err)
		}
	})

	const sessionID = "chat_embedded_claude_command_discovery"
	updates := make(chan AvailableCommandsUpdate, 4)
	manager.SetAvailableCommandsUpdateHook(func(update AvailableCommandsUpdate) {
		if update.AdapterID != "claude_code" || update.SessionID != sessionID {
			return
		}
		select {
		case updates <- update:
		default:
		}
	})

	prepared, err := manager.PrepareSession(context.Background(), PrepareSessionRequest{
		SessionID: sessionID,
		AdapterID: "claude_code",
		Workspace: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("PrepareSession(claude_code): %v", err)
	}
	if prepared.NativeSessionID == "" || manager.sessions[sessionID].peer.Kind() != acpPeerKindEmbedded {
		t.Fatalf("PrepareSession(claude_code) = %#v, want embedded native session", prepared)
	}
	first := waitForEmbeddedProviderCommandCatalog(t, updates)
	if got := first.Commands; len(got) != 3 ||
		got[0].Name != "goal" || got[0].Description != "Set a durable goal." || got[0].InputHint != "outcome" ||
		got[1].Name != "loop" || got[1].Description != "Run a loop until complete." || got[1].InputHint != "[interval]" ||
		got[2].Name != "proactive" || got[2].Description != "Alias for /loop: Run a loop until complete." || got[2].InputHint != "[interval]" {
		t.Fatalf("first provider command catalog = %#v, want canonical commands and provider alias", got)
	}

	if _, err := manager.SetSessionConfigOption(context.Background(), SetSessionConfigOptionRequest{
		SessionID: sessionID,
		ConfigID:  "effort",
		Value:     "high",
	}); err != nil {
		t.Fatalf("SetSessionConfigOption(effort): %v", err)
	}
	second := waitForEmbeddedProviderCommandCatalog(t, updates)
	if got := second.Commands; len(got) != 1 || got[0].Name != "review" || got[0].Description != "Review the current work." {
		t.Fatalf("replacement provider command catalog = %#v, want only review", got)
	}
	commands, known := manager.sessions[sessionID].availableCommandsSnapshot()
	if !known || len(commands) != 1 || commands[0].Name != "review" {
		t.Fatalf("stored provider command catalog = %#v (known=%t), want replacement review catalog", commands, known)
	}

	for index := range 2 {
		capturePath := fakeEmbeddedProviderDiscoveryCapturePath(discoveryCapture, index)
		raw, err := os.ReadFile(capturePath)
		if err != nil {
			t.Fatalf("read discovery capture %d: %v", index, err)
		}
		var capture fakeEmbeddedProviderDiscoveryCapture
		if err := json.Unmarshal(raw, &capture); err != nil {
			t.Fatalf("decode discovery capture %d: %v", index, err)
		}
		if !sameFakeEmbeddedProviderStrings(capture.Args, fakeClaudeCommandDiscoveryArgs) ||
			capture.Type != "control_request" ||
			capture.RequestID != "hecate-command-discovery" ||
			capture.Subtype != "initialize" ||
			!sameFakeEmbeddedProviderStrings(capture.SystemPrompt, []string{""}) {
			t.Fatalf("discovery capture %d = %#v, want bounded bare control request", index, capture)
		}
	}
	if raw, err := os.ReadFile(discoveryState); err != nil {
		t.Fatalf("read discovery state: %v", err)
	} else if strings.TrimSpace(string(raw)) != "2" {
		t.Fatalf("provider discovery launches = %q, want two replacement snapshots", raw)
	}
}

func waitForEmbeddedProviderCommandCatalog(t testing.TB, updates <-chan AvailableCommandsUpdate) AvailableCommandsUpdate {
	t.Helper()
	select {
	case update := <-updates:
		if update.Commands == nil {
			t.Fatal("provider command catalog omitted its explicit replacement snapshot")
		}
		return update
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for provider command catalog")
		return AvailableCommandsUpdate{}
	}
}

func installFakeEmbeddedProviderCLI(t *testing.T, command, capture, output string) string {
	t.Helper()
	return installFakeEmbeddedProviderCLIWithConfig(t, fakeEmbeddedProviderConfig{
		Command: command,
		Capture: capture,
		Output:  output,
		ExpectedFiles: map[string]string{
			"screen.png": "private-image-bytes",
			"notes.txt":  "private notes",
		},
	})
}

func installFakeEmbeddedProviderCLIWithConfig(t *testing.T, config fakeEmbeddedProviderConfig) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatalf("mkdir provider bin: %v", err)
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	executable := filepath.Join(bin, config.Command+suffix)
	copyExecutable(t, executable)
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
	if config.Command == "claude" && containsFakeEmbeddedProviderString(args, "--bare") {
		return runFakeClaudeCommandDiscovery(config, args)
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

var fakeClaudeCommandDiscoveryArgs = []string{
	"--print",
	"--bare",
	"--input-format", "stream-json",
	"--output-format", "stream-json",
	"--verbose",
	"--no-session-persistence",
	"--permission-mode", "dontAsk",
	"--strict-mcp-config",
}

func runFakeClaudeCommandDiscovery(config fakeEmbeddedProviderConfig, args []string) int {
	if !sameFakeEmbeddedProviderStrings(args, fakeClaudeCommandDiscoveryArgs) {
		return 93
	}
	requestBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 93
	}
	var rawRequest map[string]json.RawMessage
	if err := json.Unmarshal(requestBytes, &rawRequest); err != nil ||
		!hasExactFakeEmbeddedProviderJSONFields(rawRequest, "type", "request_id", "request") {
		return 93
	}
	var rawRequestBody map[string]json.RawMessage
	if err := json.Unmarshal(rawRequest["request"], &rawRequestBody); err != nil ||
		!hasExactFakeEmbeddedProviderJSONFields(rawRequestBody, "subtype", "systemPrompt") {
		return 93
	}
	var request struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype      string   `json:"subtype"`
			SystemPrompt []string `json:"systemPrompt"`
		} `json:"request"`
	}
	if err := json.Unmarshal(requestBytes, &request); err != nil ||
		request.Type != "control_request" ||
		request.RequestID != "hecate-command-discovery" ||
		request.Request.Subtype != "initialize" ||
		!sameFakeEmbeddedProviderStrings(request.Request.SystemPrompt, []string{""}) {
		return 93
	}
	index, ok := nextFakeClaudeCommandDiscoveryIndex(config.DiscoveryState)
	if !ok {
		return 93
	}
	if config.DiscoveryCapture != "" {
		capture := fakeEmbeddedProviderDiscoveryCapture{
			Args:         append([]string(nil), args...),
			Type:         request.Type,
			RequestID:    request.RequestID,
			Subtype:      request.Request.Subtype,
			SystemPrompt: append([]string(nil), request.Request.SystemPrompt...),
		}
		raw, err := json.Marshal(capture)
		if err != nil || os.WriteFile(fakeEmbeddedProviderDiscoveryCapturePath(config.DiscoveryCapture, index), raw, 0o600) != nil {
			return 93
		}
	}
	catalogs := config.DiscoveryCatalogs
	if len(catalogs) == 0 {
		catalogs = [][]fakeEmbeddedProviderCommand{{}}
	}
	if index >= len(catalogs) {
		return 93
	}
	writeFakeProviderJSON(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"request_id": "hecate-command-discovery",
			"subtype":    "success",
			"response": map[string]any{
				"commands": catalogs[index],
			},
		},
	})
	return 0
}

func hasExactFakeEmbeddedProviderJSONFields(fields map[string]json.RawMessage, want ...string) bool {
	if len(fields) != len(want) {
		return false
	}
	for _, field := range want {
		if _, ok := fields[field]; !ok {
			return false
		}
	}
	return true
}

func fakeEmbeddedProviderDiscoveryCapturePath(base string, index int) string {
	return base + "." + strconv.Itoa(index)
}

func nextFakeClaudeCommandDiscoveryIndex(statePath string) (int, bool) {
	if statePath == "" {
		return 0, true
	}
	index := 0
	if raw, err := os.ReadFile(statePath); err == nil {
		parsed, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if parseErr != nil || parsed < 0 {
			return 0, false
		}
		index = parsed
	} else if !os.IsNotExist(err) {
		return 0, false
	}
	if err := os.WriteFile(statePath, []byte(strconv.Itoa(index+1)), 0o600); err != nil {
		return 0, false
	}
	return index, true
}

func containsFakeEmbeddedProviderString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sameFakeEmbeddedProviderStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func writeFakeProviderJSON(value any) {
	raw, err := json.Marshal(value)
	if err == nil {
		_, _ = fmt.Fprintln(os.Stdout, string(raw))
	}
}
