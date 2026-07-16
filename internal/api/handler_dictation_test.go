package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestDictationOptionsAdvertiseExplicitRoutesLocalFirst(t *testing.T) {
	t.Parallel()

	local := &fakeDictationProvider{name: "localai", kind: providers.KindLocal, credential: providers.CredentialStateNotRequired}
	cloud := &fakeDictationProvider{name: "openai", kind: providers.KindCloud, credential: providers.CredentialStateConfigured}
	handler := newDictationTestHandler(providers.NewRegistry(cloud, local))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/hecate/v1/dictation/options", nil)
	NewServer(quietLogger(), handler).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
	var envelope struct {
		Data []dictationOptionResponseItem `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(envelope.Data) != 2 || envelope.Data[0].Provider != "localai" || envelope.Data[1].Provider != "openai" {
		t.Fatalf("options = %+v", envelope.Data)
	}
}

func TestCreateDictationTranscriptionUsesSelectedProvider(t *testing.T) {
	t.Parallel()

	selected := &fakeDictationProvider{name: "selected", kind: providers.KindCloud, credential: providers.CredentialStateConfigured, transcript: "voice draft"}
	other := &fakeDictationProvider{name: "other", kind: providers.KindCloud, credential: providers.CredentialStateConfigured, transcript: "wrong"}
	handler := newDictationTestHandler(providers.NewRegistry(other, selected))
	recorder := performDictationRequest(t, handler, "selected", "custom-speech-model", "audio/webm", validWebMAudio())

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
	if selected.calls.Load() != 1 || other.calls.Load() != 0 {
		t.Fatalf("selected calls=%d other calls=%d", selected.calls.Load(), other.calls.Load())
	}
	selected.mu.Lock()
	request := selected.request
	selected.mu.Unlock()
	if request.Model != "custom-speech-model" || request.MediaType != "audio/webm" || request.Filename != "dictation.webm" || !bytes.Equal(request.Audio, validWebMAudio()) {
		t.Fatalf("provider request = %+v", request)
	}
	var envelope struct {
		Data dictationTranscriptionResponseItem `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.Provider != "selected" || envelope.Data.Model != "custom-speech-model" || envelope.Data.Text != "voice draft" {
		t.Fatalf("response = %+v", envelope.Data)
	}
}

func TestCreateDictationTranscriptionRejectsMismatchedMediaBeforeDisclosure(t *testing.T) {
	t.Parallel()

	provider := &fakeDictationProvider{name: "speech", kind: providers.KindCloud, credential: providers.CredentialStateConfigured}
	handler := newDictationTestHandler(providers.NewRegistry(provider))
	recorder := performDictationRequest(t, handler, "speech", "", "audio/webm", []byte("not webm"))

	assertAPIErrorType(t, recorder, http.StatusUnprocessableEntity, errCodeDictationUnsupported)
	if provider.calls.Load() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls.Load())
	}
}

func TestCreateDictationTranscriptionRejectsOversizedAudioBeforeDisclosure(t *testing.T) {
	t.Parallel()

	provider := &fakeDictationProvider{name: "speech", kind: providers.KindCloud, credential: providers.CredentialStateConfigured}
	handler := newDictationTestHandler(providers.NewRegistry(provider))
	audio := make([]byte, maxDictationAudioBytes+1)
	copy(audio, validWebMAudio())
	recorder := performDictationRequest(t, handler, "speech", "", "audio/webm", audio)

	assertAPIErrorType(t, recorder, http.StatusRequestEntityTooLarge, errCodeDictationTooLarge)
	if provider.calls.Load() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls.Load())
	}
}

func TestCreateDictationTranscriptionReturnsBusyBeforeReadingBody(t *testing.T) {
	t.Parallel()

	handler := newDictationTestHandler(providers.NewRegistry())
	handler.dictationAdmission = deniedDictationAdmission{}
	recorder := performDictationRequest(t, handler, "speech", "", "audio/webm", validWebMAudio())

	assertAPIErrorType(t, recorder, http.StatusTooManyRequests, errCodeDictationBusy)
	if recorder.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q", recorder.Header().Get("Retry-After"))
	}
}

