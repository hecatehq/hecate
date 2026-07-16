package providers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/config"
)

func TestOpenAICompatibleProviderTranscribe(t *testing.T) {
	t.Parallel()

	audio := []byte("OggS-test-audio")
	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:                      "groq",
		Kind:                      string(KindCloud),
		BaseURL:                   "https://api.example.test/openai/v1",
		APIKey:                    "secret-key",
		Timeout:                   time.Second,
		DefaultModel:              "chat-model",
		TranscriptionPath:         "/audio/transcriptions",
		DefaultTranscriptionModel: "whisper-large-v3-turbo",
		Enabled:                   true,
	}, nil)
	provider.httpClient.Transport = testRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost {
			return nil, fmt.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.String() != "https://api.example.test/openai/v1/audio/transcriptions" {
			return nil, fmt.Errorf("URL = %s", request.URL)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer secret-key" {
			return nil, fmt.Errorf("Authorization = %q", got)
		}
		reader, err := request.MultipartReader()
		if err != nil {
			return nil, err
		}
		values := map[string]string{}
		for {
			part, nextErr := reader.NextPart()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				return nil, nextErr
			}
			body, readErr := io.ReadAll(part)
			if readErr != nil {
				return nil, readErr
			}
			if part.FormName() == "file" {
				if part.FileName() != "voice.ogg" || part.Header.Get("Content-Type") != "audio/ogg" || !bytes.Equal(body, audio) {
					return nil, fmt.Errorf("file part name=%q type=%q body=%q", part.FileName(), part.Header.Get("Content-Type"), body)
				}
				continue
			}
			values[part.FormName()] = string(body)
		}
		if values["model"] != "whisper-large-v3-turbo" || values["response_format"] != "json" {
			return nil, fmt.Errorf("form values = %#v", values)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"text":"  drafted by voice  ","model":"resolved-model"}`)),
		}, nil
	})

	response, err := provider.Transcribe(context.Background(), TranscriptionRequest{
		Audio:     audio,
		Filename:  "../voice.ogg",
		MediaType: "audio/ogg; codecs=opus",
	})
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if response.Text != "drafted by voice" || response.Model != "resolved-model" {
		t.Fatalf("Transcribe() = %+v", response)
	}
}

func TestOpenAICompatibleProviderTranscriptionRequiresExplicitCapability(t *testing.T) {
	t.Parallel()

	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:         "custom",
		BaseURL:      "https://api.example.test/v1",
		DefaultModel: "chat-model",
		Enabled:      true,
	}, nil)
	if got := provider.TranscriptionCapability(); got.DefaultModel != "" {
		t.Fatalf("TranscriptionCapability() = %+v", got)
	}
	if _, err := provider.Transcribe(context.Background(), TranscriptionRequest{Audio: []byte("audio")}); err == nil {
		t.Fatal("Transcribe() error = nil, want unsupported capability error")
	}
}

func TestOpenAICompatibleProviderTranscriptionBoundsProviderMetadata(t *testing.T) {
	t.Parallel()

	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:                      "speech",
		Kind:                      string(KindLocal),
		BaseURL:                   "http://127.0.0.1:8080/v1",
		DefaultModel:              "chat-model",
		TranscriptionPath:         "/audio/transcriptions",
		DefaultTranscriptionModel: "speech-model",
		Enabled:                   true,
	}, nil)
	provider.httpClient.Transport = testRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"text":"transcript","model":"bad\nmodel"}`)),
		}, nil
	})

	response, err := provider.Transcribe(context.Background(), TranscriptionRequest{Audio: []byte("audio")})
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if response.Model != "speech-model" {
		t.Fatalf("response model = %q, want bounded request model", response.Model)
	}
	if _, err := provider.Transcribe(context.Background(), TranscriptionRequest{
		Audio: []byte("audio"),
		Model: strings.Repeat("m", 257),
	}); err == nil {
		t.Fatal("Transcribe() error = nil, want invalid model error")
	}
}

func TestSafeTranscriptionFilenamePreservesValidUTF8(t *testing.T) {
	t.Parallel()

	got := safeTranscriptionFilename(strings.Repeat("é", 130) + ".webm")
	if !utf8.ValidString(got) || len([]rune(got)) != 128 {
		t.Fatalf("safeTranscriptionFilename() produced invalid length or UTF-8: %q", got)
	}
}
