package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/taskstate"
)

func TestChatBrowserPresetAPIFreezesIndependentNormalizedGrants(t *testing.T) {
	t.Parallel()
	for _, grant := range []string{"browser_allowed", "browser_interactions_allowed"} {
		t.Run(grant, func(t *testing.T) {
			server := newAgentPresetsTestServer()
			client := newTaskTestClient(t, server)
			body := `{"id":"chat_browser","name":"Browser policy","surface":"hecate_chat","tools_enabled":true,"` + grant + `":true,"browser_allowed_origins":["https://APP.example.test:443/","https://app.example.test"]}`
			created := mustRequestJSONStatus[AgentPresetResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/agent-presets", body)
			if len(created.Data.BrowserAllowedOrigins) != 1 || created.Data.BrowserAllowedOrigins[0] != "https://app.example.test" {
				t.Fatalf("origins = %v, want normalized exact origin", created.Data.BrowserAllowedOrigins)
			}
			session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", `{"agent_preset_id":"chat_browser"}`)
			assert := func(snapshot *ChatAgentPresetSnapshotItem) {
				t.Helper()
				if snapshot == nil || snapshot.BrowserAllowed == nil || *snapshot.BrowserAllowed != (grant == "browser_allowed") || snapshot.BrowserInteractionsAllowed == nil || *snapshot.BrowserInteractionsAllowed != (grant == "browser_interactions_allowed") || len(snapshot.BrowserAllowedOrigins) != 1 || snapshot.BrowserAllowedOrigins[0] != "https://app.example.test" {
					t.Fatalf("snapshot = %#v, want independent frozen grant %s", snapshot, grant)
				}
			}
			assert(session.Data.AgentPreset)
			mustRequestJSON[AgentPresetResponse](client, http.MethodPatch, "/hecate/v1/agent-presets/chat_browser", `{"surface":"hecate_task","browser_allowed":true,"browser_interactions_allowed":true,"browser_allowed_origins":["https://changed.example.test"]}`)
			got := mustRequestJSON[ChatSessionResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+session.Data.ID, "")
			assert(got.Data.AgentPreset)
			listed := mustRequestJSON[ChatSessionsResponse](client, http.MethodGet, "/hecate/v1/chat/sessions", "")
			if len(listed.Data) != 1 {
				t.Fatalf("sessions = %d, want one", len(listed.Data))
			}
			assert(listed.Data[0].AgentPreset)
		})
	}
}

func TestChatBrowserPresetAPIRejectsIneligibleCreationAndOriginExpansion(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"surface":"hecate_chat","tools_enabled":false,"browser_allowed":true,"browser_allowed_origins":["https://app.example.test"]}`,
		`{"surface":"hecate_chat","tools_enabled":true,"browser_allowed":true}`,
		`{"surface":"hecate_chat","tools_enabled":true,"browser_interactions_allowed":true,"browser_allowed_origins":["https://app.example.test/path"]}`,
		`{"surface":"hecate_chat","tools_enabled":true,"browser_allowed":true,"browser_allowed_origins":["https://*.example.test"]}`,
		`{"surface":"external_agent","tools_enabled":true,"browser_allowed":true,"browser_allowed_origins":["https://app.example.test"]}`,
	} {
		server := newAgentPresetsTestServer()
		body = `{"id":"invalid_browser","name":"Invalid",` + strings.TrimPrefix(body, "{")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/agent-presets", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("create status=%d body=%s for %s", rec.Code, rec.Body.String(), body)
		}
	}
}

func TestChatBrowserSnapshotCannotBeForgedByCreateRequest(t *testing.T) {
	t.Parallel()
	client := newTaskTestClient(t, newAgentPresetsTestServer())
	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", `{"agent_preset":{"id":"forged","tools_enabled":true,"browser_allowed":true,"browser_interactions_allowed":true,"browser_allowed_origins":["https://app.example.test"]},"browser_allowed":true}`)
	if session.Data.AgentPreset != nil {
		t.Fatalf("create accepted output-only snapshot: %+v", session.Data.AgentPreset)
	}
}

func TestChatBrowserSnapshotCopiesAreIndependentAndExplicit(t *testing.T) {
	t.Parallel()
	profile := agentprofiles.Profile{ID: "browser", BrowserAllowed: true, BrowserAllowedOrigins: []string{"https://app.example.test"}}
	snapshot := chatAgentPresetSnapshot(profile)
	profile.BrowserAllowedOrigins[0] = "https://changed.example.test"
	rendered := renderChatAgentPresetSnapshot(snapshot)
	*rendered.BrowserAllowed = false
	*rendered.BrowserInteractionsAllowed = true
	rendered.BrowserAllowedOrigins[0] = "https://rendered.example.test"
	if !*snapshot.BrowserAllowed || *snapshot.BrowserInteractionsAllowed || snapshot.BrowserAllowedOrigins[0] != "https://app.example.test" {
		t.Fatalf("snapshot aliases profile or rendered result: %#v", snapshot)
	}
	disabled := chatAgentPresetSnapshot(agentprofiles.Profile{ID: "disabled"})
	if disabled.BrowserAllowed == nil || *disabled.BrowserAllowed || disabled.BrowserInteractionsAllowed == nil || *disabled.BrowserInteractionsAllowed {
		t.Fatalf("new disabled snapshot must explicitly deny browser: %#v", disabled)
	}
	legacy := renderChatAgentPresetSnapshot(&chat.AgentPresetSnapshot{ID: "legacy"})
	encoded, err := json.Marshal(legacy)
	if err != nil || strings.Contains(string(encoded), "browser_") {
		t.Fatalf("legacy browser fields must remain absent: %s err=%v", encoded, err)
	}
}

func TestChatBrowserContextReportsOnlyFrozenExplicitPosture(t *testing.T) {
	t.Parallel()
	allowed, disabled := true, false
	for _, test := range []struct {
		name        string
		snapshot    *chat.AgentPresetSnapshot
		wantBrowser bool
	}{
		{name: "current", snapshot: &chat.AgentPresetSnapshot{ID: "browser", BrowserAllowed: &allowed, BrowserInteractionsAllowed: &disabled, BrowserAllowedOrigins: []string{"https://app.example.test"}}, wantBrowser: true},
		{name: "legacy", snapshot: &chat.AgentPresetSnapshot{ID: "legacy"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			packet := chat.ContextPacket{}
			appendHecateChatPresetContext(&packet, chat.Session{AgentPreset: test.snapshot})
			if len(packet.Items) != 1 {
				t.Fatalf("items = %+v", packet.Items)
			}
			metadata := packet.Items[0].Metadata
			if test.wantBrowser {
				if metadata["browser_allowed"] != "true" || metadata["browser_interactions_allowed"] != "false" || metadata["browser_allowed_origins"] != `["https://app.example.test"]` {
					t.Fatalf("browser metadata = %v", metadata)
				}
			} else {
				for key := range metadata {
					if strings.HasPrefix(key, "browser_") {
						t.Fatalf("legacy context invented browser posture: %v", metadata)
					}
				}
			}
		})
	}
}

func TestHecateAgentTaskOrchestrator_NewSegmentsCopyFrozenBrowserPolicy(t *testing.T) {
	t.Parallel()
	for _, grants := range []struct{ inspect, interact bool }{{true, false}, {false, true}, {true, true}, {false, false}} {
		store := taskstate.NewMemoryStore()
		runner := &recordingHecateAgentTaskRunner{}
		id := 0
		orchestrator := hecateAgentTaskOrchestrator{
			store: store, runner: runner,
			taskID: func() string {
				id++
				if id == 1 {
					return "first"
				}
				return "second"
			},
			resourceID: func(prefix string) string { return prefix + "_fixed" }, now: time.Now,
		}
		snapshot := &chat.AgentPresetSnapshot{ID: "frozen", ToolsEnabled: true, BrowserAllowed: &grants.inspect, BrowserInteractionsAllowed: &grants.interact}
		if grants.inspect || grants.interact {
			snapshot.BrowserAllowedOrigins = []string{"https://app.example.test"}
		}
		session := chat.Session{ID: "chat_browser", AgentPreset: snapshot}
		for _, forceNew := range []bool{false, true} {
			task, _, err := orchestrator.StartOrContinue(t.Context(), hecateAgentTaskRunCommand{Session: session, Prompt: "inspect", ForceNewTask: forceNew})
			if err != nil {
				t.Fatal(err)
			}
			if task.AgentPresetBrowserAllowed == nil || *task.AgentPresetBrowserAllowed != grants.inspect || task.AgentPresetBrowserInteractionsAllowed == nil || *task.AgentPresetBrowserInteractionsAllowed != grants.interact || len(task.AgentPresetBrowserAllowedOrigins) != len(snapshot.BrowserAllowedOrigins) {
				t.Fatalf("new task did not copy frozen browser grants: %+v", task)
			}
			*task.AgentPresetBrowserAllowed = !grants.inspect
			*task.AgentPresetBrowserInteractionsAllowed = !grants.interact
			if len(task.AgentPresetBrowserAllowedOrigins) > 0 {
				task.AgentPresetBrowserAllowedOrigins[0] = "https://mutated.example.test"
			}
			if *snapshot.BrowserAllowed != grants.inspect || *snapshot.BrowserInteractionsAllowed != grants.interact || (len(snapshot.BrowserAllowedOrigins) > 0 && snapshot.BrowserAllowedOrigins[0] != "https://app.example.test") {
				t.Fatal("task aliases chat snapshot")
			}
			session.TaskID = task.ID
		}
	}
}
