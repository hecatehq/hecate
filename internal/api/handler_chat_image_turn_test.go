package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatapp"
	"github.com/hecatehq/hecate/internal/chatattachments"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type failImageUserAppendStore struct {
	chat.Store
}

func TestHecateAgentChatToolsOnHydratesImageWithoutPersistingBodyInTaskArtifacts(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
	caps := provider.capabilities.ModelCapabilities["llama-vision"]
	caps.ToolCalling = modelcaps.ToolCallingParallel
	provider.capabilities.ModelCapabilities["llama-vision"] = caps
	apiHandler := imageTurnTestHandler(provider)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		fmt.Sprintf(`{"agent_id":"hecate","workspace":%q,"workspace_mode":"in_place","provider":"ollama","model":"llama-vision"}`, workspace))
	imageBytes := imageTurnTestPNG(t)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "tools-on.png", imageBytes)
	response := mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":true,"provider":"ollama","model":"llama-vision","content":"Inspect this image with tools available.","attachment_ids":["`+attachment.Data.ID+`"]}`)

	if response.Data.Status != "completed" || response.Data.TaskID == "" || response.Data.LatestRunID == "" {
		t.Fatalf("tools-on image response = %+v, want completed task-backed turn", response.Data)
	}
	user := imageTurnFindResponseUserMessage(t, response.Data.Messages, "Inspect this image with tools available.")
	imageTurnAssertAttachmentMetadata(t, session.Data.ID, attachment.Data, user.Attachments)
	request := provider.LastRequest()
	if len(request.Tools) == 0 {
		t.Fatal("agent-loop request had no tools")
	}
	if !request.Requirements.ImageInput || !request.Requirements.NoProviderFailover || !request.Requirements.ExactProvider || !request.Requirements.ProviderInstance.Valid() {
		t.Fatalf("agent-loop image requirements = %+v, want exact instance-fenced image route", request.Requirements)
	}
	modelUser := imageTurnFindUserMessage(t, request.Messages, "Inspect this image with tools available.")
	if len(modelUser.ContentBlocks) != 2 || modelUser.ContentBlocks[1].Image == nil || !strings.HasPrefix(modelUser.ContentBlocks[1].Image.URL, "data:image/png;base64,") {
		t.Fatalf("agent-loop user blocks = %+v, want text plus hydrated PNG", modelUser.ContentBlocks)
	}

	run, found, err := apiHandler.taskStore.GetRun(t.Context(), response.Data.TaskID, response.Data.LatestRunID)
	if err != nil || !found {
		t.Fatalf("GetRun() = found %v, error %v", found, err)
	}
	if run.InputRef == "" {
		t.Fatal("task run did not retain the opaque rich-input reference")
	}
	artifacts, err := apiHandler.taskStore.ListArtifacts(t.Context(), taskstate.ArtifactFilter{
		TaskID: response.Data.TaskID,
		RunID:  response.Data.LatestRunID,
	})
	if err != nil {
		t.Fatalf("ListArtifacts() error = %v", err)
	}
	encodedBody := base64.StdEncoding.EncodeToString(imageBytes)
	foundConversation := false
	for _, artifact := range artifacts {
		if artifact.Kind != "agent_conversation" {
			continue
		}
		foundConversation = true
		if strings.Contains(artifact.ContentText, encodedBody) || strings.Contains(artifact.ContentText, "data:image/") {
			t.Fatalf("agent conversation retained private image body: %s", artifact.ContentText)
		}
		if !strings.Contains(artifact.ContentText, "binary body not retained in task artifacts") {
			t.Fatalf("agent conversation missing image omission marker: %s", artifact.ContentText)
		}
	}
	if !foundConversation {
		t.Fatal("agent conversation artifact not found")
	}
	for range maxConcurrentChatImageTurns {
		if !apiHandler.chatImageTurnAdmission.TryAcquire() {
			t.Fatal("image turn permit was not released after agent-loop execution")
		}
		defer apiHandler.chatImageTurnAdmission.Release()
	}
}

func (s failImageUserAppendStore) AppendMessage(ctx context.Context, sessionID string, message chat.Message) (chat.Session, error) {
	if message.Role == "user" && len(message.Attachments) > 0 {
		return chat.Session{}, errors.New("injected transcript append failure")
	}
	return s.Store.AppendMessage(ctx, sessionID, message)
}

type commitThenFailImageUserAppendStore struct {
	chat.Store
}

type failImageAssistantAppendStore struct {
	chat.Store
}

func (s failImageAssistantAppendStore) AppendMessage(ctx context.Context, sessionID string, message chat.Message) (chat.Session, error) {
	if message.Role == "assistant" && message.Status == "running" {
		return chat.Session{}, errors.New("injected assistant append failure")
	}
	return s.Store.AppendMessage(ctx, sessionID, message)
}

type replaceProviderRegistryOnRunningAssistantStore struct {
	chat.Store
	once    sync.Once
	replace func()
}

func (s *replaceProviderRegistryOnRunningAssistantStore) AppendMessage(ctx context.Context, sessionID string, message chat.Message) (chat.Session, error) {
	updated, err := s.Store.AppendMessage(ctx, sessionID, message)
	if err == nil && message.Role == "assistant" && message.Status == "running" {
		s.once.Do(s.replace)
	}
	return updated, err
}

type imageTurnAttachmentStoreSpy struct {
	chatattachments.Store
	claims atomic.Int64
	gets   atomic.Int64
}

type failFirstLinkedAttachmentResolutionsStore struct {
	chatattachments.Store
	remainingFailures atomic.Int64
	resolveCalls      atomic.Int64
}

type blockingChatDeleteRaceStore struct {
	chat.Store
	sessionID       string
	clientRequestID string
	claimStarted    chan struct{}
	allowClaim      chan struct{}
	deleteStarted   chan struct{}
	allowDelete     chan struct{}
	claimOnce       sync.Once
	deleteOnce      sync.Once
}

func (s *blockingChatDeleteRaceStore) ClaimMessageRequest(ctx context.Context, sessionID, clientRequestID string, fingerprint chat.MessageRequestFingerprint) (chat.MessageRequestClaim, error) {
	if sessionID == s.sessionID && clientRequestID == s.clientRequestID {
		s.claimOnce.Do(func() { close(s.claimStarted) })
		select {
		case <-s.allowClaim:
		case <-ctx.Done():
			return chat.MessageRequestClaim{}, ctx.Err()
		}
	}
	return s.Store.ClaimMessageRequest(ctx, sessionID, clientRequestID, fingerprint)
}

func (s *blockingChatDeleteRaceStore) Delete(ctx context.Context, sessionID string) error {
	if sessionID == s.sessionID {
		s.deleteOnce.Do(func() { close(s.deleteStarted) })
		select {
		case <-s.allowDelete:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.Store.Delete(ctx, sessionID)
}

func (s *imageTurnAttachmentStoreSpy) Claim(ctx context.Context, ref chatattachments.ClaimRef) ([]chatattachments.StoredAttachment, error) {
	s.claims.Add(1)
	return s.Store.Claim(ctx, ref)
}

func (s *imageTurnAttachmentStoreSpy) Get(ctx context.Context, sessionID, id string) (chatattachments.StoredAttachment, bool, error) {
	s.gets.Add(1)
	return s.Store.Get(ctx, sessionID, id)
}

func (s *failFirstLinkedAttachmentResolutionsStore) ResolveClaim(ctx context.Context, ref chatattachments.ClaimRef, resolution chatattachments.ClaimResolution) error {
	s.resolveCalls.Add(1)
	if resolution == chatattachments.ClaimLinked && s.remainingFailures.Add(-1) >= 0 {
		return errors.New("injected attachment claim finalize failure")
	}
	return s.Store.ResolveClaim(ctx, ref, resolution)
}

type observingImageTurnAdmission struct {
	delegate chatImageTurnAdmission
	held     atomic.Int64
}

func (g *observingImageTurnAdmission) TryAcquire() bool {
	if !g.delegate.TryAcquire() {
		return false
	}
	g.held.Add(1)
	return true
}

func (g *observingImageTurnAdmission) Acquire(ctx context.Context) bool {
	if !g.delegate.Acquire(ctx) {
		return false
	}
	g.held.Add(1)
	return true
}

func (g *observingImageTurnAdmission) Release() {
	g.held.Add(-1)
	g.delegate.Release()
}

type imageTurnAdmissionCheckingProvider struct {
	providers.Provider
	admission      *observingImageTurnAdmission
	heldDuringCall atomic.Bool
}

func (p *imageTurnAdmissionCheckingProvider) Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	p.heldDuringCall.Store(p.admission.held.Load() > 0)
	return p.Provider.Chat(ctx, req)
}

func (s commitThenFailImageUserAppendStore) AppendMessage(ctx context.Context, sessionID string, message chat.Message) (chat.Session, error) {
	updated, err := s.Store.AppendMessage(ctx, sessionID, message)
	if err != nil {
		return updated, err
	}
	if message.Role == "user" && len(message.Attachments) > 0 {
		return chat.Session{}, errors.New("injected ambiguous transcript commit result")
	}
	return updated, nil
}

func TestHecateChatDirectModelImageTurnForwardsCanonicalDataURI(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandler(provider)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	imageBytes := imageTurnTestPNG(t)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "pixel.png", imageBytes)

	response := mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Describe this pixel.","attachment_ids":["`+attachment.Data.ID+`"]}`)

	if provider.CallCount() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.CallCount())
	}
	request := provider.LastRequest()
	if !request.Requirements.ImageInput || !request.Requirements.NoProviderFailover || !request.Requirements.ExactProvider {
		t.Fatalf("image request requirements = %+v, want image admission and exact one-provider boundary", request.Requirements)
	}
	if !request.Requirements.ProviderInstance.Valid() {
		t.Fatalf("image request provider instance = %+v, want admission fence", request.Requirements.ProviderInstance)
	}
	imageMessage := imageTurnFindUserMessage(t, request.Messages, "Describe this pixel.")
	if len(imageMessage.ContentBlocks) != 2 {
		t.Fatalf("content blocks = %+v, want text plus image_url", imageMessage.ContentBlocks)
	}
	if imageMessage.ContentBlocks[0].Type != "text" || imageMessage.ContentBlocks[0].Text != "Describe this pixel." {
		t.Fatalf("text block = %+v", imageMessage.ContentBlocks[0])
	}
	wantDataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes)
	imageBlock := imageMessage.ContentBlocks[1]
	if imageBlock.Type != "image_url" || imageBlock.Image == nil {
		t.Fatalf("image block = %+v, want canonical image_url block", imageBlock)
	}
	if imageBlock.Image.URL != wantDataURI || imageBlock.Image.MediaType != "image/png" {
		t.Fatalf("image payload = url %q media_type %q, want PNG data URI", imageBlock.Image.URL, imageBlock.Image.MediaType)
	}

	user := imageTurnFindResponseUserMessage(t, response.Data.Messages, "Describe this pixel.")
	imageTurnAssertAttachmentMetadata(t, session.Data.ID, attachment.Data, user.Attachments)
}

