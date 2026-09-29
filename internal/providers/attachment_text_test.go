package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestProviders_TransientTextAttachmentsReachBothWireModes(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		for _, stream := range []bool{false, true} {
			name := protocol + "/chat"
			if stream {
				name += "-stream"
			}
			t.Run(name, func(t *testing.T) {
				var captured struct {
					Messages []struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
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
				input := types.Message{Role: "user", Content: "review the file", ContentBlocks: []types.ContentBlock{
					{Type: "text", Text: "review the file"},
					{Type: "text", Text: "private-file-body", AttachmentInput: true},
				}}
				req := types.ChatRequest{Messages: []types.Message{input}}
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
				if len(captured.Messages) != 1 || captured.Messages[0].Role != "user" {
					t.Fatalf("attachment must remain user content: %+v", captured.Messages)
				}
				wire := string(captured.Messages[0].Content)
				if !strings.Contains(wire, "review the file") || !strings.Contains(wire, "private-file-body") || strings.Contains(wire, "AttachmentInput") || strings.Contains(wire, "attachment_input") {
					t.Fatalf("unexpected wire content: %s", wire)
				}
				if input.Content != "review the file" || !input.ContentBlocks[1].AttachmentInput {
					t.Fatal("wire serialization mutated transient provenance")
				}
			})
		}
	}
}

func TestOpenAIWireContent_MixedTextAttachmentAndImage(t *testing.T) {
	message := types.Message{Role: "user", Content: "review", ContentBlocks: []types.ContentBlock{
		{Type: "text", Text: "review"},
		{Type: "text", Text: "file contents", AttachmentInput: true},
		{Type: "image_url", Image: &types.ContentImage{URL: "data:image/png;base64,cG5n"}},
	}}
	wire := buildOpenAIWireContent(message)
	if len(wire.Blocks) != 3 || wire.Blocks[1].Text != "file contents" || wire.Blocks[2].ImageURL == nil {
		t.Fatalf("mixed attachment wire = %+v", wire)
	}
}

func TestProviders_TextAttachmentErrorsWithholdEchoes(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		for _, mode := range []string{"http", "stream-http", "stream-event", "transport"} {
			t.Run(protocol+"/"+mode, func(t *testing.T) {
				const private = "private-file-body-echo"
				transport := func(*http.Request) (*http.Response, error) {
					if mode == "transport" {
						return nil, errors.New(private)
					}
					status := http.StatusTooManyRequests
					payload := `{"error":{"message":"` + private + `","type":"` + private + `"}}`
					if mode == "stream-event" {
						status = http.StatusOK
						if protocol == "anthropic" {
							payload = "event: error\ndata: " + payload + "\n\n"
						} else {
							payload = "data: " + payload + "\n\n"
						}
					}
					return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}, nil
				}
				req := types.ChatRequest{Messages: []types.Message{{Role: "user", ContentBlocks: []types.ContentBlock{{Type: "text", Text: private, AttachmentInput: true}}}}}
				var err error
				var output strings.Builder
				if protocol == "anthropic" {
					p := newAnthropicTestProvider(t, transport)
					req.Model = "claude-opus-4-5"
					if strings.HasPrefix(mode, "stream") {
						err = p.ChatStream(context.Background(), req, &output)
					} else {
						_, err = p.Chat(context.Background(), req)
					}
				} else {
					p := newOpenAITestProvider(t, transport)
					req.Model = "gpt-4o-mini"
					if strings.HasPrefix(mode, "stream") {
						err = p.ChatStream(context.Background(), req, &output)
					} else {
						_, err = p.Chat(context.Background(), req)
					}
				}
				if err == nil || !strings.Contains(err.Error(), "details withheld") || strings.Contains(fmt.Sprintf("%+v", err), private) || strings.Contains(output.String(), private) {
					t.Fatalf("unsafe attachment error: %v output=%s", err, output.String())
				}
				if errors.Unwrap(err) != nil {
					t.Fatal("unsafe cause retained")
				}
				if mode != "transport" {
					var upstream *UpstreamError
					if !errors.As(err, &upstream) {
						t.Fatalf("lost status classification: %T", err)
					}
					want := http.StatusTooManyRequests
					if mode == "stream-event" {
						want = http.StatusBadGateway
					}
					if upstream.StatusCode != want {
						t.Fatalf("status=%d want=%d", upstream.StatusCode, want)
					}
				}
			})
		}
	}
}

func TestAttachmentSafeError_PreservesCancellationAndOrdinaryErrors(t *testing.T) {
	req := types.ChatRequest{Messages: []types.Message{{ContentBlocks: []types.ContentBlock{{AttachmentInput: true}}}}}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		if got := attachmentSafeError(req, fmt.Errorf("private echo: %w", cause)); got != cause {
			t.Fatalf("cancellation=%v", got)
		}
	}
	ordinary := errors.New("ordinary diagnostic")
	if attachmentSafeError(types.ChatRequest{}, ordinary) != ordinary {
		t.Fatal("ordinary errors changed")
	}
}

func TestOpenAIWireContent_RestoredTextBlocksRetainOmissions(t *testing.T) {
	const omission = "[Text attachment supplied as Task input; body not retained in Task artifacts]"
	message := types.Message{Role: "user", Content: "review", ContentBlocks: []types.ContentBlock{{Type: "text", Text: "review"}, {Type: "text", Text: omission}}}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var restored types.Message
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	wire := buildOpenAIWireContent(restored)
	if wire.Text != "review\n\n"+omission || len(wire.Blocks) != 0 {
		t.Fatalf("restored wire=%+v", wire)
	}
}

func TestPrivateAttachmentToolResultsReachProviderWire(t *testing.T) {
	const private = "private-paged-tool-result"
	message := types.Message{Role: "tool", ToolCallID: "read-1", ContentBlocks: []types.ContentBlock{{Type: "text", Text: private, AttachmentInput: true}}}
	openAI := buildOpenAIWireContent(message)
	if openAI.Text != private {
		t.Fatalf("tool result was dropped: %+v", openAI)
	}
	anthropic := toolResultBlock(message)
	if anthropic.ToolUseID != "read-1" || !strings.Contains(string(anthropic.ResultContent), private) {
		t.Fatalf("tool result was dropped: %+v", anthropic)
	}
	if message.Content != "" || !message.ContentBlocks[0].AttachmentInput {
		t.Fatal("wire serialization mutated private source")
	}
	req := types.ChatRequest{Messages: []types.Message{message}}
	if err := attachmentSafeError(req, errors.New(private)); strings.Contains(err.Error(), private) {
		t.Fatal("tool-result error echo leaked")
	}
}
