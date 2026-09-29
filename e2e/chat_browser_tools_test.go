//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHecateChatBrowserApprovalE2E(t *testing.T) {
	executable, marker := buildE2EBrowserSentinel(t)
	for _, tt := range []struct {
		name, approval, decision, tool string
		configured                     bool
	}{
		{"allow still requires approval before rejection", "allow", "reject", "browser_inspect", true},
		{"inherit still requires approval before Stop", "inherit", "stop", "browser_flow", true},
		{"unconfigured browser tools are absent", "allow", "", "browser_inspect", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			browser := ""
			if tt.configured {
				browser = executable
			}
			const origin = "https://browser.example.test"
			arguments := `{"url":"` + origin + `/"}`
			if tt.tool == "browser_flow" {
				arguments = `{"url":"` + origin + `/","actions":[{"kind":"click","role":"button","name":"Continue"}]}`
			}
			upstream, captured := fakeChatBrowserUpstream(t, []e2eBrowserCall{{tt.tool, arguments}})
			baseURL, created := createE2EBrowserChat(t, upstream, browser, marker, origin, tt.approval)
			// Editing the reusable policy must not widen, revoke, or otherwise
			// replace the independently frozen Chat and backing Task authority.
			updateE2EBrowserPreset(t, baseURL, `{"browser_allowed":false,"browser_interactions_allowed":false,"browser_allowed_origins":[]}`)
			assertE2EBrowserSnapshot(t, getJSON[e2eChatBrowserResponse](t, baseURL+"/hecate/v1/chat/sessions/"+created.Data.ID), origin, tt.approval)
			settled := postE2EBrowserMessage(t, baseURL, created.Data.ID)
			linked := waitForE2EChatTaskLink(t, baseURL, created.Data.ID, e2eChatWorkPolicyResponse{}, 10*time.Second)
			assertE2EBrowserTaskSnapshot(t, baseURL, linked.Data.TaskID, origin, tt.approval)
			if tt.configured {
				approval := waitForE2EBrowserApproval(t, baseURL, linked.Data.TaskID)
				assertE2EBrowserNotDispatched(t, baseURL, linked.Data.TaskID, linked.Data.LatestRunID, marker)
				if tt.decision == "stop" {
					response := postJSON(t, baseURL+"/hecate/v1/chat/sessions/"+created.Data.ID+"/cancel", `{}`, nil)
					defer response.Body.Close()
					if response.StatusCode != http.StatusAccepted {
						t.Fatalf("Chat Stop status=%d body=%s", response.StatusCode, readBody(t, response))
					}
				} else {
					postJSONDecode[e2eTaskApprovalResponse](t, baseURL+"/hecate/v1/tasks/"+linked.Data.TaskID+"/approvals/"+approval.ID+"/resolve", `{"decision":"reject"}`)
				}
				waitForE2ETaskRunStatus(t, baseURL, linked.Data.TaskID, linked.Data.LatestRunID, "cancelled", 10*time.Second)
			} else {
				waitForE2ETaskRunStatus(t, baseURL, linked.Data.TaskID, linked.Data.LatestRunID, "completed", 10*time.Second)
			}
			waitForE2EBrowserMessage(t, settled)
			assertE2EBrowserNotDispatched(t, baseURL, linked.Data.TaskID, linked.Data.LatestRunID, marker)
			bodies := capturedBodies(captured)
			if len(bodies) != 2 {
				t.Fatalf("upstream calls=%d, want one probe and one Chat call", len(bodies))
			}
			for _, name := range []string{"browser_inspect", "browser_flow"} {
				if got := requestAdvertisedTool(bodies[1], name); got != tt.configured {
					t.Fatalf("%s advertised=%t want=%t", name, got, tt.configured)
				}
			}
		})
	}
}

