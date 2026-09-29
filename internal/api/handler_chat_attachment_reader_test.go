package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatapp"
	"github.com/hecatehq/hecate/internal/chatattachments"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

type nativeReaderTestStore struct {
	chatattachments.Store
	gets    atomic.Int64
	tamper  bool
	fail    bool
	entered chan struct{}
	resume  chan struct{}
}

func (store *nativeReaderTestStore) Get(ctx context.Context, sessionID, id string) (chatattachments.StoredAttachment, bool, error) {
	store.gets.Add(1)
	if store.entered != nil {
		store.entered <- struct{}{}
		<-store.resume
	}
	if store.fail {
		return chatattachments.StoredAttachment{}, false, errors.New("private storage detail must not escape")
	}
	attachment, found, err := store.Store.Get(ctx, sessionID, id)
	if store.tamper && len(attachment.Data) > 0 {
		attachment.Data[0] ^= 1
	}
	return attachment, found, err
}

func TestChatAgentAttachmentReader_MetadataOnlyAndBoundedReadSearch(t *testing.T) {
	body := strings.Repeat("<é> source\n", 10000)
	h, input, store, _, _ := nativeReaderTestInput(t, t.Context(), body, false)
	if store.gets.Load() != 0 || input.AttachmentReader == nil || input.Requirements.ImageInput || !input.Requirements.NoProviderFailover {
		t.Fatalf("initial input hydrated text or lost route fence: reads=%d, input=%+v", store.gets.Load(), input.Requirements)
	}
	encoded, err := json.Marshal(input.Message)
	if err != nil || strings.Contains(string(encoded), body) || !strings.Contains(string(encoded), `\"attachment_ref\":\"attachment\"`) || !strings.Contains(string(encoded), "read_attachment") {
		t.Fatalf("initial metadata=%s, %v", encoded, err)
	}
	for _, block := range input.Message.ContentBlocks {
		if block.AttachmentInput {
			t.Fatal("body-free manifest must remain visible in conversation checkpoints")
		}
	}
	// Fill the other slots: the reader must reuse its execution's existing slot.
	for range maxConcurrentChatImageTurns - 1 {
		if !h.chatImageTurnAdmission.TryAcquire() {
			t.Fatal("expected free admission slot")
		}
		defer h.chatImageTurnAdmission.Release()
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	page, err := input.AttachmentReader.Read(ctx, orchestrator.AgentAttachmentReadRequest{AttachmentRef: "attachment", MaxBytes: 32 << 10})
	if err != nil || len(page.Text) > 32<<10 || len(page.Text) == 0 || page.NextOffset != int64(len(page.Text)) || page.EOF {
		t.Fatalf("page bytes=%d, next=%d, EOF=%v, err=%v", len(page.Text), page.NextOffset, page.EOF, err)
	}
	search, err := input.AttachmentReader.Search(ctx, orchestrator.AgentAttachmentSearchRequest{AttachmentRef: "attachment", Query: "source", MaxMatches: 20})
	if err != nil || len(search.Matches) == 0 || len(search.Matches) > 20 || search.EOF || search.NextOffset <= 0 {
		t.Fatalf("search=%+v, %v", search, err)
	}
	encoded, err = json.Marshal(search)
	if err != nil || len(encoded) > orchestrator.AgentAttachmentMaxSearchBytes {
		t.Fatalf("search encoded bytes=%d, %v", len(encoded), err)
	}
	if store.gets.Load() != 2 {
		t.Fatalf("reads=%d, want one body read per operation", store.gets.Load())
	}
}

func TestChatAgentAttachmentReader_RejectsUnrelatedRefsAndInvalidBoundsBeforeBodyLoad(t *testing.T) {
	_, input, store, _, _ := nativeReaderTestInput(t, t.Context(), "private body\n", false)
	for _, ref := range []string{"", "another-session-file", "prior-message-file", "../attachment"} {
		if _, err := input.AttachmentReader.Read(t.Context(), orchestrator.AgentAttachmentReadRequest{AttachmentRef: ref}); !errors.Is(err, chatapp.ErrNativeAttachmentUnavailable) {
			t.Fatalf("ref %q read=%v", ref, err)
		}
	}
	for _, request := range []orchestrator.AgentAttachmentReadRequest{{AttachmentRef: "attachment", Offset: -1}, {AttachmentRef: "attachment", MaxBytes: -1}, {AttachmentRef: "attachment", MaxBytes: (32 << 10) + 1}} {
		if _, err := input.AttachmentReader.Read(t.Context(), request); !errors.Is(err, chatapp.ErrNativeAttachmentReadBounds) {
			t.Fatalf("bounds read=%v", err)
		}
	}
	for _, request := range []orchestrator.AgentAttachmentSearchRequest{{AttachmentRef: "attachment"}, {AttachmentRef: "attachment", Query: strings.Repeat("x", 257)}, {AttachmentRef: "attachment", Query: "x", MaxMatches: 21}, {AttachmentRef: "attachment", Query: "x", Offset: -1}} {
		if _, err := input.AttachmentReader.Search(t.Context(), request); !errors.Is(err, chatapp.ErrNativeAttachmentSearch) {
			t.Fatalf("bounds search=%v", err)
		}
	}
	if store.gets.Load() != 0 {
		t.Fatalf("invalid requests loaded %d bodies", store.gets.Load())
	}
}

func TestChatAgentAttachmentReader_RevalidatesInputTranscript(t *testing.T) {
	for name, mutate := range map[string]func(*chat.Message){
		"task":       func(message *chat.Message) { message.TaskID = "another-task" },
		"provider":   func(message *chat.Message) { message.Provider = "another-provider" },
		"generation": func(message *chat.Message) { message.ProviderInstance.ID = "another-generation" },
		"role":       func(message *chat.Message) { message.Role = "assistant" },
		"tools":      func(message *chat.Message) { message.ToolsEnabled = false },
		"mode":       func(message *chat.Message) { message.ExecutionMode = chat.ExecutionModeExternalAgent },
		"metadata":   func(message *chat.Message) { message.Attachments[0].Filename = "replacement.txt" },
		"attachment": func(message *chat.Message) { message.Attachments = nil },
	} {
		t.Run(name, func(t *testing.T) {
			h, input, store, _, _ := nativeReaderTestInput(t, t.Context(), "private body\n", false)
			if _, err := h.agentChat.UpdateMessage(t.Context(), "session", "message", mutate); err != nil {
				t.Fatal(err)
			}
			if _, err := input.AttachmentReader.Read(t.Context(), orchestrator.AgentAttachmentReadRequest{AttachmentRef: "attachment"}); !errors.Is(err, chatapp.ErrNativeAttachmentUnavailable) {
				t.Fatalf("mutated input read=%v", err)
			}
			if store.gets.Load() != 0 {
				t.Fatalf("mutated input loaded %d bodies", store.gets.Load())
			}
		})
	}
}

func TestChatAgentAttachmentReader_FixedErrorsForTamperAndStoreFailure(t *testing.T) {
	for _, tamper := range []bool{true, false} {
		t.Run(map[bool]string{true: "digest", false: "store-error"}[tamper], func(t *testing.T) {
			_, input, store, _, _ := nativeReaderTestInput(t, t.Context(), "private body\n", false)
			store.tamper, store.fail = tamper, !tamper
			page, err := input.AttachmentReader.Read(t.Context(), orchestrator.AgentAttachmentReadRequest{AttachmentRef: "attachment"})
			if !errors.Is(err, chatapp.ErrNativeAttachmentUnavailable) || page.Text != "" || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe failure=%+v, %v", page, err)
			}
		})
	}
}

