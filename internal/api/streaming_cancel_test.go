package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

// blockingStreamProvider writes one SSE chunk then blocks until its context is
// cancelled.  It signals via readyCh once the first chunk has been written so
// tests can synchronise before cancelling.
type blockingStreamProvider struct {
	fakeProvider
	// readyCh is closed when the provider has written its first chunk.
	readyCh chan struct{}
	once    sync.Once
	// exitCh is closed when ChatStream returns, letting tests assert the
	// goroutine has actually exited (no leak).
	exitCh   chan struct{}
	exitOnce sync.Once
}

func (p *blockingStreamProvider) ChatStream(ctx context.Context, _ types.ChatRequest, w io.Writer) error {
	// Write one well-formed SSE chunk.
	chunk := "data: {\"id\":\"chatcmpl-blk\",\"object\":\"chat.completion.chunk\"," +
		"\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0," +
		"\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n"
	if _, err := io.WriteString(w, chunk); err != nil {
		return err
	}
	// Signal the test that streaming has started.  Do NOT nil out the field
	// afterwards: the test goroutine reads provider.readyCh, and a nil channel
	// blocks forever in a select, making the receive impossible.
	p.once.Do(func() { close(p.readyCh) })
	// Block until context is done.
	<-ctx.Done()
	// Signal that the goroutine has exited.
	p.exitOnce.Do(func() { close(p.exitCh) })
	return ctx.Err()
}

// newBlockingProvider builds a blockingStreamProvider wired for test use.
func newBlockingProvider() *blockingStreamProvider {
	return &blockingStreamProvider{
		fakeProvider: fakeProvider{name: "openai"},
		readyCh:      make(chan struct{}),
		exitCh:       make(chan struct{}),
	}
}

func streamRequestWithCtx(ctx context.Context, path, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(ctx)
}

// TestChatCompletionsStreamExitsOnContextCancel verifies that
// handleChatCompletionsStream returns promptly when the request context is
// cancelled (simulating a client disconnect).
func TestChatCompletionsStreamExitsOnContextCancel(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := newBlockingProvider()
	handler := newTestHTTPHandler(logger, provider)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	body := `{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := streamRequestWithCtx(ctx, "/v1/chat/completions", body)
	rec := httptest.NewRecorder()

	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		handler.ServeHTTP(rec, req)
	}()

	// Wait for streaming to start.
	select {
	case <-provider.readyCh:
	case <-time.After(3 * time.Second):
		t.Fatal("provider never started streaming")
	}

	// Cancel — simulates client disconnect.
	cancel()

	// Handler must exit promptly.
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("handleChatCompletionsStream did not exit after context cancellation")
	}

	// The upstream ChatStream goroutine must also have exited.
	select {
	case <-provider.exitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("provider ChatStream goroutine did not exit after context cancellation")
	}
}

// TestMessagesStreamExitsOnContextCancel verifies that handleMessagesStream
// returns promptly and its internal io.Pipe goroutine is not leaked when the
// request context is cancelled.
func TestMessagesStreamExitsOnContextCancel(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := newBlockingProvider()
	handler := newTestHTTPHandler(logger, provider)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	body := `{"model":"gpt-4o-mini","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := streamRequestWithCtx(ctx, "/v1/messages", body)
	rec := httptest.NewRecorder()

	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		handler.ServeHTTP(rec, req)
	}()

	// Wait for streaming to start (provider has written its first chunk and the
	// pipe goroutine has begun translating).
	select {
	case <-provider.readyCh:
	case <-time.After(3 * time.Second):
		t.Fatal("provider never started streaming")
	}

	// Give the translate goroutine a moment to process the initial chunk.
	time.Sleep(20 * time.Millisecond)

	// Cancel — simulates client disconnect.
	cancel()

	// Handler (and its translate loop) must exit promptly.
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("handleMessagesStream did not exit after context cancellation")
	}

	// The upstream ChatStream goroutine must also have exited (no goroutine
	// leak from the io.Pipe inside handleMessagesStream).
	select {
	case <-provider.exitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("provider ChatStream goroutine did not exit — possible goroutine leak in handleMessagesStream")
	}
}

// TestChatCompletionsStreamWriteErrorUnblocksProvider verifies that when the
// downstream writer fails (client hung-up mid-stream), the upstream provider
// goroutine is also unblocked rather than leaking.
func TestChatCompletionsStreamWriteErrorUnblocksProvider(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := newBlockingProvider()
	handler := newTestHTTPHandler(logger, provider)

	// Use a context with a short deadline instead of a perpetually-blocking
	// writer; the HTTP transport will cancel the upstream body read.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	body := `{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := streamRequestWithCtx(ctx, "/v1/chat/completions", body)
	rec := httptest.NewRecorder()

	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		handler.ServeHTTP(rec, req)
	}()

	// Handler must finish within a generous timeout (deadline fires at 200 ms).
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("handleChatCompletionsStream did not exit after deadline")
	}

	select {
	case <-provider.exitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("provider ChatStream goroutine did not exit after deadline")
	}
}

// TestMessagesStreamPipeGoroutineCleanup is a targeted regression test for the
// io.Pipe goroutine leak that existed before the fix: if
// translateOpenAIToAnthropicSSE exits early without closing pr, the goroutine
// writing to pw would block forever.
func TestMessagesStreamPipeGoroutineCleanup(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := newBlockingProvider()
	handler := newTestHTTPHandler(logger, provider)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	body := `{"model":"gpt-4o-mini","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := streamRequestWithCtx(ctx, "/v1/messages", body)
	rec := httptest.NewRecorder()

	// Run the handler; the context deadline will fire at 300 ms.
	handler.ServeHTTP(rec, req)

	// After ServeHTTP returns the pipe goroutine must have exited.
	select {
	case <-provider.exitCh:
	case <-time.After(2 * time.Second):
		t.Fatal("pipe goroutine not cleaned up after handler returned — goroutine leak")
	}
}
