package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

type repeatingByteReader byte

func (r repeatingByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

type blockingInferenceReadCloser struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func newBlockingInferenceReadCloser() *blockingInferenceReadCloser {
	return &blockingInferenceReadCloser{started: make(chan struct{}), closed: make(chan struct{})}
}

func (b *blockingInferenceReadCloser) Read([]byte) (int, error) {
	b.startOnce.Do(func() { close(b.started) })
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *blockingInferenceReadCloser) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestInferenceEndpointsRejectOversizedRequestBodies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path   string
		prefix string
	}{
		{path: "/v1/chat/completions", prefix: `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`},
		{path: "/v1/messages", prefix: `{"model":"gpt-4o-mini","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()
			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}
			handler := newTestHTTPHandler(logger, provider)
			body := io.MultiReader(
				strings.NewReader(test.prefix),
				io.LimitReader(repeatingByteReader(' '), maxInferenceRequestBodyBytes),
			)
			req := httptest.NewRequest(http.MethodPost, test.path, body)
			req.RemoteAddr = "127.0.0.1:1234"
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			if recorder.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413; body=%s", recorder.Code, recorder.Body.String())
			}
			var payload struct {
				Type  string `json:"type"`
				Error struct {
					Type           string `json:"type"`
					UserMessage    string `json:"user_message"`
					OperatorAction string `json:"operator_action"`
				} `json:"error"`
			}
			if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			wantErrorType := errCodeRequestTooLarge
			if test.path == "/v1/messages" {
				if payload.Type != "error" {
					t.Errorf("type = %q, want error", payload.Type)
				}
				wantErrorType = "request_too_large"
			}
			if payload.Error.Type != wantErrorType {
				t.Errorf("error.type = %q, want %q", payload.Error.Type, wantErrorType)
			}
			if payload.Error.UserMessage == "" || payload.Error.OperatorAction == "" {
				t.Fatalf("error guidance = %+v, want user message and operator action", payload.Error)
			}
			if provider.CallCount() != 0 {
				t.Fatalf("provider call count = %d, want 0", provider.CallCount())
			}
		})
	}
}

func TestInferenceEndpointsRejectMultipleJSONValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		body string
	}{
		{
			path: "/v1/chat/completions",
			body: `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]} {}`,
		},
		{
			path: "/v1/messages",
			body: `{"model":"gpt-4o-mini","max_tokens":1,"messages":[{"role":"user","content":"hi"}]} {}`,
		},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()
			provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}
			handler := newTestHTTPHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), provider)
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.RemoteAddr = "127.0.0.1:1234"
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "exactly one JSON value") {
				t.Fatalf("response = %d %s, want 400 for a second JSON value", response.Code, response.Body.String())
			}
			if test.path == "/v1/messages" {
				var payload struct {
					Type  string `json:"type"`
					Error struct {
						Type string `json:"type"`
					} `json:"error"`
				}
				if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
					t.Fatalf("decode Anthropic error envelope: %v", err)
				}
				if payload.Type != "error" || payload.Error.Type != "invalid_request_error" {
					t.Fatalf("error envelope = %+v, want Anthropic error/invalid_request_error", payload)
				}
			}
			if provider.CallCount() != 0 {
				t.Fatalf("provider call count = %d, want 0", provider.CallCount())
			}
		})
	}
}

func TestMessagesRejectsMalformedJSONWithAnthropicEnvelope(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}
	handler := newTestHTTPHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), provider)
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`!`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	var payload struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode Anthropic error envelope: %v", err)
	}
	if response.Code != http.StatusBadRequest || payload.Type != "error" || payload.Error.Type != "invalid_request_error" {
		t.Fatalf("response = %d %+v, want 400 Anthropic error/invalid_request_error", response.Code, payload)
	}
	if provider.CallCount() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.CallCount())
	}
}

func TestInferenceEndpointsTimeOutStalledRequestBodies(t *testing.T) {
	t.Parallel()

	const readTimeout = 50 * time.Millisecond
	for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}
			apiHandler := newTestAPIHandlerWithSettings(
				logger,
				[]providers.Provider{provider},
				config.Config{},
				nil,
			)
			apiHandler.inferenceRequestBodyReadTimeout = readTimeout
			body := newBlockingInferenceReadCloser()
			req := httptest.NewRequest(http.MethodPost, path, body)
			recorder := httptest.NewRecorder()
			done := make(chan struct{})

			go func() {
				if path == "/v1/messages" {
					apiHandler.HandleMessages(recorder, req)
				} else {
					apiHandler.HandleChatCompletions(recorder, req)
				}
				close(done)
			}()

			select {
			case <-body.started:
			case <-time.After(time.Second):
				t.Fatal("handler did not start reading the request body")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("stalled request did not return after its read timeout")
			}
			if recorder.Code != http.StatusRequestTimeout {
				t.Fatalf("status = %d, want 408; body=%s", recorder.Code, recorder.Body.String())
			}
			var payload struct {
				Type  string `json:"type"`
				Error struct {
					Type          string  `json:"type"`
					ReadTimeoutMS float64 `json:"read_timeout_ms"`
				} `json:"error"`
			}
			if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			wantErrorType := errCodeRequestBodyTimeout
			if path == "/v1/messages" {
				if payload.Type != "error" {
					t.Errorf("type = %q, want error", payload.Type)
				}
				wantErrorType = "invalid_request_error"
			}
			if payload.Error.Type != wantErrorType {
				t.Errorf("error.type = %q, want %q", payload.Error.Type, wantErrorType)
			}
			if payload.Error.ReadTimeoutMS != float64(readTimeout.Milliseconds()) {
				t.Errorf("read_timeout_ms = %v, want %d", payload.Error.ReadTimeoutMS, readTimeout.Milliseconds())
			}
			if recorder.Header().Get("Connection") != "close" {
				t.Errorf("Connection = %q, want close", recorder.Header().Get("Connection"))
			}
			select {
			case <-body.closed:
			default:
				t.Fatal("timed-out request body was not closed")
			}
			if provider.CallCount() != 0 {
				t.Fatalf("provider call count = %d, want 0", provider.CallCount())
			}
		})
	}
}

func TestInferenceEndpointsRealServerReadDeadlineUnblocksStalledBodies(t *testing.T) {
	const readTimeout = 250 * time.Millisecond

	for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
		path := path
		t.Run(path, func(t *testing.T) {
			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}
			handler := newTestAPIHandlerWithSettings(
				logger,
				[]providers.Provider{provider},
				config.Config{},
				nil,
			)
			handler.inferenceRequestBodyReadTimeout = readTimeout
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/messages" {
					handler.HandleMessages(w, r)
					return
				}
				handler.HandleChatCompletions(w, r)
			}))
			defer server.Close()

			connection, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatalf("dial stalled request: %v", err)
			}
			defer connection.Close()
			prefix := `{"model":"gpt-4o-mini","messages":[`
			if path == "/v1/messages" {
				prefix = `{"model":"gpt-4o-mini","max_tokens":64,"messages":[`
			}
			request := fmt.Sprintf(
				"POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: 4096\r\n\r\n%s",
				path,
				server.Listener.Addr().String(),
				prefix,
			)
			if _, err := io.WriteString(connection, request); err != nil {
				t.Fatalf("write stalled request: %v", err)
			}
			if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatalf("set client read deadline: %v", err)
			}
			response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
			if err != nil {
				t.Fatalf("read stalled response: %v", err)
			}
			defer response.Body.Close()
			var payload struct {
				Type  string `json:"type"`
				Error struct {
					Type          string  `json:"type"`
					ReadTimeoutMS float64 `json:"read_timeout_ms"`
				} `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				t.Fatalf("decode timeout response: %v", err)
			}
			wantErrorType := errCodeRequestBodyTimeout
			if path == "/v1/messages" {
				if payload.Type != "error" {
					t.Fatalf("type = %q, want error", payload.Type)
				}
				wantErrorType = "invalid_request_error"
			}
			if response.StatusCode != http.StatusRequestTimeout || payload.Error.Type != wantErrorType {
				t.Fatalf("response = status %d error %+v, want 408/%s", response.StatusCode, payload.Error, wantErrorType)
			}
			if payload.Error.ReadTimeoutMS != float64(readTimeout.Milliseconds()) {
				t.Fatalf("read_timeout_ms = %v, want %d", payload.Error.ReadTimeoutMS, readTimeout.Milliseconds())
			}
			if !response.Close {
				t.Fatal("timed-out response did not close its expired-deadline connection")
			}
			if provider.CallCount() != 0 {
				t.Fatalf("provider call count = %d, want 0", provider.CallCount())
			}
		})
	}
}