func TestDeleteChatSessionFencesDelayedImageTurnBeforeAttachmentClaimOrProviderDispatch(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandler(provider)
	attachmentStore := &imageTurnAttachmentStoreSpy{Store: apiHandler.chatAttachments}
	apiHandler.chatAttachments = attachmentStore
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "delete-race.png", imageTurnTestPNG(t))
	const clientRequestID = "delete-race-image-turn"
	raceStore := &blockingChatDeleteRaceStore{
		Store:           apiHandler.agentChat,
		sessionID:       session.Data.ID,
		clientRequestID: clientRequestID,
		claimStarted:    make(chan struct{}),
		allowClaim:      make(chan struct{}),
		deleteStarted:   make(chan struct{}),
		allowDelete:     make(chan struct{}),
	}
	apiHandler.agentChat = raceStore
	defer func() {
		select {
		case <-raceStore.allowClaim:
		default:
			close(raceStore.allowClaim)
		}
		select {
		case <-raceStore.allowDelete:
		default:
			close(raceStore.allowDelete)
		}
	}()

	messageDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages", strings.NewReader(
			`{"client_request_id":"`+clientRequestID+`","execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Do not dispatch.","attachment_ids":["`+attachment.Data.ID+`"]}`,
		))
		request.RemoteAddr = "127.0.0.1:1234"
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		messageDone <- recorder
	}()

	select {
	case <-raceStore.claimStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("message did not reach the request-claim gate")
	}

	deleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodDelete, "/hecate/v1/chat/sessions/"+session.Data.ID, nil)
		request.RemoteAddr = "127.0.0.1:1234"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		deleteDone <- recorder
	}()

	select {
	case <-raceStore.deleteStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("delete did not reach the fenced transcript-delete gate")
	}
	close(raceStore.allowClaim)

	var messageRecorder *httptest.ResponseRecorder
	select {
	case messageRecorder = <-messageDone:
	case <-time.After(3 * time.Second):
		t.Fatal("message did not finish after request claim was released")
	}
	if messageRecorder.Code != http.StatusConflict {
		t.Fatalf("message status = %d, want %d, body=%s", messageRecorder.Code, http.StatusConflict, messageRecorder.Body.String())
	}
	var messageError struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(messageRecorder.Body.Bytes(), &messageError); err != nil {
		t.Fatalf("decode message error: %v", err)
	}
	if messageError.Error.Type != errCodeSessionStopping {
		t.Fatalf("message error type = %q, want %q", messageError.Error.Type, errCodeSessionStopping)
	}
	if provider.CallCount() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.CallCount())
	}
	if attachmentStore.claims.Load() != 0 || attachmentStore.gets.Load() != 0 {
		t.Fatalf("attachment store calls = claims %d, gets %d; want no claim or body read", attachmentStore.claims.Load(), attachmentStore.gets.Load())
	}

	close(raceStore.allowDelete)
	var deleteRecorder *httptest.ResponseRecorder
	select {
	case deleteRecorder = <-deleteDone:
	case <-time.After(3 * time.Second):
		t.Fatal("delete did not finish after transcript deletion was released")
	}
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d, body=%s", deleteRecorder.Code, http.StatusNoContent, deleteRecorder.Body.String())
	}
	if _, ok, err := apiHandler.agentChat.Get(context.Background(), session.Data.ID); err != nil || ok {
		t.Fatalf("deleted session Get() = found %v, error %v", ok, err)
	}
	if _, ok, err := apiHandler.chatAttachments.Get(context.Background(), session.Data.ID, attachment.Data.ID); err != nil || ok {
		t.Fatalf("deleted attachment Get() = found %v, error %v", ok, err)
	}
}

func TestHecateChatImageOnlyTurnPersistsMetadataWithoutImageBodyInSessionJSON(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandler(provider)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	imageBytes := imageTurnTestPNG(t)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "only-image.png", imageBytes)
	body := `{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"","attachment_ids":["` + attachment.Data.ID + `"]}`
	recorder := client.mustRequestStatus(http.StatusOK, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages", body)
	imageTurnAssertNoImageBody(t, recorder.Body.Bytes(), imageBytes)
	response := decodeRecorder[ChatSessionResponse](t, recorder)

	imageMessage := imageTurnFindUserMessage(t, provider.LastRequest().Messages, "")
	if imageMessage.Content != "" || len(imageMessage.ContentBlocks) != 1 {
		t.Fatalf("image-only upstream message = %+v, want one image block and no text", imageMessage)
	}
	if block := imageMessage.ContentBlocks[0]; block.Type != "image_url" || block.Image == nil {
		t.Fatalf("image-only block = %+v", block)
	}

	user := imageTurnFindResponseUserMessage(t, response.Data.Messages, "")
	imageTurnAssertAttachmentMetadata(t, session.Data.ID, attachment.Data, user.Attachments)
	getRecorder := client.mustRequest(http.MethodGet, "/hecate/v1/chat/sessions/"+session.Data.ID, "")
	imageTurnAssertNoImageBody(t, getRecorder.Body.Bytes(), imageBytes)

	persisted, ok, err := apiHandler.agentChat.Get(context.Background(), session.Data.ID)
	if err != nil || !ok {
		t.Fatalf("Get() = found %v, error %v", ok, err)
	}
	persistedUser := imageTurnFindStoredUserMessage(t, persisted.Messages, "")
	if len(persistedUser.Attachments) != 1 || persistedUser.Attachments[0].ID != attachment.Data.ID {
		t.Fatalf("persisted attachment metadata = %+v", persistedUser.Attachments)
	}
	storedJSON, err := json.Marshal(persisted)
	if err != nil {
		t.Fatalf("Marshal(persisted session) error = %v", err)
	}
	imageTurnAssertNoImageBody(t, storedJSON, imageBytes)
}

func TestHecateChatImageTurnSaturatedAdmissionRejectsBeforeClaimOrTranscriptMutation(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandler(provider)
	attachmentStore := &imageTurnAttachmentStoreSpy{Store: apiHandler.chatAttachments}
	apiHandler.chatAttachments = attachmentStore
	admission := newChatImageTurnAdmission(maxConcurrentChatImageTurns)
	apiHandler.chatImageTurnAdmission = admission
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	imageSession := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, imageSession.Data.ID, "busy.png", imageTurnTestPNG(t))
	for range maxConcurrentChatImageTurns {
		if !admission.TryAcquire() {
			t.Fatal("failed to hold chat image turn admission permit")
		}
		defer admission.Release()
	}

	recorder := client.mustRequestStatus(http.StatusTooManyRequests, http.MethodPost,
		"/hecate/v1/chat/sessions/"+imageSession.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Inspect","attachment_ids":["`+attachment.Data.ID+`"]}`)
	if got := recorder.Header().Get("Retry-After"); got != strconv.Itoa(chatImageTurnRetryAfter) {
		t.Fatalf("Retry-After = %q, want %d", got, chatImageTurnRetryAfter)
	}
	var busyResponse struct {
		Error struct {
			Type                    string `json:"type"`
			MaxConcurrentImageTurns int    `json:"max_concurrent_image_turns"`
			RetryAfterSeconds       int    `json:"retry_after_seconds"`
			UserMessage             string `json:"user_message"`
			OperatorAction          string `json:"operator_action"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &busyResponse); err != nil {
		t.Fatalf("decode image turn saturation response: %v", err)
	}
	if busyResponse.Error.Type != errCodeImageTurnBusy ||
		busyResponse.Error.MaxConcurrentImageTurns != maxConcurrentChatImageTurns ||
		busyResponse.Error.RetryAfterSeconds != chatImageTurnRetryAfter {
		t.Fatalf("image turn saturation response = %+v", busyResponse.Error)
	}
	if busyResponse.Error.UserMessage == "" || busyResponse.Error.OperatorAction == "" {
		t.Fatalf("image turn operator metadata = %+v", busyResponse.Error)
	}
	if got := attachmentStore.claims.Load(); got != 0 {
		t.Fatalf("attachment claims = %d, want 0 before saturated rejection", got)
	}
	if got := attachmentStore.gets.Load(); got != 0 {
		t.Fatalf("attachment gets = %d, want 0 before saturated rejection", got)
	}
	persisted, ok, err := apiHandler.agentChat.Get(context.Background(), imageSession.Data.ID)
	if err != nil || !ok {
		t.Fatalf("Get() = found %v, error %v", ok, err)
	}
	if len(persisted.Messages) != 0 {
		t.Fatalf("persisted messages = %+v, want no transcript mutation", persisted.Messages)
	}
	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete,
		"/hecate/v1/chat/sessions/"+imageSession.Data.ID+"/attachments/"+attachment.Data.ID, "")

	textSession := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+textSession.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Text only"}`)
	if provider.CallCount() != 1 {
		t.Fatalf("text-only provider calls = %d, want 1 while image gate is saturated", provider.CallCount())
	}
}

func TestHecateChatHistoricalImageTurnSaturatedAdmissionRejectsBeforeHydration(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandler(provider)
	attachmentStore := &imageTurnAttachmentStoreSpy{Store: apiHandler.chatAttachments}
	apiHandler.chatAttachments = attachmentStore
	admission := newChatImageTurnAdmission(maxConcurrentChatImageTurns)
	apiHandler.chatImageTurnAdmission = admission
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "history-busy.png", imageTurnTestPNG(t))
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Remember","attachment_ids":["`+attachment.Data.ID+`"]}`)
	if got := attachmentStore.gets.Load(); got != 0 {
		t.Fatalf("attachment gets after current-image turn = %d, want 0", got)
	}
	for range maxConcurrentChatImageTurns {
		if !admission.TryAcquire() {
			t.Fatal("failed to hold chat image turn admission permit")
		}
		defer admission.Release()
	}

	client.mustRequestStatus(http.StatusTooManyRequests, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Recall"}`)
	if got := attachmentStore.gets.Load(); got != 0 {
		t.Fatalf("attachment gets = %d, want 0 before saturated historical hydration", got)
	}
	if got := attachmentStore.claims.Load(); got != 1 {
		t.Fatalf("attachment claims = %d, want only the admitted first turn", got)
	}
	if provider.CallCount() != 1 {
		t.Fatalf("provider calls = %d, want only the admitted first turn", provider.CallCount())
	}
	persisted, ok, err := apiHandler.agentChat.Get(context.Background(), session.Data.ID)
	if err != nil || !ok {
		t.Fatalf("Get() = found %v, error %v", ok, err)
	}
	if len(persisted.Messages) != 2 {
		t.Fatalf("persisted messages = %+v, want no rejected follow-up mutation", persisted.Messages)
	}
}

func TestHecateChatImageTurnPermitSpansProviderCallAndReleases(t *testing.T) {
	admission := &observingImageTurnAdmission{delegate: newChatImageTurnAdmission(1)}
	provider := &imageTurnAdmissionCheckingProvider{
		Provider:  imageTurnTestProvider(modelcaps.ImageInputSupported),
		admission: admission,
	}
	apiHandler := imageTurnTestHandler(provider)
	apiHandler.chatImageTurnAdmission = admission
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "permit.png", imageTurnTestPNG(t))
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Inspect","attachment_ids":["`+attachment.Data.ID+`"]}`)

	if !provider.heldDuringCall.Load() {
		t.Fatal("image turn permit was not held during provider call")
	}
	if got := admission.held.Load(); got != 0 {
		t.Fatalf("held permits after provider call = %d, want 0", got)
	}
	if !admission.TryAcquire() {
		t.Fatal("image turn permit was not reusable after provider call")
	}
	admission.Release()
}

