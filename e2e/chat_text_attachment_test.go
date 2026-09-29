//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeChatTextAttachmentE2E(t *testing.T) {
	const body = "private-native-file-sentinel: source code and notes"
	const model = "native-text-test-model"
	captured := &capturedRequests{}
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
			if !strings.Contains(string(wire), body) {
				t.Fatalf("provider did not receive file: %s", wire)
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
				if strings.Contains(string(artifacts), body) || !strings.Contains(string(artifacts), "body not retained in Task artifacts") {
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
