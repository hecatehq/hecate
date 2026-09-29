package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatapp"
	"github.com/hecatehq/hecate/internal/chatattachments"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestNativeTextAttachmentUploadAndInertDownload(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	const body = "<html><script>private_file_body()</script></html>"
	upload := fixture.upload(t, "chat_images", "../notes.html", "text/html", []byte(body))
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", upload.Code, upload.Body.String())
	}
	attachment := decodeChatAttachmentResponse(t, upload).Data
	if attachment.MediaType != "text/plain" || attachment.Filename != "notes.html" || strings.Contains(upload.Body.String(), "private_file_body") {
		t.Fatalf("unsafe metadata: %+v", attachment)
	}
	content := fixture.request(http.MethodGet, attachment.ContentURL, nil, "", chatAttachmentTestRuntimeToken)
	if content.Code != http.StatusOK || content.Body.String() != body || content.Header().Get("Content-Type") != "text/plain" || !strings.HasPrefix(content.Header().Get("Content-Disposition"), "attachment;") || content.Header().Get("X-Content-Type-Options") != "nosniff" || content.Header().Get("Cache-Control") != "private, no-store" || content.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" {
		t.Fatalf("unsafe download: status=%d headers=%v", content.Code, content.Header())
	}
}

func TestNativeTextAttachmentStorageLifecycleParity(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			fixture := newChatAttachmentHTTPFixture(t)
			if backend == "sqlite" {
				client, err := storage.NewSQLiteClient(t.Context(), storage.SQLiteConfig{
					Path: filepath.Join(t.TempDir(), "attachments.db"), TablePrefix: "text_test",
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.Close() })
				store, err := chatattachments.NewSQLiteStore(t.Context(), client)
				if err != nil {
					t.Fatal(err)
				}
				fixture.handler.chatAttachments = store
			}
			const body = "  private_lifecycle_body: 世界\t\r\n"
			upload := fixture.upload(t, "chat_images", "notes.txt", "text/plain", []byte(body))
			if upload.Code != http.StatusCreated {
				t.Fatalf("upload: %d %s", upload.Code, upload.Body.String())
			}
			attachment := decodeChatAttachmentResponse(t, upload).Data
			app := fixture.handler.chatApplication()
			ref := chatattachments.ClaimRef{SessionID: "chat_images", MessageID: "first", AttachmentIDs: []string{attachment.ID}}
			claimed, err := app.ClaimAttachments(t.Context(), ref)
			if err != nil || len(claimed) != 1 || string(claimed[0].Data) != body || claimed[0].MediaType != "text/plain" {
				t.Fatalf("claim did not preserve canonical text: count=%d err=%v", len(claimed), err)
			}
			if _, err := app.GetAttachment(t.Context(), chatapp.AttachmentCommand{SessionID: "chat_other", AttachmentID: attachment.ID}); !errors.Is(err, chatapp.ErrAttachmentNotFound) {
				t.Fatalf("cross-session read: %v", err)
			}
			if err := app.DeleteAttachment(t.Context(), chatapp.AttachmentCommand{SessionID: ref.SessionID, AttachmentID: attachment.ID}); !errors.Is(err, chatapp.ErrAttachmentInUse) {
				t.Fatalf("claimed delete: %v", err)
			}
			if err := app.ResolveAttachmentClaim(t.Context(), ref, chatattachments.ClaimReleased); err != nil {
				t.Fatal(err)
			}
			ref.MessageID = "second"
			if _, err := app.ClaimAttachments(t.Context(), ref); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.handler.agentChat.AppendMessage(t.Context(), ref.SessionID, chat.Message{ID: ref.MessageID, Role: "user", Content: "Inspect", Attachments: chatMessageAttachments(claimed)}); err != nil {
				t.Fatal(err)
			}
			if err := app.ResolveAttachmentClaim(t.Context(), ref, chatattachments.ClaimLinked); err != nil {
				t.Fatal(err)
			}
			pending, err := fixture.handler.chatAttachments.ListPendingClaims(t.Context())
			if err != nil || len(pending) != 0 {
				t.Fatalf("claim was not finalized: pending=%d err=%v", len(pending), err)
			}
			content := fixture.request(http.MethodGet, attachment.ContentURL, nil, "", chatAttachmentTestRuntimeToken)
			if content.Code != http.StatusOK || content.Body.String() != body {
				t.Fatalf("linked download: %d", content.Code)
			}
			if err := app.DeleteSession(t.Context(), chatapp.DeleteSessionCommand{SessionID: ref.SessionID}); err != nil {
				t.Fatal(err)
			}
			if _, found, err := fixture.handler.chatAttachments.Get(t.Context(), ref.SessionID, attachment.ID); err != nil || found {
				t.Fatalf("session cleanup left text body: found=%t err=%v", found, err)
			}
		})
	}
}