func TestInferenceEndpointsCloseStalledBodiesAfterEarlyJSONRejection(t *testing.T) {
	const readTimeout = 250 * time.Millisecond
	tests := []struct {
		name   string
		path   string
		prefix string
	}{
		{name: "openai malformed", path: "/v1/chat/completions", prefix: `!`},
		{name: "anthropic malformed", path: "/v1/messages", prefix: `!`},
		{name: "openai second value", path: "/v1/chat/completions", prefix: `{}` + " " + `{}`},
		{name: "anthropic second value", path: "/v1/messages", prefix: `{}` + " " + `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}
			handler := newTestAPIHandlerWithSettings(
				slog.New(slog.NewJSONHandler(io.Discard, nil)),
				[]providers.Provider{provider},
				config.Config{},
				nil,
			)
			handler.inferenceRequestBodyReadTimeout = readTimeout
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/messages" {
					handler.HandleMessages(w, r)
					return
				}
				handler.HandleChatCompletions(w, r)
			}))
			defer server.Close()

			response, elapsed := sendStalledInferenceRequest(t, server, test.path, test.prefix)
			defer response.Body.Close()
			if elapsed > 2*time.Second {
				t.Fatalf("early reject took %v, want bounded by route-local deadline", elapsed)
			}
			if response.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d, want 400 after stalled close; body=%s", response.StatusCode, body)
			}
			if !response.Close {
				t.Fatal("early reject did not close the stalled connection")
			}
			if provider.CallCount() != 0 {
				t.Fatalf("provider calls = %d, want 0", provider.CallCount())
			}
		})
	}
}

func TestInferenceEndpointsCloseRateLimitedStalledBodies(t *testing.T) {
	const readTimeout = 250 * time.Millisecond
	for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
		path := path
		t.Run(path, func(t *testing.T) {
			provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}
			handler := newTestAPIHandlerWithSettings(
				slog.New(slog.NewJSONHandler(io.Discard, nil)),
				[]providers.Provider{provider},
				config.Config{Server: config.ServerConfig{RateLimit: config.RateLimitConfig{
					Enabled: true, RequestsPerMinute: 60, BurstSize: 1,
				}}},
				nil,
			)
			handler.inferenceRequestBodyReadTimeout = readTimeout

			validBody := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
			if path == "/v1/messages" {
				validBody = `{"model":"gpt-4o-mini","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
			}
			primeRequest := httptest.NewRequest(http.MethodPost, path, strings.NewReader(validBody))
			primeResponse := httptest.NewRecorder()
			if path == "/v1/messages" {
				handler.HandleMessages(primeResponse, primeRequest)
			} else {
				handler.HandleChatCompletions(primeResponse, primeRequest)
			}
			if primeResponse.Code != http.StatusOK {
				t.Fatalf("prime status = %d, want 200; body=%s", primeResponse.Code, primeResponse.Body.String())
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/messages" {
					handler.HandleMessages(w, r)
					return
				}
				handler.HandleChatCompletions(w, r)
			}))
			defer server.Close()

			response, elapsed := sendStalledInferenceRequest(t, server, path, `{`)
			defer response.Body.Close()
			if elapsed > 2*time.Second {
				t.Fatalf("rate-limit reject took %v, want bounded by route-local deadline", elapsed)
			}
			if response.StatusCode != http.StatusTooManyRequests {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d, want 429; body=%s", response.StatusCode, body)
			}
			if !response.Close {
				t.Fatal("rate-limit response did not close the stalled connection")
			}
		})
	}
}