func TestHecateChatImageTurnReleasesClaimWhenTranscriptAppendFails(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandler(provider)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "retry.png", imageTurnTestPNG(t))
	apiHandler.SetAgentChatStore(failImageUserAppendStore{Store: apiHandler.agentChat})

	client.mustRequestStatus(http.StatusInternalServerError, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Inspect","attachment_ids":[" `+attachment.Data.ID+` "]}`)
	if provider.CallCount() != 0 {
		t.Fatalf("provider calls = %d, want no dispatch after append failure", provider.CallCount())
	}
	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/attachments/"+attachment.Data.ID, "")
}

func TestHecateChatImageTurnReconcilesClaimWhenTranscriptCommitResultIsAmbiguous(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandler(provider)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "committed.png", imageTurnTestPNG(t))
	apiHandler.SetAgentChatStore(commitThenFailImageUserAppendStore{Store: apiHandler.agentChat})

	client.mustRequestStatus(http.StatusInternalServerError, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Inspect","attachment_ids":["`+attachment.Data.ID+`"]}`)
	if provider.CallCount() != 0 {
		t.Fatalf("provider calls = %d, want no dispatch after ambiguous append result", provider.CallCount())
	}
	persisted, ok, err := apiHandler.agentChat.Get(context.Background(), session.Data.ID)
	if err != nil || !ok {
		t.Fatalf("Get session = ok %v, err %v", ok, err)
	}
	user := imageTurnFindStoredUserMessage(t, persisted.Messages, "Inspect")
	if len(user.Attachments) != 1 || user.Attachments[0].ID != attachment.Data.ID {
		t.Fatalf("committed user attachment metadata = %+v", user.Attachments)
	}
	if err := apiHandler.chatAttachments.DeleteDraft(context.Background(), session.Data.ID, attachment.Data.ID); !errors.Is(err, chatattachments.ErrNotDraft) {
		t.Fatalf("DeleteDraft committed attachment error = %v, want ErrNotDraft", err)
	}
}

func TestHecateChatImageTurnReplayRepairsCommittedPendingAttachmentClaim(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandler(provider)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "replay-finalize.png", imageTurnTestPNG(t))
	attachments := &failFirstLinkedAttachmentResolutionsStore{Store: apiHandler.chatAttachments}
	attachments.remainingFailures.Store(2)
	apiHandler.SetChatAttachmentStore(attachments)
	const clientRequestID = "image-finalize-replay"
	body := `{"client_request_id":"` + clientRequestID + `","execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Repair this committed image.","attachment_ids":["` + attachment.Data.ID + `"]}`

	first := client.mustRequestStatus(http.StatusInternalServerError, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages", body)
	if !strings.Contains(first.Body.String(), chatAttachmentFinalizeFailureMessage) {
		t.Fatalf("first response = %s, want fixed finalize failure", first.Body.String())
	}
	if provider.CallCount() != 0 {
		t.Fatalf("provider calls after failed finalize = %d, want zero", provider.CallCount())
	}
	pending, err := attachments.ListPendingClaims(context.Background())
	if err != nil || len(pending) != 1 || pending[0].Ref.SessionID != session.Data.ID || pending[0].Ref.AttachmentIDs[0] != attachment.Data.ID {
		t.Fatalf("pending claims after failed finalize = %+v, err=%v", pending, err)
	}

	replay := mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages", body)
	if replay.MessageRequest == nil || !replay.MessageRequest.Replay || replay.MessageRequest.CommittedMessageID == "" {
		t.Fatalf("replay metadata = %+v, want committed replay", replay.MessageRequest)
	}
	if provider.CallCount() != 0 {
		t.Fatalf("provider calls after replay repair = %d, want zero", provider.CallCount())
	}
	pending, err = attachments.ListPendingClaims(context.Background())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending claims after replay repair = %+v, err=%v", pending, err)
	}
	if got := attachments.resolveCalls.Load(); got != 3 {
		t.Fatalf("ResolveClaim calls = %d, want initial finalize, reconcile, and replay repair", got)
	}
	if err := attachments.DeleteDraft(context.Background(), session.Data.ID, attachment.Data.ID); !errors.Is(err, chatattachments.ErrNotDraft) {
		t.Fatalf("DeleteDraft repaired attachment error = %v, want ErrNotDraft", err)
	}
}

func TestHecateChatImageTurnDoesNotReplayAfterAssistantAppendFailsBeforeProviderCall(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandler(provider)
	baseStore := apiHandler.agentChat
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "not-disclosed.png", imageTurnTestPNG(t))
	admitted, err := apiHandler.modelApplication().ResolveProviderRoute(context.Background(), "ollama", "llama-vision")
	if err != nil {
		t.Fatalf("ResolveProviderRoute() before failed turn error = %v", err)
	}
	if !admitted.Instance.Valid() {
		t.Fatalf("admitted provider instance = %+v, want stable execution identity", admitted.Instance)
	}
	apiHandler.SetAgentChatStore(failImageAssistantAppendStore{Store: baseStore})

	client.mustRequestStatus(http.StatusInternalServerError, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Image that was not disclosed.","attachment_ids":["`+attachment.Data.ID+`"]}`)
	if provider.CallCount() != 0 {
		t.Fatalf("provider calls = %d, want no dispatch after assistant append failure", provider.CallCount())
	}
	persisted, ok, err := baseStore.Get(context.Background(), session.Data.ID)
	if err != nil || !ok {
		t.Fatalf("Get() after assistant append failure = found %v, error %v", ok, err)
	}
	failedUser := imageTurnFindStoredUserMessage(t, persisted.Messages, "Image that was not disclosed.")
	if failedUser.ProviderInstance.Valid() {
		t.Fatalf("failed user provider instance = %+v, want empty before a provider call attempt", failedUser.ProviderInstance)
	}

	apiHandler.SetAgentChatStore(baseStore)
	stillConfigured, err := apiHandler.modelApplication().ResolveProviderRoute(context.Background(), "ollama", "llama-vision")
	if err != nil {
		t.Fatalf("ResolveProviderRoute() before follow-up error = %v", err)
	}
	if stillConfigured.Instance != admitted.Instance {
		t.Fatalf("provider instance changed = before %+v, after %+v; test requires a stable generation", admitted.Instance, stillConfigured.Instance)
	}
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Continue without the undisclosed image."}`)

	if provider.CallCount() != 1 {
		t.Fatalf("provider calls = %d, want only the safe follow-up dispatch", provider.CallCount())
	}
	request := provider.LastRequest()
	if request.Requirements.ImageInput || request.Requirements.NoProviderFailover || request.Requirements.ProviderInstance.Valid() {
		t.Fatalf("follow-up requirements = %+v, want text-only dispatch after omission", request.Requirements)
	}
	historical := imageTurnFindUserMessage(t, request.Messages, "Image that was not disclosed.\n\n[Earlier image omitted from model context because the active provider differs from the route that previously received it.]")
	if len(historical.ContentBlocks) != 0 || strings.Contains(historical.Content, "data:image/") {
		t.Fatalf("historical message = %+v, want omission without image bytes", historical)
	}
}

