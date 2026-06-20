package agentadapters

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/acp-adapter-kit/adaptercli"
	claudecodeadapter "github.com/hecatehq/claude-code-acp-adapter/claudecodeadapter"
	codexadapter "github.com/hecatehq/codex-acp-adapter/codexadapter"
	"github.com/hecatehq/hecate/internal/agentcontrols"
)

func TestGoACPAdapterHelperProcess(t *testing.T) {
	adapterID := os.Getenv("HECATE_TEST_GO_ACP_ADAPTER")
	if adapterID == "" {
		return
	}
	switch adapterID {
	case "codex":
		os.Exit(adaptercli.Run([]string{}, codexadapter.NewCLISpec("test", os.Stdin, os.Stdout, os.Stderr)))
	case "claude_code":
		os.Exit(adaptercli.Run([]string{}, claudecodeadapter.NewCLISpec("test", os.Stdin, os.Stdout, os.Stderr)))
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown HECATE_TEST_GO_ACP_ADAPTER %q\n", adapterID)
		os.Exit(2)
	}
}

func TestGoACPAdaptersProbeAuthenticateLogout(t *testing.T) {
	for _, tt := range goACPAdapterTestCases() {
		t.Run(tt.adapterID, func(t *testing.T) {
			installGoACPAdapterExecutable(t, tt.adapterID, tt.adapterCommand)
			installFakeVendorCLI(t, tt.vendorCommand, tt.vendorScript)

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

func TestSessionManagerRunsThroughGoACPAdapters(t *testing.T) {
	for _, tt := range goACPAdapterTestCases() {
		t.Run(tt.adapterID, func(t *testing.T) {
			installGoACPAdapterExecutable(t, tt.adapterID, tt.adapterCommand)
			installFakeVendorCLI(t, tt.vendorCommand, tt.vendorScript)

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
			for _, id := range tt.wantConfigOptionIDs {
				assertConfigOption(t, prepared.ConfigOptions, id, "", agentcontrols.ConfigOptionTypeSelect)
			}
			for _, name := range tt.wantCommands {
				if !hasCommand(prepared.AvailableCommands, name) {
					t.Fatalf("PrepareSession(%s) commands = %#v, want %q", tt.adapterID, prepared.AvailableCommands, name)
				}
			}

			result, err := manager.Run(context.Background(), RunRequest{
				SessionID:      "chat_" + tt.adapterID,
				AdapterID:      tt.adapterID,
				Workspace:      workspace,
				Prompt:         tt.prompt,
				Timeout:        5 * time.Second,
				MaxOutputBytes: 64 * 1024,
			})
			if err != nil {
				t.Fatalf("Run(%s): %v", tt.adapterID, err)
			}
			if result.DriverKind != DriverKindACP || result.NativeSessionID != prepared.NativeSessionID || result.SessionStarted {
				t.Fatalf("Run(%s) = %#v, want reused ACP session %q", tt.adapterID, result, prepared.NativeSessionID)
			}
			if !strings.Contains(result.Output, tt.wantOutput) {
				t.Fatalf("Run(%s) output = %q, want %q", tt.adapterID, result.Output, tt.wantOutput)
			}
			if result.Usage.ContextSize != 100 || result.Usage.ContextUsed != 15 {
				t.Fatalf("Run(%s) usage = %#v, want 15/100", tt.adapterID, result.Usage)
			}
			if !hasCommand(result.AvailableCommands, tt.wantCommands[0]) {
				t.Fatalf("Run(%s) commands = %#v, want retained command %q", tt.adapterID, result.AvailableCommands, tt.wantCommands[0])
			}
		})
	}
}

type goACPAdapterTestCase struct {
	adapterID           string
	adapterCommand      string
	vendorCommand       string
	vendorScript        string
	prompt              string
	wantOutput          string
	wantConfigOptionIDs []string
	wantCommands        []string
}

func goACPAdapterTestCases() []goACPAdapterTestCase {
	return []goACPAdapterTestCase{
		{
			adapterID:      "codex",
			adapterCommand: "codex-acp-adapter",
			vendorCommand:  "codex",
			vendorScript: `
case "$1" in
  --version)
    printf 'codex 9.8.7\n'
    exit 0
    ;;
  login)
    printf 'logged in\n'
    exit 0
    ;;
  logout)
    printf 'logged out\n'
    exit 0
    ;;
  exec)
    printf '{"method":"item/started","params":{"item":{"type":"local_shell_call","id":"tool-1","command":"go test ./..."}}}\n'
    printf '{"method":"item/reasoning/textDelta","params":{"item_id":"thought-1","delta":"checking"}}\n'
    printf '{"method":"item/completed","params":{"item":{"type":"agent_message","id":"msg-1","text":"go codex answer"}}}\n'
    printf '{"method":"turn/completed","params":{"usage":{"input_tokens":10,"output_tokens":5,"context_window":100}}}\n'
    printf '{"method":"item/completed","params":{"item":{"type":"local_shell_call","id":"tool-1","status":"completed","stdout":"ok"}}}\n'
    exit 0
    ;;
esac
echo "unexpected codex command: $*" >&2
exit 64
`,
			prompt:              "hello codex",
			wantOutput:          "go codex answer",
			wantConfigOptionIDs: []string{"reasoning_effort", "sandbox", "web_search"},
			wantCommands:        []string{"review", "init"},
		},
		{
			adapterID:      "claude_code",
			adapterCommand: "claude-code-acp-adapter",
			vendorCommand:  "claude",
			vendorScript: `
case "$1" in
  --version)
    printf 'claude 9.8.7\n'
    exit 0
    ;;
  /login)
    printf 'logged in\n'
    exit 0
    ;;
  auth)
    if [ "$2" = "logout" ]; then
      printf 'logged out\n'
      exit 0
    fi
    ;;
  --print)
    printf '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"go test ./..."}}]}}\n'
    printf '{"type":"assistant","message":{"content":[{"type":"thinking","id":"thought-1","thinking":"checking"},{"type":"text","text":"go claude answer"}]}}\n'
    printf '{"type":"result","usage":{"input_tokens":10,"output_tokens":5,"context_window":100}}\n'
    printf '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"ok"}]}}\n'
    exit 0
    ;;
esac
echo "unexpected claude command: $*" >&2
exit 64
`,
			prompt:              "hello claude",
			wantOutput:          "go claude answer",
			wantConfigOptionIDs: []string{"effort", "permission_mode"},
			wantCommands:        []string{"init", "review", "code-review", "security-review", "compact", "debug", "run", "verify"},
		},
	}
}

func installGoACPAdapterExecutable(t *testing.T, adapterID, name string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script adapter launcher is Unix-only")
	}
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatalf("mkdir adapter bin: %v", err)
	}
	exe := filepath.Join(bin, name)
	script := fmt.Sprintf("#!/bin/sh\nHECATE_TEST_GO_ACP_ADAPTER=%q exec %q -test.run '^TestGoACPAdapterHelperProcess$'\n", adapterID, os.Args[0])
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatalf("write adapter launcher: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installFakeVendorCLI(t *testing.T, name, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake vendor CLI is Unix-only")
	}
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatalf("mkdir vendor bin: %v", err)
	}
	exe := filepath.Join(bin, name)
	script := "#!/bin/sh\n" + strings.TrimSpace(body) + "\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s CLI: %v", name, err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
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