// Opt-in only: exercise the real Chat → Task → approval → Chromium path with
// a local test page and a fake model. Hecate owns a fresh temporary browser
// profile for each call; this test never attaches to the user's browser.
func TestHecateChatBrowserChromiumSmokeE2E(t *testing.T) {
	if os.Getenv("HECATE_BROWSER_SMOKE") != "1" {
		t.Skip("set HECATE_BROWSER_SMOKE=1 and HECATE_TASK_BROWSER_EXECUTABLE for the real Chromium smoke")
	}
	executable := strings.TrimSpace(os.Getenv("HECATE_TASK_BROWSER_EXECUTABLE"))
	if executable == "" {
		t.Fatal("HECATE_TASK_BROWSER_EXECUTABLE is required")
	}
	if info, err := os.Stat(executable); err != nil || info.IsDir() {
		t.Fatalf("configured browser executable unavailable: %v", err)
	}
	var pageLoads, scriptCalls atomic.Int32
	var reusedCookie atomic.Bool
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/script-ran" {
			scriptCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != "/" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		pageLoads.Add(1)
		if _, err := r.Cookie("hecate_chat_browser_smoke"); err == nil {
			reusedCookie.Store(true)
		}
		http.SetCookie(w, &http.Cookie{Name: "hecate_chat_browser_smoke", Value: "isolated", Path: "/"})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Chat browser smoke</title></head><body>
<h1>Chat browser evidence</h1>
<button aria-label="Continue" onclick="scriptReady.then(() => { document.title='Chat flow complete'; document.getElementById('ready').hidden=false; })">Continue</button>
<div id="ready" role="status" aria-label="Ready" hidden>Ready</div>
<script>const scriptReady = fetch('/script-ran');</script></body></html>`)
	}))
	t.Cleanup(site.Close)
	calls := []e2eBrowserCall{
		{"browser_inspect", fmt.Sprintf(`{"url":%q}`, site.URL+"/")},
		{"browser_flow", fmt.Sprintf(`{"url":%q,"actions":[{"kind":"click","role":"button","name":"Continue"},{"kind":"wait_for","role":"status","name":"Ready"}]}`, site.URL+"/")},
	}
	upstream, captured := fakeChatBrowserUpstream(t, calls)
	baseURL, created := createE2EBrowserChat(t, upstream, executable, "", site.URL, "allow")
	settled := postE2EBrowserMessage(t, baseURL, created.Data.ID)
	linked := waitForE2EChatTaskLink(t, baseURL, created.Data.ID, e2eChatWorkPolicyResponse{}, 10*time.Second)
	for index := range calls {
		approval := waitForE2EBrowserApproval(t, baseURL, linked.Data.TaskID)
		if got := pageLoads.Load(); got != int32(index) {
			t.Fatalf("page loads before approval %d = %d, want %d", index+1, got, index)
		}
		if scriptCalls.Load() != 0 {
			t.Fatal("static inspection executed page scripts before flow approval")
		}
		postJSONDecode[e2eTaskApprovalResponse](t, baseURL+"/hecate/v1/tasks/"+linked.Data.TaskID+"/approvals/"+approval.ID+"/resolve", `{"decision":"approve"}`)
	}
	waitForE2ETaskRunStatus(t, baseURL, linked.Data.TaskID, linked.Data.LatestRunID, "completed", 45*time.Second)
	waitForE2EBrowserMessage(t, settled)
	if pageLoads.Load() != 2 || scriptCalls.Load() != 1 || reusedCookie.Load() {
		t.Fatalf("browser lifecycle: page loads=%d scripts=%d reused cookie=%t", pageLoads.Load(), scriptCalls.Load(), reusedCookie.Load())
	}
	artifacts := getJSON[codeIntelligenceDogfoodArtifactsResponse](t, baseURL+"/hecate/v1/tasks/"+linked.Data.TaskID+"/runs/"+linked.Data.LatestRunID+"/artifacts")
	evidence := make(map[string]string)
	for _, artifact := range artifacts.Data {
		evidence[artifact.Kind] = artifact.ContentText
	}
	if !strings.Contains(evidence["browser_evidence"], "Chat browser evidence") || !strings.Contains(evidence["browser_flow_evidence"], "Chat flow complete") || !strings.Contains(evidence["browser_flow_evidence"], `wait_for role="status" name="Ready": completed`) {
		t.Fatalf("missing complete browser evidence: %+v", evidence)
	}
	bodies := capturedBodies(captured)
	if len(bodies) != 4 {
		t.Fatalf("upstream calls=%d, want probe plus three Chat calls", len(bodies))
	}
	for _, tool := range calls {
		if !requestAdvertisedTool(bodies[1], tool.name) {
			t.Fatalf("%s missing from Chat tool catalog", tool.name)
		}
	}
}

type e2eChatBrowserResponse struct {
	Data struct {
		ID          string `json:"id"`
		AgentPreset struct {
			ApprovalPolicy             string   `json:"approval_policy"`
			BrowserAllowed             bool     `json:"browser_allowed"`
			BrowserInteractionsAllowed bool     `json:"browser_interactions_allowed"`
			BrowserAllowedOrigins      []string `json:"browser_allowed_origins"`
		} `json:"agent_preset"`
	} `json:"data"`
}

func createE2EBrowserChat(t *testing.T, upstream, executable, marker, origin, approvalPolicy string) (string, e2eChatBrowserResponse) {
	t.Helper()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite", "HECATE_TASK_APPROVAL_POLICIES=",
		"HECATE_TASK_BROWSER_EXECUTABLE="+executable, "HECATE_TASK_BROWSER_ALLOW_PRIVATE_IPS=1", "HECATE_TASK_BROWSER_TIMEOUT=30s",
		"HECATE_E2E_BROWSER_MARKER="+marker,
		"PROVIDER_FAKE_API_KEY=dummy", "PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_KIND=local", "PROVIDER_FAKE_MODELS="+agentLoopE2EModel,
	)
	postJSONDecodeStatus[e2eAgentPresetResponse](t, baseURL+"/hecate/v1/agent-presets", fmt.Sprintf(`{"id":"chat_browser","name":"Chat browser","surface":"hecate_chat","tools_enabled":true,"writes_allowed":true,"network_allowed":false,"approval_policy":%q,"browser_allowed":true,"browser_interactions_allowed":true,"browser_allowed_origins":[%q]}`, approvalPolicy, origin), http.StatusCreated)
	probe := postJSONDecode[e2eModelToolProbeResponse](t, baseURL+"/hecate/v1/model-capabilities/tool-probes", fmt.Sprintf(`{"provider":"fake","model":%q}`, agentLoopE2EModel))
	if probe.Data.Verification == nil || probe.Data.Verification.Status != "supported" {
		t.Fatalf("tool verification=%+v", probe.Data.Verification)
	}
	created := postJSONDecode[e2eChatBrowserResponse](t, baseURL+"/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"hecate","agent_preset_id":"chat_browser","provider":"fake","model":%q,"workspace":%q,"workspace_mode":"in_place"}`, agentLoopE2EModel, workspace))
	assertE2EBrowserSnapshot(t, created, origin, approvalPolicy)
	return baseURL, created
}