func TestNativeTextAttachmentInvalidUploadsDoNotPersist(t *testing.T) {
	for _, test := range []struct {
		name   string
		data   []byte
		status int
	}{
		{"invalid UTF8", []byte{0xff, 0xfe}, http.StatusUnprocessableEntity},
		{"binary", []byte("before\x00after"), http.StatusUnprocessableEntity},
		{"oversized", []byte(strings.Repeat("x", chatapp.MaxNativeTextAttachmentBytes+1)), http.StatusRequestEntityTooLarge},
		{"disguised PDF", []byte("%PDF-1.7\n"), http.StatusUnprocessableEntity},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newChatAttachmentHTTPFixture(t)
			response := fixture.upload(t, "chat_images", "notes.txt", "text/plain", test.data)
			if response.Code != test.status {
				t.Fatalf("invalid upload: %d %s", response.Code, response.Body.String())
			}
			assertNoStoredChatAttachments(t, fixture.handler.chatAttachments, "chat_images")
		})
	}
}

func TestNativeTextAttachmentBusyAdmissionDoesNotHydrateOrCommit(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputNone)
	h := imageTurnTestHandler(provider)
	spy := &imageTurnAttachmentStoreSpy{Store: h.chatAttachments}
	h.chatAttachments = spy
	handler := NewServer(imageTurnTestLogger(), h)
	client := newTaskTestClient(t, handler)
	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", `{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "notes.txt", []byte("private data"))
	for range maxConcurrentChatImageTurns {
		if !h.chatImageTurnAdmission.TryAcquire() {
			t.Fatal("failed to saturate attachment admission")
		}
		defer h.chatImageTurnAdmission.Release()
	}
	client.mustRequestStatus(http.StatusTooManyRequests, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages", fmt.Sprintf(`{"tools_enabled":false,"content":"Inspect","attachment_ids":[%q]}`, attachment.Data.ID))
	if spy.claims.Load() != 0 || spy.gets.Load() != 0 || provider.CallCount() != 0 {
		t.Fatal("busy attachment was hydrated or disclosed")
	}
	persisted, _, _ := h.agentChat.Get(t.Context(), session.Data.ID)
	if len(persisted.Messages) != 0 {
		t.Fatal("busy attachment mutated transcript")
	}
}