func TestChatAgentAttachmentReader_RejectsDeletedClosedAndReleasedOwners(t *testing.T) {
	for _, action := range []string{"delete", "close", "release"} {
		t.Run(action, func(t *testing.T) {
			h, input, store, _, _ := nativeReaderTestInput(t, t.Context(), "private body\n", false)
			switch action {
			case "delete":
				if err := h.agentChat.Delete(t.Context(), "session"); err != nil {
					t.Fatal(err)
				}
			case "close":
				closure := h.agentChatLive.closeSessionLifecycle("session")
				if !closure.waitForOperations(t.Context()) {
					t.Fatal("close did not drain")
				}
				closure.release()
			case "release":
				input.Release()
				input.Release()
			}
			if _, err := input.AttachmentReader.Read(t.Context(), orchestrator.AgentAttachmentReadRequest{AttachmentRef: "attachment"}); !errors.Is(err, chatapp.ErrNativeAttachmentUnavailable) {
				t.Fatalf("%s read=%v", action, err)
			}
			if store.gets.Load() != 0 {
				t.Fatalf("closed input loaded %d bodies", store.gets.Load())
			}
		})
	}
}

func TestChatAgentAttachmentReader_CancellationAfterBodyReadAndLifecycleDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	h, input, store, _, _ := nativeReaderTestInput(t, ctx, "private body\n", false)
	store.entered, store.resume = make(chan struct{}, 1), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		page, err := input.AttachmentReader.Read(t.Context(), orchestrator.AgentAttachmentReadRequest{AttachmentRef: "attachment"})
		if page.Text != "" {
			result <- errors.New("cancelled read returned private body")
			return
		}
		result <- err
	}()
	<-store.entered
	closure := h.agentChatLive.closeSessionLifecycle("session")
	defer closure.release()
	select {
	case <-closure.drained:
		t.Fatal("lifecycle closed through an admitted body read")
	default:
	}
	cancel()
	close(store.resume)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read=%v", err)
	}
	if !closure.waitForOperations(t.Context()) {
		t.Fatal("lifecycle did not drain after read")
	}
}

