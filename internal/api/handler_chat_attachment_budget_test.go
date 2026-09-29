package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestNativeTextAttachmentFinalModelBudgetAfterRewrite(t *testing.T) {
	for _, tt := range []struct {
		name        string
		finalWindow int
		wantCalls   int
	}{{"smaller", 8192, 0}, {"larger", 200000, 1}} {
		t.Run(tt.name, func(t *testing.T) {
			provider := imageTurnTestProvider(modelcaps.ImageInputNone)
			provider.capabilities.Models = []string{"large", "rewritten"}
			provider.capabilities.DefaultModel = "large"
			provider.capabilities.ModelCapabilities = map[string]types.ModelCapabilities{
				"large":     {MaxContextTokens: 200000, ImageInput: modelcaps.ImageInputNone},
				"rewritten": {MaxContextTokens: tt.finalWindow, ImageInput: modelcaps.ImageInputNone},
			}
			h := newTestAPIHandlerWithSettings(imageTurnTestLogger(), []providers.Provider{provider}, config.Config{
				Governor: config.GovernorConfig{PolicyRules: []config.PolicyRuleConfig{{ID: "rewrite-text-model", Action: "rewrite_model", Models: []string{"large"}, RewriteModelTo: "rewritten"}}},
			}, controlplane.NewMemoryStore())
			handler := NewServer(imageTurnTestLogger(), h)
			client := newTaskTestClient(t, handler)
			session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", `{"agent_id":"hecate","provider":"ollama","model":"large"}`)
			upload := imageTurnTestUpload(t, handler, session.Data.ID, "source.go", []byte(strings.Repeat("x", 100<<10)))
			response := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages", fmt.Sprintf(`{"tools_enabled":false,"content":"Review","attachment_ids":[%q]}`, upload.Data.ID))
			if provider.CallCount() != tt.wantCalls {
				t.Fatalf("calls=%d want=%d", provider.CallCount(), tt.wantCalls)
			}
			if tt.wantCalls == 0 {
				if response.Data.Status != "failed" || len(response.Data.Messages) != 2 {
					t.Fatalf("missing settled failed turn: %+v", response.Data)
				}
				if !strings.Contains(response.Data.Messages[1].Error, "turn Tools on") {
					t.Fatalf("missing repair guidance: %+v", response.Data.Messages[1])
				}
				stored, _, _ := h.agentChat.Get(t.Context(), session.Data.ID)
				for _, message := range stored.Messages {
					if message.Role == "user" && message.ProviderInstance.Valid() {
						t.Fatal("blocked dispatch recorded disclosure")
					}
				}
			} else if response.Data.Status != "completed" {
				t.Fatalf("larger rewritten model failed: %+v", response.Data)
			}
		})
	}
}
