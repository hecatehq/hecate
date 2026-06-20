//go:build acp_release

package agentadapters

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestACPAdapterReleaseBinariesSmoke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release smoke uses Unix shell fake vendor CLIs")
	}
	for _, tt := range acpReleaseSmokeTestCases(t) {
		t.Run(tt.adapterID, func(t *testing.T) {
			binDir := t.TempDir()
			downloadACPAdapterReleaseBinary(t, tt.repo, tt.binary, tt.version, binDir)
			installFakeVendorCLI(t, tt.vendorCommand, tt.vendorScript)
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			probe := Probe(context.Background(), tt.adapterID)
			if probe.Status != ProbeStatusReady || probe.Stage != ProbeStageReady {
				t.Fatalf("Probe(%s) = %#v, want ready", tt.adapterID, probe)
			}
			if !probe.CapabilitiesKnown || !probe.SupportsAuthenticate || !probe.SupportsLogout || !probe.SupportsLoadSession {
				t.Fatalf("Probe(%s) capabilities = %#v, want auth/logout/load-session known", tt.adapterID, probe)
			}
			if !hasProbeAuthMethod(probe.AuthMethods, ACPAuthMethodAgentLogin, "agent") {
				t.Fatalf("Probe(%s) auth methods = %#v, want agent-login method", tt.adapterID, probe.AuthMethods)
			}

			auth, err := Authenticate(context.Background(), tt.adapterID)
			if err != nil {
				t.Fatalf("Authenticate(%s): %v", tt.adapterID, err)
			}
			if auth.Status != AuthenticateStatusAuthenticated || auth.MethodID != ACPAuthMethodAgentLogin {
				t.Fatalf("Authenticate(%s) = %#v, want authenticated agent-login", tt.adapterID, auth)
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
			prepared, err := manager.PrepareSession(context.Background(), PrepareSessionRequest{
				SessionID:  "release_" + tt.adapterID,
				AdapterID:  tt.adapterID,
				Workspace:  workspace,
				MCPServers: tt.mcpServers,
			})
			if err != nil {
				t.Fatalf("PrepareSession(%s): %v", tt.adapterID, err)
			}
			if prepared.DriverKind != DriverKindACP || prepared.NativeSessionID == "" || !prepared.SessionStarted {
				t.Fatalf("PrepareSession(%s) = %#v, want started ACP session", tt.adapterID, prepared)
			}
			currentConfigOptions := prepared.ConfigOptions
			for _, id := range tt.wantConfigOptionIDs {
				if findConfigOption(prepared.ConfigOptions, id) == nil {
					t.Fatalf("PrepareSession(%s) options = %#v, want %q", tt.adapterID, prepared.ConfigOptions, id)
				}
			}
			for _, name := range tt.wantCommands {
				if !hasCommand(prepared.AvailableCommands, name) {
					t.Fatalf("PrepareSession(%s) commands = %#v, want %q", tt.adapterID, prepared.AvailableCommands, name)
				}
			}
			for _, option := range tt.setConfigOptions {
				updated, err := manager.SetSessionConfigOption(context.Background(), SetSessionConfigOptionRequest{
					SessionID: "release_" + tt.adapterID,
					ConfigID:  option.id,
					Value:     option.value,
				})
				if err != nil {
					t.Fatalf("SetSessionConfigOption(%s, %s=%s): %v", tt.adapterID, option.id, option.value, err)
				}
				got := findConfigOption(updated.ConfigOptions, option.id)
				if got == nil || got.CurrentValue != option.value {
					t.Fatalf("SetSessionConfigOption(%s, %s) options = %#v, want current value %q", tt.adapterID, option.id, updated.ConfigOptions, option.value)
				}
				currentConfigOptions = updated.ConfigOptions
			}

			var activityCapture acpReleaseActivityCapture
			run, err := manager.Run(context.Background(), RunRequest{
				SessionID:      "release_" + tt.adapterID,
				AdapterID:      tt.adapterID,
				Workspace:      workspace,
				Prompt:         "hello " + tt.adapterID,
				MCPServers:     tt.mcpServers,
				Timeout:        5 * time.Second,
				MaxOutputBytes: 64 * 1024,
				OnActivity:     activityCapture.record,
			})
			if err != nil {
				t.Fatalf("Run(%s): %v", tt.adapterID, err)
			}
			if run.DriverKind != DriverKindACP || run.NativeSessionID != prepared.NativeSessionID || run.SessionStarted {
				t.Fatalf("Run(%s) = %#v, want reused ACP session %q", tt.adapterID, run, prepared.NativeSessionID)
			}
			if !strings.Contains(run.Output, tt.wantOutput) {
				t.Fatalf("Run(%s) output = %q, want %q", tt.adapterID, run.Output, tt.wantOutput)
			}
			if run.Usage.ContextSize == 0 || run.Usage.ContextUsed == 0 {
				t.Fatalf("Run(%s) usage = %#v, want adapter-reported usage", tt.adapterID, run.Usage)
			}
			if run.StopReason != tt.wantStopReason {
				t.Fatalf("Run(%s) stop reason = %q, want %q", tt.adapterID, run.StopReason, tt.wantStopReason)
			}
			assertReleaseToolActivities(t, tt.adapterID, activityCapture.snapshot(), tt.wantToolActivityTitle)

			for _, commandRun := range tt.commandRuns {
				run, err := manager.Run(context.Background(), RunRequest{
					SessionID:      "release_" + tt.adapterID,
					AdapterID:      tt.adapterID,
					Workspace:      workspace,
					Prompt:         commandRun.prompt,
					MCPServers:     tt.mcpServers,
					Timeout:        5 * time.Second,
					MaxOutputBytes: 64 * 1024,
				})
				if err != nil {
					t.Fatalf("Run(%s, %q): %v", tt.adapterID, commandRun.prompt, err)
				}
				if run.DriverKind != DriverKindACP || run.NativeSessionID != prepared.NativeSessionID || run.SessionStarted {
					t.Fatalf("Run(%s, %q) = %#v, want reused ACP session %q", tt.adapterID, commandRun.prompt, run, prepared.NativeSessionID)
				}
				if !strings.Contains(run.Output, commandRun.wantOutput) {
					t.Fatalf("Run(%s, %q) output = %q, want %q", tt.adapterID, commandRun.prompt, run.Output, commandRun.wantOutput)
				}
				if run.StopReason != commandRun.wantStopReason {
					t.Fatalf("Run(%s, %q) stop reason = %q, want %q", tt.adapterID, commandRun.prompt, run.StopReason, commandRun.wantStopReason)
				}
			}

			if tt.authFailurePrompt != "" {
				_, err := manager.Run(context.Background(), RunRequest{
					SessionID:      "release_" + tt.adapterID,
					AdapterID:      tt.adapterID,
					Workspace:      workspace,
					Prompt:         tt.authFailurePrompt,
					MCPServers:     tt.mcpServers,
					Timeout:        5 * time.Second,
					MaxOutputBytes: 64 * 1024,
				})
				if err == nil {
					t.Fatalf("Run(%s, %q) succeeded, want auth-required error", tt.adapterID, tt.authFailurePrompt)
				}
				normalized := NormalizeError(prepared.Adapter.Name, err)
				for _, marker := range tt.wantAuthFailureMarkers {
					if !strings.Contains(normalized, marker) {
						t.Fatalf("Run(%s, %q) normalized error = %q, want marker %q", tt.adapterID, tt.authFailurePrompt, normalized, marker)
					}
				}
			}

			if tt.reloadPrompt != "" {
				reloadManager := NewSessionManager()
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := reloadManager.Shutdown(ctx); err != nil {
						t.Errorf("Shutdown reload manager (%s): %v", tt.adapterID, err)
					}
				})
				reloaded, err := reloadManager.Run(context.Background(), RunRequest{
					SessionID:               "release_reload_" + tt.adapterID,
					AdapterID:               tt.adapterID,
					Workspace:               workspace,
					PreviousNativeSessionID: prepared.NativeSessionID,
					Prompt:                  tt.reloadPrompt,
					ConfigOptions:           currentConfigOptions,
					MCPServers:              tt.mcpServers,
					Timeout:                 5 * time.Second,
					MaxOutputBytes:          64 * 1024,
				})
				if err != nil {
					t.Fatalf("Run(%s reload): %v", tt.adapterID, err)
				}
				if !strings.Contains(reloaded.Output, tt.wantReloadOutput) {
					t.Fatalf("Run(%s reload) output = %q, want %q", tt.adapterID, reloaded.Output, tt.wantReloadOutput)
				}
				if reloaded.SessionStarted != true || reloaded.SessionResumed != tt.wantReloadResumed {
					t.Fatalf("Run(%s reload) session flags = started:%v resumed:%v, want started:true resumed:%v", tt.adapterID, reloaded.SessionStarted, reloaded.SessionResumed, tt.wantReloadResumed)
				}
				if tt.wantReloadSameNative && reloaded.NativeSessionID != prepared.NativeSessionID {
					t.Fatalf("Run(%s reload) native session = %q, want previous %q", tt.adapterID, reloaded.NativeSessionID, prepared.NativeSessionID)
				}
				if tt.wantReloadRecovery && !strings.Contains(reloaded.SessionRecovery, prepared.NativeSessionID) {
					t.Fatalf("Run(%s reload) recovery = %q, want previous native session id %q", tt.adapterID, reloaded.SessionRecovery, prepared.NativeSessionID)
				}
				if !tt.wantReloadRecovery && reloaded.SessionRecovery != "" {
					t.Fatalf("Run(%s reload) recovery = %q, want empty recovery", tt.adapterID, reloaded.SessionRecovery)
				}
			}

			logout, err := Logout(context.Background(), tt.adapterID)
			if err != nil {
				t.Fatalf("Logout(%s): %v", tt.adapterID, err)
			}
			if logout.Status != LogoutStatusLoggedOut {
				t.Fatalf("Logout(%s) = %#v, want logged_out", tt.adapterID, logout)
			}
		})
	}
}

