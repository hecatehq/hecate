//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestNativeChatTextAttachmentE2E(t *testing.T) {
	const sentinel = "private-native-file-sentinel: source code and notes"
	const model = "native-text-test-model"
	captured := &capturedRequests{}
	refPattern := regexp.MustCompile(`"attachment_ref":"([^"]+)"`)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"object":"list","data":[{"id":%q}]}`, model)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		captured.record(request)
		if request["tool_choice"] != nil {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"probe","model":%q,"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"probe","type":"function","function":{"name":"hecate_capability_probe","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, model)
			return
		}
		if request["tools"] != nil {
			messages, _ := request["messages"].([]any)
			toolResults := 0
			ref := ""
			for _, item := range messages {
				message, _ := item.(map[string]any)
				if message["role"] == "tool" {
					toolResults++
				}
				if text, ok := message["content"].(string); ok {
					if match := refPattern.FindStringSubmatch(text); len(match) == 2 {
						ref = match[1]
					}
				}
			}
			if toolResults < 2 {
				if ref == "" {
					t.Error("private attachment reference missing")
					w.WriteHeader(400)
					return
				}
				if toolResults == 0 {
					encoded, _ := json.Marshal(request)
					if strings.Contains(string(encoded), sentinel) {
						t.Error("initial task prompt eagerly disclosed text body")
					}
				}
				name := "read_attachment"
				args := fmt.Sprintf(`{"attachment_ref":%q,"offset":0,"max_bytes":1024}`, ref)
				if toolResults == 1 {
					name = "search_attachment"
					args = fmt.Sprintf(`{"attachment_ref":%q,"query":"private-native-file-sentinel","max_matches":1}`, ref)
				}
				if request["stream"] == true {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"id\":\"read\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%q}}]},\"finish_reason\":null}]}\n\ndata: {\"id\":\"read\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", model, fmt.Sprintf("file-%d", toolResults), name, args, model)
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"id":"read","model":%q,"choices":[{"message":{"role":"assistant","tool_calls":[{"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]},"finish_reason":"tool_calls"}]}`, model, fmt.Sprintf("file-%d", toolResults), name, args)
				}
				return
			}
		}
		if request["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"id\":\"reply\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"File received.\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"reply\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", model, model)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"reply","model":%q,"choices":[{"message":{"role":"assistant","content":"File received."},"finish_reason":"stop"}]}`, model)
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()
	baseURL := gatewayServer(t, "HECATE_BACKEND=sqlite", "HECATE_TASK_APPROVAL_POLICIES=",
		"PROVIDER_FAKE_API_KEY=dummy", "PROVIDER_FAKE_BASE_URL="+upstream.URL,
		"PROVIDER_FAKE_KIND=local", "PROVIDER_FAKE_MODELS="+model)
	probe := postJSONDecode[e2eModelToolProbeResponse](t, baseURL+"/hecate/v1/model-capabilities/tool-probes", fmt.Sprintf(`{"provider":"fake","model":%q}`, model))
	if probe.Data.Verification == nil || probe.Data.Verification.Status != "supported" {
		t.Fatalf("tool probe failed: %+v", probe.Data)
	}
	for _, toolsOn := range []bool{false, true} {
		t.Run(fmt.Sprintf("tools=%t", toolsOn), func(t *testing.T) {
			padding := 40 << 10
			if toolsOn {
				padding = 100 << 10
			}
			body := strings.Repeat("x", padding) + "\n" + sentinel
			created := postJSONDecode[e2eChatSessionResponse](t, baseURL+"/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"hecate","provider":"fake","model":%q,"workspace":%q,"workspace_mode":"in_place"}`, model, t.TempDir()))
			upload, raw := e2eUploadChatAttachmentType(t, baseURL, created.Data.ID, "notes.go", "application/octet-stream", []byte(body))
			if upload.Data.MediaType != "text/plain" || strings.Contains(string(raw), body) {
				t.Fatalf("unsafe text metadata: %s", raw)
			}
			response := postJSON(t, baseURL+"/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", fmt.Sprintf(`{"execution_mode":"hecate_task","tools_enabled":%t,"content":"Review the file.","attachment_ids":[%q]}`, toolsOn, upload.Data.ID), nil)
			defer response.Body.Close()
			transcript, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK || !strings.Contains(string(transcript), "File received.") {
				t.Fatalf("turn failed: %d %s", response.StatusCode, transcript)
			}
			if strings.Contains(string(transcript), body) {
				t.Fatal("file body copied to transcript")
			}
			wire, err := json.Marshal(captured.lastBody())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(wire), sentinel) {
				t.Fatal("provider did not receive file excerpt")
			}
			if !toolsOn {
				messages := captured.lastBody()["messages"].([]any)
				last := messages[len(messages)-1].(map[string]any)
				if !strings.HasSuffix(last["content"].(string), body) {
					t.Fatal("direct file was truncated")
				}
			}
			if toolsOn {
				var session struct {
					Data struct {
						TaskID      string `json:"task_id"`
						LatestRunID string `json:"latest_run_id"`
					} `json:"data"`
				}
				if err := json.Unmarshal(transcript, &session); err != nil {
					t.Fatal(err)
				}
				if session.Data.TaskID == "" || session.Data.LatestRunID == "" {
					t.Fatal("tools turn did not create a backing run")
				}
				artifacts := e2eGetRaw(t, baseURL+"/hecate/v1/tasks/"+session.Data.TaskID+"/runs/"+session.Data.LatestRunID+"/artifacts", http.StatusOK)
				if strings.Contains(string(artifacts), sentinel) || !strings.Contains(string(artifacts), "not retained in Task artifacts") {
					t.Fatalf("unsafe or missing conversation omission: %s", artifacts)
				}
			}
			content := e2eGetRaw(t, baseURL+upload.Data.ContentURL, http.StatusOK)
			if string(content) != body {
				t.Fatal("stored text content changed")
			}
		})
	}
}
