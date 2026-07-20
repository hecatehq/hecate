package main

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

func TestRunACPServerExitsCleanlyWhenParentIsCancelled(t *testing.T) {
	// runACPServer reads its configuration from the process environment. This
	// test intentionally does not run in parallel for that reason.
	t.Setenv("HECATE_BASE_URL", "http://127.0.0.1:8765")
	t.Setenv("HECATE_RUNTIME_TOKEN", "")

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	input, inputWriter := io.Pipe()
	defer inputWriter.Close()
	diagnostics := &startedWriter{started: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- runACPServer(parent, input, io.Discard, diagnostics)
	}()

	select {
	case <-diagnostics.started:
	case <-time.After(time.Second):
		t.Fatal("ACP server did not start")
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runACPServer returned %v after parent cancellation, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runACPServer did not exit after parent cancellation")
	}
}

type startedWriter struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	started chan struct{}
	once    sync.Once
}

func (w *startedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buffer.Write(p)
	w.mu.Unlock()
	w.once.Do(func() { close(w.started) })
	return n, err
}