type acpReleaseSmokeTestCase struct {
	adapterID              string
	repo                   string
	binary                 string
	version                string
	vendorCommand          string
	vendorScript           string
	wantOutput             string
	wantStopReason         string
	wantConfigOptionIDs    []string
	setConfigOptions       []acpReleaseConfigOption
	wantCommands           []string
	commandRuns            []acpReleaseCommandRun
	mcpServers             []types.MCPServerConfig
	authFailurePrompt      string
	wantAuthFailureMarkers []string
	reloadPrompt           string
	wantReloadOutput       string
	wantReloadResumed      bool
	wantReloadSameNative   bool
	wantReloadRecovery     bool
	wantToolActivityTitle  string
}

type acpReleaseConfigOption struct {
	id    string
	value string
}

type acpReleaseCommandRun struct {
	prompt         string
	wantOutput     string
	wantStopReason string
}

func acpReleaseSmokeTestCases(t *testing.T) []acpReleaseSmokeTestCase {
	t.Helper()
	dev := readDockerfile(t, "Dockerfile")
	mcpServers := []types.MCPServerConfig{{
		Name: "docs",
		URL:  "https://docs.example.com/mcp",
		Headers: map[string]string{
			"Authorization": "Bearer token",
		},
	}}
	return []acpReleaseSmokeTestCase{
		{
			adapterID:      "codex",
			repo:           "codex-acp-adapter",
			binary:         "codex-acp-adapter",
			version:        dockerfileArgValue(dev, "CODEX_ACP_ADAPTER_VERSION"),
			vendorCommand:  "codex",
			vendorScript:   fakeCodexCLIScript,
			wantOutput:     "go codex answer",
			wantStopReason: "max_tokens",
			wantConfigOptionIDs: []string{
				"model",
				"reasoning_effort",
				"sandbox",
				"web_search",
			},
			setConfigOptions: []acpReleaseConfigOption{
				{id: "model", value: "gpt-5-codex"},
				{id: "reasoning_effort", value: "high"},
				{id: "sandbox", value: "read-only"},
				{id: "web_search", value: "enabled"},
			},
			wantCommands:           []string{"review", "init"},
			mcpServers:             mcpServers,
			authFailurePrompt:      "/auth-fail",
			wantAuthFailureMarkers: []string{"Codex error: Authentication required"},
			reloadPrompt:           "reload codex",
			wantReloadOutput:       "go codex reload",
			wantReloadResumed:      false,
			wantReloadSameNative:   false,
			wantReloadRecovery:     true,
			wantToolActivityTitle:  "go test ./...",
			commandRuns: []acpReleaseCommandRun{
				{
					prompt:         "/review focus on tests",
					wantOutput:     "go codex review",
					wantStopReason: "end_turn",
				},
				{
					prompt:         "/init focus on repo guidance",
					wantOutput:     "go codex init",
					wantStopReason: "end_turn",
				},
			},
		},
		{
			adapterID:      "claude_code",
			repo:           "claude-code-acp-adapter",
			binary:         "claude-code-acp-adapter",
			version:        dockerfileArgValue(dev, "CLAUDE_CODE_ACP_ADAPTER_VERSION"),
			vendorCommand:  "claude",
			vendorScript:   fakeClaudeCodeCLIScript,
			wantOutput:     "go claude answer",
			wantStopReason: "max_turn_requests",
			wantConfigOptionIDs: []string{
				"model",
				"effort",
				"permission_mode",
			},
			setConfigOptions: []acpReleaseConfigOption{
				{id: "model", value: "sonnet"},
				{id: "effort", value: "high"},
				{id: "permission_mode", value: "plan"},
			},
			wantCommands:           []string{"init", "review", "code-review", "security-review", "compact", "debug", "run", "verify"},
			mcpServers:             mcpServers,
			authFailurePrompt:      "/auth-fail",
			wantAuthFailureMarkers: []string{"isn't signed in", "claude_code_auth_required"},
			reloadPrompt:           "reload claude_code",
			wantReloadOutput:       "go claude reload",
			wantReloadResumed:      true,
			wantReloadSameNative:   true,
			wantReloadRecovery:     false,
			wantToolActivityTitle:  "Bash",
			commandRuns: []acpReleaseCommandRun{
				{
					prompt:         "/verify release smoke",
					wantOutput:     "go claude verify",
					wantStopReason: "end_turn",
				},
			},
		},
	}
}