func assertE2EBrowserSnapshot(t *testing.T, response e2eChatBrowserResponse, origin, approvalPolicy string) {
	t.Helper()
	snapshot := response.Data.AgentPreset
	if !snapshot.BrowserAllowed || !snapshot.BrowserInteractionsAllowed || !slices.Equal(snapshot.BrowserAllowedOrigins, []string{origin}) || snapshot.ApprovalPolicy != approvalPolicy {
		t.Fatalf("Chat browser snapshot=%+v", snapshot)
	}
}

func assertE2EBrowserTaskSnapshot(t *testing.T, baseURL, taskID, origin, approvalPolicy string) {
	t.Helper()
	response := getJSON[struct {
		Data struct {
			ApprovalPolicy             string   `json:"agent_preset_approval_policy"`
			BrowserAllowed             bool     `json:"agent_preset_browser_allowed"`
			BrowserInteractionsAllowed bool     `json:"agent_preset_browser_interactions_allowed"`
			BrowserAllowedOrigins      []string `json:"agent_preset_browser_allowed_origins"`
		} `json:"data"`
	}](t, baseURL+"/hecate/v1/tasks/"+taskID)
	if !response.Data.BrowserAllowed || !response.Data.BrowserInteractionsAllowed || !slices.Equal(response.Data.BrowserAllowedOrigins, []string{origin}) || response.Data.ApprovalPolicy != approvalPolicy {
		t.Fatalf("backing Task browser snapshot=%+v", response.Data)
	}
}