func TestReconcileDirectModelAttachmentClaimIgnoresCanceledRequestContext(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandler(provider)
	ctx := context.Background()
	session, err := apiHandler.agentChat.Create(ctx, chat.Session{ID: "chat_cancelled_finalize", AgentID: chat.DefaultAgentID})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	body := imageTurnTestPNG(t)
	created, err := apiHandler.chatApplication().CreateAttachment(ctx, chatapp.CreateAttachmentCommand{Attachment: chatattachments.StoredAttachment{
		Attachment: chatattachments.Attachment{
			ID:        "att_cancelled_finalize",
			SessionID: session.ID,
			Filename:  "cancelled.png",
			MediaType: "image/png",
			SizeBytes: int64(len(body)),
			SHA256:    fmt.Sprintf("%x", sha256.Sum256(body)),
		},
		Data: body,
	}})
	if err != nil {
		t.Fatalf("CreateAttachment: %v", err)
	}
	ref := chatattachments.ClaimRef{SessionID: session.ID, MessageID: "msg_cancelled_finalize", AttachmentIDs: []string{created.ID}}
	claimed, err := apiHandler.chatApplication().ClaimAttachments(ctx, ref)
	if err != nil {
		t.Fatalf("ClaimAttachments: %v", err)
	}
	if _, err := apiHandler.agentChat.AppendMessage(ctx, session.ID, chat.Message{
		ID: ref.MessageID, Role: "user", Attachments: chatMessageAttachments(claimed),
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := apiHandler.reconcileChatAttachmentClaim(canceledCtx, ref, claimed); err != nil {
		t.Fatalf("reconcileChatAttachmentClaim: %v", err)
	}
	if err := apiHandler.chatAttachments.DeleteDraft(ctx, session.ID, created.ID); !errors.Is(err, chatattachments.ErrNotDraft) {
		t.Fatalf("DeleteDraft finalized attachment error = %v, want ErrNotDraft", err)
	}
}

func TestHecateChatImageTurnRejectsUnknownOrIncapableModelBeforeDispatch(t *testing.T) {
	for _, imageInput := range []string{modelcaps.ImageInputUnknown, modelcaps.ImageInputNone} {
		t.Run(imageInput, func(t *testing.T) {
			provider := imageTurnTestProvider(imageInput)
			apiHandler := imageTurnTestHandler(provider)
			handler := NewServer(imageTurnTestLogger(), apiHandler)
			client := newTaskTestClient(t, handler)

			session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
				`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
			attachment := imageTurnTestUpload(t, handler, session.Data.ID, "blocked.png", imageTurnTestPNG(t))
			admission := apiHandler.chatImageTurnAdmission
			for range maxConcurrentChatImageTurns {
				if !admission.TryAcquire() {
					t.Fatal("failed to hold chat image turn admission permit")
				}
				defer admission.Release()
			}
			recorder := client.mustRequestStatus(http.StatusUnprocessableEntity, http.MethodPost,
				"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
				`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"inspect","attachment_ids":["`+attachment.Data.ID+`"]}`)
			payload := decodeRecorder[struct {
				Error struct {
					Type string `json:"type"`
				} `json:"error"`
			}](t, recorder)
			if payload.Error.Type != errCodeImageCapability {
				t.Fatalf("error type = %q, want %q", payload.Error.Type, errCodeImageCapability)
			}
			if provider.CallCount() != 0 {
				t.Fatalf("provider calls = %d, want no dispatch", provider.CallCount())
			}
			persisted, ok, err := apiHandler.agentChat.Get(context.Background(), session.Data.ID)
			if err != nil || !ok {
				t.Fatalf("Get() = found %v, error %v", ok, err)
			}
			if len(persisted.Messages) != 0 {
				t.Fatalf("persisted messages = %+v, want rejection before transcript mutation", persisted.Messages)
			}
		})
	}
}

func TestHecateChatAutoImageTurnPersistsActualRouteSnapshots(t *testing.T) {
	textOnly := imageTurnNamedTestProvider("a-text", modelcaps.ImageInputNone)
	vision := imageTurnNamedTestProvider("b-vision", modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandlerWithProviders(textOnly, vision)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","model":"llama-vision"}`)
	if session.Data.Capabilities.ImageInput != modelcaps.ImageInputUnknown {
		t.Fatalf("initial Auto capability = %+v, want route-safe aggregate before dispatch", session.Data.Capabilities)
	}
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "auto.png", imageTurnTestPNG(t))
	response := mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"model":"llama-vision","content":"Route this image.","attachment_ids":["`+attachment.Data.ID+`"]}`)

	if textOnly.CallCount() != 0 || vision.CallCount() != 1 {
		t.Fatalf("provider calls text=%d vision=%d, want strict route to b-vision", textOnly.CallCount(), vision.CallCount())
	}
	if request := vision.LastRequest(); !request.Requirements.ImageInput || !request.Requirements.NoProviderFailover || request.Requirements.ExactProvider {
		t.Fatalf("Auto image requirements = %+v, want capability/failover boundary without a preselected exact provider", request.Requirements)
	}
	if response.Data.Provider != "b-vision" || response.Data.Model != "llama-vision" || response.Data.Capabilities.ImageInput != modelcaps.ImageInputSupported {
		t.Fatalf("session route snapshot = provider %q model %q capabilities %+v", response.Data.Provider, response.Data.Model, response.Data.Capabilities)
	}
	segmentID := ""
	for _, message := range response.Data.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if message.Provider != "b-vision" || message.Model != "llama-vision" || message.Capabilities.ImageInput != modelcaps.ImageInputSupported {
			t.Fatalf("%s route snapshot = provider %q model %q capabilities %+v", message.Role, message.Provider, message.Model, message.Capabilities)
		}
		if segmentID == "" {
			segmentID = message.SegmentID
		} else if message.SegmentID != segmentID {
			t.Fatalf("message segment ids differ: first %q, %s %q", segmentID, message.Role, message.SegmentID)
		}
	}
	if len(response.Data.Segments) != 1 || response.Data.Segments[0].ID != segmentID || response.Data.Segments[0].Provider != "b-vision" || response.Data.Segments[0].Model != "llama-vision" {
		t.Fatalf("rendered route segment = %+v, want one actual b-vision/llama-vision segment", response.Data.Segments)
	}
	persisted, ok, err := apiHandler.agentChat.Get(context.Background(), session.Data.ID)
	if err != nil || !ok {
		t.Fatalf("Get() = found %v, error %v", ok, err)
	}
	if len(persisted.Messages) != 2 || !persisted.Messages[0].ProviderInstance.Valid() || persisted.Messages[0].ProviderInstance != persisted.Messages[1].ProviderInstance {
		t.Fatalf("persisted provider instances = %+v, want the Auto-selected instance on both turn rows", persisted.Messages)
	}
}

func TestHecateChatFailedAutoImageTurnPersistsAttemptedRouteAndTrace(t *testing.T) {
	vision := imageTurnNamedTestProvider("b-vision", modelcaps.ImageInputSupported)
	vision.err = errors.New("upstream rejected image")
	apiHandler := imageTurnTestHandler(vision)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "failed-auto.png", imageTurnTestPNG(t))
	response := mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"model":"llama-vision","content":"Route this image.","attachment_ids":["`+attachment.Data.ID+`"]}`)

	if vision.CallCount() != 1 {
		t.Fatalf("provider calls = %d, want one attempted image disclosure", vision.CallCount())
	}
	if response.Data.Provider != "b-vision" || response.Data.Model != "llama-vision" {
		t.Fatalf("session attempted route = provider %q model %q, want b-vision/llama-vision", response.Data.Provider, response.Data.Model)
	}
	for _, message := range response.Data.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if message.Provider != "b-vision" || message.Model != "llama-vision" {
			t.Fatalf("%s attempted route = provider %q model %q, want b-vision/llama-vision", message.Role, message.Provider, message.Model)
		}
		if message.Role == "assistant" {
			if message.Status != "failed" || message.TraceID == "" || message.SpanID == "" {
				t.Fatalf("failed assistant metadata = status %q trace %q span %q, want correlated failure", message.Status, message.TraceID, message.SpanID)
			}
		}
	}
	persisted, ok, err := apiHandler.agentChat.Get(context.Background(), session.Data.ID)
	if err != nil || !ok {
		t.Fatalf("Get() = found %v, error %v", ok, err)
	}
	if len(persisted.Messages) != 2 || !persisted.Messages[0].ProviderInstance.Valid() || persisted.Messages[0].ProviderInstance != persisted.Messages[1].ProviderInstance {
		t.Fatalf("persisted failed Auto provider instances = %+v, want attempted instance on both turn rows", persisted.Messages)
	}
}

func TestHecateChatFailedImageTurnPersistsGovernorRewrittenModel(t *testing.T) {
	const (
		requestedModel = "vision-requested"
		attemptedModel = "vision-rewritten"
	)
	imageCapability := types.ModelCapabilities{
		ToolCalling: modelcaps.ToolCallingNone,
		ImageInput:  modelcaps.ImageInputSupported,
		Streaming:   true,
		Source:      modelcaps.SourceProvider,
	}
	provider := &fakeProvider{
		name: "vision",
		capabilities: providers.Capabilities{
			Name:         "vision",
			Kind:         providers.KindCloud,
			DefaultModel: requestedModel,
			Models:       []string{requestedModel, attemptedModel},
			ModelCapabilities: map[string]types.ModelCapabilities{
				requestedModel: imageCapability,
				attemptedModel: imageCapability,
			},
		},
		err: errors.New("rewritten upstream rejected image"),
	}
	apiHandler := newTestAPIHandlerWithSettings(imageTurnTestLogger(), []providers.Provider{provider}, config.Config{
		Governor: config.GovernorConfig{PolicyRules: []config.PolicyRuleConfig{{
			ID:             "rewrite-image-model",
			Action:         "rewrite_model",
			Models:         []string{requestedModel},
			RewriteModelTo: attemptedModel,
		}}},
	}, controlplane.NewMemoryStore())
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"vision","model":"`+requestedModel+`"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "rewrite-failed.png", imageTurnTestPNG(t))
	response := mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision","model":"`+requestedModel+`","content":"Route rewritten image.","attachment_ids":["`+attachment.Data.ID+`"]}`)

	if request := provider.LastRequest(); request.Model != attemptedModel {
		t.Fatalf("provider attempted model = %q, want governor-rewritten %q", request.Model, attemptedModel)
	}
	if response.Data.Provider != "vision" || response.Data.Model != attemptedModel {
		t.Fatalf("session attempted route = provider %q model %q, want vision/%s", response.Data.Provider, response.Data.Model, attemptedModel)
	}
	for _, message := range response.Data.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if message.Provider != "vision" || message.Model != attemptedModel {
			t.Fatalf("%s attempted route = provider %q model %q, want vision/%s", message.Role, message.Provider, message.Model, attemptedModel)
		}
		if message.Role == "assistant" && (message.Status != "failed" || message.TraceID == "") {
			t.Fatalf("failed assistant = status %q trace %q, want correlated rewritten attempt", message.Status, message.TraceID)
		}
	}
}

