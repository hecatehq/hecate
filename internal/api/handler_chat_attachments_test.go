package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"runtime"
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
)

const chatAttachmentTestRuntimeToken = "attachment-runtime-token-123456"

const (
	chatAttachmentSensitiveSQL = `SELECT body FROM chat_attachments WHERE session_id = 'private-session'`
	chatAttachmentSensitiveDSN = "postgres://private-user:private-password@db.internal/hecate"
)

type chatAttachmentHTTPFixture struct {
	handler *Handler
	server  http.Handler
}

type chatAttachmentErrorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type corruptAttachmentGetStore struct {
	chatattachments.Store
}

type corruptAttachmentClaimStore struct {
	chatattachments.Store
}

type sensitiveAttachmentCreateErrorStore struct {
	chatattachments.Store
	attachment chatattachments.StoredAttachment
	err        error
}

type sensitiveAttachmentGetErrorStore struct {
	chatattachments.Store
	attachment chatattachments.StoredAttachment
	err        error
}

type sensitiveAttachmentDeleteErrorStore struct {
	chatattachments.Store
	attachment chatattachments.StoredAttachment
	err        error
}

type failFirstChatAttachmentSessionDeleteStore struct {
	chatattachments.Store
	deleteCalls int
	deleteErr   error
}

type ownerDeletingChatAttachmentStore struct {
	chatattachments.Store
	sessions        chat.Store
	created         chatattachments.StoredAttachment
	rollbackMessage string
}

type countingCreateChatAttachmentStore struct {
	chatattachments.Store
	creates atomic.Int64
}

type blockingCreateChatAttachmentStore struct {
	chatattachments.Store
	createStarted chan struct{}
	allowCreate   chan struct{}
	deleteStarted chan struct{}
	createOnce    sync.Once
	deleteOnce    sync.Once
}

type blockingDeleteDraftChatAttachmentStore struct {
	chatattachments.Store
	deleteStarted chan struct{}
	allowDelete   chan struct{}
	deleteCalls   atomic.Int64
	deleteOnce    sync.Once
	releaseOnce   sync.Once
}

type gatedChatAttachmentRequestBody struct {
	reader      *bytes.Reader
	readStarted chan struct{}
	allowRead   chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once
}

type chatAttachmentReadSpy struct {
	reads atomic.Int64
}

type countingGetChatAttachmentStore struct {
	chatattachments.Store
	gets atomic.Int64
}

type deadlineBlockingAttachmentResponseWriter struct {
	header       http.Header
	status       int
	writeStarted chan struct{}
	deadlineSet  chan struct{}
	expired      chan struct{}
	deadlineOnce sync.Once
	writeOnce    sync.Once
}

type blockingAttachmentResponseWriter struct {
	header       http.Header
	status       int
	writeStarted chan struct{}
	allowWrite   chan struct{}
	writeOnce    sync.Once
	releaseOnce  sync.Once
}

type deadlineSupportingResponseRecorder struct {
	*httptest.ResponseRecorder
}

type deadlineRejectingAttachmentResponseWriter struct {
	*httptest.ResponseRecorder
}

type blockingChatAttachmentReadCloser struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

type observingChatImageUploadAdmission struct {
	delegate chatImageUploadAdmission
	acquired chan struct{}
}

func (a *observingChatImageUploadAdmission) TryAcquire() bool {
	if !a.delegate.TryAcquire() {
		return false
	}
	a.acquired <- struct{}{}
	return true
}

func (a *observingChatImageUploadAdmission) Release() {
	a.delegate.Release()
}

func newBlockingChatAttachmentReadCloser() *blockingChatAttachmentReadCloser {
	return &blockingChatAttachmentReadCloser{
		started: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (b *blockingChatAttachmentReadCloser) Read([]byte) (int, error) {
	b.startOnce.Do(func() { close(b.started) })
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *blockingChatAttachmentReadCloser) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func (s *chatAttachmentReadSpy) Read([]byte) (int, error) {
	s.reads.Add(1)
	return 0, io.EOF
}

func (s *countingGetChatAttachmentStore) Get(ctx context.Context, sessionID, id string) (chatattachments.StoredAttachment, bool, error) {
	s.gets.Add(1)
	return s.Store.Get(ctx, sessionID, id)
}

func newDeadlineBlockingAttachmentResponseWriter() *deadlineBlockingAttachmentResponseWriter {
	return &deadlineBlockingAttachmentResponseWriter{
		header:       make(http.Header),
		writeStarted: make(chan struct{}),
		deadlineSet:  make(chan struct{}),
		expired:      make(chan struct{}),
	}
}

func (w *deadlineBlockingAttachmentResponseWriter) Header() http.Header { return w.header }

func (w *deadlineBlockingAttachmentResponseWriter) WriteHeader(status int) { w.status = status }

func (w *deadlineBlockingAttachmentResponseWriter) Write([]byte) (int, error) {
	w.writeOnce.Do(func() { close(w.writeStarted) })
	<-w.expired
	return 0, context.DeadlineExceeded
}

func (w *deadlineBlockingAttachmentResponseWriter) SetWriteDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		return nil
	}
	w.deadlineOnce.Do(func() {
		close(w.deadlineSet)
		delay := time.Until(deadline)
		if delay < 0 {
			delay = 0
		}
		time.AfterFunc(delay, func() { close(w.expired) })
	})
	return nil
}

func newBlockingAttachmentResponseWriter() *blockingAttachmentResponseWriter {
	return &blockingAttachmentResponseWriter{
		header:       make(http.Header),
		writeStarted: make(chan struct{}),
		allowWrite:   make(chan struct{}),
	}
}

func (w *blockingAttachmentResponseWriter) Header() http.Header { return w.header }

func (w *blockingAttachmentResponseWriter) WriteHeader(status int) { w.status = status }

func (w *blockingAttachmentResponseWriter) Write(body []byte) (int, error) {
	w.writeOnce.Do(func() { close(w.writeStarted) })
	<-w.allowWrite
	return len(body), nil
}

func (w *blockingAttachmentResponseWriter) SetWriteDeadline(time.Time) error { return nil }

func (w *deadlineSupportingResponseRecorder) SetWriteDeadline(time.Time) error { return nil }

func (w *deadlineRejectingAttachmentResponseWriter) SetWriteDeadline(time.Time) error {
	return http.ErrNotSupported
}

func (w *blockingAttachmentResponseWriter) release() {
	w.releaseOnce.Do(func() { close(w.allowWrite) })
}

func (s corruptAttachmentGetStore) Get(ctx context.Context, sessionID, id string) (chatattachments.StoredAttachment, bool, error) {
	attachment, ok, err := s.Store.Get(ctx, sessionID, id)
	if ok {
		attachment.SizeBytes++
	}
	return attachment, ok, err
}

func (s corruptAttachmentClaimStore) Claim(ctx context.Context, ref chatattachments.ClaimRef) ([]chatattachments.StoredAttachment, error) {
	attachments, err := s.Store.Claim(ctx, ref)
	if err == nil && len(attachments) > 0 {
		attachments[0].SizeBytes++
	}
	return attachments, err
}

func (s *sensitiveAttachmentCreateErrorStore) Create(_ context.Context, attachment chatattachments.StoredAttachment) (chatattachments.StoredAttachment, error) {
	s.attachment = attachment
	s.err = sensitiveChatAttachmentStoreError("create", attachment)
	return chatattachments.StoredAttachment{}, s.err
}

func (s *sensitiveAttachmentGetErrorStore) Get(ctx context.Context, sessionID, id string) (chatattachments.StoredAttachment, bool, error) {
	attachment, ok, err := s.Store.Get(ctx, sessionID, id)
	if err != nil || !ok {
		return attachment, ok, err
	}
	s.attachment = attachment
	s.err = sensitiveChatAttachmentStoreError("get", attachment)
	return chatattachments.StoredAttachment{}, false, s.err
}

func (s *sensitiveAttachmentDeleteErrorStore) DeleteDraft(ctx context.Context, sessionID, id string) error {
	attachment, ok, err := s.Store.Get(ctx, sessionID, id)
	if err != nil {
		return err
	}
	if !ok {
		return chatattachments.ErrNotFound
	}
	s.attachment = attachment
	s.err = sensitiveChatAttachmentStoreError("delete", attachment)
	return s.err
}

func sensitiveChatAttachmentStoreError(operation string, attachment chatattachments.StoredAttachment) error {
	return fmt.Errorf(
		"%s failed: session_id=%s attachment_id=%s filename=%s digest=%s body=%x sql=%s dsn=%s",
		operation,
		attachment.SessionID,
		attachment.ID,
		attachment.Filename,
		attachment.SHA256,
		attachment.Data,
		chatAttachmentSensitiveSQL,
		chatAttachmentSensitiveDSN,
	)
}

func (s *failFirstChatAttachmentSessionDeleteStore) DeleteBySessionID(ctx context.Context, sessionID string) error {
	s.deleteCalls++
	if s.deleteCalls == 1 {
		return s.deleteErr
	}
	return s.Store.DeleteBySessionID(ctx, sessionID)
}

func (s *ownerDeletingChatAttachmentStore) Create(ctx context.Context, attachment chatattachments.StoredAttachment) (chatattachments.StoredAttachment, error) {
	created, err := s.Store.Create(ctx, attachment)
	if err != nil {
		return chatattachments.StoredAttachment{}, err
	}
	s.created = created
	if err := s.sessions.Delete(ctx, attachment.SessionID); err != nil {
		return chatattachments.StoredAttachment{}, err
	}
	return created, nil
}

func (s *ownerDeletingChatAttachmentStore) DeleteDraft(context.Context, string, string) error {
	s.rollbackMessage = fmt.Sprintf(
		"rollback failed: id=%s filename=%s digest=%s body=%x",
		s.created.ID,
		s.created.Filename,
		s.created.SHA256,
		s.created.Data,
	)
	return errors.New(s.rollbackMessage)
}

func (s *countingCreateChatAttachmentStore) Create(ctx context.Context, attachment chatattachments.StoredAttachment) (chatattachments.StoredAttachment, error) {
	s.creates.Add(1)
	return s.Store.Create(ctx, attachment)
}

func (s *blockingCreateChatAttachmentStore) Create(ctx context.Context, attachment chatattachments.StoredAttachment) (chatattachments.StoredAttachment, error) {
	s.createOnce.Do(func() { close(s.createStarted) })
	// This store intentionally ignores cancellation while blocked. The session
	// lifecycle guard, rather than cooperative storage cancellation, must keep
	// destructive cleanup behind an already-admitted persistence operation.
	<-s.allowCreate
	return s.Store.Create(ctx, attachment)
}

func (s *blockingCreateChatAttachmentStore) DeleteBySessionID(ctx context.Context, sessionID string) error {
	s.deleteOnce.Do(func() { close(s.deleteStarted) })
	return s.Store.DeleteBySessionID(ctx, sessionID)
}

