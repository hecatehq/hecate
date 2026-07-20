package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestHecateChatSession_AgentPresetSnapshotsHintsAndToolsOff(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-preset",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "A constrained answer."},
				FinishReason: "stop",
			}},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	if _, err := apiHandler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:               "safe_review",
		Name:             "Safe reviewer",
		Surface:          agentprofiles.SurfaceHecateChat,
		ProviderHint:     "openai",
		ModelHint:        "gpt-4o-mini",
		Instructions:     "Inspect before proposing changes.",
		ExecutionProfile: "review",
		ToolsEnabled:     false,
		WritesAllowed:    false,
		NetworkAllowed:   true,
	}); err != nil {
		t.Fatalf("Create preset: %v", err)
	}
	client := newTaskTestClient(t, NewServer(logger, apiHandler))

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", `{"agent_preset_id":"safe_review"}`)
	if created.Data.AgentID != chat.DefaultAgentID || created.Data.Provider != "openai" || created.Data.Model != "gpt-4o-mini" {
		t.Fatalf("created session route = agent %q provider %q model %q, want hecate/openai/gpt-4o-mini", created.Data.AgentID, created.Data.Provider, created.Data.Model)
	}
	if created.Data.AgentPreset == nil || created.Data.AgentPreset.ID != "safe_review" || created.Data.AgentPreset.Name != "Safe reviewer" || created.Data.AgentPreset.ExecutionProfile != "review" {
		t.Fatalf("created preset snapshot = %#v, want safe_review", created.Data.AgentPreset)
	}
	if created.Data.AgentPreset.ToolsEnabled || created.Data.AgentPreset.WritesAllowed || !created.Data.AgentPreset.NetworkAllowed {
		t.Fatalf("created preset posture = %#v, want tools=false writes=false network=true", created.Data.AgentPreset)
	}

	if _, err := apiHandler.agentProfiles.Update(t.Context(), "safe_review", func(profile *agentprofiles.Profile) {
		profile.Name = "Mutated reviewer"
		profile.Instructions = "Never allow this changed instruction into the existing chat."
		profile.ProviderHint = "mutated-provider"
		profile.ModelHint = "mutated-model"
	}); err != nil {
		t.Fatalf("Update preset after session creation: %v", err)
	}
	if err := apiHandler.agentProfiles.Delete(t.Context(), "safe_review"); err != nil {
		t.Fatalf("Delete preset after session creation: %v", err)
	}

	stored := mustRequestJSON[ChatSessionResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+created.Data.ID, "")
	if stored.Data.AgentPreset == nil || stored.Data.AgentPreset.Name != "Safe reviewer" || stored.Data.AgentPreset.Instructions != "Inspect before proposing changes." || stored.Data.AgentPreset.ProviderHint != "openai" || stored.Data.AgentPreset.ModelHint != "gpt-4o-mini" {
		t.Fatalf("stored preset snapshot changed after profile update: %#v", stored.Data.AgentPreset)
	}
	listed := mustRequestJSON[ChatSessionsResponse](client, http.MethodGet, "/hecate/v1/chat/sessions", "")
	if len(listed.Data) != 1 || listed.Data[0].AgentPreset == nil || listed.Data[0].AgentPreset.Name != "Safe reviewer" {
		t.Fatalf("listed preset snapshot changed after profile update: %#v", listed.Data)
	}

	rejected := client.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"execution_mode":"hecate_task","tools_enabled":true,"content":"do not start tools"}`)
	if !strings.Contains(rejected.Body.String(), "selected agent preset disables tools") {
		t.Fatalf("tools-on rejection response = %s", rejected.Body.String())
	}

	turned := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"execution_mode":"hecate_task","content":"answer directly","system_prompt":"Keep it concise."}`)
	if turned.Data.TaskID != "" || turned.Data.LatestRunID != "" {
		t.Fatalf("tools-off preset created a backing task: task=%q run=%q", turned.Data.TaskID, turned.Data.LatestRunID)
	}
	if len(turned.Data.Messages) != 2 || turned.Data.Messages[1].ToolsEnabled {
		t.Fatalf("tools-off turn messages = %#v, want direct model assistant with tools disabled", turned.Data.Messages)
	}
	if turned.Data.Messages[1].ContextPacket == nil || turned.Data.Messages[1].ContextPacket.ExecutionProfile != "review" {
		t.Fatalf("direct-model preset context = %#v, want review execution profile", turned.Data.Messages[1].ContextPacket)
	}
	request := provider.LastRequest()
	if len(request.Messages) < 2 || request.Messages[0].Role != "system" {
		t.Fatalf("provider request messages = %#v, want system and user messages", request.Messages)
	}
	for _, want := range []string{
		"Agent preset instructions:\nInspect before proposing changes.",
		"Operator system prompt:\nKeep it concise.",
	} {
		if !strings.Contains(request.Messages[0].Content, want) {
			t.Fatalf("frozen system prompt missing %q:\n%s", want, request.Messages[0].Content)
		}
	}
	if strings.Contains(request.Messages[0].Content, "Never allow this changed instruction") {
		t.Fatalf("provider system prompt used mutable profile instructions:\n%s", request.Messages[0].Content)
	}
}

func TestHecateChatSession_AgentPresetRejectsUnsupportedSelection(t *testing.T) {
	apiHandler := newTestAPIHandlerWithSettings(quietLogger(), nil, config.Config{}, controlplane.NewMemoryStore())
	if _, err := apiHandler.agentProfiles.Create(context.Background(), agentprofiles.Profile{
		ID:      "task_only",
		Name:    "Task only",
		Surface: agentprofiles.SurfaceHecateTask,
	}); err != nil {
		t.Fatalf("Create task-only preset: %v", err)
	}
	client := newTaskTestClient(t, NewServer(quietLogger(), apiHandler))

	wrongSurface := client.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/chat/sessions", `{"agent_preset_id":"task_only"}`)
	if !strings.Contains(wrongSurface.Body.String(), "agent preset is not available for Hecate Chat") {
		t.Fatalf("wrong surface response = %s", wrongSurface.Body.String())
	}
	missing := client.mustRequestStatus(http.StatusNotFound, http.MethodPost, "/hecate/v1/chat/sessions", `{"agent_preset_id":"missing"}`)
	if !strings.Contains(missing.Body.String(), "agent preset not found") {
		t.Fatalf("missing preset response = %s", missing.Body.String())
	}
	external := client.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/chat/sessions", `{"agent_id":"claude_code","agent_preset_id":"task_only"}`)
	if !strings.Contains(external.Body.String(), "only supported for Hecate Chat") {
		t.Fatalf("external preset response = %s", external.Body.String())
	}
}