func TestInferenceEndpointsHTTP2EarlyRejectPreservesErrorEnvelope(t *testing.T) {
	const readTimeout = 250 * time.Millisecond
	for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
		path := path
		t.Run(path, func(t *testing.T) {
			provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}
			handler := newTestAPIHandlerWithSettings(
				slog.New(slog.NewJSONHandler(io.Discard, nil)),
				[]providers.Provider{provider},
				config.Config{},
				nil,
			)
			handler.inferenceRequestBodyReadTimeout = readTimeout
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/messages" {
					handler.HandleMessages(w, r)
					return
				}
				handler.HandleChatCompletions(w, r)
			}))
			server.EnableHTTP2 = true
			server.StartTLS()
			defer server.Close()

			bodyReader, bodyWriter := io.Pipe()
			defer bodyWriter.Close()
			request, err := http.NewRequest(http.MethodPost, server.URL+path, bodyReader)
			if err != nil {
				t.Fatalf("create HTTP/2 request: %v", err)
			}
			request.ContentLength = 4096
			request.Header.Set("Content-Type", "application/json")
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
			if _, err := io.WriteString(bodyWriter, `!`); err != nil {
				t.Fatalf("write malformed HTTP/2 prefix: %v", err)
			}

			var result responseResult
			select {
			case result = <-responseCh:
			case <-time.After(2 * time.Second):
				t.Fatal("HTTP/2 early reject did not return a response")
			}
			_ = bodyWriter.Close()
			if result.err != nil {
				t.Fatalf("HTTP/2 early reject: %v", result.err)
			}
			defer result.response.Body.Close()
			if result.response.ProtoMajor != 2 {
				t.Fatalf("protocol = %s, want HTTP/2", result.response.Proto)
			}
			if connection := result.response.Header.Get("Connection"); connection != "" {
				t.Fatalf("Connection = %q, want no connection-scoped HTTP/2 header", connection)
			}
			var payload struct {
				Type  string `json:"type"`
				Error struct {
					Type string `json:"type"`
				} `json:"error"`
			}
			if err := json.NewDecoder(result.response.Body).Decode(&payload); err != nil {
				t.Fatalf("decode HTTP/2 error envelope: %v", err)
			}
			wantType := errCodeInvalidRequest
			if path == "/v1/messages" {
				if payload.Type != "error" {
					t.Fatalf("top-level type = %q, want error", payload.Type)
				}
				wantType = "invalid_request_error"
			}
			if result.response.StatusCode != http.StatusBadRequest || payload.Error.Type != wantType {
				t.Fatalf("response = %d %+v, want 400/%s", result.response.StatusCode, payload, wantType)
			}
		})
	}
}