func (s *blockingDeleteDraftChatAttachmentStore) DeleteDraft(ctx context.Context, sessionID, id string) error {
	s.deleteCalls.Add(1)
	s.deleteOnce.Do(func() { close(s.deleteStarted) })
	// Deliberately ignore cancellation while blocked. The lifecycle operation,
	// rather than cooperative storage cancellation, must keep session teardown
	// behind an already-admitted draft deletion.
	<-s.allowDelete
	return s.Store.DeleteDraft(ctx, sessionID, id)
}

func (s *blockingDeleteDraftChatAttachmentStore) release() {
	s.releaseOnce.Do(func() { close(s.allowDelete) })
}

func (b *gatedChatAttachmentRequestBody) Read(p []byte) (int, error) {
	b.startOnce.Do(func() {
		close(b.readStarted)
		<-b.allowRead
	})
	return b.reader.Read(p)
}

func (b *gatedChatAttachmentRequestBody) Close() error { return nil }

func (b *gatedChatAttachmentRequestBody) release() {
	b.releaseOnce.Do(func() { close(b.allowRead) })
}

func TestChatAttachmentHTTP_UploadAndContentAreAuthenticatedAndSessionScoped(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	payload := validChatAttachmentPNG(t)
	recorder := fixture.upload(t, "chat_images", "diagram.png", "image/png", payload)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d, body=%s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	response := decodeChatAttachmentResponse(t, recorder)
	digest := sha256.Sum256(payload)
	if response.Object != "chat_attachment" {
		t.Fatalf("object = %q, want chat_attachment", response.Object)
	}
	if response.Data.ID == "" || !strings.HasPrefix(response.Data.ID, "att_") {
		t.Fatalf("attachment id = %q, want att_ prefix", response.Data.ID)
	}
	if response.Data.SessionID != "chat_images" || response.Data.Filename != "diagram.png" {
		t.Fatalf("attachment owner/name = %q/%q", response.Data.SessionID, response.Data.Filename)
	}
	if response.Data.MediaType != "image/png" || response.Data.SizeBytes != int64(len(payload)) {
		t.Fatalf("attachment media/size = %q/%d", response.Data.MediaType, response.Data.SizeBytes)
	}
	if response.Data.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("attachment sha256 = %q, want %x", response.Data.SHA256, digest)
	}
	if _, err := time.Parse(time.RFC3339Nano, response.Data.CreatedAt); err != nil {
		t.Fatalf("created_at = %q: %v", response.Data.CreatedAt, err)
	}
	expectedContentURL := "/hecate/v1/chat/sessions/chat_images/attachments/" + response.Data.ID + "/content"
	if response.Data.ContentURL != expectedContentURL {
		t.Fatalf("content_url = %q, want %q", response.Data.ContentURL, expectedContentURL)
	}
	if bytes.Contains(recorder.Body.Bytes(), payload) {
		t.Fatal("upload response contains raw attachment bytes")
	}

	unauthenticated := fixture.request(http.MethodGet, response.Data.ContentURL, nil, "", "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated content status = %d, want %d", unauthenticated.Code, http.StatusUnauthorized)
	}
	if bytes.Equal(unauthenticated.Body.Bytes(), payload) {
		t.Fatal("unauthenticated content response exposed attachment bytes")
	}

	content := fixture.request(http.MethodGet, response.Data.ContentURL, nil, "", chatAttachmentTestRuntimeToken)
	if content.Code != http.StatusOK {
		t.Fatalf("content status = %d, want %d, body=%s", content.Code, http.StatusOK, content.Body.String())
	}
	if !bytes.Equal(content.Body.Bytes(), payload) {
		t.Fatalf("content body length = %d, want %d", content.Body.Len(), len(payload))
	}
	if got := content.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", got)
	}
	if got := content.Header().Get("Content-Length"); got != strconv.Itoa(len(payload)) {
		t.Fatalf("Content-Length = %q, want %d", got, len(payload))
	}
	disposition, params, err := mime.ParseMediaType(content.Header().Get("Content-Disposition"))
	if err != nil || disposition != "inline" || params["filename"] != "diagram.png" {
		t.Fatalf("Content-Disposition = %q, parsed=%q params=%v err=%v", content.Header().Get("Content-Disposition"), disposition, params, err)
	}
	if got := content.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := content.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}

	crossSessionURL := "/hecate/v1/chat/sessions/chat_other/attachments/" + response.Data.ID + "/content"
	crossSession := fixture.request(http.MethodGet, crossSessionURL, nil, "", chatAttachmentTestRuntimeToken)
	assertChatAttachmentError(t, crossSession, http.StatusNotFound, errCodeAttachmentNotFound, "not found")
	crossDelete := fixture.request(
		http.MethodDelete,
		"/hecate/v1/chat/sessions/chat_other/attachments/"+response.Data.ID,
		nil,
		"",
		chatAttachmentTestRuntimeToken,
	)
	assertChatAttachmentError(t, crossDelete, http.StatusNotFound, errCodeAttachmentNotFound, "not found")
	content = fixture.request(http.MethodGet, response.Data.ContentURL, nil, "", chatAttachmentTestRuntimeToken)
	if content.Code != http.StatusOK {
		t.Fatalf("owner content after cross-session delete status = %d, want %d", content.Code, http.StatusOK)
	}

	deleted := fixture.request(
		http.MethodDelete,
		"/hecate/v1/chat/sessions/chat_images/attachments/"+response.Data.ID,
		nil,
		"",
		chatAttachmentTestRuntimeToken,
	)
	if deleted.Code != http.StatusNoContent || deleted.Body.Len() != 0 {
		t.Fatalf("draft delete status/body = %d/%q, want 204 with empty body", deleted.Code, deleted.Body.String())
	}
	missing := fixture.request(http.MethodGet, response.Data.ContentURL, nil, "", chatAttachmentTestRuntimeToken)
	assertChatAttachmentError(t, missing, http.StatusNotFound, errCodeAttachmentNotFound, "not found")
}

func TestChatAttachmentHTTP_ContentAdmissionRejectsBeforeStoreRead(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	payload := validChatAttachmentPNG(t)
	upload := decodeChatAttachmentResponse(t, fixture.upload(t, "chat_images", "busy-content.png", "image/png", payload))
	store := &countingGetChatAttachmentStore{Store: fixture.handler.chatAttachments}
	fixture.handler.SetChatAttachmentStore(store)
	admission := newChatAttachmentContentAdmission(1)
	fixture.handler.chatAttachmentContentAdmission = admission
	if !admission.TryAcquire() {
		t.Fatal("failed to occupy content admission permit")
	}

	busy := fixture.request(http.MethodGet, upload.Data.ContentURL, nil, "", chatAttachmentTestRuntimeToken)
	assertChatAttachmentError(t, busy, http.StatusTooManyRequests, errCodeAttachmentContentBusy, "capacity is busy")
	if got := busy.Header().Get("Retry-After"); got != strconv.Itoa(chatAttachmentContentRetryAfter) {
		t.Fatalf("Retry-After = %q, want %d", got, chatAttachmentContentRetryAfter)
	}
	var response struct {
		Error struct {
			MaxConcurrentDownloads float64 `json:"max_concurrent_downloads"`
			RetryAfterSeconds      float64 `json:"retry_after_seconds"`
		} `json:"error"`
	}
	if err := json.Unmarshal(busy.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode busy response: %v", err)
	}
	if response.Error.MaxConcurrentDownloads != maxConcurrentChatAttachmentContentReads ||
		response.Error.RetryAfterSeconds != chatAttachmentContentRetryAfter {
		t.Fatalf("busy fields = %+v", response.Error)
	}
	if got := store.gets.Load(); got != 0 {
		t.Fatalf("attachment Get calls while saturated = %d, want zero", got)
	}

	admission.Release()
	content := fixture.request(http.MethodGet, upload.Data.ContentURL, nil, "", chatAttachmentTestRuntimeToken)
	if content.Code != http.StatusOK || !bytes.Equal(content.Body.Bytes(), payload) {
		t.Fatalf("content after permit release = status %d body length %d", content.Code, content.Body.Len())
	}
	if got := store.gets.Load(); got != 1 {
		t.Fatalf("attachment Get calls after release = %d, want one", got)
	}
}

func TestChatAttachmentHTTP_ContentWriteDeadlineReleasesAdmission(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	upload := decodeChatAttachmentResponse(t, fixture.upload(t, "chat_images", "deadline-content.png", "image/png", validChatAttachmentPNG(t)))
	admission := newChatAttachmentContentAdmission(1)
	fixture.handler.chatAttachmentContentAdmission = admission
	fixture.handler.chatAttachmentContentWriteTimeout = 20 * time.Millisecond
	w := newDeadlineBlockingAttachmentResponseWriter()
	r := httptest.NewRequest(http.MethodGet, upload.Data.ContentURL, nil)
	r.SetPathValue("id", upload.Data.SessionID)
	r.SetPathValue("attachment_id", upload.Data.ID)
	done := make(chan struct{})
	go func() {
		fixture.handler.HandleChatAttachmentContent(w, r)
		close(done)
	}()

	select {
	case <-w.deadlineSet:
	case <-time.After(time.Second):
		t.Fatal("content handler did not set its route-local write deadline")
	}
	select {
	case <-w.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("content handler did not start the blocked body write")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("content handler did not return after its write deadline")
	}
	if w.status != http.StatusOK {
		t.Fatalf("content status = %d, want 200 before write timeout", w.status)
	}
	if !admission.TryAcquire() {
		t.Fatal("content admission permit remained held after write deadline")
	}
	admission.Release()
}

func TestChatAttachmentHTTP_ContentFailsBeforeSuccessWhenWriteDeadlineIsUnsupported(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	upload := decodeChatAttachmentResponse(t, fixture.upload(t, "chat_images", "unsupported-deadline.png", "image/png", validChatAttachmentPNG(t)))
	admission := newChatAttachmentContentAdmission(1)
	fixture.handler.chatAttachmentContentAdmission = admission
	w := &deadlineRejectingAttachmentResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	r := httptest.NewRequest(http.MethodGet, upload.Data.ContentURL, nil)
	r.SetPathValue("id", upload.Data.SessionID)
	r.SetPathValue("attachment_id", upload.Data.ID)

	fixture.handler.HandleChatAttachmentContent(w, r)

	assertChatAttachmentError(t, w.ResponseRecorder, http.StatusInternalServerError, errCodeGatewayError, chatAttachmentContentFailureMessage)
	if got := w.Header().Get("Connection"); got != "close" {
		t.Fatalf("Connection = %q, want close", got)
	}
	if got := w.Header().Get("Content-Type"); strings.HasPrefix(got, "image/") {
		t.Fatalf("content response committed image media type after deadline failure: %q", got)
	}
	if !admission.TryAcquire() {
		t.Fatal("content admission permit remained held after deadline setup failure")
	}
	admission.Release()
}

