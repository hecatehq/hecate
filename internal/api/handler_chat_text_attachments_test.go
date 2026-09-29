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
				if len(message.ContentBlocks) < 2 || !message.ContentBlocks[1].AttachmentInput || !strings.Contains(message.ContentBlocks[1].Text, private) || !strings.Contains(message.ContentBlocks[1].Text, "example.go") || strings.Contains(message.Content, private) {
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
					if strings.Contains(string(requestJSON), "private_text_body_73") || !strings.Contains(string(requestJSON), "Text attachment supplied as Task input; body not retained in Task artifacts") {
						t.Fatal("continued task rehydrated previous text instead of retaining its explicit omission")
					}
				}
			})
		}
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
				ids = append(ids, imageTurnTestUpload(t, handler, session.Data.ID, "large.txt", []byte(strings.Repeat("a", chatapp.MaxNativeTextAttachmentBytes))).Data.ID)
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
		upload := imageTurnTestUpload(t, handler, session.Data.ID, "notes.txt", []byte(strings.Repeat(string(rune('a'+i)), chatapp.MaxNativeTextAttachmentBytes)))
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
		{"budget", "ollama", current, "64 KiB text-attachment context limit", false},
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