func TestInferenceEndpointsHTTP2StalledBodiesReturnTimeoutEnvelope(t *testing.T) {
	const readTimeout = 250 * time.Millisecond
	for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
		path := path
		t.Run(path, func(t *testing.T) {
			provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}
			handler := newTestAPIHandlerWithSettings(
				slog.New(slog.NewJSONHandler(io.Discard, nil)),
				[]providers.Provider{provider},
				config.Config{},
				nil,
			)
			handler.inferenceRequestBodyReadTimeout = readTimeout
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/messages" {
					handler.HandleMessages(w, r)
					return
				}
				handler.HandleChatCompletions(w, r)
			}))
			server.EnableHTTP2 = true
			server.StartTLS()
			defer server.Close()

			bodyReader, bodyWriter := io.Pipe()
			defer bodyWriter.Close()
			request, err := http.NewRequest(http.MethodPost, server.URL+path, bodyReader)
			if err != nil {
				t.Fatalf("create stalled HTTP/2 request: %v", err)
			}
			request.ContentLength = 4096
			request.Header.Set("Content-Type", "application/json")
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
			prefix := `{"model":"gpt-4o-mini","messages":[`
			if path == "/v1/messages" {
				prefix = `{"model":"gpt-4o-mini","max_tokens":64,"messages":[`
			}
			if _, err := io.WriteString(bodyWriter, prefix); err != nil {
				t.Fatalf("write stalled HTTP/2 prefix: %v", err)
			}

			var result responseResult
			select {
			case result = <-responseCh:
			case <-time.After(2 * time.Second):
				t.Fatal("stalled HTTP/2 request did not return after its read timeout")
			}
			_ = bodyWriter.Close()
			if result.err != nil {
				t.Fatalf("stalled HTTP/2 request: %v", result.err)
			}
			defer result.response.Body.Close()
			if result.response.ProtoMajor != 2 {
				t.Fatalf("protocol = %s, want HTTP/2", result.response.Proto)
			}
			if connection := result.response.Header.Get("Connection"); connection != "" {
				t.Fatalf("Connection = %q, want no connection-scoped HTTP/2 header", connection)
			}
			var payload struct {
				Type  string `json:"type"`
				Error struct {
					Type          string  `json:"type"`
					ReadTimeoutMS float64 `json:"read_timeout_ms"`
				} `json:"error"`
			}
			if err := json.NewDecoder(result.response.Body).Decode(&payload); err != nil {
				t.Fatalf("decode HTTP/2 timeout envelope: %v", err)
			}
			wantType := errCodeRequestBodyTimeout
			if path == "/v1/messages" {
				if payload.Type != "error" {
					t.Fatalf("top-level type = %q, want error", payload.Type)
				}
				wantType = "invalid_request_error"
			}
			if result.response.StatusCode != http.StatusRequestTimeout || payload.Error.Type != wantType {
				t.Fatalf("response = %d %+v, want 408/%s", result.response.StatusCode, payload, wantType)
			}
			if payload.Error.ReadTimeoutMS != float64(readTimeout.Milliseconds()) {
				t.Fatalf("read_timeout_ms = %v, want %d", payload.Error.ReadTimeoutMS, readTimeout.Milliseconds())
			}
			if provider.CallCount() != 0 {
				t.Fatalf("provider calls = %d, want 0", provider.CallCount())
			}
		})
	}
}

func sendStalledInferenceRequest(t *testing.T, server *httptest.Server, path, prefix string) (*http.Response, time.Duration) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial stalled request: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	request := fmt.Sprintf(
		"POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: 4096\r\n\r\n%s",
		path,
		server.Listener.Addr().String(),
		prefix,
	)
	started := time.Now()
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatalf("write stalled request: %v", err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set client read deadline: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatalf("read stalled response: %v", err)
	}
	return response, time.Since(started)
}
