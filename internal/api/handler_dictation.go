package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/dictationapp"
	"github.com/hecatehq/hecate/internal/telemetry"
)

const (
	maxDictationAudioBytes               = int64(10 << 20)
	maxDictationRequestBodyBytes         = maxDictationAudioBytes + int64(1<<20)
	maxDictationFormValueBytes           = int64(512)
	maxConcurrentDictationRequests       = 2
	dictationBusyRetryAfter              = 1
	defaultDictationBodyReadTimeout      = 60 * time.Second
	defaultDictationTranscriptionTimeout = 5 * time.Minute
)

var supportedDictationMediaTypes = map[string]string{
	"audio/webm": "webm",
	"video/webm": "webm",
	"audio/ogg":  "ogg",
	"audio/mp4":  "m4a",
	"audio/mpeg": "mp3",
	"audio/mp3":  "mp3",
	"audio/wav":  "wav",
}

type dictationAdmission interface {
	TryAcquire() bool
	Release()
}

type fixedDictationAdmission struct {
	permits chan struct{}
}

func newDictationAdmission(capacity int) *fixedDictationAdmission {
	if capacity <= 0 {
		panic("dictation admission capacity must be positive")
	}
	return &fixedDictationAdmission{permits: make(chan struct{}, capacity)}
}