func TestHecateChatDirectModelTurnHydratesHistoricalImage(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandler(provider)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	imageBytes := imageTurnTestPNG(t)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "history.png", imageBytes)
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Remember this image.","attachment_ids":["`+attachment.Data.ID+`"]}`)
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"What was in it?"}`)

	if provider.CallCount() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.CallCount())
	}
	request := provider.LastRequest()
	historical := imageTurnFindUserMessage(t, request.Messages, "Remember this image.")
	if len(historical.ContentBlocks) != 2 || historical.ContentBlocks[1].Image == nil {
		t.Fatalf("historical message = %+v, want hydrated text and image blocks", historical)
	}
	wantDataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes)
	if historical.ContentBlocks[1].Image.URL != wantDataURI {
		t.Fatalf("historical image URL = %q, want hydrated data URI", historical.ContentBlocks[1].Image.URL)
	}
	current := imageTurnFindUserMessage(t, request.Messages, "What was in it?")
	if len(current.ContentBlocks) != 0 {
		t.Fatalf("current text-only message blocks = %+v, want string-content path", current.ContentBlocks)
	}
}

func TestHecateChatImageHistoryMatchesConfiguredProviderAlias(t *testing.T) {
	provider := imageTurnNamedTestProvider("Vision Production", modelcaps.ImageInputSupported)
	provider.aliases = []string{"vision-prod"}
	apiHandler := imageTurnTestHandler(provider)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"vision-prod","model":"llama-vision"}`)
	imageBytes := imageTurnTestPNG(t)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "alias-history.png", imageBytes)
	first := mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-prod","model":"llama-vision","content":"Remember this alias image.","attachment_ids":["`+attachment.Data.ID+`"]}`)
	if got := provider.LastRequest().Scope.ProviderHint; got != "Vision Production" {
		t.Fatalf("first provider hint = %q, want canonical image-bearing route", got)
	}
	stored := imageTurnFindResponseUserMessage(t, first.Data.Messages, "Remember this alias image.")
	if stored.Provider != "Vision Production" {
		t.Fatalf("persisted provider = %q, want gateway runtime name", stored.Provider)
	}

	second := mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-prod","model":"llama-vision","content":"What was in the alias image?"}`)

	if provider.CallCount() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.CallCount())
	}
	request := provider.LastRequest()
	historical := imageTurnFindUserMessage(t, request.Messages, "Remember this alias image.")
	if len(historical.ContentBlocks) != 2 || historical.ContentBlocks[1].Image == nil {
		t.Fatalf("historical message = %+v, want alias-matched image hydration", historical)
	}
	wantDataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes)
	if historical.ContentBlocks[1].Image.URL != wantDataURI {
		t.Fatalf("historical image URL = %q, want hydrated data URI", historical.ContentBlocks[1].Image.URL)
	}
	segmentID := second.Data.Messages[0].SegmentID
	for _, message := range second.Data.Messages {
		if message.SegmentID != segmentID {
			t.Fatalf("message %q segment = %q, want canonical alias segment %q", message.ID, message.SegmentID, segmentID)
		}
		if message.Provider != "Vision Production" {
			t.Fatalf("message %q provider = %q, want canonical runtime provider", message.ID, message.Provider)
		}
	}
	if len(second.Data.Segments) != 1 || second.Data.Segments[0].ID != segmentID {
		t.Fatalf("segments = %+v, want two alias turns in one canonical segment %q", second.Data.Segments, segmentID)
	}
}

func TestHecateChatDirectModelAmbiguousProviderAliasReturnsClientError(t *testing.T) {
	providerA := imageTurnNamedTestProvider("vision-a", modelcaps.ImageInputSupported)
	providerA.aliases = []string{"shared-vision"}
	providerB := imageTurnNamedTestProvider("vision-b", modelcaps.ImageInputSupported)
	providerB.aliases = []string{"shared-vision"}
	apiHandler := imageTurnTestHandlerWithProviders(providerA, providerB)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"vision-a","model":"llama-vision"}`)
	recorder := client.mustRequestStatus(http.StatusUnprocessableEntity, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"shared-vision","model":"llama-vision","content":"Do not guess the provider."}`)
	payload := decodeRecorder[struct {
		Error struct {
			Type     string `json:"type"`
			Provider string `json:"provider"`
		} `json:"error"`
	}](t, recorder)
	if payload.Error.Type != errCodeProviderAmbiguous || payload.Error.Provider != "shared-vision" {
		t.Fatalf("ambiguous alias error = %+v, want stable provider_ambiguous client error", payload.Error)
	}
	if providerA.CallCount() != 0 || providerB.CallCount() != 0 {
		t.Fatalf("provider calls A=%d B=%d, want fail-closed admission", providerA.CallCount(), providerB.CallCount())
	}
}

func TestHecateChatImageHistoryRejectsAliasCollisionWithZeroModelProvider(t *testing.T) {
	providerA := imageTurnNamedTestProvider("vision-a", modelcaps.ImageInputSupported)
	providerA.aliases = []string{"shared-vision"}
	providerB := imageTurnNamedTestProvider("vision-b", modelcaps.ImageInputSupported)
	providerB.aliases = []string{"shared-vision"}
	providerB.noDefault = true
	providerB.capabilities.DefaultModel = ""
	providerB.capabilities.Models = nil
	providerB.capabilities.ModelCapabilities = nil
	apiHandler := imageTurnTestHandlerWithProviders(providerA, providerB)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"vision-a","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "zero-model-collision-private.png", imageTurnTestPNG(t))
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-a","model":"llama-vision","content":"Private zero-model collision image.","attachment_ids":["`+attachment.Data.ID+`"]}`)
	recorder := client.mustRequestStatus(http.StatusUnprocessableEntity, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"shared-vision","model":"llama-vision","content":"Do not leak this history."}`)
	payload := decodeRecorder[struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}](t, recorder)
	if payload.Error.Type != errCodeProviderAmbiguous {
		t.Fatalf("error.type = %q, want provider_ambiguous", payload.Error.Type)
	}
	if providerA.CallCount() != 1 || providerB.CallCount() != 0 {
		t.Fatalf("provider calls A=%d B=%d, want only the admitted original image turn", providerA.CallCount(), providerB.CallCount())
	}
}

func TestHecateChatImageHistoryMatchesAliasAfterFirstTurnUpstreamFailure(t *testing.T) {
	providerA := imageTurnNamedTestProvider("Vision Production", modelcaps.ImageInputSupported)
	providerA.aliases = []string{"vision-prod"}
	providerA.err = errors.New("injected non-retryable upstream failure")
	providerB := imageTurnNamedTestProvider("vision-b", modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandlerWithProviders(providerA, providerB)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"vision-prod","model":"llama-vision"}`)
	imageBytes := imageTurnTestPNG(t)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "failed-alias-history.png", imageBytes)
	failed := mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-prod","model":"llama-vision","content":"Remember this failed alias image.","attachment_ids":["`+attachment.Data.ID+`"]}`)
	if failed.Data.Provider != "Vision Production" {
		t.Fatalf("failed session provider = %q, want canonical runtime provider", failed.Data.Provider)
	}
	failedUser := imageTurnFindResponseUserMessage(t, failed.Data.Messages, "Remember this failed alias image.")
	for _, message := range failed.Data.Messages {
		if message.Provider != "Vision Production" {
			t.Fatalf("failed-turn message %q provider = %q, want canonical runtime provider", message.ID, message.Provider)
		}
		if message.Role == "assistant" && (message.Status != "failed" || message.ContextPacket == nil || message.ContextPacket.Provider != "Vision Production") {
			t.Fatalf("failed assistant snapshot = status %q context %+v, want failed canonical provider context", message.Status, message.ContextPacket)
		}
	}
	if failedUser.SegmentID == "" {
		t.Fatal("failed user segment is empty")
	}
	providerA.mu.Lock()
	providerA.err = nil
	providerA.mu.Unlock()

	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-prod","model":"llama-vision","content":"Retry context through the alias."}`)
	historical := imageTurnFindUserMessage(t, providerA.LastRequest().Messages, "Remember this failed alias image.")
	if len(historical.ContentBlocks) != 2 || historical.ContentBlocks[1].Image == nil {
		t.Fatalf("same-alias historical message = %+v, want hydrated image after failed first turn", historical)
	}
	wantDataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes)
	if historical.ContentBlocks[1].Image.URL != wantDataURI {
		t.Fatalf("same-alias historical image URL = %q, want hydrated data URI", historical.ContentBlocks[1].Image.URL)
	}

	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-b","model":"llama-vision","content":"Continue on a distinct provider."}`)
	distinctHistorical := imageTurnFindUserMessage(t, providerB.LastRequest().Messages, "Remember this failed alias image.\n\n[Earlier image omitted from model context because the active provider differs from the route that previously received it.]")
	if len(distinctHistorical.ContentBlocks) != 0 {
		t.Fatalf("distinct-provider historical blocks = %+v, want no image bytes", distinctHistorical.ContentBlocks)
	}
}

