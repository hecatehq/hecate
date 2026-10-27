package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestWorkspaceSkillInput_ProviderErrorsDoNotEchoInstructions(t *testing.T) {
	req := types.ChatRequest{Messages: []types.Message{{ContentBlocks: []types.ContentBlock{{Type: "text", Text: "private-skill-sentinel", WorkspaceSkillInput: true}}}}}
	err := attachmentSafeError(req, errors.New("upstream echoed private-skill-sentinel"))
	if err == nil || strings.Contains(err.Error(), "private-skill-sentinel") || errors.Unwrap(err) != nil {
		t.Fatal("private error was retained")
	}
}

func TestProviders_WorkspaceSkillInstructionsReachBothWireModes(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		for _, stream := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "/chat", true: "/stream"}[stream], func(t *testing.T) {
				var captured json.RawMessage
				transport := func(r *http.Request) (*http.Response, error) {
					if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
						t.Fatal(err)
					}
					if !stream {
						if protocol == "anthropic" {
							return anthropicTextResponse(t, "received"), nil
						}
						return openAITextResponse(t, "received"), nil
					}
					body := "data: [DONE]\n\n"
					if protocol == "anthropic" {
						body = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"claude-opus-4-5\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				}
				req := types.ChatRequest{Messages: []types.Message{{Role: "system", ContentBlocks: []types.ContentBlock{{Type: "text", Text: "private-skill-sentinel", WorkspaceSkillInput: true}}}, {Role: "user", Content: "review"}}}
				var err error
				if protocol == "anthropic" {
					provider := newAnthropicTestProvider(t, transport)
					req.Model = "claude-opus-4-5"
					if stream {
						err = provider.ChatStream(context.Background(), req, io.Discard)
					} else {
						_, err = provider.Chat(context.Background(), req)
					}
				} else {
					provider := newOpenAITestProvider(t, transport)
					req.Model = "gpt-4o-mini"
					if stream {
						err = provider.ChatStream(context.Background(), req, io.Discard)
					} else {
						_, err = provider.Chat(context.Background(), req)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(captured), "private-skill-sentinel") || strings.Contains(string(captured), "WorkspaceSkillInput") {
					t.Fatal("private instructions missing from wire or internal marker leaked")
				}
			})
		}
	}
}