type acpReleaseActivityCapture struct {
	mu         sync.Mutex
	activities []Activity
}

func (c *acpReleaseActivityCapture) record(activity Activity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.activities = append(c.activities, activity)
}

func (c *acpReleaseActivityCapture) snapshot() []Activity {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Activity, len(c.activities))
	copy(out, c.activities)
	return out
}

func assertReleaseToolActivities(t *testing.T, adapterID string, activities []Activity, wantTitle string) {
	t.Helper()
	var sawStart, sawFinish bool
	for _, activity := range activities {
		if activity.Type != "tool_call" || activity.Kind != "execute" {
			continue
		}
		if activity.Status == "running" && strings.Contains(activity.Title, wantTitle) {
			sawStart = true
		}
		if activity.Status == "completed" && (strings.Contains(activity.ArtifactPreview, "ok") || strings.Contains(activity.Detail, "ok")) {
			sawFinish = true
		}
	}
	if !sawStart || !sawFinish {
		t.Fatalf("Run(%s) activities = %#v, want running execute %q and completed output", adapterID, activities, wantTitle)
	}
}

func downloadACPAdapterReleaseBinary(t *testing.T, repo, binary, version, binDir string) {
	t.Helper()
	if version == "" {
		t.Fatalf("%s Dockerfile version is empty", binary)
	}
	releaseVersion := strings.TrimPrefix(version, "v")
	archiveName := fmt.Sprintf("%s_%s_%s_%s.tar.gz", binary, releaseVersion, runtime.GOOS, runtime.GOARCH)
	baseURL := fmt.Sprintf("https://github.com/hecatehq/%s/releases/download/%s", repo, version)
	checksums := downloadURL(t, baseURL+"/checksums.txt")
	archive := downloadURL(t, baseURL+"/"+archiveName)
	verifyReleaseChecksum(t, checksums, archiveName, archive)
	extractSingleBinary(t, archive, binary, filepath.Join(binDir, binary))
}

