//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeChatWorkspaceSkillsE2E(t *testing.T) {
	const model = "workspace-skill-test-model"
	const sentinel = "private-workspace-skill-sentinel: review carefully"
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
			w.WriteHeader(http.StatusBadRequest)
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
			fmt.Fprintf(w, "data: {\"id\":\"reply\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Review complete.\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"reply\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", model, model)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"reply","model":%q,"choices":[{"message":{"role":"assistant","content":"Review complete."},"finish_reason":"stop"}]}`, model)
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()
	baseURL := gatewayServer(t, "HECATE_BACKEND=sqlite", "HECATE_TASK_APPROVAL_POLICIES=", "PROVIDER_FAKE_API_KEY=dummy", "PROVIDER_FAKE_BASE_URL="+upstream.URL, "PROVIDER_FAKE_KIND=local", "PROVIDER_FAKE_MODELS="+model)
	probe := postJSONDecode[e2eModelToolProbeResponse](t, baseURL+"/hecate/v1/model-capabilities/tool-probes", fmt.Sprintf(`{"provider":"fake","model":%q}`, model))
	if probe.Data.Verification == nil || probe.Data.Verification.Status != "supported" {
		t.Fatal("tool probe failed")
	}
	for _, toolsOn := range []bool{false, true} {
		t.Run(fmt.Sprintf("tools=%t", toolsOn), func(t *testing.T) {
			workspace := t.TempDir()
			filename := filepath.Join(workspace, ".agents", "skills", "review", "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filename, []byte(sentinel), 0o600); err != nil {
				t.Fatal(err)
			}
			catalog := postJSONDecode[struct {
				Data []struct {
					Path   string `json:"path"`
					SHA256 string `json:"sha256"`
				} `json:"data"`
			}](t, baseURL+"/hecate/v1/workspace-skills", fmt.Sprintf(`{"workspace":%q}`, workspace))
			if len(catalog.Data) != 1 {
				t.Fatal("skill missing from catalog")
			}
			created := postJSONDecode[e2eChatSessionResponse](t, baseURL+"/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"hecate","provider":"fake","model":%q,"workspace":%q,"workspace_mode":"in_place","workspace_skills":[{"path":%q,"sha256":%q}]}`, model, workspace, catalog.Data[0].Path, catalog.Data[0].SHA256))
			if err := os.WriteFile(filename, []byte("changed-skill-sentinel"), 0o600); err != nil {
				t.Fatal(err)
			}
			response := postJSON(t, baseURL+"/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", fmt.Sprintf(`{"execution_mode":"hecate_task","tools_enabled":%t,"content":"Review the workspace."}`, toolsOn), nil)
			defer response.Body.Close()
			transcript := readBody(t, response)
			if response.StatusCode != http.StatusOK || !strings.Contains(transcript, "Review complete.") || strings.Contains(transcript, sentinel) {
				t.Fatalf("unexpected turn: status=%d body=%s", response.StatusCode, transcript)
			}
			wire, _ := json.Marshal(captured.lastBody())
			if !strings.Contains(string(wire), sentinel) || strings.Contains(string(wire), "changed-skill-sentinel") {
				t.Fatal("provider did not receive original frozen skill")
			}
			if toolsOn {
				var session struct {
					Data struct {
						TaskID      string `json:"task_id"`
						LatestRunID string `json:"latest_run_id"`
					} `json:"data"`
				}
				if err := json.Unmarshal([]byte(transcript), &session); err != nil {
					t.Fatal(err)
				}
				if session.Data.TaskID == "" || session.Data.LatestRunID == "" {
					t.Fatal("backing task missing")
				}
				for _, suffix := range []string{"", "/runs/" + session.Data.LatestRunID + "/artifacts"} {
					raw := e2eGetRaw(t, baseURL+"/hecate/v1/tasks/"+session.Data.TaskID+suffix, http.StatusOK)
					if strings.Contains(string(raw), sentinel) {
						t.Fatal("skill body escaped through task metadata or artifacts")
					}
				}
			}
		})
	}
}