func TestChatAgentAttachmentReader_AutoRequiresRecordedDispatchAndFreshGeneration(t *testing.T) {
	h, input, store, run, registry := nativeReaderTestInput(t, t.Context(), "private body\n", true)
	request := orchestrator.AgentAttachmentReadRequest{AttachmentRef: "attachment"}
	if _, err := input.AttachmentReader.Read(t.Context(), request); !errors.Is(err, chatapp.ErrNativeAttachmentUnavailable) {
		t.Fatalf("unrecorded Auto read=%v", err)
	}
	if store.gets.Load() != 0 {
		t.Fatal("unrecorded Auto hydrated a body")
	}
	route, err := h.modelApplication().ResolveProviderRoute(t.Context(), "ollama", "llama-vision")
	if err != nil {
		t.Fatal(err)
	}
	run.Provider, run.Model, run.InputProviderInstance, run.InputProviderDispatchRecorded = route.Name, "llama-vision", route.Instance, true
	if _, err := h.taskStore.UpdateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	page, err := input.AttachmentReader.Read(t.Context(), request)
	if err != nil || page.Text != "private body\n" {
		t.Fatalf("recorded Auto read=%+v, %v", page, err)
	}
	registry.Replace(imageTurnTestProvider(modelcaps.ImageInputNone))
	if _, err := input.AttachmentReader.Read(t.Context(), request); !errors.Is(err, chatapp.ErrNativeAttachmentUnavailable) {
		t.Fatalf("replacement read=%v", err)
	}
	if store.gets.Load() != 1 {
		t.Fatalf("replacement performed another body read: %d", store.gets.Load())
	}
}

func TestChatAgentAttachmentReader_ContextBudgetUsesFinalGovernorModel(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputNone)
	large := provider.capabilities.ModelCapabilities["llama-vision"]
	large.MaxContextTokens = 1 << 20
	small := large
	small.MaxContextTokens = 8192
	provider.capabilities.ModelCapabilities["llama-vision"] = large
	provider.capabilities.ModelCapabilities["small-model"] = small
	provider.capabilities.Models = append(provider.capabilities.Models, "small-model")
	h, input, store, run, registry := nativeReaderTestInputWithProvider(t, t.Context(), "private body\n", false, provider)
	if input.AttachmentContextBytes != 64<<10 {
		t.Fatalf("initial budget=%d", input.AttachmentContextBytes)
	}
	// This is the model rewrite recorded at first dispatch, before any reader
	// has pinned the effective route. Provider name/generation stay unchanged.
	run.Model = "small-model"
	if _, err := h.taskStore.UpdateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	budget, err := input.AttachmentReader.ContextBudget(t.Context())
	if err != nil || budget != 2048 {
		t.Fatalf("final model budget=%d, %v", budget, err)
	}
	if store.gets.Load() != 0 {
		t.Fatal("budget lookup hydrated file bodies")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := input.AttachmentReader.ContextBudget(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled budget=%v", err)
	}
	registry.Replace(imageTurnTestProvider(modelcaps.ImageInputNone))
	if _, err := input.AttachmentReader.ContextBudget(t.Context()); !errors.Is(err, chatapp.ErrNativeAttachmentUnavailable) {
		t.Fatalf("replaced budget=%v", err)
	}
}

