package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"strings"
	"unicode"
)

const (
	maxProviderTranscriptionAudioBytes    = 25 << 20
	maxProviderTranscriptionResponseBytes = 1 << 20
	maxProviderTranscriptBytes            = 256 << 10
)

type openAITranscriptionResponse struct {
	Text  string `json:"text"`
	Model string `json:"model,omitempty"`
}

func (p *OpenAICompatibleProvider) TranscriptionCapability() TranscriptionCapability {
	if strings.TrimSpace(p.config.TranscriptionPath) == "" || strings.TrimSpace(p.config.DefaultTranscriptionModel) == "" {
		return TranscriptionCapability{}
	}
	return TranscriptionCapability{DefaultModel: strings.TrimSpace(p.config.DefaultTranscriptionModel)}
}

func (p *OpenAICompatibleProvider) Transcribe(ctx context.Context, req TranscriptionRequest) (*TranscriptionResponse, error) {
	capability := p.TranscriptionCapability()
	if capability.DefaultModel == "" {
		return nil, fmt.Errorf("provider %s does not support audio transcription", p.Name())
	}
	if len(req.Audio) == 0 {
		return nil, fmt.Errorf("transcription audio is required")
	}
	if len(req.Audio) > maxProviderTranscriptionAudioBytes {
		return nil, fmt.Errorf("transcription audio exceeds provider request limit")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = capability.DefaultModel
	}
	if !validTranscriptionModel(model) {
		return nil, fmt.Errorf("transcription model is invalid")
	}
	if p.config.StubMode {
		return &TranscriptionResponse{Text: strings.TrimSpace(p.config.StubResponse), Model: model}, nil
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filename := safeTranscriptionFilename(req.Filename)
	mediaType := normalizedTranscriptionMediaType(req.MediaType)
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name":     "file",
		"filename": filename,
	}))
	partHeader.Set("Content-Type", mediaType)
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		return nil, fmt.Errorf("create transcription file part: %w", err)
	}
	if _, err := part.Write(req.Audio); err != nil {
		return nil, fmt.Errorf("write transcription file part: %w", err)
	}
	if err := writer.WriteField("model", model); err != nil {
		return nil, fmt.Errorf("write transcription model: %w", err)
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		return nil, fmt.Errorf("write transcription response format: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish transcription request: %w", err)
	}

	endpoint := buildProviderURL(p.config.BaseURL, p.config.TranscriptionPath)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return nil, fmt.Errorf("build transcription request: %w", err)
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	if p.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	}
	injectTraceContext(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send transcription request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, decodeUpstreamError(resp)
	}

	encoded, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderTranscriptionResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read transcription response: %w", err)
	}
	if len(encoded) > maxProviderTranscriptionResponseBytes {
		return nil, fmt.Errorf("transcription response exceeds provider response limit")
	}
	var payload openAITranscriptionResponse
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, fmt.Errorf("decode transcription response: %w", err)
	}
	text := strings.TrimSpace(payload.Text)
	if text == "" {
		return nil, fmt.Errorf("transcription response did not include text")
	}
	if len(text) > maxProviderTranscriptBytes {
		return nil, fmt.Errorf("transcript exceeds provider response limit")
	}
	resolvedModel := strings.TrimSpace(payload.Model)
	if !validTranscriptionModel(resolvedModel) {
		resolvedModel = model
	}
	return &TranscriptionResponse{Text: text, Model: resolvedModel}, nil
}

func validTranscriptionModel(value string) bool {
	return value != "" && len(value) <= 256 && strings.IndexFunc(value, unicode.IsControl) == -1
}

func safeTranscriptionFilename(value string) string {
	value = path.Base(strings.TrimSpace(value))
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	if value == "" || value == "." {
		return "dictation.webm"
	}
	runes := []rune(value)
	if len(runes) > 128 {
		value = string(runes[:128])
	}
	return value
}

func normalizedTranscriptionMediaType(value string) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err == nil && strings.HasPrefix(mediaType, "audio/") {
		return mediaType
	}
	return "application/octet-stream"
}