func TestNativeTextAttachmentTurnsKeepBodiesTransientAndFenceDisclosure(t *testing.T) {
	for _, tools := range []bool{false, true} {
		for _, mixed := range []bool{false, true} {
			t.Run(fmt.Sprintf("tools=%t/mixed=%t", tools, mixed), func(t *testing.T) {
				capability := modelcaps.ImageInputNone
				if mixed {
					capability = modelcaps.ImageInputSupported
				}
				provider := imageTurnTestProvider(capability)
				caps := provider.capabilities.ModelCapabilities["llama-vision"]
				caps.ToolCalling = modelcaps.ToolCallingParallel
				provider.capabilities.ModelCapabilities["llama-vision"] = caps
				apiHandler := imageTurnTestHandler(provider)
				handler := NewServer(imageTurnTestLogger(), apiHandler)
				client := newTaskTestClient(t, handler)
				session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"hecate","workspace":%q,"provider":"ollama","model":"llama-vision"}`, t.TempDir()))
				const private = "private_text_body_73: package example\n"
				text := imageTurnTestUpload(t, handler, session.Data.ID, "example.go", []byte(private))
				ids := []string{text.Data.ID}
				if mixed {
					ids = append(ids, imageTurnTestUpload(t, handler, session.Data.ID, "pixel.png", imageTurnTestPNG(t)).Data.ID)
				}
				encodedIDs, _ := json.Marshal(ids)
				response := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages", fmt.Sprintf(`{"tools_enabled":%t,"content":"Inspect attached input","attachment_ids":%s}`, tools, encodedIDs))
				if response.Data.Status != "completed" {
					t.Fatalf("turn failed: %+v", response.Data)
				}
				request := provider.LastRequest()
				if request.Requirements.ImageInput != mixed || !request.Requirements.NoProviderFailover || !request.Requirements.ExactProvider || !request.Requirements.ProviderInstance.Valid() {
					t.Fatalf("incorrect attachment requirements: %+v", request.Requirements)
				}
				message := imageTurnFindUserMessage(t, request.Messages, "Inspect attached input")
				if tools {
					encoded, _ := json.Marshal(message)
					if strings.Contains(string(encoded), private) || !strings.Contains(string(encoded), "example.go") || !strings.Contains(string(encoded), text.Data.ID) {
						t.Fatalf("tools input must contain scoped metadata only: %+v", message)
					}
				} else if len(message.ContentBlocks) < 2 || !message.ContentBlocks[1].AttachmentInput || !strings.Contains(message.ContentBlocks[1].Text, private) || !strings.Contains(message.ContentBlocks[1].Text, "example.go") || strings.Contains(message.Content, private) {
					t.Fatalf("attachment provenance not preserved: %+v", message)
				}
				serialized, _ := json.Marshal(response)
				if strings.Contains(string(serialized), private) {
					t.Fatal("transcript response retained attachment bytes")
				}
				if tools {
					run, ok, err := apiHandler.taskStore.GetRun(t.Context(), response.Data.TaskID, response.Data.LatestRunID)
					if err != nil || !ok || run.InputRef == "" || !run.InputProviderDispatchRecorded {
						t.Fatalf("missing input/disclosure fence: %+v %v", run, err)
					}
					artifacts, err := apiHandler.taskStore.ListArtifacts(t.Context(), taskstate.ArtifactFilter{TaskID: response.Data.TaskID, RunID: response.Data.LatestRunID})
					if err != nil {
						t.Fatal(err)
					}
					for _, artifact := range artifacts {
						if strings.Contains(artifact.ContentText, "private_text_body_73") {
							t.Fatal("task artifact retained private text")
						}
					}
					continued := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages", `{"tools_enabled":true,"content":"Continue without a new file"}`)
					if continued.Data.Status != "completed" || continued.Data.TaskID != response.Data.TaskID {
						t.Fatalf("continuation failed: %+v", continued.Data)
					}
					requestJSON, err := json.Marshal(provider.LastRequest())
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(requestJSON), "private_text_body_73") {
						t.Fatal("continued task rehydrated previous text")
					}
					for _, tool := range provider.LastRequest().Tools {
						if tool.Function.Name == "read_attachment" || tool.Function.Name == "search_attachment" {
							t.Fatal("continuation advertised access to previous input attachments")
						}
					}
				}
			})
		}
	}
}

func TestNativeTextAttachmentInlineModelBudget(t *testing.T) {
	for _, tt := range []struct {
		name                 string
		filename             string
		window, size, status int
	}{
		{"large model accepts source over old limits", "source.go", 200000, 100 << 10, http.StatusOK},
		{"small model rejects before disclosure", "source.go", 8192, 20 << 10, http.StatusRequestEntityTooLarge},
		{"unknown context uses conservative fallback", "source.go", 0, 100 << 10, http.StatusRequestEntityTooLarge},
		{"escaped filename framing rejects before commit", strings.Repeat("\"", 128), 8192, 450, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tt.name, func(t *testing.T) {
			provider := imageTurnTestProvider(modelcaps.ImageInputNone)
			caps := provider.capabilities.ModelCapabilities["llama-vision"]
			caps.MaxContextTokens = tt.window
			provider.capabilities.ModelCapabilities["llama-vision"] = caps
			h := imageTurnTestHandler(provider)
			handler := NewServer(imageTurnTestLogger(), h)
			client := newTaskTestClient(t, handler)
			session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", `{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
			body := strings.Repeat("a", tt.size)
			upload := imageTurnTestUpload(t, handler, session.Data.ID, tt.filename, []byte(body))
			client.mustRequestStatus(tt.status, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages", fmt.Sprintf(`{"tools_enabled":false,"content":"Review source","attachment_ids":[%q]}`, upload.Data.ID))
			if tt.status != http.StatusOK {
				if provider.CallCount() != 0 {
					t.Fatal("overflow disclosed attachment")
				}
				stored, _, _ := h.agentChat.Get(t.Context(), session.Data.ID)
				if len(stored.Messages) != 0 {
					t.Fatal("overflow committed transcript")
				}
				client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/chat/sessions/"+session.Data.ID+"/attachments/"+upload.Data.ID, "")
			} else {
				message := imageTurnFindUserMessage(t, provider.LastRequest().Messages, "Review source")
				if len(message.ContentBlocks) < 2 || !strings.Contains(message.ContentBlocks[1].Text, body) {
					t.Fatal("large text was silently truncated")
				}
			}
		})
	}
}

func TestNativeChatInlineTextBudgetAccountsForConversation(t *testing.T) {
	caps := types.ModelCapabilities{MaxContextTokens: 200000}
	before := nativeChatInlineTextBudget(caps, []types.Message{{Role: "user", Content: "hello"}}, 0)
	after := nativeChatInlineTextBudget(caps, []types.Message{{Role: "user", Content: strings.Repeat("a", 10005)}}, 1)
	if before-after != 10000+8192 {
		t.Fatalf("ordinary history/image budget mismatch: %d %d", before, after)
	}
}

func TestNativeTextAttachmentCombinedLimitReleasesClaims(t *testing.T) {
	for _, tools := range []bool{false, true} {
		t.Run(fmt.Sprintf("tools=%t", tools), func(t *testing.T) {
			provider := imageTurnTestProvider(modelcaps.ImageInputNone)
			caps := provider.capabilities.ModelCapabilities["llama-vision"]
			caps.ToolCalling = modelcaps.ToolCallingParallel
			provider.capabilities.ModelCapabilities["llama-vision"] = caps
			apiHandler := imageTurnTestHandler(provider)
			handler := NewServer(imageTurnTestLogger(), apiHandler)
			client := newTaskTestClient(t, handler)
			session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"hecate","workspace":%q,"provider":"ollama","model":"llama-vision"}`, t.TempDir()))
			var ids []string
			for range 3 {
				ids = append(ids, imageTurnTestUpload(t, handler, session.Data.ID, "large.txt", []byte(strings.Repeat("a", 5<<20))).Data.ID)
			}
			encoded, _ := json.Marshal(ids)
			client.mustRequestStatus(http.StatusRequestEntityTooLarge, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages", fmt.Sprintf(`{"tools_enabled":%t,"content":"Inspect","attachment_ids":%s}`, tools, encoded))
			if provider.CallCount() != 0 {
				t.Fatal("oversized text was disclosed")
			}
			persisted, _, _ := apiHandler.agentChat.Get(t.Context(), session.Data.ID)
			if len(persisted.Messages) != 0 {
				t.Fatal("oversized text mutated transcript")
			}
			for _, id := range ids {
				client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/chat/sessions/"+session.Data.ID+"/attachments/"+id, "")
			}
		})
	}
}

func TestNativeTextAttachmentHistoryUsesSharedBudgetAndProviderFence(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputNone)
	h := imageTurnTestHandler(provider)
	handler := NewServer(imageTurnTestLogger(), h)
	client := newTaskTestClient(t, handler)
	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", `{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	route, err := h.modelApplication().ResolveProviderRoute(t.Context(), "ollama", "llama-vision")
	if err != nil {
		t.Fatal(err)
	}
	var current []chatattachments.StoredAttachment
	for i := range 3 {
		upload := imageTurnTestUpload(t, handler, session.Data.ID, "notes.txt", []byte(strings.Repeat(string(rune('a'+i)), 30<<10)))
		stored, _, err := h.chatAttachments.Get(t.Context(), session.Data.ID, upload.Data.ID)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			_, err = h.agentChat.AppendMessage(t.Context(), session.Data.ID, chat.Message{ID: "msg_history", Role: "user", Content: "Earlier text", Provider: "ollama", ProviderInstance: route.Instance, Attachments: chatMessageAttachments([]chatattachments.StoredAttachment{stored})})
			if err != nil {
				t.Fatal(err)
			}
		} else {
			current = append(current, stored)
		}
	}
	storedSession, _, _ := h.agentChat.Get(t.Context(), session.Data.ID)
	for _, test := range []struct {
		name, provider string
		current        []chatattachments.StoredAttachment
		wantReason     string
		wantBodies     bool
	}{
		{"same provider", "ollama", nil, "", true},
		{"budget", "ollama", current, "inline text context budget", false},
		{"other provider", "other", nil, "active provider differs", false},
		{"Auto", "", nil, "active provider differs", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			history, images, err := h.agentChatModelHistoryWithAttachments(context.Background(), storedSession, "", "Now", test.current, false, test.provider, route.Instance)
			if err != nil || images {
				t.Fatalf("history images=%t err=%v", images, err)
			}
			if chatMessagesHaveAttachmentBodies(history[:1]) != test.wantBodies {
				t.Fatalf("historical disclosure mismatch: %+v", history[0])
			}
			if test.wantReason != "" && !strings.Contains(history[0].Content, test.wantReason) {
				t.Fatalf("missing omission reason: %q", history[0].Content)
			}
		})
	}
}

func TestNativeTextAttachmentHistoryAccountsForHistoricalImages(t *testing.T) {
	for _, tt := range []struct {
		name                                 string
		budget, currentBytes                 int
		historicalText, staleImageGeneration bool
		otherImageProvider                   bool
		wantImage, wantHistoricalText        bool
		wantReason                           string
	}{
		{name: "current text wins", budget: 6000, currentBytes: 5000, wantReason: "inline attachment context budget"},
		{name: "images charged before newer historical text", budget: 9000, historicalText: true, wantImage: true, wantReason: "inline text context budget"},
		{name: "historical image and text fit", budget: 11000, historicalText: true, wantImage: true, wantHistoricalText: true},
		{name: "image only keeps existing envelope", budget: 0, wantImage: true},
		{name: "other provider image does not consume context", budget: 3000, historicalText: true, otherImageProvider: true, wantHistoricalText: true, wantReason: "active provider differs"},
		{name: "stale generation image does not consume context", budget: 3000, historicalText: true, staleImageGeneration: true, wantHistoricalText: true, wantReason: "active provider differs"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
			h := imageTurnTestHandler(provider)
			handler := NewServer(imageTurnTestLogger(), h)
			client := newTaskTestClient(t, handler)
			created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", `{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
			route, err := h.modelApplication().ResolveProviderRoute(t.Context(), "ollama", "llama-vision")
			if err != nil {
				t.Fatal(err)
			}
			upload := func(filename string, data []byte) chatattachments.StoredAttachment {
				t.Helper()
				item := imageTurnTestUpload(t, handler, created.Data.ID, filename, data)
				stored, ok, err := h.chatAttachments.Get(t.Context(), created.Data.ID, item.Data.ID)
				if err != nil || !ok {
					t.Fatalf("load attachment: found=%t err=%v", ok, err)
				}
				return stored
			}
			image := upload("earlier.png", imageTurnTestPNG(t))
			imageMessage := chat.Message{ID: "earlier-image", Role: "user", Content: "Earlier image", Provider: "ollama", ProviderInstance: route.Instance, Attachments: chatMessageAttachments([]chatattachments.StoredAttachment{image})}
			if tt.otherImageProvider {
				imageMessage.Provider = "another-provider"
			}
			if tt.staleImageGeneration {
				imageMessage.ProviderInstance.ID += "-stale"
			}
			session := chat.Session{ID: created.Data.ID, Messages: []chat.Message{imageMessage}}
			historicalBody := strings.Repeat("h", 2000)
			if tt.historicalText {
				text := upload("earlier.txt", []byte(historicalBody))
				// Newer text would be selected first by a single reverse-history
				// pass; image context must nevertheless be reserved before it.
				session.Messages = append(session.Messages, chat.Message{ID: "later-text", Role: "user", Content: "Earlier text", Provider: "ollama", ProviderInstance: route.Instance, Attachments: chatMessageAttachments([]chatattachments.StoredAttachment{text})})
			}
			var current []chatattachments.StoredAttachment
			currentBody := strings.Repeat("c", tt.currentBytes)
			if tt.currentBytes > 0 {
				current = []chatattachments.StoredAttachment{upload("current.txt", []byte(currentBody))}
			}
			history, images, err := h.agentChatModelHistoryWithAttachments(t.Context(), session, "", "Now", current, true, "ollama", route.Instance, tt.budget)
			if err != nil || images != tt.wantImage {
				t.Fatalf("history images=%t want=%t err=%v", images, tt.wantImage, err)
			}
			imageCount := 0
			historicalTextFound, currentTextFound := false, false
			for _, message := range history {
				for _, block := range message.ContentBlocks {
					if block.Image != nil {
						imageCount++
					}
					if block.AttachmentInput {
						historicalTextFound = historicalTextFound || strings.Contains(block.Text, historicalBody)
						currentTextFound = currentTextFound || (currentBody != "" && strings.Contains(block.Text, currentBody))
					}
				}
			}
			if (imageCount > 0) != tt.wantImage || historicalTextFound != tt.wantHistoricalText || currentTextFound != (tt.currentBytes > 0) {
				t.Fatalf("selected bodies: images=%d historical=%t current=%t", imageCount, historicalTextFound, currentTextFound)
			}
			if tt.wantReason != "" {
				reasons := ""
				for _, message := range history {
					reasons += message.Content
				}
				if !strings.Contains(reasons, tt.wantReason) {
					t.Fatalf("missing explicit omission %q: %s", tt.wantReason, reasons)
				}
			}
		})
	}
}