func nativeReaderTestInput(t *testing.T, ctx context.Context, body string, auto bool) (*Handler, orchestrator.AgentInput, *nativeReaderTestStore, types.TaskRun, *providers.MutableRegistry) {
	t.Helper()
	provider := imageTurnTestProvider(modelcaps.ImageInputNone)
	return nativeReaderTestInputWithProvider(t, ctx, body, auto, provider)
}

func nativeReaderTestInputWithProvider(t *testing.T, ctx context.Context, body string, auto bool, provider *fakeProvider) (*Handler, orchestrator.AgentInput, *nativeReaderTestStore, types.TaskRun, *providers.MutableRegistry) {
	t.Helper()
	caps := provider.capabilities.ModelCapabilities["llama-vision"]
	caps.ToolCalling = modelcaps.ToolCallingParallel
	provider.capabilities.ModelCapabilities["llama-vision"] = caps
	registry := providers.NewMutableRegistry(provider)
	h := newTestAPIHandlerWithRegistry(imageTurnTestLogger(), registry, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	store := &nativeReaderTestStore{Store: chatattachments.NewMemoryStore()}
	h.chatAttachments = store
	route, err := h.modelApplication().ResolveProviderRoute(t.Context(), "ollama", "llama-vision")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.agentChat.Create(t.Context(), chat.Session{ID: "session", AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(body))
	metadata := chat.MessageAttachment{ID: "attachment", Filename: "notes.txt", MediaType: "text/plain", SizeBytes: int64(len(body)), SHA256: hex.EncodeToString(digest[:]), CreatedAt: time.Now().UTC()}
	if _, err := store.Create(t.Context(), chatattachments.StoredAttachment{Attachment: chatattachments.Attachment{ID: metadata.ID, SessionID: "session", Filename: metadata.Filename, MediaType: metadata.MediaType, SizeBytes: metadata.SizeBytes, SHA256: metadata.SHA256, CreatedAt: metadata.CreatedAt}, Data: []byte(body)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(t.Context(), chatattachments.ClaimRef{SessionID: "session", MessageID: "message", AttachmentIDs: []string{metadata.ID}}); err != nil {
		t.Fatal(err)
	}
	message := chat.Message{ID: "message", Role: "user", Content: "inspect the file", TaskID: "task", ToolsEnabled: true, ExecutionMode: chat.ExecutionModeHecateTask, Attachments: []chat.MessageAttachment{metadata}, Provider: route.Name, ProviderInstance: route.Instance}
	if auto {
		message.Provider, message.ProviderInstance = "", types.ProviderInstanceIdentity{}
	}
	if _, err := h.agentChat.AppendMessage(t.Context(), "session", message); err != nil {
		t.Fatal(err)
	}
	task, err := h.taskStore.CreateTask(t.Context(), types.Task{ID: "task", OriginKind: "chat", OriginID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.taskStore.CreateRun(t.Context(), types.TaskRun{ID: "run", TaskID: task.ID, InputRef: message.ID, Provider: message.Provider, Model: "llama-vision", InputProviderInstance: message.ProviderInstance, InputProviderDispatchRecorded: !auto})
	if err != nil {
		t.Fatal(err)
	}
	input, err := h.resolveHecateAgentInput(ctx, task, run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(input.Release)
	return h, input, store, run, registry
}