func (g *fixedDictationAdmission) TryAcquire() bool {
	select {
	case g.permits <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g *fixedDictationAdmission) Release() {
	<-g.permits
}

type dictationOptionResponseItem struct {
	Provider          string `json:"provider"`
	ProviderKind      string `json:"provider_kind"`
	DefaultModel      string `json:"default_model"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

type dictationTranscriptionResponseItem struct {
	Provider     string `json:"provider"`
	ProviderKind string `json:"provider_kind"`
	Model        string `json:"model"`
	Text         string `json:"text"`
}

type dictationUpload struct {
	Audio     []byte
	Filename  string
	MediaType string
	Provider  string
	Model     string
}

type dictationRequestError struct {
	Status  int
	Code    string
	Message string
}

func (e *dictationRequestError) Error() string {
	return e.Message
}

func (h *Handler) HandleDictationOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	options, err := h.dictationApplication().ProviderOptions()
	if err != nil {
		WriteError(w, http.StatusServiceUnavailable, errCodeDictationUnavailable, "dictation provider routing is not configured")
		return
	}
	data := make([]dictationOptionResponseItem, 0, len(options))
	for _, option := range options {
		data = append(data, dictationOptionResponseItem{
			Provider:          option.Provider,
			ProviderKind:      option.ProviderKind,
			DefaultModel:      option.DefaultModel,
			Available:         option.Available,
			UnavailableReason: option.UnavailableReason,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "dictation_options",
		"data":   data,
	})
}

func (h *Handler) HandleCreateDictationTranscription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, maxDictationRequestBodyBytes)
	readTimeout := h.dictationBodyReadTimeout
	if readTimeout <= 0 {
		readTimeout = defaultDictationBodyReadTimeout
	}
	bodyRead := startRequestBodyReadDeadline(r.Context(), w, r.Body, readTimeout, r.ProtoMajor)
	defer bodyRead.Finish()
	writeRejected := func(write func()) {
		writeRejectedRequestBodyResponse(w, bodyRead, write)
	}

	if h.dictationAdmission == nil || !h.dictationAdmission.TryAcquire() {
		writeRejected(func() {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", dictationBusyRetryAfter))
			WriteError(w, http.StatusTooManyRequests, errCodeDictationBusy, "dictation capacity is busy")
		})
		return
	}
	defer h.dictationAdmission.Release()

	upload, err := readDictationUpload(r)
	if err != nil {
		writeRejected(func() { writeDictationReadError(w, err, bodyRead, readTimeout) })
		return
	}
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		writeRejected(func() { writeDictationReadError(w, err, bodyRead, readTimeout) })
		return
	}
	if bodyRead.Finish() {
		writeRejected(func() { writeDictationBodyTimeout(w, readTimeout) })
		return
	}

	mediaType, extension, err := validateDictationAudio(upload.Audio, upload.MediaType)
	if err != nil {
		WriteError(w, http.StatusUnprocessableEntity, errCodeDictationUnsupported, err.Error())
		return
	}
	upload.MediaType = mediaType
	upload.Filename = "dictation." + extension

	app := h.dictationApplication()
	route, err := app.ResolveRoute(upload.Provider)
	if err != nil {
		writeDictationApplicationError(w, err)
		return
	}
	timeout := h.dictationTranscriptionTimeout
	if timeout <= 0 {
		timeout = defaultDictationTranscriptionTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	result, err := app.Transcribe(ctx, dictationapp.TranscribeCommand{
		Route:     route,
		Audio:     upload.Audio,
		Filename:  upload.Filename,
		MediaType: upload.MediaType,
		Model:     upload.Model,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			WriteError(w, http.StatusGatewayTimeout, errCodeDictationTimeout, "dictation provider timed out")
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) {
			return
		}
		if errors.Is(err, dictationapp.ErrProviderChanged) {
			WriteError(w, http.StatusConflict, errCodeDictationRouteChanged, "dictation provider changed before audio disclosure")
			return
		}
		if errors.Is(err, dictationapp.ErrModelInvalid) {
			WriteError(w, http.StatusBadRequest, errCodeDictationInvalid, err.Error())
			return
		}
		telemetry.Error(h.logger, r.Context(), "dictation.transcription.failed",
			slog.String("event.name", "dictation.transcription.failed"),
			slog.String("provider", route.Provider),
			slog.String("provider_kind", route.ProviderKind),
			slog.String("model", firstNonEmpty(upload.Model, route.DefaultModel)),
			slog.String("failure_class", "provider_call"),
		)
		WriteErrorDetails(w, http.StatusBadGateway, errCodeDictationUpstream, "dictation provider failed", ErrorDetails{
			Fields: map[string]any{"provider": route.Provider, "model": firstNonEmpty(upload.Model, route.DefaultModel)},
		})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "dictation_transcription",
		"data": dictationTranscriptionResponseItem{
			Provider:     result.Provider,
			ProviderKind: result.ProviderKind,
			Model:        result.Model,
			Text:         result.Text,
		},
	})
}

func readDictationUpload(r *http.Request) (dictationUpload, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return dictationUpload{}, &dictationRequestError{Status: http.StatusBadRequest, Code: errCodeDictationInvalid, Message: "invalid multipart dictation request"}
	}
	var upload dictationUpload
	seen := map[string]bool{}
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return dictationUpload{}, nextErr
		}
		name := part.FormName()
		if seen[name] || (name != "file" && name != "provider" && name != "model") {
			_ = part.Close()
			return dictationUpload{}, &dictationRequestError{Status: http.StatusUnprocessableEntity, Code: errCodeDictationInvalid, Message: "multipart request must contain one file, one provider, and at most one model field"}
		}
		seen[name] = true
		switch name {
		case "file":
			upload.Filename = part.FileName()
			upload.MediaType = part.Header.Get("Content-Type")
			upload.Audio, err = io.ReadAll(io.LimitReader(part, maxDictationAudioBytes+1))
		case "provider":
			upload.Provider, err = readDictationFormValue(part)
		case "model":
			upload.Model, err = readDictationFormValue(part)
		}
		closeErr := part.Close()
		if err != nil {
			return dictationUpload{}, err
		}
		if closeErr != nil {
			return dictationUpload{}, closeErr
		}
	}
	if int64(len(upload.Audio)) > maxDictationAudioBytes {
		return dictationUpload{}, &dictationRequestError{Status: http.StatusRequestEntityTooLarge, Code: errCodeDictationTooLarge, Message: "dictation audio exceeds the 10 MiB limit"}
	}
	if len(upload.Audio) == 0 {
		return dictationUpload{}, &dictationRequestError{Status: http.StatusUnprocessableEntity, Code: errCodeDictationInvalid, Message: "dictation audio is required"}
	}
	if strings.TrimSpace(upload.Provider) == "" {
		return dictationUpload{}, &dictationRequestError{Status: http.StatusBadRequest, Code: errCodeDictationInvalid, Message: "dictation provider is required"}
	}
	return upload, nil
}

func readDictationFormValue(reader io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxDictationFormValueBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxDictationFormValueBytes {
		return "", &dictationRequestError{Status: http.StatusBadRequest, Code: errCodeDictationInvalid, Message: "dictation form value is too long"}
	}
	return strings.TrimSpace(string(data)), nil
}

func validateDictationAudio(data []byte, declared string) (string, string, error) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(declared))
	if err != nil {
		return "", "", fmt.Errorf("dictation audio content type is invalid")
	}
	extension, ok := supportedDictationMediaTypes[mediaType]
	if !ok {
		return "", "", fmt.Errorf("dictation supports WebM, Ogg, M4A/MP4, MP3, or WAV audio")
	}
	valid := false
	switch extension {
	case "webm":
		valid = len(data) >= 4 && bytes.Equal(data[:4], []byte{0x1a, 0x45, 0xdf, 0xa3})
		mediaType = "audio/webm"
	case "ogg":
		valid = len(data) >= 4 && bytes.Equal(data[:4], []byte("OggS"))
	case "m4a":
		valid = len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp"))
	case "mp3":
		valid = len(data) >= 3 && (bytes.Equal(data[:3], []byte("ID3")) || (data[0] == 0xff && data[1]&0xe0 == 0xe0))
		mediaType = "audio/mpeg"
	case "wav":
		valid = len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE"))
	}
	if !valid {
		return "", "", fmt.Errorf("dictation audio does not match its declared content type")
	}
	return mediaType, extension, nil
}

func writeDictationReadError(w http.ResponseWriter, err error, bodyRead *inferenceBodyReadGuard, timeout time.Duration) {
	if bodyRead.Expired() {
		writeDictationBodyTimeout(w, timeout)
		return
	}
	var requestErr *dictationRequestError
	if errors.As(err, &requestErr) {
		WriteError(w, requestErr.Status, requestErr.Code, requestErr.Message)
		return
	}
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		WriteError(w, http.StatusRequestEntityTooLarge, errCodeDictationTooLarge, "dictation request exceeds the 10 MiB audio limit")
		return
	}
	WriteError(w, http.StatusBadRequest, errCodeDictationInvalid, "invalid multipart dictation request")
}

func writeDictationBodyTimeout(w http.ResponseWriter, timeout time.Duration) {
	WriteErrorDetails(w, http.StatusRequestTimeout, errCodeDictationBodyTimeout, "dictation request body read timed out", ErrorDetails{
		Fields: map[string]any{"read_timeout_ms": timeout.Milliseconds()},
	})
}

func writeDictationApplicationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, dictationapp.ErrNotConfigured):
		WriteError(w, http.StatusServiceUnavailable, errCodeDictationUnavailable, "dictation provider routing is not configured")
	case errors.Is(err, dictationapp.ErrProviderRequired), errors.Is(err, dictationapp.ErrModelInvalid):
		WriteError(w, http.StatusBadRequest, errCodeDictationInvalid, err.Error())
	case errors.Is(err, dictationapp.ErrProviderNotFound):
		WriteError(w, http.StatusNotFound, errCodeDictationRouteUnavailable, "dictation provider was not found")
	case errors.Is(err, dictationapp.ErrProviderUnsupported), errors.Is(err, dictationapp.ErrProviderUnavailable):
		WriteError(w, http.StatusUnprocessableEntity, errCodeDictationRouteUnavailable, err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, errCodeInternalError, "failed to resolve dictation provider")
	}
}