func TestHecateChatImageHistoryIsOmittedWhenFollowUpRouteCannotAcceptImages(t *testing.T) {
	visionProvider := imageTurnNamedTestProvider("vision", modelcaps.ImageInputSupported)
	textProvider := imageTurnNamedTestProvider("text-only", modelcaps.ImageInputNone)
	apiHandler := imageTurnTestHandlerWithProviders(visionProvider, textProvider)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"vision","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "history.png", imageTurnTestPNG(t))
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"tools_enabled":false,"provider":"vision","model":"llama-vision","content":"Remember this image.","attachment_ids":["`+attachment.Data.ID+`"]}`)
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"tools_enabled":false,"provider":"text-only","model":"llama-vision","content":"Continue without vision."}`)

	if textProvider.CallCount() != 1 {
		t.Fatalf("text-only provider calls = %d, want 1", textProvider.CallCount())
	}
	request := textProvider.LastRequest()
	if request.Requirements.ImageInput {
		t.Fatal("ImageInput requirement = true, want false after historical images are omitted")
	}
	var historical types.Message
	for _, message := range request.Messages {
		if message.Role == "user" && strings.Contains(message.Content, "Remember this image.") {
			historical = message
			break
		}
	}
	if historical.Content == "" || !strings.Contains(historical.Content, "Earlier image omitted from model context") {
		t.Fatalf("historical message = %+v, want omission marker", historical)
	}
	if len(historical.ContentBlocks) != 0 {
		t.Fatalf("historical blocks = %+v, want no image sent to text-only route", historical.ContentBlocks)
	}
}

func TestHecateChatImageHistoryDoesNotCrossCapableProviders(t *testing.T) {
	providerA := imageTurnNamedTestProvider("vision-a", modelcaps.ImageInputSupported)
	providerB := imageTurnNamedTestProvider("vision-b", modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandlerWithProviders(providerA, providerB)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"vision-a","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "private.png", imageTurnTestPNG(t))
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"tools_enabled":false,"provider":"vision-a","model":"llama-vision","content":"Private image.","attachment_ids":["`+attachment.Data.ID+`"]}`)
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"tools_enabled":false,"provider":"vision-b","model":"llama-vision","content":"Continue on B."}`)

	if providerB.CallCount() != 1 {
		t.Fatalf("provider B calls = %d, want 1", providerB.CallCount())
	}
	request := providerB.LastRequest()
	if request.Requirements.ImageInput || request.Requirements.NoProviderFailover {
		t.Fatalf("provider B requirements = %+v, want text-only request after historical omission", request.Requirements)
	}
	var historical types.Message
	for _, message := range request.Messages {
		if message.Role == "user" && strings.Contains(message.Content, "Private image.") {
			historical = message
			break
		}
	}
	if len(historical.ContentBlocks) != 0 || !strings.Contains(historical.Content, "active provider differs") {
		t.Fatalf("historical message = %+v, want provider-boundary omission marker and no bytes", historical)
	}
}

func TestHecateChatImageHistoryCanonicalProviderWinsCollidingAlias(t *testing.T) {
	providerA := imageTurnNamedTestProvider("vision-a", modelcaps.ImageInputSupported)
	providerA.aliases = []string{"vision-b"}
	providerB := imageTurnNamedTestProvider("vision-b", modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandlerWithProviders(providerA, providerB)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"vision-a","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "alias-collision-private.png", imageTurnTestPNG(t))
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-a","model":"llama-vision","content":"Private alias-collision image.","attachment_ids":["`+attachment.Data.ID+`"]}`)
	second := mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-b","model":"llama-vision","content":"Continue on canonical B."}`)

	if providerA.CallCount() != 1 || providerB.CallCount() != 1 {
		t.Fatalf("provider calls A=%d B=%d, want one turn routed to each canonical provider", providerA.CallCount(), providerB.CallCount())
	}
	request := providerB.LastRequest()
	if request.Scope.ProviderHint != "vision-b" {
		t.Fatalf("provider B hint = %q, want exact canonical provider", request.Scope.ProviderHint)
	}
	if request.Requirements.ImageInput || request.Requirements.NoProviderFailover {
		t.Fatalf("provider B requirements = %+v, want text-only request after provider-bound history omission", request.Requirements)
	}
	historical := imageTurnFindUserMessage(t, request.Messages,
		"Private alias-collision image.\n\n[Earlier image omitted from model context because the active provider differs from the route that previously received it.]")
	if len(historical.ContentBlocks) != 0 {
		t.Fatalf("historical blocks = %+v, want no image bytes disclosed to canonical provider B", historical.ContentBlocks)
	}
	current := imageTurnFindResponseUserMessage(t, second.Data.Messages, "Continue on canonical B.")
	if current.Provider != "vision-b" {
		t.Fatalf("current persisted provider = %q, want canonical vision-b", current.Provider)
	}
}

func TestHecateChatImageHistoryRejectsSameNameRuntimeReplacementAfterAdmission(t *testing.T) {
	providerA := imageTurnNamedTestProvider("vision-a", modelcaps.ImageInputSupported)
	providerA.aliases = []string{"vision-route"}
	providerB := imageTurnNamedTestProvider("vision-b", modelcaps.ImageInputSupported)
	registry := providers.NewMutableRegistry(providerA, providerB)
	apiHandler := newTestAPIHandlerWithRegistry(
		imageTurnTestLogger(),
		registry,
		[]providers.Provider{providerA, providerB},
		config.Config{},
		controlplane.NewMemoryStore(),
	)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"vision-a","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "alias-reassignment-private.png", imageTurnTestPNG(t))
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-a","model":"llama-vision","content":"Private reload image.","attachment_ids":["`+attachment.Data.ID+`"]}`)

	reloadedA := imageTurnNamedTestProvider("vision-a", modelcaps.ImageInputSupported)
	reloadedB := imageTurnNamedTestProvider("vision-b", modelcaps.ImageInputSupported)
	reloadedB.aliases = []string{"vision-route"}
	apiHandler.SetAgentChatStore(&replaceProviderRegistryOnRunningAssistantStore{
		Store: apiHandler.agentChat,
		replace: func() {
			registry.Replace(reloadedA, reloadedB)
		},
	})
	second := mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-route","model":"llama-vision","content":"Recall through the live alias."}`)

	if providerA.CallCount() != 1 || providerB.CallCount() != 0 {
		t.Fatalf("pre-reload provider calls A=%d B=%d, want only the original A turn", providerA.CallCount(), providerB.CallCount())
	}
	if reloadedA.CallCount() != 0 || reloadedB.CallCount() != 0 {
		t.Fatalf("post-reload provider calls A=%d B=%d, want no disclosure after the admitted runtime instance changed", reloadedA.CallCount(), reloadedB.CallCount())
	}
	current := imageTurnFindResponseUserMessage(t, second.Data.Messages, "Recall through the live alias.")
	if current.Provider != "vision-a" {
		t.Fatalf("current persisted provider = %q, want canonical vision-a route snapshot", current.Provider)
	}
	var assistant ChatMessageItem
	for _, message := range second.Data.Messages {
		if message.Role == "assistant" {
			assistant = message
		}
	}
	if assistant.Status != "failed" || !strings.Contains(assistant.Error, "changed during image admission") {
		t.Fatalf("assistant = %+v, want fail-closed provider-instance replacement", assistant)
	}
}

func TestHecateChatImageHistoryOmitsSameNameRuntimeReplacementBeforeAdmission(t *testing.T) {
	original := imageTurnNamedTestProvider("vision-a", modelcaps.ImageInputSupported)
	registry := providers.NewMutableRegistry(original)
	apiHandler := newTestAPIHandlerWithRegistry(
		imageTurnTestLogger(),
		registry,
		[]providers.Provider{original},
		config.Config{},
		controlplane.NewMemoryStore(),
	)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"vision-a","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "same-name-private.png", imageTurnTestPNG(t))
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-a","model":"llama-vision","content":"Private image before recreation.","attachment_ids":["`+attachment.Data.ID+`"]}`)

	replacement := imageTurnNamedTestProvider("vision-a", modelcaps.ImageInputSupported)
	registry.Replace(replacement)
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-a","model":"llama-vision","content":"Recall after recreation."}`)

	if original.CallCount() != 1 || replacement.CallCount() != 1 {
		t.Fatalf("provider calls original=%d replacement=%d, want one original image call and one safe text call", original.CallCount(), replacement.CallCount())
	}
	request := replacement.LastRequest()
	if request.Requirements.ImageInput || request.Requirements.NoProviderFailover || request.Requirements.ExactProvider || request.Requirements.ProviderInstance.Valid() {
		t.Fatalf("replacement requirements = %+v, want text-only follow-up", request.Requirements)
	}
	historical := imageTurnFindUserMessage(t, request.Messages,
		"Private image before recreation.\n\n[Earlier image omitted from model context because the active provider differs from the route that previously received it.]")
	if len(historical.ContentBlocks) != 0 {
		t.Fatalf("historical blocks = %+v, want no bytes sent to recreated same-name provider", historical.ContentBlocks)
	}
}

func TestHecateChatImageHistoryRejectsAliasTakeoverWhenCanonicalProviderDisappears(t *testing.T) {
	providerA := imageTurnNamedTestProvider("vision-a", modelcaps.ImageInputSupported)
	providerB := imageTurnNamedTestProvider("vision-b", modelcaps.ImageInputSupported)
	registry := providers.NewMutableRegistry(providerA, providerB)
	apiHandler := newTestAPIHandlerWithRegistry(
		imageTurnTestLogger(),
		registry,
		[]providers.Provider{providerA, providerB},
		config.Config{},
		controlplane.NewMemoryStore(),
	)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"vision-a","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "alias-takeover-private.png", imageTurnTestPNG(t))
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-a","model":"llama-vision","content":"Private image before removal.","attachment_ids":["`+attachment.Data.ID+`"]}`)

	reloadedB := imageTurnNamedTestProvider("vision-b", modelcaps.ImageInputSupported)
	reloadedB.aliases = []string{"vision-a"}
	apiHandler.SetAgentChatStore(&replaceProviderRegistryOnRunningAssistantStore{
		Store: apiHandler.agentChat,
		replace: func() {
			registry.Replace(reloadedB)
		},
	})
	response := mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-a","model":"llama-vision","content":"Recall after removal."}`)

	if providerA.CallCount() != 1 || providerB.CallCount() != 0 || reloadedB.CallCount() != 0 {
		t.Fatalf("provider calls original A=%d original B=%d reloaded B=%d, want no post-removal disclosure", providerA.CallCount(), providerB.CallCount(), reloadedB.CallCount())
	}
	current := imageTurnFindResponseUserMessage(t, response.Data.Messages, "Recall after removal.")
	if current.Provider != "vision-a" {
		t.Fatalf("current persisted provider = %q, want fenced canonical vision-a", current.Provider)
	}
	var assistant ChatMessageItem
	for _, message := range response.Data.Messages {
		if message.Role == "assistant" {
			assistant = message
		}
	}
	if assistant.Status != "failed" || !strings.Contains(assistant.Error, `provider "vision-a" not found`) {
		t.Fatalf("assistant = %+v, want failed exact-provider route after removal", assistant)
	}
}

func TestHecateChatImageHistoryRejectsNormalizedNameTakeover(t *testing.T) {
	providerA := imageTurnNamedTestProvider("vision-a", modelcaps.ImageInputSupported)
	registry := providers.NewMutableRegistry(providerA)
	apiHandler := newTestAPIHandlerWithRegistry(
		imageTurnTestLogger(),
		registry,
		[]providers.Provider{providerA},
		config.Config{},
		controlplane.NewMemoryStore(),
	)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"vision-a","model":"llama-vision"}`)
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "normalized-takeover-private.png", imageTurnTestPNG(t))
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-a","model":"llama-vision","content":"Private image before case takeover.","attachment_ids":["`+attachment.Data.ID+`"]}`)

	replacement := imageTurnNamedTestProvider("VISION-A", modelcaps.ImageInputSupported)
	registry.Replace(replacement)
	second := mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-a","model":"llama-vision","content":"Recall after case takeover."}`)

	if providerA.CallCount() != 1 || replacement.CallCount() != 1 {
		t.Fatalf("provider calls original=%d replacement=%d, want one original turn and one safe text-only replacement turn", providerA.CallCount(), replacement.CallCount())
	}
	request := replacement.LastRequest()
	if request.Requirements.ImageInput || request.Requirements.NoProviderFailover || request.Requirements.ExactProvider {
		t.Fatalf("replacement requirements = %+v, want text-only request after exact identity mismatch", request.Requirements)
	}
	historical := imageTurnFindUserMessage(t, request.Messages,
		"Private image before case takeover.\n\n[Earlier image omitted from model context because the active provider differs from the route that previously received it.]")
	if len(historical.ContentBlocks) != 0 {
		t.Fatalf("historical blocks = %+v, want no image bytes disclosed to case-only replacement", historical.ContentBlocks)
	}
	current := imageTurnFindResponseUserMessage(t, second.Data.Messages, "Recall after case takeover.")
	if current.Provider != "VISION-A" {
		t.Fatalf("current provider = %q, want resolved replacement identity without historical bytes", current.Provider)
	}
}