func downloadURL(t *testing.T, url string) []byte {
	t.Helper()
	client := http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("download %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("download %s: status %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return body
}

func verifyReleaseChecksum(t *testing.T, checksums []byte, archiveName string, archive []byte) {
	t.Helper()
	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == archiveName {
			if fields[0] != got {
				t.Fatalf("%s checksum = %s, want %s", archiveName, got, fields[0])
			}
			return
		}
	}
	t.Fatalf("checksums.txt missing %s", archiveName)
}

func extractSingleBinary(t *testing.T, archive []byte, binary, dst string) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("open release archive: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read release archive: %v", err)
		}
		if header.FileInfo().IsDir() || filepath.Base(header.Name) != binary {
			continue
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			t.Fatalf("create %s: %v", dst, err)
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			t.Fatalf("extract %s: %v", binary, err)
		}
		if err := out.Close(); err != nil {
			t.Fatalf("close %s: %v", dst, err)
		}
		return
	}
	t.Fatalf("archive missing binary %q", binary)
}

func installFakeVendorCLI(t *testing.T, name, body string) {
	t.Helper()
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

const fakeCodexCLIScript = `
require_contains() {
  pattern="$1"
  shift
  case " $* " in
    *"$pattern"*) ;;
    *) echo "missing expected codex argument pattern: $pattern in $*" >&2; exit 65 ;;
  esac
}

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
    require_contains " --sandbox read-only " "$@"
    require_contains " --model gpt-5-codex " "$@"
    require_contains " --config model_reasoning_effort=\"high\" " "$@"
    require_contains " --config mcp_servers.hecate_01_docs={url=\"https://docs.example.com/mcp\",http_headers={\"Authorization\"=\"Bearer token\"}} " "$@"
    require_contains " --search " "$@"
    case "$*" in
      *"/auth-fail"*) echo "Authentication required: run codex login" >&2; exit 67;;
      *"/init focus on repo guidance"*) message="go codex init";;
      *"reload codex"*) message="go codex reload";;
      *"hello codex"*) message="go codex answer";;
      *) echo "unexpected codex exec prompt: $*" >&2; exit 66 ;;
    esac
    printf '{"method":"item/started","params":{"item":{"type":"local_shell_call","id":"tool-1","command":"go test ./..."}}}\n'
    printf '{"method":"item/reasoning/textDelta","params":{"item_id":"thought-1","delta":"checking"}}\n'
    printf '{"method":"item/completed","params":{"item":{"type":"agent_message","id":"msg-1","text":"%s"}}}\n' "$message"
    if [ "$message" = "go codex answer" ]; then
      finish_reason="max_tokens"
    else
      finish_reason="end_turn"
    fi
    printf '{"method":"turn/completed","params":{"finish_reason":"%s","usage":{"input_tokens":10,"output_tokens":5,"context_window":100}}}\n' "$finish_reason"
    printf '{"method":"item/completed","params":{"item":{"type":"local_shell_call","id":"tool-1","status":"completed","stdout":"ok"}}}\n'
    exit 0
    ;;
  review)
    require_contains " --uncommitted " "$@"
    require_contains " --config model=\"gpt-5-codex\" " "$@"
    require_contains " --config model_reasoning_effort=\"high\" " "$@"
    require_contains " --config mcp_servers.hecate_01_docs={url=\"https://docs.example.com/mcp\",http_headers={\"Authorization\"=\"Bearer token\"}} " "$@"
    require_contains " focus on tests " "$@"
    printf '{"method":"item/completed","params":{"item":{"type":"agent_message","id":"msg-review","text":"go codex review"}}}\n'
    printf '{"method":"turn/completed","params":{"finish_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":6,"context_window":100}}}\n'
    exit 0
    ;;
esac
echo "unexpected codex command: $*" >&2
exit 64
`

const fakeClaudeCodeCLIScript = `
require_contains() {
  pattern="$1"
  shift
  case " $* " in
    *"$pattern"*) ;;
    *) echo "missing expected claude argument pattern: $pattern in $*" >&2; exit 65 ;;
  esac
}

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
    require_contains " --permission-mode plan " "$@"
    require_contains " --model sonnet " "$@"
    require_contains " --effort high " "$@"
    require_contains " --strict-mcp-config " "$@"
    require_contains " --mcp-config {\"mcpServers\":{\"docs\":{\"headers\":{\"Authorization\":\"Bearer token\"},\"type\":\"http\",\"url\":\"https://docs.example.com/mcp\"}}} " "$@"
    case "$*" in
      *"/auth-fail"*) echo "Authentication required: run claude /login" >&2; exit 67;;
      *"/verify release smoke"*) message="go claude verify"; stop_reason="end_turn";;
      *"reload claude_code"*) message="go claude reload"; stop_reason="end_turn";;
      *"hello claude_code"*) message="go claude answer"; stop_reason="error_max_turns";;
      *) echo "unexpected claude prompt: $*" >&2; exit 66 ;;
    esac
    printf '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"go test ./..."}}]}}\n'
    printf '{"type":"assistant","message":{"content":[{"type":"thinking","id":"thought-1","thinking":"checking"},{"type":"text","text":"%s"}]}}\n' "$message"
    printf '{"type":"result","subtype":"%s","usage":{"input_tokens":10,"output_tokens":5,"context_window":100}}\n' "$stop_reason"
    printf '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"ok"}]}}\n'
    exit 0
    ;;
esac
echo "unexpected claude command: $*" >&2
exit 64
`