func updateE2EBrowserPreset(t *testing.T, baseURL, body string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPatch, baseURL+"/hecate/v1/agent-presets/chat_browser", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update browser policy: status=%d body=%s", response.StatusCode, readBody(t, response))
	}
}

func postE2EBrowserMessage(t *testing.T, baseURL, sessionID string) <-chan e2eAsyncChatMessageResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	result := make(chan e2eAsyncChatMessageResult, 1)
	go func() {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/hecate/v1/chat/sessions/"+sessionID+"/messages", strings.NewReader(`{"tools_enabled":true,"content":"Inspect the approved page, then perform its approved browser flow."}`))
		if err != nil {
			result <- e2eAsyncChatMessageResult{err: err}
			return
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			result <- e2eAsyncChatMessageResult{err: err}
			return
		}
		defer response.Body.Close()
		var decoded e2eChatWorkPolicyResponse
		err = json.NewDecoder(response.Body).Decode(&decoded)
		result <- e2eAsyncChatMessageResult{status: response.StatusCode, err: err}
	}()
	return result
}

func waitForE2EBrowserMessage(t *testing.T, result <-chan e2eAsyncChatMessageResult) {
	t.Helper()
	select {
	case settled := <-result:
		if settled.err != nil || settled.status != http.StatusOK {
			t.Fatalf("Chat message settlement=%+v", settled)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Chat message did not settle")
	}
}

func waitForE2EBrowserApproval(t *testing.T, baseURL, taskID string) e2eTaskApproval {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		approvals := getJSON[e2eTaskApprovalsResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/approvals")
		for _, approval := range approvals.Data {
			if approval.Status == "pending" {
				return approval
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("Chat browser call did not request approval")
	return e2eTaskApproval{}
}

func assertE2EBrowserNotDispatched(t *testing.T, baseURL, taskID, runID, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("browser executable was invoked before approval: %v", err)
	}
	steps := getJSON[e2eTaskStepsResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/runs/"+runID+"/steps")
	for _, step := range steps.Data {
		if step.Kind == "tool" && (step.ToolName == "browser_inspect" || step.ToolName == "browser_flow") {
			t.Fatalf("browser tool dispatched without approval: %+v", step)
		}
	}
}

func buildE2EBrowserSentinel(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	name := "browser-sentinel"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	executable := filepath.Join(dir, name)
	command := exec.CommandContext(t.Context(), "go", "build", "-o", executable, "./e2e/testdata/browser-sentinel")
	command.Dir = moduleRootDir()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build inert browser sentinel: %v\n%s", err, output)
	}
	return executable, filepath.Join(dir, "unexpected-browser-launch")
}

type e2eBrowserCall struct{ name, arguments string }

func fakeChatBrowserUpstream(t *testing.T, calls []e2eBrowserCall) (string, *capturedRequests) {
	t.Helper()
	captured := &capturedRequests{}
	var chatCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"object":"list","data":[{"id":%q,"object":"model"}]}`, agentLoopE2EModel)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid test request", http.StatusBadRequest)
			return
		}
		captured.record(body)
		call := e2eBrowserCall{}
		if requestAdvertisedTool(body, "hecate_capability_probe") {
			call = e2eBrowserCall{"hecate_capability_probe", `{}`}
		} else if index := int(chatCalls.Add(1)) - 1; index < len(calls) && requestAdvertisedTool(body, calls[index].name) {
			call = calls[index]
		}
		if streamed, _ := body["stream"].(bool); streamed {
			w.Header().Set("Content-Type", "text/event-stream")
			if call.name != "" {
				writeAgentLoopNamedToolCallStream(t, w, "chat-browser-call", "call-"+call.name, call.name, call.arguments, "I will use the approved browser.")
			} else {
				writeAgentLoopFinalAnswerStream(t, w)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if call.name != "" {
			_, _ = fmt.Fprintf(w, `{"id":"chat-browser-call","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"I will use the approved browser.","tool_calls":[{"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`, agentLoopE2EModel, "call-"+call.name, call.name, call.arguments)
		} else {
			_, _ = fmt.Fprintf(w, `{"id":"chat-browser-final","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"Browser work finished."},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`, agentLoopE2EModel)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL, captured
}