func TestChatAttachmentHTTP_SessionDeleteWaitsForBlockedContentWrite(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	upload := decodeChatAttachmentResponse(t, fixture.upload(t, "chat_images", "blocked-content.png", "image/png", validChatAttachmentPNG(t)))
	fixture.handler.chatAttachmentContentWriteTimeout = time.Hour
	w := newBlockingAttachmentResponseWriter()
	defer w.release()
	r := httptest.NewRequest(http.MethodGet, upload.Data.ContentURL, nil)
	r.SetPathValue("id", upload.Data.SessionID)
	r.SetPathValue("attachment_id", upload.Data.ID)
	contentDone := make(chan struct{})
	go func() {
		fixture.handler.HandleChatAttachmentContent(w, r)
		close(contentDone)
	}()
	select {
	case <-w.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("content handler did not reach the blocked body write")
	}

	deleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		deleteDone <- fixture.request(
			http.MethodDelete,
			"/hecate/v1/chat/sessions/"+upload.Data.SessionID,
			nil,
			"",
			chatAttachmentTestRuntimeToken,
		)
	}()
	waitForAgentChatLifecycleClosure(t, fixture.handler.agentChatLive, upload.Data.SessionID)
	select {
	case response := <-deleteDone:
		t.Fatalf("delete completed during blocked content write: status=%d body=%s", response.Code, response.Body.String())
	default:
	}
	if _, ok, err := fixture.handler.chatAttachments.Get(context.Background(), upload.Data.SessionID, upload.Data.ID); err != nil || !ok {
		t.Fatalf("attachment during blocked write = found %v, error %v; want retained", ok, err)
	}

	w.release()
	select {
	case <-contentDone:
	case <-time.After(time.Second):
		t.Fatal("content handler did not finish after body write was released")
	}
	var deleted *httptest.ResponseRecorder
	select {
	case deleted = <-deleteDone:
	case <-time.After(3 * time.Second):
		t.Fatal("delete did not finish after content write released")
	}
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204, body=%s", deleted.Code, deleted.Body.String())
	}
	if _, ok, err := fixture.handler.chatAttachments.Get(context.Background(), upload.Data.SessionID, upload.Data.ID); err != nil || ok {
		t.Fatalf("attachment after delete = found %v, error %v; want removed", ok, err)
	}
}

func TestChatAttachmentHTTP_SessionDeleteClosesDraftDeleteAdmissionAndDrainsAdmittedDelete(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	upload := decodeChatAttachmentResponse(t, fixture.upload(t, "chat_images", "blocked-delete.png", "image/png", validChatAttachmentPNG(t)))
	attachments := &blockingDeleteDraftChatAttachmentStore{
		Store:         fixture.handler.chatAttachments,
		deleteStarted: make(chan struct{}),
		allowDelete:   make(chan struct{}),
	}
	defer attachments.release()
	fixture.handler.SetChatAttachmentStore(attachments)
	deleteURL := "/hecate/v1/chat/sessions/" + upload.Data.SessionID + "/attachments/" + upload.Data.ID

	admittedDeleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		admittedDeleteDone <- fixture.request(http.MethodDelete, deleteURL, nil, "", chatAttachmentTestRuntimeToken)
	}()
	select {
	case <-attachments.deleteStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("attachment delete did not reach the cancellation-ignoring DeleteDraft")
	}

	sessionDeleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		sessionDeleteDone <- fixture.request(
			http.MethodDelete,
			"/hecate/v1/chat/sessions/"+upload.Data.SessionID,
			nil,
			"",
			chatAttachmentTestRuntimeToken,
		)
	}()
	waitForAgentChatLifecycleClosure(t, fixture.handler.agentChatLive, upload.Data.SessionID)
	select {
	case response := <-sessionDeleteDone:
		t.Fatalf("session delete completed before the admitted draft delete released: status=%d body=%s", response.Code, response.Body.String())
	default:
	}

	delayedDelete := fixture.request(http.MethodDelete, deleteURL, nil, "", chatAttachmentTestRuntimeToken)
	assertChatAttachmentError(t, delayedDelete, http.StatusConflict, errCodeSessionStopping, "still stopping")
	if got := attachments.deleteCalls.Load(); got != 1 {
		t.Fatalf("DeleteDraft calls after lifecycle closure = %d, want one admitted call", got)
	}
	if _, ok, err := attachments.Get(context.Background(), upload.Data.SessionID, upload.Data.ID); err != nil || !ok {
		t.Fatalf("attachment before admitted delete release = found %v, error %v; want retained", ok, err)
	}

	attachments.release()
	var admittedDelete *httptest.ResponseRecorder
	select {
	case admittedDelete = <-admittedDeleteDone:
	case <-time.After(3 * time.Second):
		t.Fatal("admitted attachment delete did not finish after DeleteDraft released")
	}
	if admittedDelete.Code != http.StatusNoContent || admittedDelete.Body.Len() != 0 {
		t.Fatalf("admitted attachment delete status/body = %d/%q, want 204 with empty body", admittedDelete.Code, admittedDelete.Body.String())
	}

	var sessionDelete *httptest.ResponseRecorder
	select {
	case sessionDelete = <-sessionDeleteDone:
	case <-time.After(3 * time.Second):
		t.Fatal("session delete did not finish after the admitted draft delete drained")
	}
	if sessionDelete.Code != http.StatusNoContent || sessionDelete.Body.Len() != 0 {
		t.Fatalf("session delete status/body = %d/%q, want 204 with empty body", sessionDelete.Code, sessionDelete.Body.String())
	}
	if _, ok, err := attachments.Get(context.Background(), upload.Data.SessionID, upload.Data.ID); err != nil || ok {
		t.Fatalf("attachment after session delete = found %v, error %v; want removed", ok, err)
	}
}

func TestChatAttachmentHTTP_RejectsInvalidHecateUploadsAndAcceptsExternalInputs(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	validPNG := validChatAttachmentPNG(t)

	tests := []struct {
		name         string
		filename     string
		declaredType string
		data         []byte
		wantStatus   int
		wantCode     string
		wantMessage  string
	}{
		{
			name:         "unsupported",
			filename:     "notes.txt",
			declaredType: "text/plain",
			data:         []byte("not an image"),
			wantStatus:   http.StatusUnprocessableEntity,
			wantCode:     errCodeAttachmentUnsupported,
			wantMessage:  "only PNG, JPEG, and WebP",
		},
		{
			name:         "declared type mismatch",
			filename:     "mismatch.jpg",
			declaredType: "image/jpeg",
			data:         validPNG,
			wantStatus:   http.StatusUnprocessableEntity,
			wantCode:     errCodeAttachmentUnsupported,
			wantMessage:  "does not match",
		},
		{
			name:         "malformed image",
			filename:     "broken.png",
			declaredType: "image/png",
			data:         []byte("\x89PNG\r\n\x1a\nnot-a-valid-png"),
			wantStatus:   http.StatusUnprocessableEntity,
			wantCode:     errCodeAttachmentUnsupported,
			wantMessage:  "malformed",
		},
		{
			name:         "truncated image with valid header",
			filename:     "truncated.png",
			declaredType: "image/png",
			data:         validPNG[:len(validPNG)-8],
			wantStatus:   http.StatusUnprocessableEntity,
			wantCode:     errCodeAttachmentUnsupported,
			wantMessage:  "malformed",
		},
		{
			name:         "oversize",
			filename:     "large.png",
			declaredType: "image/png",
			data:         bytes.Repeat([]byte{0}, int(maxChatImageAttachmentBytes+1)),
			wantStatus:   http.StatusRequestEntityTooLarge,
			wantCode:     errCodeAttachmentTooLarge,
			wantMessage:  "5 MiB",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := fixture.upload(t, "chat_images", tt.filename, tt.declaredType, tt.data)
			assertChatAttachmentError(t, recorder, tt.wantStatus, tt.wantCode, tt.wantMessage)
		})
	}

	malformedMultipart := fixture.request(
		http.MethodPost,
		"/hecate/v1/chat/sessions/chat_images/attachments",
		strings.NewReader("not a multipart body"),
		"multipart/form-data",
		chatAttachmentTestRuntimeToken,
	)
	assertChatAttachmentError(t, malformedMultipart, http.StatusUnprocessableEntity, errCodeAttachmentInvalid, "invalid multipart")

	externalImage := fixture.upload(t, "chat_external", "diagram.png", "application/octet-stream", validPNG)
	if externalImage.Code != http.StatusCreated {
		t.Fatalf("external image status = %d, want 201, body=%s", externalImage.Code, externalImage.Body.String())
	}
	if got := decodeChatAttachmentResponse(t, externalImage).Data.MediaType; got != "image/png" {
		t.Fatalf("external image media type = %q, want image/png", got)
	}

	externalMismatchedImage := fixture.upload(t, "chat_external", "diagram.jpg", "image/jpeg", validPNG)
	if externalMismatchedImage.Code != http.StatusCreated {
		t.Fatalf("external mismatched image status = %d, want 201, body=%s", externalMismatchedImage.Code, externalMismatchedImage.Body.String())
	}
	if got := decodeChatAttachmentResponse(t, externalMismatchedImage).Data.MediaType; got != "image/png" {
		t.Fatalf("external mismatched image media type = %q, want image/png", got)
	}

	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "malformed", data: []byte("\x89PNG\r\n\x1a\nnot-a-valid-png")},
		{name: "over-dimension", data: chatAttachmentPNGHeader(t, maxChatImageDimension+1, 1)},
	} {
		t.Run("external opaque "+test.name+" raster", func(t *testing.T) {
			recorder := fixture.upload(t, "chat_external", test.name+".png", "image/png", test.data)
			if recorder.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201, body=%s", recorder.Code, recorder.Body.String())
			}
			if got := decodeChatAttachmentResponse(t, recorder).Data.MediaType; got != "application/octet-stream" {
				t.Fatalf("media type = %q, want application/octet-stream", got)
			}
		})
	}

	externalFile := fixture.upload(t, "chat_external", "notes.json", "application/json", []byte(`{"private":true}`))
	if externalFile.Code != http.StatusCreated {
		t.Fatalf("external file status = %d, want 201, body=%s", externalFile.Code, externalFile.Body.String())
	}
	file := decodeChatAttachmentResponse(t, externalFile)
	content := fixture.request(http.MethodGet, file.Data.ContentURL, nil, "", chatAttachmentTestRuntimeToken)
	if content.Code != http.StatusOK || content.Body.String() != `{"private":true}` {
		t.Fatalf("external file content status=%d body=%q", content.Code, content.Body.String())
	}
	if got := content.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Fatalf("external file Content-Disposition = %q", got)
	}
	if got := content.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("external file X-Content-Type-Options = %q", got)
	}
	if got := content.Header().Get("Content-Security-Policy"); got != "sandbox; default-src 'none'" {
		t.Fatalf("external file Content-Security-Policy = %q", got)
	}
}