func TestCreateDictationTranscriptionBoundsProviderTime(t *testing.T) {
	t.Parallel()

	provider := &fakeDictationProvider{
		name:       "slow",
		kind:       providers.KindCloud,
		credential: providers.CredentialStateConfigured,
		transcribe: func(ctx context.Context, _ providers.TranscriptionRequest) (*providers.TranscriptionResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	handler := newDictationTestHandler(providers.NewRegistry(provider))
	handler.dictationTranscriptionTimeout = time.Millisecond
	recorder := performDictationRequest(t, handler, "slow", "", "audio/webm", validWebMAudio())

	assertAPIErrorType(t, recorder, http.StatusGatewayTimeout, errCodeDictationTimeout)
}

func newDictationTestHandler(registry providers.Registry) *Handler {
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	handler.dictationRegistry = registry
	return handler
}

func performDictationRequest(t *testing.T, handler *Handler, provider, model, mediaType string, audio []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("provider", provider); err != nil {
		t.Fatal(err)
	}
	if model != "" {
		if err := writer.WriteField("model", model); err != nil {
			t.Fatal(err)
		}
	}
	header := make(textproto.MIMEHeader)
	header["Content-Disposition"] = []string{`form-data; name="file"; filename="recording.webm"`}
	header["Content-Type"] = []string{mediaType}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(audio); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/hecate/v1/dictation/transcriptions", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	NewServer(quietLogger(), handler).ServeHTTP(recorder, request)
	return recorder
}

func validWebMAudio() []byte {
	return []byte{0x1a, 0x45, 0xdf, 0xa3, 0x42, 0x86, 0x81, 0x01}
}

func assertAPIErrorType(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, status, recorder.Body.String())
	}
	var envelope struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if envelope.Error.Type != code {
		t.Fatalf("error type = %q, want %q body=%s", envelope.Error.Type, code, recorder.Body.String())
	}
}

type deniedDictationAdmission struct{}

func (deniedDictationAdmission) TryAcquire() bool { return false }
func (deniedDictationAdmission) Release()         {}

type fakeDictationProvider struct {
	name       string
	kind       providers.Kind
	credential providers.CredentialState
	transcript string
	transcribe func(context.Context, providers.TranscriptionRequest) (*providers.TranscriptionResponse, error)
	calls      atomic.Int64
	mu         sync.Mutex
	request    providers.TranscriptionRequest
}

func (p *fakeDictationProvider) Name() string         { return p.name }
func (p *fakeDictationProvider) Kind() providers.Kind { return p.kind }
func (p *fakeDictationProvider) DefaultModel() string { return "chat-model" }
func (p *fakeDictationProvider) Supports(string) bool { return true }
func (p *fakeDictationProvider) CredentialState() providers.CredentialState {
	return p.credential
}
func (p *fakeDictationProvider) Capabilities(context.Context) (providers.Capabilities, error) {
	return providers.Capabilities{Name: p.name, Kind: p.kind, DefaultModel: "chat-model"}, nil
}
func (p *fakeDictationProvider) Chat(context.Context, types.ChatRequest) (*types.ChatResponse, error) {
	return nil, errors.New("not implemented")
}
func (p *fakeDictationProvider) TranscriptionCapability() providers.TranscriptionCapability {
	return providers.TranscriptionCapability{DefaultModel: "speech-model"}
}
func (p *fakeDictationProvider) Transcribe(ctx context.Context, request providers.TranscriptionRequest) (*providers.TranscriptionResponse, error) {
	p.calls.Add(1)
	p.mu.Lock()
	p.request = request
	p.mu.Unlock()
	if p.transcribe != nil {
		return p.transcribe(ctx, request)
	}
	text := p.transcript
	if text == "" {
		text = "transcript"
	}
	return &providers.TranscriptionResponse{Text: text, Model: request.Model}, nil
}