func TestHecateChatImageHistoryOmitsLegacySameNameMessageWithoutProviderInstance(t *testing.T) {
	provider := imageTurnNamedTestProvider("vision-a", modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandler(provider)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"vision-a","model":"llama-vision"}`)
	if _, err := apiHandler.agentChat.AppendMessage(context.Background(), session.Data.ID, chat.Message{
		ID:       "msg_legacy_missing_generation",
		Role:     "user",
		Provider: "vision-a",
		Content:  "Legacy private image.",
		Attachments: []chat.MessageAttachment{{
			ID:        "att_body_must_not_be_loaded",
			Filename:  "legacy.png",
			MediaType: "image/png",
			SizeBytes: 1,
			SHA256:    "legacy",
		}},
	}); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}

	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"vision-a","model":"llama-vision","content":"Continue without legacy bytes."}`)

	if provider.CallCount() != 1 {
		t.Fatalf("provider calls = %d, want safe text-only follow-up", provider.CallCount())
	}
	request := provider.LastRequest()
	if request.Requirements.ImageInput || request.Requirements.NoProviderFailover || request.Requirements.ProviderInstance.Valid() {
		t.Fatalf("legacy follow-up requirements = %+v, want no image dispatch fence", request.Requirements)
	}
	historical := imageTurnFindUserMessage(t, request.Messages,
		"Legacy private image.\n\n[Earlier image omitted from model context because the active provider differs from the route that previously received it.]")
	if len(historical.ContentBlocks) != 0 {
		t.Fatalf("historical blocks = %+v, want legacy image omitted before attachment lookup", historical.ContentBlocks)
	}
}

func TestHecateChatAutoHistoryDoesNotHydrateWithoutKnownProviderBoundary(t *testing.T) {
	handler := &Handler{}
	session := chat.Session{
		ID: "chat_auto_boundary",
		Messages: []chat.Message{{
			ID:       "msg_provider_a",
			Role:     "user",
			Provider: "vision-a",
			Content:  "Private image",
			Attachments: []chat.MessageAttachment{{
				ID:        "att_private",
				Filename:  "private.png",
				MediaType: "image/png",
				SizeBytes: 1,
			}},
		}},
	}

	history, requiresImageInput, err := handler.agentChatModelHistoryWithAttachments(
		context.Background(), session, "", "Continue on Auto", nil, true, "", types.ProviderInstanceIdentity{},
	)
	if err != nil {
		t.Fatalf("agentChatModelHistoryWithAttachments: %v", err)
	}
	if requiresImageInput {
		t.Fatal("requiresImageInput = true, want false without a known provider boundary")
	}
	if len(history) != 2 || len(history[0].ContentBlocks) != 0 || !strings.Contains(history[0].Content, "active provider differs") {
		t.Fatalf("history = %+v, want Auto provider-boundary omission", history)
	}
}

func TestHecateChatImageHistoryUsesOmissionMarkerAtByteLimit(t *testing.T) {
	handler := &Handler{}
	providerInstance := types.ProviderInstanceIdentity{ID: "runtime-test", Kind: types.ProviderInstanceIdentityRuntime}
	session := chat.Session{
		ID: "chat_image_history_limit",
		Messages: []chat.Message{{
			ID:               "msg_large_image",
			Role:             "user",
			Provider:         "ollama",
			ProviderInstance: providerInstance,
			Content:          "Earlier image",
			Attachments: []chat.MessageAttachment{{
				ID:        "att_large_image",
				Filename:  "large.png",
				MediaType: "image/png",
				SizeBytes: agentChatMaxImageHistoryBytes + 1,
			}},
		}},
	}

	history, requiresImageInput, err := handler.agentChatModelHistoryWithAttachments(context.Background(), session, "", "Continue", nil, true, "ollama", providerInstance)
	if err != nil {
		t.Fatalf("agentChatModelHistoryWithAttachments() error = %v", err)
	}
	if requiresImageInput {
		t.Fatal("requiresImageInput = true, want false when the only image exceeds the history budget")
	}
	if len(history) != 2 {
		t.Fatalf("history = %+v, want historical and current user messages", history)
	}
	wantMarker := fmt.Sprintf("Earlier image omitted from model context because the %d MiB image-history limit was reached.", agentChatMaxImageHistoryBytes>>20)
	if !strings.Contains(history[0].Content, wantMarker) {
		t.Fatalf("historical content = %q, want explicit omission marker", history[0].Content)
	}
	if len(history[0].ContentBlocks) != 0 {
		t.Fatalf("omitted historical image blocks = %+v, want none", history[0].ContentBlocks)
	}
}

func TestValidateStoredChatAttachmentTranscriptRequiresImmutableMetadataMatch(t *testing.T) {
	createdAt := time.Date(2026, 7, 13, 14, 15, 16, 123456789, time.FixedZone("test-offset", 2*60*60))
	transcript := chat.MessageAttachment{
		ID:        "att_integrity",
		Filename:  "integrity.png",
		MediaType: "image/png",
		SizeBytes: 128,
		SHA256:    strings.Repeat("a", 64),
		CreatedAt: createdAt,
	}
	stored := chatattachments.StoredAttachment{Attachment: chatattachments.Attachment{
		ID:        transcript.ID,
		SessionID: "chat_integrity",
		Filename:  transcript.Filename,
		MediaType: transcript.MediaType,
		SizeBytes: transcript.SizeBytes,
		SHA256:    transcript.SHA256,
		CreatedAt: createdAt.UTC(),
	}}

	tests := []struct {
		name   string
		mutate func(*chatattachments.StoredAttachment)
		wantOK bool
	}{
		{name: "exact match", mutate: func(*chatattachments.StoredAttachment) {}, wantOK: true},
		{name: "different owner", mutate: func(item *chatattachments.StoredAttachment) { item.SessionID = "chat_other" }},
		{name: "different id", mutate: func(item *chatattachments.StoredAttachment) { item.ID = "att_other" }},
		{name: "different filename", mutate: func(item *chatattachments.StoredAttachment) { item.Filename = "renamed.png" }},
		{name: "different media", mutate: func(item *chatattachments.StoredAttachment) { item.MediaType = "image/jpeg" }},
		{name: "different size", mutate: func(item *chatattachments.StoredAttachment) { item.SizeBytes++ }},
		{name: "different digest", mutate: func(item *chatattachments.StoredAttachment) { item.SHA256 = strings.Repeat("b", 64) }},
		{name: "different creation time", mutate: func(item *chatattachments.StoredAttachment) { item.CreatedAt = item.CreatedAt.Add(time.Nanosecond) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := stored
			test.mutate(&candidate)
			err := validateStoredChatAttachmentTranscript("chat_integrity", transcript, candidate)
			if test.wantOK && err != nil {
				t.Fatalf("validateStoredChatAttachmentTranscript() error = %v, want nil", err)
			}
			if !test.wantOK && !errors.Is(err, errStoredChatAttachmentTranscriptMismatch) {
				t.Fatalf("validateStoredChatAttachmentTranscript() error = %v, want safe transcript mismatch", err)
			}
		})
	}
}