func TestNormalizeChatAttachmentFilenameUsesSafeMediaDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		filename  string
		mediaType string
		want      string
	}{
		{name: "empty PNG", mediaType: "image/png", want: "image.png"},
		{name: "dot path", filename: ".", mediaType: "image/jpeg", want: "image.jpg"},
		{name: "parent path", filename: "..", mediaType: "text/plain", want: "attachment.txt"},
		{name: "nested path", filename: "../notes.json", mediaType: "application/json", want: "notes.json"},
		{name: "unknown media", mediaType: "application/octet-stream", want: "attachment.bin"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeChatAttachmentFilename(test.filename, test.mediaType); got != test.want {
				t.Fatalf("normalizeChatAttachmentFilename(%q, %q) = %q, want %q", test.filename, test.mediaType, got, test.want)
			}
		})
	}
}

func TestExternalAgentMessageClaimsAndDispatchesPrivateAttachmentInput(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	workspace := t.TempDir()
	if _, err := fixture.handler.agentChat.UpdateSession(context.Background(), "chat_external", func(session *chat.Session) {
		session.Workspace = workspace
	}); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	runner := &fakeAgentChatRunner{output: "inspected"}
	fixture.handler.SetAgentChatRunner(runner)
	upload := fixture.upload(t, "chat_external", "notes.json", "application/json", []byte(`{"secret":"selected"}`))
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body=%s", upload.Code, upload.Body.String())
	}
	attachment := decodeChatAttachmentResponse(t, upload)
	body := fmt.Sprintf(`{"content":"inspect this file","execution_mode":"external_agent","client_request_id":"external-file-1","attachment_ids":[%q]}`, attachment.Data.ID)
	response := fixture.request(
		http.MethodPost,
		"/hecate/v1/chat/sessions/chat_external/messages",
		strings.NewReader(body),
		"application/json",
		chatAttachmentTestRuntimeToken,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("message status = %d, want 200, body=%s", response.Code, response.Body.String())
	}
	for range maxConcurrentExternalFileTurns {
		if !fixture.handler.chatExternalFileTurnAdmission.TryAcquire() {
			t.Fatal("external file turn permit remained held after runner return")
		}
	}
	for range maxConcurrentExternalFileTurns {
		fixture.handler.chatExternalFileTurnAdmission.Release()
	}
	if strings.Contains(response.Body.String(), `"secret":"selected"`) {
		t.Fatal("chat response exposed external attachment bytes")
	}
	if len(runner.runRequests) != 1 {
		t.Fatalf("runner requests = %d, want 1", len(runner.runRequests))
	}
	prompt := runner.runRequests[0].Prompt
	if prompt.Text != "inspect this file" || len(prompt.Files) != 1 {
		t.Fatalf("runner prompt = %#v", prompt)
	}
	file := prompt.Files[0]
	if file.Filename != "notes.json" || file.MediaType != "application/json" || string(file.Data) != `{"secret":"selected"}` {
		t.Fatalf("runner file = %#v", file)
	}
	var session ChatSessionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode chat response: %v", err)
	}
	if len(session.Data.Messages) != 2 || len(session.Data.Messages[0].Attachments) != 1 {
		t.Fatalf("transcript messages = %#v", session.Data.Messages)
	}
	replay := fixture.request(
		http.MethodPost,
		"/hecate/v1/chat/sessions/chat_external/messages",
		strings.NewReader(body),
		"application/json",
		chatAttachmentTestRuntimeToken,
	)
	if replay.Code != http.StatusOK || len(runner.runRequests) != 1 {
		t.Fatalf("replay status=%d runner requests=%d body=%s", replay.Code, len(runner.runRequests), replay.Body.String())
	}
	deleteDraft := fixture.request(
		http.MethodDelete,
		"/hecate/v1/chat/sessions/chat_external/attachments/"+attachment.Data.ID,
		nil,
		"",
		chatAttachmentTestRuntimeToken,
	)
	assertChatAttachmentError(t, deleteDraft, http.StatusConflict, errCodeAttachmentInUse, "already used")
}

