package agentadapters

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentcontrols"
)

// These tests validate Hecate's ACP process boundary against a repo-local fake.
// Adapter implementation parity belongs in the standalone adapter repositories,
// so this package should not import adapter source modules.
func TestBuiltInACPAdaptersProbeAuthenticateLogout(t *testing.T) {
	for _, tt := range builtInACPBoundaryTestCases() {
		t.Run(tt.adapterID, func(t *testing.T) {
			t.Setenv("HECATE_FAKE_ACP_AUTH_AGENT_LOGIN", "1")
			t.Setenv("HECATE_FAKE_ACP_SUPPORTS_LOGOUT", "1")
			installFakeACPExecutable(t, tt.adapterCommand)

			probe := Probe(context.Background(), tt.adapterID)
			if probe.Status != ProbeStatusReady || probe.Stage != ProbeStageReady {
				t.Fatalf("Probe(%s) = %#v, want ready", tt.adapterID, probe)
			}
			if !probe.CapabilitiesKnown || !probe.SupportsAuthenticate || !probe.SupportsLogout || !probe.SupportsLoadSession {
				t.Fatalf("Probe(%s) capabilities = %#v, want auth/logout/load-session known", tt.adapterID, probe)
			}
			if !hasProbeAuthMethod(probe.AuthMethods, ACPAuthMethodAgentLogin, "agent") {
				t.Fatalf("Probe(%s) auth methods = %#v, want agent-login agent method", tt.adapterID, probe.AuthMethods)
			}

			auth, err := Authenticate(context.Background(), tt.adapterID)
			if err != nil {
				t.Fatalf("Authenticate(%s): %v", tt.adapterID, err)
			}
			if auth.Status != AuthenticateStatusAuthenticated || auth.MethodID != ACPAuthMethodAgentLogin || auth.Path == "" {
				t.Fatalf("Authenticate(%s) = %#v, want authenticated agent-login", tt.adapterID, auth)
			}

			logout, err := Logout(context.Background(), tt.adapterID)
			if err != nil {
				t.Fatalf("Logout(%s): %v", tt.adapterID, err)
			}
			if logout.Status != LogoutStatusLoggedOut || logout.Path == "" {
				t.Fatalf("Logout(%s) = %#v, want logged_out", tt.adapterID, logout)
			}
		})
	}
}

func TestSessionManagerRunsThroughBuiltInACPAdapters(t *testing.T) {
	for _, tt := range builtInACPBoundaryTestCases() {
		t.Run(tt.adapterID, func(t *testing.T) {
			t.Setenv("HECATE_FAKE_ACP_MODELS", "1")
			t.Setenv("HECATE_FAKE_ACP_CONFIG_OPTIONS", "1")
			t.Setenv("HECATE_FAKE_ACP_COMMANDS_DELAY", "1ms")
			installFakeACPExecutable(t, tt.adapterCommand)

			workspace := t.TempDir()
			manager := NewSessionManager()
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := manager.Shutdown(ctx); err != nil {
					t.Errorf("Shutdown(%s): %v", tt.adapterID, err)
				}
			})
			prepared, err := manager.PrepareSession(context.Background(), PrepareSessionRequest{
				SessionID: "chat_" + tt.adapterID,
				AdapterID: tt.adapterID,
				Workspace: workspace,
			})
			if err != nil {
				t.Fatalf("PrepareSession(%s): %v", tt.adapterID, err)
			}
			if prepared.DriverKind != DriverKindACP || prepared.NativeSessionID == "" || !prepared.SessionStarted {
				t.Fatalf("PrepareSession(%s) = %#v, want started ACP native session", tt.adapterID, prepared)
			}
			assertConfigOption(t, prepared.ConfigOptions, "model", "model", agentcontrols.ConfigOptionTypeSelect)
			assertConfigOption(t, prepared.ConfigOptions, "mode", "", agentcontrols.ConfigOptionTypeSelect)
			for _, name := range []string{"web", "plan"} {
				if !hasCommand(prepared.AvailableCommands, name) {
					t.Fatalf("PrepareSession(%s) commands = %#v, want %q", tt.adapterID, prepared.AvailableCommands, name)
				}
			}

			result, err := manager.Run(context.Background(), RunRequest{
				SessionID:      "chat_" + tt.adapterID,
				AdapterID:      tt.adapterID,
				Workspace:      workspace,
				Prompt:         PromptInput{Text: "hello " + tt.adapterID},
				Timeout:        5 * time.Second,
				MaxOutputBytes: 64 * 1024,
			})
			if err != nil {
				t.Fatalf("Run(%s): %v", tt.adapterID, err)
			}
			if result.DriverKind != DriverKindACP || result.NativeSessionID != prepared.NativeSessionID || result.SessionStarted {
				t.Fatalf("Run(%s) = %#v, want reused ACP session %q", tt.adapterID, result, prepared.NativeSessionID)
			}
			wantOutput := "turn 1: hello " + tt.adapterID
			if !strings.Contains(result.Output, wantOutput) {
				t.Fatalf("Run(%s) output = %q, want %q", tt.adapterID, result.Output, wantOutput)
			}
			if result.Usage.ContextSize != 200_000 || result.Usage.ContextUsed != 10_000 {
				t.Fatalf("Run(%s) usage = %#v, want 10000/200000", tt.adapterID, result.Usage)
			}
			if !hasCommand(result.AvailableCommands, "web") {
				t.Fatalf("Run(%s) commands = %#v, want retained web command", tt.adapterID, result.AvailableCommands)
			}
		})
	}
}

type builtInACPBoundaryTestCase struct {
	adapterID      string
	adapterCommand string
}

func builtInACPBoundaryTestCases() []builtInACPBoundaryTestCase {
	return []builtInACPBoundaryTestCase{
		{adapterID: "codex", adapterCommand: "codex-acp-adapter"},
		{adapterID: "claude_code", adapterCommand: "claude-code-acp-adapter"},
	}
}

func hasProbeAuthMethod(methods []ProbeAuthMethod, id, kind string) bool {
	for _, method := range methods {
		if method.ID == id && method.Kind == kind {
			return true
		}
	}
	return false
}

func hasCommand(commands []agentcontrols.Command, name string) bool {
	for _, command := range commands {
		if command.Name == name {
			return true
		}
	}
	return false
}

func assertConfigOption(t *testing.T, options []agentcontrols.ConfigOption, id, category, optionType string) {
	t.Helper()
	option := findConfigOption(options, id)
	if option == nil {
		t.Fatalf("config options = %#v, want %q", options, id)
	}
	if category != "" && option.Category != category {
		t.Fatalf("config option %q category = %q, want %q", id, option.Category, category)
	}
	if option.Type != optionType {
		t.Fatalf("config option %q type = %q, want %q", id, option.Type, optionType)
	}
	if len(option.Options) == 0 {
		t.Fatalf("config option %q has no select values: %#v", id, *option)
	}
}