func TestHecateChatImageHistoryOmitsTamperedTranscriptSizeBeforeHydration(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandler(provider)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	imageBytes := make([]byte, agentChatMaxImageHistoryBytes+1)
	copy(imageBytes, imageTurnTestPNG(t))
	digest := sha256.Sum256(imageBytes)
	created, err := apiHandler.chatAttachments.Create(context.Background(), chatattachments.StoredAttachment{
		Attachment: chatattachments.Attachment{
			ID:        "att_tampered_history_size",
			SessionID: session.Data.ID,
			Filename:  "large-history.png",
			MediaType: "image/png",
			SizeBytes: int64(len(imageBytes)),
			SHA256:    fmt.Sprintf("%x", digest),
			CreatedAt: time.Date(2026, 7, 13, 12, 30, 0, 0, time.UTC),
		},
		Data: imageBytes,
	})
	if err != nil {
		t.Fatalf("Create() stored attachment error = %v", err)
	}
	const messageID = "msg_tampered_history_size"
	providerRoute, err := apiHandler.modelApplication().ResolveProviderRoute(context.Background(), "ollama", "llama-vision")
	if err != nil {
		t.Fatalf("ResolveProviderRoute() error = %v", err)
	}
	ref := chatattachments.ClaimRef{
		SessionID:     session.Data.ID,
		MessageID:     messageID,
		AttachmentIDs: []string{created.ID},
	}
	if _, err := apiHandler.chatAttachments.Claim(context.Background(), ref); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := apiHandler.agentChat.AppendMessage(context.Background(), session.Data.ID, chat.Message{
		ID:               messageID,
		Role:             "user",
		Provider:         "ollama",
		ProviderInstance: providerRoute.Instance,
		Content:          "Earlier oversized image",
		Attachments: []chat.MessageAttachment{{
			ID:        created.ID,
			Filename:  created.Filename,
			MediaType: created.MediaType,
			SizeBytes: 1,
			SHA256:    created.SHA256,
			CreatedAt: created.CreatedAt,
		}},
		CreatedAt: time.Date(2026, 7, 13, 12, 31, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	if err := apiHandler.chatAttachments.ResolveClaim(context.Background(), ref, chatattachments.ClaimLinked); err != nil {
		t.Fatalf("ResolveClaim() error = %v", err)
	}

	response := mustRequestJSON[ChatSessionResponse](client, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Continue safely."}`)
	if provider.CallCount() != 1 {
		t.Fatalf("provider calls = %d, want one text-only dispatch after safe image omission", provider.CallCount())
	}
	request := provider.LastRequest()
	if request.Requirements.ImageInput || request.Requirements.NoProviderFailover {
		t.Fatalf("provider requirements = %+v, want no image requirement after integrity omission", request.Requirements)
	}
	historical := imageTurnFindUserMessage(t, request.Messages, "Earlier oversized image\n\n[Earlier image omitted from model context because its stored metadata no longer matches the immutable transcript record.]")
	if len(historical.ContentBlocks) != 0 {
		t.Fatalf("historical blocks = %+v, want no hydrated image after transcript metadata mismatch", historical.ContentBlocks)
	}
	if strings.Contains(historical.Content, created.SHA256) {
		t.Fatalf("historical omission marker exposed attachment digest: %q", historical.Content)
	}
	if len(response.Data.Messages) != 3 || response.Data.Messages[2].Status != "completed" {
		t.Fatalf("response messages = %+v, want safe text-only follow-up completion", response.Data.Messages)
	}
}

func TestHecateChatImageTurnReleasesCurrentDraftWhenHistoryPreparationFails(t *testing.T) {
	provider := imageTurnTestProvider(modelcaps.ImageInputSupported)
	apiHandler := imageTurnTestHandler(provider)
	handler := NewServer(imageTurnTestLogger(), apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"ollama","model":"llama-vision"}`)
	providerRoute, err := apiHandler.modelApplication().ResolveProviderRoute(context.Background(), "ollama", "llama-vision")
	if err != nil {
		t.Fatalf("ResolveProviderRoute() error = %v", err)
	}
	if _, err := apiHandler.agentChat.AppendMessage(context.Background(), session.Data.ID, chat.Message{
		ID:               "msg_missing_historical_image",
		Role:             "user",
		Provider:         "ollama",
		ProviderInstance: providerRoute.Instance,
		Content:          "Earlier image",
		Attachments: []chat.MessageAttachment{{
			ID:        "att_missing",
			Filename:  "missing.png",
			MediaType: "image/png",
			SizeBytes: 1,
			SHA256:    "missing",
		}},
	}); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	attachment := imageTurnTestUpload(t, handler, session.Data.ID, "current.png", imageTurnTestPNG(t))

	recorder := client.mustRequestStatus(http.StatusInternalServerError, http.MethodPost,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"ollama","model":"llama-vision","content":"Inspect this image.","attachment_ids":["`+attachment.Data.ID+`"]}`)
	if !strings.Contains(recorder.Body.String(), "failed to prepare chat image context") {
		t.Fatalf("error body = %s, want bounded history-preparation error", recorder.Body.String())
	}
	if provider.CallCount() != 0 {
		t.Fatalf("provider calls = %d, want no dispatch", provider.CallCount())
	}

	deleteRecorder := client.mustRequestStatus(http.StatusNoContent, http.MethodDelete,
		"/hecate/v1/chat/sessions/"+session.Data.ID+"/attachments/"+attachment.Data.ID, "")
	if deleteRecorder.Body.Len() != 0 {
		t.Fatalf("delete body = %q, want released draft to be deletable", deleteRecorder.Body.String())
	}
	for range maxConcurrentChatImageTurns {
		if !apiHandler.chatImageTurnAdmission.TryAcquire() {
			t.Fatal("image turn permit was not released after history preparation failed")
		}
		defer apiHandler.chatImageTurnAdmission.Release()
	}
}

func imageTurnTestProvider(imageInput string) *fakeProvider {
	return imageTurnNamedTestProvider("ollama", imageInput)
}

func imageTurnNamedTestProvider(name, imageInput string) *fakeProvider {
	return &fakeProvider{
		name: name,
		capabilities: providers.Capabilities{
			Name:         name,
			Kind:         providers.KindLocal,
			DefaultModel: "llama-vision",
			Models:       []string{"llama-vision"},
			ModelCapabilities: map[string]types.ModelCapabilities{
				"llama-vision": {
					ToolCalling: modelcaps.ToolCallingNone,
					ImageInput:  imageInput,
					Streaming:   true,
					Source:      modelcaps.SourceProvider,
				},
			},
		},
		response: &types.ChatResponse{
			ID:        "chatcmpl-image-turn",
			Model:     "llama-vision",
			CreatedAt: time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "I can see the image."},
				FinishReason: "stop",
			}},
		},
	}
}

func imageTurnTestHandler(provider providers.Provider) *Handler {
	return imageTurnTestHandlerWithProviders(provider)
}

func imageTurnTestHandlerWithProviders(provider ...providers.Provider) *Handler {
	return newTestAPIHandlerWithSettings(
		imageTurnTestLogger(),
		provider,
		config.Config{},
		controlplane.NewMemoryStore(),
	)
}

func imageTurnTestLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func imageTurnTestPNG(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	canvas.Set(0, 0, color.NRGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff})
	canvas.Set(1, 0, color.NRGBA{R: 0xab, G: 0xcd, B: 0xef, A: 0xff})
	var data bytes.Buffer
	if err := png.Encode(&data, canvas); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return data.Bytes()
}

func imageTurnTestUpload(t *testing.T, handler http.Handler, sessionID, filename string, data []byte) ChatAttachmentResponse {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart image error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("multipart Close() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/attachments", &body)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d, body=%s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	return decodeRecorder[ChatAttachmentResponse](t, recorder)
}

func imageTurnFindUserMessage(t *testing.T, messages []types.Message, content string) types.Message {
	t.Helper()
	for _, message := range messages {
		if message.Role == "user" && message.Content == content {
			return message
		}
	}
	t.Fatalf("user message with content %q not found in %+v", content, messages)
	return types.Message{}
}

func imageTurnFindResponseUserMessage(t *testing.T, messages []ChatMessageItem, content string) ChatMessageItem {
	t.Helper()
	for _, message := range messages {
		if message.Role == "user" && message.Content == content {
			return message
		}
	}
	t.Fatalf("response user message with content %q not found in %+v", content, messages)
	return ChatMessageItem{}
}

func imageTurnFindStoredUserMessage(t *testing.T, messages []chat.Message, content string) chat.Message {
	t.Helper()
	for _, message := range messages {
		if message.Role == "user" && message.Content == content {
			return message
		}
	}
	t.Fatalf("stored user message with content %q not found in %+v", content, messages)
	return chat.Message{}
}

func imageTurnAssertAttachmentMetadata(t *testing.T, sessionID string, uploaded ChatAttachmentItem, attached []ChatAttachmentItem) {
	t.Helper()
	if len(attached) != 1 {
		t.Fatalf("attachments = %+v, want one metadata item", attached)
	}
	got := attached[0]
	if got.ID != uploaded.ID || got.SessionID != sessionID || got.Filename != uploaded.Filename ||
		got.MediaType != "image/png" || got.SizeBytes != uploaded.SizeBytes || got.SHA256 != uploaded.SHA256 {
		t.Fatalf("attachment metadata = %+v, uploaded = %+v", got, uploaded)
	}
	wantContentURL := "/hecate/v1/chat/sessions/" + sessionID + "/attachments/" + uploaded.ID + "/content"
	if got.ContentURL != wantContentURL {
		t.Fatalf("content_url = %q, want %q", got.ContentURL, wantContentURL)
	}
}

func imageTurnAssertNoImageBody(t *testing.T, payload, imageBytes []byte) {
	t.Helper()
	encoded := base64.StdEncoding.EncodeToString(imageBytes)
	if bytes.Contains(payload, []byte(encoded)) || bytes.Contains(payload, []byte("data:image/")) || bytes.Contains(payload, []byte(`"data_base64"`)) {
		t.Fatalf("session JSON exposed image body: %s", payload)
	}
}