func TestExternalAgentMessageReleasesClaimAfterAttachmentIntegrityFailure(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	if _, err := fixture.handler.agentChat.UpdateSession(context.Background(), "chat_external", func(session *chat.Session) {
		session.Workspace = t.TempDir()
	}); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	runner := &fakeAgentChatRunner{output: "must not run"}
	fixture.handler.SetAgentChatRunner(runner)
	upload := fixture.upload(t, "chat_external", "notes.txt", "text/plain", []byte("private input"))
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body=%s", upload.Code, upload.Body.String())
	}
	attachment := decodeChatAttachmentResponse(t, upload)
	fixture.handler.SetChatAttachmentStore(corruptAttachmentClaimStore{Store: fixture.handler.chatAttachments})

	response := fixture.request(
		http.MethodPost,
		"/hecate/v1/chat/sessions/chat_external/messages",
		strings.NewReader(fmt.Sprintf(`{"execution_mode":"external_agent","attachment_ids":[%q]}`, attachment.Data.ID)),
		"application/json",
		chatAttachmentTestRuntimeToken,
	)
	assertChatAttachmentError(t, response, http.StatusInternalServerError, errCodeGatewayError, "integrity validation")
	if len(runner.runRequests) != 0 {
		t.Fatalf("runner requests = %d, want no dispatch", len(runner.runRequests))
	}

	deleted := fixture.request(
		http.MethodDelete,
		"/hecate/v1/chat/sessions/chat_external/attachments/"+attachment.Data.ID,
		nil,
		"",
		chatAttachmentTestRuntimeToken,
	)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete released draft status = %d, want 204, body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestExternalAgentMessageSaturatedFileTurnAdmissionRejectsBeforeClaim(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	if _, err := fixture.handler.agentChat.UpdateSession(context.Background(), "chat_external", func(session *chat.Session) {
		session.Workspace = t.TempDir()
	}); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	runner := &fakeAgentChatRunner{output: "must not run"}
	fixture.handler.SetAgentChatRunner(runner)
	admission := newChatExternalFileTurnAdmission(maxConcurrentExternalFileTurns)
	fixture.handler.chatExternalFileTurnAdmission = admission
	for range maxConcurrentExternalFileTurns {
		if !admission.TryAcquire() {
			t.Fatal("failed to hold external file turn admission permit")
		}
		defer admission.Release()
	}
	upload := fixture.upload(t, "chat_external", "notes.txt", "text/plain", []byte("private input"))
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body=%s", upload.Code, upload.Body.String())
	}
	attachment := decodeChatAttachmentResponse(t, upload)

	response := fixture.request(
		http.MethodPost,
		"/hecate/v1/chat/sessions/chat_external/messages",
		strings.NewReader(fmt.Sprintf(`{"execution_mode":"external_agent","attachment_ids":[%q]}`, attachment.Data.ID)),
		"application/json",
		chatAttachmentTestRuntimeToken,
	)
	assertChatAttachmentError(t, response, http.StatusTooManyRequests, errCodeExternalFileTurnBusy, "capacity is busy")
	if response.Header().Get("Retry-After") != strconv.Itoa(chatExternalFileTurnRetryAfter) {
		t.Fatalf("Retry-After = %q", response.Header().Get("Retry-After"))
	}
	if !strings.Contains(response.Body.String(), `"max_concurrent_external_file_turns":2`) {
		t.Fatalf("busy response missing typed capacity: %s", response.Body.String())
	}
	if len(runner.runRequests) != 0 {
		t.Fatalf("runner requests = %d, want no dispatch", len(runner.runRequests))
	}
	session, ok, err := fixture.handler.agentChat.Get(context.Background(), "chat_external")
	if err != nil || !ok || len(session.Messages) != 0 {
		t.Fatalf("session after rejection = found %v, messages %d, error %v", ok, len(session.Messages), err)
	}
	deleted := fixture.request(
		http.MethodDelete,
		"/hecate/v1/chat/sessions/chat_external/attachments/"+attachment.Data.ID,
		nil,
		"",
		chatAttachmentTestRuntimeToken,
	)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete unclaimed draft status = %d, want 204, body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestChatAttachmentHTTP_SaturatedUploadAdmissionRejectsBeforeReadingBody(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	admission := newChatImageUploadAdmission(maxConcurrentChatImageUploads)
	fixture.handler.chatImageUploadAdmission = admission
	for range maxConcurrentChatImageUploads {
		if !admission.TryAcquire() {
			t.Fatal("failed to hold chat image upload admission permit")
		}
	}

	body := &chatAttachmentReadSpy{}
	recorder := fixture.request(
		http.MethodPost,
		"/hecate/v1/chat/sessions/chat_images/attachments",
		body,
		"multipart/form-data; boundary=unused",
		chatAttachmentTestRuntimeToken,
	)
	assertChatAttachmentError(t, recorder, http.StatusTooManyRequests, errCodeAttachmentUploadBusy, "capacity is busy")
	if got := recorder.Header().Get("Retry-After"); got != strconv.Itoa(chatImageUploadRetryAfter) {
		t.Fatalf("Retry-After = %q, want %d", got, chatImageUploadRetryAfter)
	}
	if got := body.reads.Load(); got != 0 {
		t.Fatalf("request body reads = %d, want 0", got)
	}
	var response struct {
		Error struct {
			MaxConcurrentUploads float64 `json:"max_concurrent_uploads"`
			RetryAfterSeconds    float64 `json:"retry_after_seconds"`
			UserMessage          string  `json:"user_message"`
			OperatorAction       string  `json:"operator_action"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode saturation response: %v", err)
	}
	if response.Error.MaxConcurrentUploads != maxConcurrentChatImageUploads || response.Error.RetryAfterSeconds != chatImageUploadRetryAfter {
		t.Fatalf("saturation fields = %#v", response.Error)
	}
	if response.Error.UserMessage == "" || response.Error.OperatorAction == "" {
		t.Fatalf("saturation operator metadata = %#v", response.Error)
	}
}

func TestChatAttachmentHTTP_HTTP1EarlyRejectsCloseStalledBodiesWithErrorEnvelope(t *testing.T) {
	tests := []struct {
		name       string
		sessionID  string
		configure  func(*testing.T, *Handler)
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing session",
			sessionID:  "chat_missing",
			wantStatus: http.StatusNotFound,
			wantCode:   errCodeNotFound,
		},
		{
			name:      "busy admission",
			sessionID: "chat_images",
			configure: func(t *testing.T, handler *Handler) {
				t.Helper()
				admission := newChatImageUploadAdmission(maxConcurrentChatImageUploads)
				for range maxConcurrentChatImageUploads {
					if !admission.TryAcquire() {
						t.Fatal("failed to occupy upload admission")
					}
				}
				handler.chatImageUploadAdmission = admission
			},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   errCodeAttachmentUploadBusy,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newChatAttachmentHTTPFixture(t)
			fixture.handler.chatImageUploadReadTimeout = 2 * time.Second
			if test.configure != nil {
				test.configure(t, fixture.handler)
			}
			server := httptest.NewServer(fixture.server)
			t.Cleanup(server.Close)

			connection, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatalf("dial stalled upload: %v", err)
			}
			t.Cleanup(func() { _ = connection.Close() })
			request := fmt.Sprintf(
				"POST /hecate/v1/chat/sessions/%s/attachments HTTP/1.1\r\n"+
					"Host: %s\r\n"+
					"X-Hecate-Runtime-Token: %s\r\n"+
					"Content-Type: multipart/form-data; boundary=stalled\r\n"+
					"Content-Length: 4096\r\n\r\n--stalled\r\n",
				test.sessionID,
				server.Listener.Addr().String(),
				chatAttachmentTestRuntimeToken,
			)
			started := time.Now()
			if _, err := io.WriteString(connection, request); err != nil {
				t.Fatalf("write stalled upload: %v", err)
			}
			if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatalf("set client read deadline: %v", err)
			}
			response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
			if err != nil {
				t.Fatalf("read stalled upload response: %v", err)
			}
			defer response.Body.Close()
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("early reject took %v, want response before the body deadline", elapsed)
			}
			var payload chatAttachmentErrorResponse
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				t.Fatalf("decode early-reject envelope: %v", err)
			}
			if response.StatusCode != test.wantStatus || payload.Error.Type != test.wantCode {
				t.Fatalf("response = status %d error %#v, want %d/%s", response.StatusCode, payload.Error, test.wantStatus, test.wantCode)
			}
			if !response.Close {
				t.Fatal("HTTP/1 early reject did not close its unread request connection")
			}
		})
	}
}

func TestChatAttachmentHTTP_HTTP1FinalBoundaryWithStalledChunkedEpilogueTimesOut(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	fixture.handler.chatImageUploadAdmission = newChatImageUploadAdmission(1)
	const readTimeout = 500 * time.Millisecond
	fixture.handler.chatImageUploadReadTimeout = readTimeout
	server := httptest.NewServer(fixture.server)
	t.Cleanup(server.Close)

	body, contentType := chatAttachmentMultipartBody(t, "stalled-epilogue.png", "image/png", validChatAttachmentPNG(t))
	connection, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial stalled chunked upload: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	request := fmt.Sprintf(
		"POST /hecate/v1/chat/sessions/chat_images/attachments HTTP/1.1\r\n"+
			"Host: %s\r\n"+
			"X-Hecate-Runtime-Token: %s\r\n"+
			"Content-Type: %s\r\n"+
			"Transfer-Encoding: chunked\r\n\r\n",
		server.Listener.Addr().String(),
		chatAttachmentTestRuntimeToken,
		contentType,
	)
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatalf("write stalled chunked upload headers: %v", err)
	}
	if err := writeChatAttachmentHTTPChunk(connection, body); err != nil {
		t.Fatalf("write stalled chunked multipart body: %v", err)
	}
	if _, err := io.WriteString(connection, "1000\r\nstalled-epilogue"); err != nil {
		t.Fatalf("write partial chunked multipart epilogue: %v", err)
	}
	// The multipart body is complete, but the chunked epilogue deliberately
	// stalls partway through its declared chunk. The handler must keep the upload
	// within its route deadline instead of returning success at the final boundary.
	if err := connection.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set stalled chunked upload read deadline: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatalf("read stalled chunked upload response: %v", err)
	}
	defer response.Body.Close()
	var payload struct {
		Error struct {
			Type          string  `json:"type"`
			ReadTimeoutMS float64 `json:"read_timeout_ms"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode stalled chunked upload response: %v", err)
	}
	if response.StatusCode != http.StatusRequestTimeout || payload.Error.Type != errCodeAttachmentUploadTimeout {
		t.Fatalf("response = status %d error %#v, want %d/%s", response.StatusCode, payload.Error, http.StatusRequestTimeout, errCodeAttachmentUploadTimeout)
	}
	if payload.Error.ReadTimeoutMS != float64(readTimeout.Milliseconds()) {
		t.Fatalf("read_timeout_ms = %v, want %d", payload.Error.ReadTimeoutMS, readTimeout.Milliseconds())
	}
	if !response.Close {
		t.Fatal("HTTP/1 stalled chunked upload response did not close its request connection")
	}
	assertNoStoredChatAttachments(t, fixture.handler.chatAttachments, "chat_images")

	accepted := fixture.upload(t, "chat_images", "after-stalled-epilogue.png", "image/png", validChatAttachmentPNG(t))
	if accepted.Code != http.StatusCreated {
		t.Fatalf("upload after stalled epilogue status = %d, want %d, body=%s", accepted.Code, http.StatusCreated, accepted.Body.String())
	}
}

func TestChatAttachmentHTTP_HTTP1RejectsOversizedChunkedMultipartEpilogue(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	fixture.handler.chatImageUploadAdmission = newChatImageUploadAdmission(1)
	server := httptest.NewServer(fixture.server)
	t.Cleanup(server.Close)

	body, contentType := chatAttachmentMultipartBody(t, "oversized-epilogue.png", "image/png", validChatAttachmentPNG(t))
	epilogueSize := int(maxChatImageUploadBodyBytes) - len(body) + 1
	if epilogueSize <= 0 {
		t.Fatalf("multipart fixture size = %d, want less than request limit %d", len(body), maxChatImageUploadBodyBytes)
	}
	payload := make([]byte, len(body)+epilogueSize)
	copy(payload, body)
	for i := len(body); i < len(payload); i++ {
		payload[i] = 'x'
	}

	connection, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial oversized chunked upload: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	request := fmt.Sprintf(
		"POST /hecate/v1/chat/sessions/chat_images/attachments HTTP/1.1\r\n"+
			"Host: %s\r\n"+
			"X-Hecate-Runtime-Token: %s\r\n"+
			"Content-Type: %s\r\n"+
			"Transfer-Encoding: chunked\r\n\r\n",
		server.Listener.Addr().String(),
		chatAttachmentTestRuntimeToken,
		contentType,
	)
	if err := connection.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set oversized chunked upload write deadline: %v", err)
	}
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatalf("write oversized chunked upload headers: %v", err)
	}
	if err := writeChatAttachmentHTTPChunk(connection, payload); err != nil {
		t.Fatalf("write oversized chunked multipart body: %v", err)
	}
	// The body exceeds the decoded upload cap by one byte after a syntactically
	// complete multipart boundary. It does not need a chunked terminator: the
	// MaxBytesReader rejection must happen while draining the epilogue.
	if err := connection.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set oversized chunked upload read deadline: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatalf("read oversized chunked upload response: %v", err)
	}
	defer response.Body.Close()
	var responsePayload chatAttachmentErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&responsePayload); err != nil {
		t.Fatalf("decode oversized chunked upload response: %v", err)
	}
	if response.StatusCode != http.StatusRequestEntityTooLarge || responsePayload.Error.Type != errCodeAttachmentTooLarge {
		t.Fatalf("response = status %d error %#v, want %d/%s", response.StatusCode, responsePayload.Error, http.StatusRequestEntityTooLarge, errCodeAttachmentTooLarge)
	}
	if !response.Close {
		t.Fatal("HTTP/1 oversized chunked upload response did not close its request connection")
	}
	assertNoStoredChatAttachments(t, fixture.handler.chatAttachments, "chat_images")

	accepted := fixture.upload(t, "chat_images", "after-oversized-epilogue.png", "image/png", validChatAttachmentPNG(t))
	if accepted.Code != http.StatusCreated {
		t.Fatalf("upload after oversized epilogue status = %d, want %d, body=%s", accepted.Code, http.StatusCreated, accepted.Body.String())
	}
}

func TestChatAttachmentHTTP_UploadAdmissionReleasesAfterRejectedUpload(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	fixture.handler.chatImageUploadAdmission = newChatImageUploadAdmission(1)

	rejected := fixture.request(
		http.MethodPost,
		"/hecate/v1/chat/sessions/chat_images/attachments",
		strings.NewReader("not multipart"),
		"multipart/form-data; boundary=unused",
		chatAttachmentTestRuntimeToken,
	)
	assertChatAttachmentError(t, rejected, http.StatusUnprocessableEntity, errCodeAttachmentInvalid, "attachment is required")

	accepted := fixture.upload(t, "chat_images", "after-rejection.png", "image/png", validChatAttachmentPNG(t))
	if accepted.Code != http.StatusCreated {
		t.Fatalf("upload after rejected request status = %d, want %d, body=%s", accepted.Code, http.StatusCreated, accepted.Body.String())
	}
}

func TestChatAttachmentHTTP_StalledUploadBodiesTimeOutAndReleaseAdmission(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	const testReadTimeout = 500 * time.Millisecond
	fixture.handler.chatImageUploadReadTimeout = testReadTimeout

	bodies := []*blockingChatAttachmentReadCloser{
		newBlockingChatAttachmentReadCloser(),
		newBlockingChatAttachmentReadCloser(),
	}
	responses := make(chan *httptest.ResponseRecorder, len(bodies))
	for _, body := range bodies {
		go func() {
			responses <- fixture.request(
				http.MethodPost,
				"/hecate/v1/chat/sessions/chat_images/attachments",
				body,
				"multipart/form-data; boundary=stalled",
				chatAttachmentTestRuntimeToken,
			)
		}()
	}

	for i, body := range bodies {
		select {
		case <-body.started:
		case <-time.After(2 * time.Second):
			t.Fatalf("blocking body %d did not start reading", i)
		}
	}
	for i, body := range bodies {
		select {
		case <-body.closed:
			t.Fatalf("blocking body %d closed before both admission permits were occupied", i)
		default:
		}
	}

	for range bodies {
		select {
		case recorder := <-responses:
			assertChatAttachmentError(t, recorder, http.StatusRequestTimeout, errCodeAttachmentUploadTimeout, "body read timed out")
			var response struct {
				Error struct {
					ReadTimeoutMS float64 `json:"read_timeout_ms"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode upload timeout response: %v", err)
			}
			if response.Error.ReadTimeoutMS != float64(testReadTimeout.Milliseconds()) {
				t.Fatalf("read_timeout_ms = %v, want %d", response.Error.ReadTimeoutMS, testReadTimeout.Milliseconds())
			}
		case <-time.After(2 * time.Second):
			t.Fatal("stalled upload did not return after its read timeout")
		}
	}
	for i, body := range bodies {
		select {
		case <-body.closed:
		default:
			t.Fatalf("blocking body %d was not closed by the upload deadline", i)
		}
	}

	accepted := fixture.upload(t, "chat_images", "after-timeout.png", "image/png", validChatAttachmentPNG(t))
	if accepted.Code != http.StatusCreated {
		t.Fatalf("upload after timed-out requests status = %d, want %d, body=%s", accepted.Code, http.StatusCreated, accepted.Body.String())
	}
}

func TestChatAttachmentHTTP_RealServerReadDeadlineUnblocksStalledUploads(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	const socketReadTimeout = 2 * time.Second
	fixture.handler.chatImageUploadReadTimeout = socketReadTimeout
	acquired := make(chan struct{}, maxConcurrentChatImageUploads)
	fixture.handler.chatImageUploadAdmission = &observingChatImageUploadAdmission{
		delegate: newChatImageUploadAdmission(maxConcurrentChatImageUploads),
		acquired: acquired,
	}
	server := httptest.NewServer(fixture.server)
	t.Cleanup(server.Close)

	connections := make([]net.Conn, 0, maxConcurrentChatImageUploads)
	for i := range maxConcurrentChatImageUploads {
		conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatalf("dial stalled upload %d: %v", i, err)
		}
		connections = append(connections, conn)
		t.Cleanup(func() { _ = conn.Close() })
		bodyPrefix := "--stalled\r\n" +
			"Content-Disposition: form-data; name=\"file\"; filename=\"stalled.png\"\r\n" +
			"Content-Type: image/png\r\n\r\n" +
			"\x89PNG"
		request := fmt.Sprintf(
			"POST /hecate/v1/chat/sessions/chat_images/attachments HTTP/1.1\r\n"+
				"Host: %s\r\n"+
				"X-Hecate-Runtime-Token: %s\r\n"+
				"Content-Type: multipart/form-data; boundary=stalled\r\n"+
				"Content-Length: 4096\r\n\r\n%s",
			server.Listener.Addr().String(),
			chatAttachmentTestRuntimeToken,
			bodyPrefix,
		)
		if _, err := io.WriteString(conn, request); err != nil {
			t.Fatalf("write stalled upload %d: %v", i, err)
		}
	}

	for i := range maxConcurrentChatImageUploads {
		select {
		case <-acquired:
		case <-time.After(time.Second):
			t.Fatalf("real stalled request %d did not acquire an upload admission permit", i)
		}
	}
	probe := fixture.request(
		http.MethodPost,
		"/hecate/v1/chat/sessions/chat_images/attachments",
		&chatAttachmentReadSpy{},
		"multipart/form-data; boundary=probe",
		chatAttachmentTestRuntimeToken,
	)
	if probe.Code != http.StatusTooManyRequests {
		t.Fatalf("admission probe status = %d, want 429, body=%s", probe.Code, probe.Body.String())
	}

	for i, conn := range connections {
		if err := conn.SetReadDeadline(time.Now().Add(2 * socketReadTimeout)); err != nil {
			t.Fatalf("set client read deadline %d: %v", i, err)
		}
		response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPost})
		if err != nil {
			t.Fatalf("read stalled upload response %d: %v", i, err)
		}
		var payload struct {
			Error struct {
				Type          string  `json:"type"`
				ReadTimeoutMS float64 `json:"read_timeout_ms"`
			} `json:"error"`
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&payload)
		closeErr := response.Body.Close()
		if decodeErr != nil {
			t.Fatalf("decode stalled upload response %d: %v", i, decodeErr)
		}
		if closeErr != nil {
			t.Fatalf("close stalled upload response %d: %v", i, closeErr)
		}
		if response.StatusCode != http.StatusRequestTimeout || payload.Error.Type != errCodeAttachmentUploadTimeout {
			t.Fatalf("stalled upload response %d = status %d error %#v", i, response.StatusCode, payload.Error)
		}
		if payload.Error.ReadTimeoutMS != float64(socketReadTimeout.Milliseconds()) {
			t.Fatalf("stalled upload %d read_timeout_ms = %v, want %d", i, payload.Error.ReadTimeoutMS, socketReadTimeout.Milliseconds())
		}
		if !response.Close {
			t.Fatalf("stalled upload response %d did not close its expired-deadline connection", i)
		}
	}

	var uploadBody bytes.Buffer
	writer := multipart.NewWriter(&uploadBody)
	part, err := writer.CreateFormFile("file", "after-socket-timeout.png")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(validChatAttachmentPNG(t)); err != nil {
		t.Fatalf("write valid upload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		server.URL+"/hecate/v1/chat/sessions/chat_images/attachments",
		&uploadBody,
	)
	if err != nil {
		t.Fatalf("new valid upload request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set(runtimeTokenHeader, chatAttachmentTestRuntimeToken)
	client := server.Client()
	client.Timeout = 3 * time.Second
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("valid upload after socket timeouts: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("valid upload after socket timeouts status = %d, want %d, body=%s", response.StatusCode, http.StatusCreated, body)
	}
}

func TestChatAttachmentHTTP_HTTP2StalledUploadReturnsTimeoutEnvelope(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	const readTimeout = 250 * time.Millisecond
	fixture.handler.chatImageUploadReadTimeout = readTimeout
	server := httptest.NewUnstartedServer(fixture.server)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	bodyReader, bodyWriter := io.Pipe()
	defer bodyWriter.Close()
	request, err := http.NewRequest(
		http.MethodPost,
		server.URL+"/hecate/v1/chat/sessions/chat_images/attachments",
		bodyReader,
	)
	if err != nil {
		t.Fatalf("create stalled HTTP/2 upload: %v", err)
	}
	request.ContentLength = 4096
	request.Header.Set(runtimeTokenHeader, chatAttachmentTestRuntimeToken)
	request.Header.Set("Content-Type", "multipart/form-data; boundary=stalled")
	client := server.Client()
	client.Timeout = 2 * time.Second
	type responseResult struct {
		response *http.Response
		err      error
	}
	responseCh := make(chan responseResult, 1)
	go func() {
		response, requestErr := client.Do(request)
		responseCh <- responseResult{response: response, err: requestErr}
	}()
	prefix := "--stalled\r\n" +
		"Content-Disposition: form-data; name=\"file\"; filename=\"stalled.png\"\r\n" +
		"Content-Type: image/png\r\n\r\n" +
		"\x89PNG"
	if _, err := io.WriteString(bodyWriter, prefix); err != nil {
		t.Fatalf("write stalled HTTP/2 upload prefix: %v", err)
	}

	var result responseResult
	select {
	case result = <-responseCh:
	case <-time.After(2 * time.Second):
		t.Fatal("stalled HTTP/2 upload did not return after its read timeout")
	}
	_ = bodyWriter.Close()
	if result.err != nil {
		t.Fatalf("stalled HTTP/2 upload: %v", result.err)
	}
	defer result.response.Body.Close()
	if result.response.ProtoMajor != 2 {
		t.Fatalf("protocol = %s, want HTTP/2", result.response.Proto)
	}
	if connection := result.response.Header.Get("Connection"); connection != "" {
		t.Fatalf("Connection = %q, want no connection-scoped HTTP/2 header", connection)
	}
	var payload struct {
		Error struct {
			Type          string  `json:"type"`
			ReadTimeoutMS float64 `json:"read_timeout_ms"`
		} `json:"error"`
	}
	if err := json.NewDecoder(result.response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode HTTP/2 timeout envelope: %v", err)
	}
	if result.response.StatusCode != http.StatusRequestTimeout || payload.Error.Type != errCodeAttachmentUploadTimeout {
		t.Fatalf("response = status %d error %#v, want 408/%s", result.response.StatusCode, payload.Error, errCodeAttachmentUploadTimeout)
	}
	if payload.Error.ReadTimeoutMS != float64(readTimeout.Milliseconds()) {
		t.Fatalf("read_timeout_ms = %v, want %d", payload.Error.ReadTimeoutMS, readTimeout.Milliseconds())
	}
}

func TestRequestBodyReadDeadline_ParentCancellationIsNotRouteTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	finish := startRequestBodyReadDeadline(
		ctx,
		httptest.NewRecorder(),
		io.NopCloser(strings.NewReader("")),
		time.Second,
		1,
	)
	cancel()
	if finish.Finish() {
		t.Fatal("parent cancellation was classified as the route-local upload timeout")
	}
}

func TestValidateChatImageEnforcesDecodedBoundsAndFullIntegrity(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		wantMessage string
	}{
		{
			name:        "axis limit",
			data:        chatAttachmentPNGHeader(t, maxChatImageDimension+1, 1),
			wantMessage: "between 1 and 8000 pixels",
		},
		{
			name:        "pixel limit",
			data:        chatAttachmentPNGHeader(t, 4001, 4000),
			wantMessage: "16 megapixel",
		},
		{
			name:        "truncated after valid header",
			data:        chatAttachmentPNGHeader(t, 2, 2),
			wantMessage: "malformed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateChatImage(tt.data, "image/png")
			if err == nil || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("validateChatImage() error = %v, want %q", err, tt.wantMessage)
			}
		})
	}
}

func TestChatAttachmentHTTP_LinkedDeleteConflictsAndSessionDeleteCleansUp(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	payload := validChatAttachmentPNG(t)
	upload := fixture.upload(t, "chat_images", "linked.png", "image/png", payload)
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d, body=%s", upload.Code, http.StatusCreated, upload.Body.String())
	}
	response := decodeChatAttachmentResponse(t, upload)
	createdAt, err := time.Parse(time.RFC3339Nano, response.Data.CreatedAt)
	if err != nil {
		t.Fatalf("created_at = %q: %v", response.Data.CreatedAt, err)
	}
	claim := chatattachments.ClaimRef{SessionID: "chat_images", MessageID: "message_with_image", AttachmentIDs: []string{response.Data.ID}}
	if _, err := fixture.handler.chatApplication().ClaimAttachments(context.Background(), claim); err != nil {
		t.Fatalf("ClaimAttachments: %v", err)
	}
	if _, err := fixture.handler.agentChat.AppendMessage(context.Background(), "chat_images", chat.Message{
		ID:      "message_with_image",
		Role:    "user",
		Content: "Review this image",
		Attachments: []chat.MessageAttachment{{
			ID:        response.Data.ID,
			Filename:  response.Data.Filename,
			MediaType: response.Data.MediaType,
			SizeBytes: response.Data.SizeBytes,
			SHA256:    response.Data.SHA256,
			CreatedAt: createdAt,
		}},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := fixture.handler.chatApplication().ResolveAttachmentClaim(context.Background(), claim, chatattachments.ClaimLinked); err != nil {
		t.Fatalf("LinkAttachments: %v", err)
	}

	deleteURL := "/hecate/v1/chat/sessions/chat_images/attachments/" + response.Data.ID
	linkedDelete := fixture.request(http.MethodDelete, deleteURL, nil, "", chatAttachmentTestRuntimeToken)
	assertChatAttachmentError(t, linkedDelete, http.StatusConflict, errCodeAttachmentInUse, "already used")

	sessionDelete := fixture.request(
		http.MethodDelete,
		"/hecate/v1/chat/sessions/chat_images",
		nil,
		"",
		chatAttachmentTestRuntimeToken,
	)
	if sessionDelete.Code != http.StatusNoContent {
		t.Fatalf("session delete status = %d, want %d, body=%s", sessionDelete.Code, http.StatusNoContent, sessionDelete.Body.String())
	}
	if _, ok, err := fixture.handler.chatAttachments.Get(context.Background(), "chat_images", response.Data.ID); err != nil || ok {
		t.Fatalf("attachment after session delete: ok=%v err=%v", ok, err)
	}
	content := fixture.request(http.MethodGet, response.Data.ContentURL, nil, "", chatAttachmentTestRuntimeToken)
	assertChatAttachmentError(t, content, http.StatusNotFound, errCodeAttachmentNotFound, "not found")
}

func TestChatAttachmentHTTP_SessionDeleteRetryFinishesCleanupAfterTranscriptIsMissing(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	payload := validChatAttachmentPNG(t)
	upload := fixture.upload(t, "chat_images", "retry.png", "image/png", payload)
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d, body=%s", upload.Code, http.StatusCreated, upload.Body.String())
	}
	attachment := decodeChatAttachmentResponse(t, upload)
	privateAttachment := chatattachments.StoredAttachment{
		Attachment: chatattachments.Attachment{
			ID:        attachment.Data.ID,
			SessionID: attachment.Data.SessionID,
			Filename:  attachment.Data.Filename,
			MediaType: attachment.Data.MediaType,
			SizeBytes: attachment.Data.SizeBytes,
			SHA256:    attachment.Data.SHA256,
		},
		Data: payload,
	}
	attachments := &failFirstChatAttachmentSessionDeleteStore{
		Store:     fixture.handler.chatAttachments,
		deleteErr: sensitiveChatAttachmentStoreError("delete session", privateAttachment),
	}
	fixture.handler.SetChatAttachmentStore(attachments)
	deleteURL := "/hecate/v1/chat/sessions/chat_images"

	first := fixture.request(http.MethodDelete, deleteURL, nil, "", chatAttachmentTestRuntimeToken)
	assertChatAttachmentError(t, first, http.StatusInternalServerError, errCodeGatewayError, chatapp.ErrAttachmentSessionCleanup.Error())
	assertChatAttachmentResponseOmitsStoreSecrets(t, first, privateAttachment, attachments.deleteErr)
	if _, ok, err := fixture.handler.agentChat.Get(context.Background(), "chat_images"); err != nil || ok {
		t.Fatalf("session after first delete = ok %v, err %v", ok, err)
	}
	if _, ok, err := attachments.Get(context.Background(), "chat_images", attachment.Data.ID); err != nil || !ok {
		t.Fatalf("attachment after failed cleanup = ok %v, err %v", ok, err)
	}

	retry := fixture.request(http.MethodDelete, deleteURL, nil, "", chatAttachmentTestRuntimeToken)
	if retry.Code != http.StatusNoContent || retry.Body.Len() != 0 {
		t.Fatalf("retry status/body = %d/%q, want 204 with empty body", retry.Code, retry.Body.String())
	}
	if _, ok, err := attachments.Get(context.Background(), "chat_images", attachment.Data.ID); err != nil || ok {
		t.Fatalf("attachment after retry = ok %v, err %v", ok, err)
	}

	idempotent := fixture.request(http.MethodDelete, deleteURL, nil, "", chatAttachmentTestRuntimeToken)
	if idempotent.Code != http.StatusNoContent || idempotent.Body.Len() != 0 {
		t.Fatalf("idempotent status/body = %d/%q, want 204 with empty body", idempotent.Code, idempotent.Body.String())
	}
	if attachments.deleteCalls != 3 {
		t.Fatalf("attachment delete calls = %d, want 3", attachments.deleteCalls)
	}
}

func TestChatAttachmentHTTP_DraftQuotaHasStableError(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	payload := validChatAttachmentPNG(t)
	for i := 0; i < chatattachments.MaxDraftAttachmentsPerSession; i++ {
		recorder := fixture.upload(t, "chat_images", fmt.Sprintf("draft-%d.png", i), "image/png", payload)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("upload %d status = %d, body=%s", i, recorder.Code, recorder.Body.String())
		}
	}
	recorder := fixture.upload(t, "chat_images", "over-quota.png", "image/png", payload)
	assertChatAttachmentError(t, recorder, http.StatusConflict, errCodeAttachmentDraftQuota, "quota exceeded")
	var response struct {
		Error struct {
			MaxDraftAttachments float64 `json:"max_draft_attachments"`
			MaxDraftBytes       float64 `json:"max_draft_bytes"`
			DraftTTLSeconds     float64 `json:"draft_ttl_seconds"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode quota response: %v", err)
	}
	if response.Error.MaxDraftAttachments != float64(chatattachments.MaxDraftAttachmentsPerSession) ||
		response.Error.MaxDraftBytes != float64(chatattachments.MaxDraftBytesPerSession) ||
		response.Error.DraftTTLSeconds != float64(chatattachments.DraftTTL/time.Second) {
		t.Fatalf("quota fields = %#v", response.Error)
	}
}

func TestChatAttachmentHTTP_CombinedMessageLimitHasStableError(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeChatAttachmentAppError(recorder, chatapp.ErrAttachmentMessageBytes, "internal failure")
	assertChatAttachmentError(t, recorder, http.StatusRequestEntityTooLarge, errCodeAttachmentTooLarge, "per-message limit")
	var response struct {
		Error struct {
			MaxMessageAttachmentBytes float64 `json:"max_message_attachment_bytes"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode combined-limit response: %v", err)
	}
	if response.Error.MaxMessageAttachmentBytes != float64(chatapp.MaxMessageAttachmentBytes) {
		t.Fatalf("combined-limit fields = %#v", response.Error)
	}
}

func TestChatAttachmentHTTP_SessionStorageLimitHasStableError(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeChatAttachmentAppError(recorder, chatapp.ErrAttachmentSessionQuota, "internal failure")
	assertChatAttachmentError(t, recorder, http.StatusConflict, errCodeAttachmentSessionQuota, "storage quota")
	var response struct {
		Error struct {
			MaxSessionAttachmentBytes float64 `json:"max_session_attachment_bytes"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode session storage response: %v", err)
	}
	if response.Error.MaxSessionAttachmentBytes != float64(chatattachments.MaxStoredBytesPerSession) {
		t.Fatalf("session storage fields = %#v", response.Error)
	}
}

func TestChatAttachmentHTTP_TotalStorageLimitHasStableError(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeChatAttachmentAppError(recorder, chatapp.AttachmentTotalQuotaError{LimitBytes: chatattachments.MaxMemoryStoredBytesTotal}, "internal failure")
	assertChatAttachmentError(t, recorder, http.StatusConflict, errCodeAttachmentTotalQuota, "storage quota")
	var response struct {
		Error struct {
			MaxTotalAttachmentBytes float64 `json:"max_total_attachment_bytes"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode total storage response: %v", err)
	}
	if response.Error.MaxTotalAttachmentBytes != float64(chatattachments.MaxMemoryStoredBytesTotal) {
		t.Fatalf("total storage fields = %#v", response.Error)
	}
}

func TestChatAttachmentHTTP_ContentFailsClosedOnStoredIntegrityMismatch(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	payload := validChatAttachmentPNG(t)
	upload := fixture.upload(t, "chat_images", "corrupt.png", "image/png", payload)
	response := decodeChatAttachmentResponse(t, upload)
	fixture.handler.SetChatAttachmentStore(corruptAttachmentGetStore{Store: fixture.handler.chatAttachments})
	recorder := fixture.request(http.MethodGet, response.Data.ContentURL, nil, "", chatAttachmentTestRuntimeToken)
	assertChatAttachmentError(t, recorder, http.StatusInternalServerError, errCodeGatewayError, "integrity validation")
	if bytes.Equal(recorder.Body.Bytes(), payload) {
		t.Fatal("corrupt stored attachment response exposed body")
	}
}

func TestChatAttachmentHTTP_UnknownStoreFailuresReturnFixedSanitizedErrors(t *testing.T) {
	t.Run("upload", func(t *testing.T) {
		fixture := newChatAttachmentHTTPFixture(t)
		store := &sensitiveAttachmentCreateErrorStore{Store: fixture.handler.chatAttachments}
		fixture.handler.SetChatAttachmentStore(store)

		recorder := fixture.upload(t, "chat_images", "private-upload.png", "image/png", validChatAttachmentPNG(t))
		assertChatAttachmentError(t, recorder, http.StatusInternalServerError, errCodeGatewayError, chatAttachmentUploadFailureMessage)
		assertChatAttachmentResponseOmitsStoreSecrets(t, recorder, store.attachment, store.err)
	})

	t.Run("content", func(t *testing.T) {
		fixture := newChatAttachmentHTTPFixture(t)
		payload := validChatAttachmentPNG(t)
		upload := decodeChatAttachmentResponse(t, fixture.upload(t, "chat_images", "private-content.png", "image/png", payload))
		store := &sensitiveAttachmentGetErrorStore{Store: fixture.handler.chatAttachments}
		fixture.handler.SetChatAttachmentStore(store)

		recorder := fixture.request(http.MethodGet, upload.Data.ContentURL, nil, "", chatAttachmentTestRuntimeToken)
		assertChatAttachmentError(t, recorder, http.StatusInternalServerError, errCodeGatewayError, chatAttachmentContentFailureMessage)
		assertChatAttachmentResponseOmitsStoreSecrets(t, recorder, store.attachment, store.err)
	})

	t.Run("delete", func(t *testing.T) {
		fixture := newChatAttachmentHTTPFixture(t)
		payload := validChatAttachmentPNG(t)
		upload := decodeChatAttachmentResponse(t, fixture.upload(t, "chat_images", "private-delete.png", "image/png", payload))
		store := &sensitiveAttachmentDeleteErrorStore{Store: fixture.handler.chatAttachments}
		fixture.handler.SetChatAttachmentStore(store)

		recorder := fixture.request(
			http.MethodDelete,
			"/hecate/v1/chat/sessions/chat_images/attachments/"+upload.Data.ID,
			nil,
			"",
			chatAttachmentTestRuntimeToken,
		)
		assertChatAttachmentError(t, recorder, http.StatusInternalServerError, errCodeGatewayError, chatAttachmentDeleteFailureMessage)
		assertChatAttachmentResponseOmitsStoreSecrets(t, recorder, store.attachment, store.err)
	})
}

func TestChatAttachmentHTTP_UploadRollbackFailureReturnsSanitizedServerError(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	attachments := &ownerDeletingChatAttachmentStore{
		Store:    chatattachments.NewMemoryStore(),
		sessions: fixture.handler.agentChat,
	}
	fixture.handler.SetChatAttachmentStore(attachments)
	payload := validChatAttachmentPNG(t)

	recorder := fixture.upload(t, "chat_images", "private.png", "image/png", payload)
	assertChatAttachmentError(t, recorder, http.StatusInternalServerError, errCodeGatewayError, chatapp.ErrAttachmentRollback.Error())

	responseBody := recorder.Body.String()
	for _, sensitive := range []string{
		attachments.created.ID,
		attachments.created.Filename,
		attachments.created.SHA256,
		hex.EncodeToString(attachments.created.Data),
		attachments.rollbackMessage,
	} {
		if sensitive != "" && strings.Contains(responseBody, sensitive) {
			t.Fatalf("rollback failure response exposed %q: %s", sensitive, responseBody)
		}
	}
	if _, ok, err := attachments.Store.Get(context.Background(), "chat_images", attachments.created.ID); err != nil || !ok {
		t.Fatalf("attachment after failed rollback = ok %v, err %v", ok, err)
	}
}

func TestChatAttachmentHTTP_UploadCapturedBeforeDeleteCannotPersistAfterLifecycleReopens(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	attachments := &countingCreateChatAttachmentStore{Store: chatattachments.NewMemoryStore()}
	fixture.handler.SetChatAttachmentStore(attachments)
	body, contentType := chatAttachmentMultipartBody(t, "stale.png", "image/png", validChatAttachmentPNG(t))
	gatedBody := &gatedChatAttachmentRequestBody{
		reader:      bytes.NewReader(body),
		readStarted: make(chan struct{}),
		allowRead:   make(chan struct{}),
	}
	defer gatedBody.release()

	uploadDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		uploadDone <- fixture.request(
			http.MethodPost,
			"/hecate/v1/chat/sessions/chat_images/attachments",
			gatedBody,
			contentType,
			chatAttachmentTestRuntimeToken,
		)
	}()
	select {
	case <-gatedBody.readStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not finish its session read and reach multipart body input")
	}

	deleted := fixture.request(http.MethodDelete, "/hecate/v1/chat/sessions/chat_images", nil, "", chatAttachmentTestRuntimeToken)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d, body=%s", deleted.Code, http.StatusNoContent, deleted.Body.String())
	}
	gatedBody.release()

	var upload *httptest.ResponseRecorder
	select {
	case upload = <-uploadDone:
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not finish after multipart body input released")
	}
	assertChatAttachmentError(t, upload, http.StatusConflict, errCodeSessionStopping, "still stopping")
	if got := attachments.creates.Load(); got != 0 {
		t.Fatalf("attachment Create calls = %d, want zero for stale lifecycle", got)
	}
}

func TestChatAttachmentHTTP_AdmittedCreateDrainsBeforeDeleteCleanup(t *testing.T) {
	fixture := newChatAttachmentHTTPFixture(t)
	attachments := &blockingCreateChatAttachmentStore{
		Store:         chatattachments.NewMemoryStore(),
		createStarted: make(chan struct{}),
		allowCreate:   make(chan struct{}),
		deleteStarted: make(chan struct{}),
	}
	fixture.handler.SetChatAttachmentStore(attachments)
	body, contentType := chatAttachmentMultipartBody(t, "admitted.png", "image/png", validChatAttachmentPNG(t))

	uploadDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		uploadDone <- fixture.request(
			http.MethodPost,
			"/hecate/v1/chat/sessions/chat_images/attachments",
			bytes.NewReader(body),
			contentType,
			chatAttachmentTestRuntimeToken,
		)
	}()
	select {
	case <-attachments.createStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not reach the cancellation-ignoring Create")
	}

	deleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		deleteDone <- fixture.request(http.MethodDelete, "/hecate/v1/chat/sessions/chat_images", nil, "", chatAttachmentTestRuntimeToken)
	}()
	waitForAgentChatLifecycleClosure(t, fixture.handler.agentChatLive, "chat_images")
	select {
	case <-attachments.deleteStarted:
		t.Fatal("attachment cleanup started before the admitted Create released")
	default:
	}
	select {
	case response := <-deleteDone:
		t.Fatalf("delete completed before the admitted Create released: status=%d body=%s", response.Code, response.Body.String())
	default:
	}

	close(attachments.allowCreate)
	var upload *httptest.ResponseRecorder
	select {
	case upload = <-uploadDone:
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not finish after Create released")
	}
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d, body=%s", upload.Code, http.StatusCreated, upload.Body.String())
	}
	created := decodeChatAttachmentResponse(t, upload)

	var deleted *httptest.ResponseRecorder
	select {
	case deleted = <-deleteDone:
	case <-time.After(3 * time.Second):
		t.Fatal("delete did not finish after admitted Create drained")
	}
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d, body=%s", deleted.Code, http.StatusNoContent, deleted.Body.String())
	}
	select {
	case <-attachments.deleteStarted:
	default:
		t.Fatal("attachment cleanup did not run after the admitted Create drained")
	}
	if _, ok, err := attachments.Store.Get(context.Background(), "chat_images", created.Data.ID); err != nil || ok {
		t.Fatalf("attachment after delete = found %v, error %v; want cleaned", ok, err)
	}
	if _, ok, err := fixture.handler.agentChat.Get(context.Background(), "chat_images"); err != nil || ok {
		t.Fatalf("session after delete = found %v, error %v; want missing", ok, err)
	}
}

func waitForAgentChatLifecycleClosure(t *testing.T, live *agentChatLive, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		live.mu.Lock()
		state := live.lifecycles[sessionID]
		closed := state != nil && state.closures > 0
		live.mu.Unlock()
		if closed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("delete did not close the session lifecycle")
		}
		runtime.Gosched()
	}
}

func newChatAttachmentHTTPFixture(t *testing.T) chatAttachmentHTTPFixture {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{RuntimeToken: chatAttachmentTestRuntimeToken},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := handler.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	for _, session := range []chat.Session{
		{ID: "chat_images", AgentID: chat.DefaultAgentID, Title: "Images"},
		{ID: "chat_other", AgentID: chat.DefaultAgentID, Title: "Other"},
		{ID: "chat_external", AgentID: "codex", Title: "External"},
	} {
		if _, err := handler.agentChat.Create(context.Background(), session); err != nil {
			t.Fatalf("Create session %q: %v", session.ID, err)
		}
	}
	return chatAttachmentHTTPFixture{
		handler: handler,
		server:  NewServer(quietLogger(), handler),
	}
}

func chatAttachmentMultipartBody(t *testing.T, filename, declaredType string, data []byte) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name":     "file",
		"filename": filename,
	}))
	if declaredType != "" {
		header.Set("Content-Type", declaredType)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart data: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func writeChatAttachmentHTTPChunk(w io.Writer, data []byte) error {
	if _, err := fmt.Fprintf(w, "%x\r\n", len(data)); err != nil {
		return err
	}
	if written, err := w.Write(data); err != nil {
		return err
	} else if written != len(data) {
		return io.ErrShortWrite
	}
	_, err := io.WriteString(w, "\r\n")
	return err
}

func assertNoStoredChatAttachments(t *testing.T, store chatattachments.Store, sessionID string) {
	t.Helper()
	attachments, err := store.List(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("list chat attachments for %q: %v", sessionID, err)
	}
	if len(attachments) != 0 {
		t.Fatalf("stored chat attachments for %q = %#v, want none", sessionID, attachments)
	}
}

func (f chatAttachmentHTTPFixture) upload(t *testing.T, sessionID, filename, declaredType string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := chatAttachmentMultipartBody(t, filename, declaredType, data)
	return f.request(
		http.MethodPost,
		"/hecate/v1/chat/sessions/"+sessionID+"/attachments",
		bytes.NewReader(body),
		contentType,
		chatAttachmentTestRuntimeToken,
	)
}

func (f chatAttachmentHTTPFixture) request(method, requestPath string, body io.Reader, contentType, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, requestPath, body)
	req.RemoteAddr = "127.0.0.1:1234"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set(runtimeTokenHeader, token)
	}
	recorder := httptest.NewRecorder()
	f.server.ServeHTTP(&deadlineSupportingResponseRecorder{ResponseRecorder: recorder}, req)
	return recorder
}

func validChatAttachmentPNG(t *testing.T) []byte {
	t.Helper()
	imageData := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	imageData.Set(0, 0, color.NRGBA{R: 0x44, G: 0x88, B: 0xcc, A: 0xff})
	var data bytes.Buffer
	if err := png.Encode(&data, imageData); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return data.Bytes()
}

func chatAttachmentPNGHeader(t *testing.T, width, height int) []byte {
	t.Helper()
	data := make([]byte, 0, 33)
	data = append(data, []byte("\x89PNG\r\n\x1a\n")...)
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, 13)
	data = append(data, length...)
	chunk := make([]byte, 17)
	copy(chunk, "IHDR")
	binary.BigEndian.PutUint32(chunk[4:8], uint32(width))
	binary.BigEndian.PutUint32(chunk[8:12], uint32(height))
	chunk[12] = 8
	chunk[13] = 6
	data = append(data, chunk...)
	checksum := make([]byte, 4)
	binary.BigEndian.PutUint32(checksum, crc32.ChecksumIEEE(chunk))
	return append(data, checksum...)
}

func decodeChatAttachmentResponse(t *testing.T, recorder *httptest.ResponseRecorder) ChatAttachmentResponse {
	t.Helper()
	var response ChatAttachmentResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode attachment response: %v, body=%s", err, recorder.Body.String())
	}
	return response
}

func assertChatAttachmentError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code, message string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, status, recorder.Body.String())
	}
	var response chatAttachmentErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v, body=%s", err, recorder.Body.String())
	}
	if response.Error.Type != code || !strings.Contains(response.Error.Message, message) {
		t.Fatalf("error = %#v, want type %q containing %q", response.Error, code, message)
	}
}

func assertChatAttachmentResponseOmitsStoreSecrets(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	attachment chatattachments.StoredAttachment,
	storeErr error,
) {
	t.Helper()
	responseBody := recorder.Body.String()
	sensitive := []string{
		attachment.SessionID,
		attachment.ID,
		attachment.Filename,
		attachment.SHA256,
		hex.EncodeToString(attachment.Data),
		chatAttachmentSensitiveSQL,
		chatAttachmentSensitiveDSN,
	}
	if storeErr != nil {
		sensitive = append(sensitive, storeErr.Error())
	}
	for _, value := range sensitive {
		if value != "" && strings.Contains(responseBody, value) {
			t.Fatalf("attachment error response exposed %q: %s", value, responseBody)
		}
	}
}
