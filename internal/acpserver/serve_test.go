package acpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServeKeepsProtocolOnOutput(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{}
	input := strings.NewReader(`{"jsonrpc":"2.0","id":"initialize","method":"initialize","params":{"protocolVersion":1}}` + "\n")
	var output bytes.Buffer
	if err := Serve(context.Background(), input, &output, runtime, Config{Version: "test"}); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var response struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  struct {
			AgentInfo struct {
				Name string `json:"name"`
			} `json:"agentInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &response); err != nil {
		t.Fatalf("decode ACP output: %v\n%s", err, output.String())
	}
	if response.JSONRPC != "2.0" || response.ID != "initialize" || response.Result.AgentInfo.Name != "hecate" {
		t.Fatalf("ACP output = %#v", response)
	}
}

func TestServeCancelsActiveRunWhenInputCloses(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{
		blockEventPoll: true,
		pollStarted:    make(chan struct{}, 1),
		cancelContexts: make(chan error, 1),
		cancelled: map[string][]RunEvent{
			"run_1": {{Sequence: 1, Type: "run.cancelled", Data: map[string]any{}}},
		},
	}
	input, inputWriter := io.Pipe()
	output := &lockedBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), input, output, runtime, Config{
			Version:        "test",
			PollInterval:   time.Millisecond,
			RequestTimeout: time.Second,
			CancelTimeout:  time.Second,
		})
	}()

	writeACPLine(t, inputWriter, map[string]any{
		"jsonrpc": "2.0",
		"id":      "initialize",
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": 1},
	})
	_ = waitACPResponse(t, output, "initialize")
	writeACPLine(t, inputWriter, map[string]any{
		"jsonrpc": "2.0",
		"id":      "new",
		"method":  "session/new",
		"params":  map[string]any{"cwd": "/workspace", "mcpServers": []any{}},
	})
	newResponse := waitACPResponse(t, output, "new")
	var newResult struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(newResponse.Result, &newResult); err != nil || newResult.SessionID == "" {
		t.Fatalf("session/new result = %s, %v", newResponse.Result, err)
	}
	writeACPLine(t, inputWriter, promptEnvelope("prompt", newResult.SessionID, "keep working"))
	select {
	case <-runtime.pollStarted:
	case <-time.After(time.Second):
		t.Fatal("prompt did not start a blocking event poll")
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close ACP input: %v", err)
	}

	select {
	case cancelledContext := <-runtime.cancelContexts:
		if cancelledContext != nil {
			t.Fatalf("CancelRun received cancelled context: %v", cancelledContext)
		}
	case <-time.After(time.Second):
		t.Fatal("input EOF did not cancel the active Hecate run")
	}
	select {
	case err := <-done:
		if err != nil && err != io.EOF {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after the ACP peer disconnected")
	}
}

func TestServeWaitsForNativeCancellationBeforeReturning(t *testing.T) {
	t.Parallel()

	releaseCancel := make(chan struct{})
	runtime := &fakeRuntime{
		blockEventPoll: true,
		pollStarted:    make(chan struct{}, 1),
		cancelStarted:  make(chan struct{}, 1),
		releaseCancel:  releaseCancel,
		cancelled: map[string][]RunEvent{
			"run_1": {{Sequence: 1, Type: "run.cancelled", Data: map[string]any{}}},
		},
	}
	input, inputWriter := io.Pipe()
	output := &lockedBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), input, output, runtime, Config{
			Version:        "test",
			PollInterval:   time.Millisecond,
			RequestTimeout: time.Second,
			CancelTimeout:  time.Second,
		})
	}()

	writeACPLine(t, inputWriter, map[string]any{
		"jsonrpc": "2.0",
		"id":      "initialize",
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": 1},
	})
	_ = waitACPResponse(t, output, "initialize")
	writeACPLine(t, inputWriter, map[string]any{
		"jsonrpc": "2.0",
		"id":      "new",
		"method":  "session/new",
		"params":  map[string]any{"cwd": "/workspace", "mcpServers": []any{}},
	})
	newResponse := waitACPResponse(t, output, "new")
	var newResult struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(newResponse.Result, &newResult); err != nil || newResult.SessionID == "" {
		t.Fatalf("session/new result = %s, %v", newResponse.Result, err)
	}
	writeACPLine(t, inputWriter, promptEnvelope("prompt", newResult.SessionID, "keep working"))
	select {
	case <-runtime.pollStarted:
	case <-time.After(time.Second):
		t.Fatal("prompt did not enter the event poll")
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close ACP input: %v", err)
	}
	select {
	case <-runtime.cancelStarted:
	case <-time.After(time.Second):
		t.Fatal("input EOF did not begin native cancellation")
	}
	select {
	case err := <-done:
		t.Fatalf("Serve returned %v before native cancellation completed", err)
	case <-time.After(40 * time.Millisecond):
	}

	close(releaseCancel)
	select {
	case err := <-done:
		if err != nil && err != io.EOF {
			t.Fatalf("Serve returned %v after native cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after native cancellation completed")
	}
}

func TestAgentServerCancelsNativeRunWhenACPOutputFails(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{
		events: map[string][]RunEvent{
			"run_1": {
				{Sequence: 1, Type: "assistant.text_complete", Data: map[string]any{"text": "write this"}},
				{Sequence: 2, Type: "run.finished", Data: map[string]any{}},
			},
		},
		cancelContexts: make(chan error, 1),
	}
	agent := newTestAgent(t, runtime)
	input, inputWriter := io.Pipe()
	defer inputWriter.Close()
	output := &failingACPOutput{successfulWrites: 2}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- agent.Server().Serve(input, output)
	}()

	writeACPLine(t, inputWriter, map[string]any{
		"jsonrpc": "2.0",
		"id":      "initialize",
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": 1},
	})
	_ = waitACPResponse(t, &output.buffer, "initialize")
	writeACPLine(t, inputWriter, map[string]any{
		"jsonrpc": "2.0",
		"id":      "new",
		"method":  "session/new",
		"params":  map[string]any{"cwd": "/workspace", "mcpServers": []any{}},
	})
	newResponse := waitACPResponse(t, &output.buffer, "new")
	var newResult struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(newResponse.Result, &newResult); err != nil || newResult.SessionID == "" {
		t.Fatalf("session/new result = %s, %v", newResponse.Result, err)
	}
	writeACPLine(t, inputWriter, promptEnvelope("prompt", newResult.SessionID, "keep working"))

	select {
	case cancelledContext := <-runtime.cancelContexts:
		if cancelledContext != nil {
			t.Fatalf("CancelRun received a cancelled context: %v", cancelledContext)
		}
	case <-time.After(time.Second):
		t.Fatal("ACP output failure did not cancel the active Hecate run")
	}

	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close ACP input: %v", err)
	}
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("ACP server did not stop after its input closed")
	}
}

func TestAgentServerCancelsNativeRunWhenACPOutputBlocks(t *testing.T) {
	t.Parallel()

	releaseOutput := make(chan struct{})
	runtime := &fakeRuntime{
		events: map[string][]RunEvent{
			"run_1": {
				{Sequence: 1, Type: "assistant.text_complete", Data: map[string]any{"text": "write this"}},
			},
		},
		cancelContexts: make(chan error, 1),
		cancelled: map[string][]RunEvent{
			"run_1": {{Sequence: 2, Type: "run.cancelled", Data: map[string]any{}}},
		},
	}
	agent := newTestAgent(t, runtime)
	input, inputWriter := io.Pipe()
	defer inputWriter.Close()
	output := &blockingACPOutput{
		successfulWrites: 2,
		blocked:          make(chan struct{}),
		release:          releaseOutput,
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- agent.Server().Serve(input, output)
	}()

	writeACPLine(t, inputWriter, map[string]any{
		"jsonrpc": "2.0",
		"id":      "initialize",
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": 1},
	})
	_ = waitACPResponse(t, &output.buffer, "initialize")
	writeACPLine(t, inputWriter, map[string]any{
		"jsonrpc": "2.0",
		"id":      "new",
		"method":  "session/new",
		"params":  map[string]any{"cwd": "/workspace", "mcpServers": []any{}},
	})
	newResponse := waitACPResponse(t, &output.buffer, "new")
	var newResult struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(newResponse.Result, &newResult); err != nil || newResult.SessionID == "" {
		t.Fatalf("session/new result = %s, %v", newResponse.Result, err)
	}
	writeACPLine(t, inputWriter, promptEnvelope("prompt", newResult.SessionID, "keep working"))
	select {
	case <-output.blocked:
	case <-time.After(time.Second):
		t.Fatal("ACP output did not block on the assistant update")
	}

	writeACPLine(t, inputWriter, map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/cancel",
		"params":  map[string]any{"sessionId": newResult.SessionID},
	})
	select {
	case cancelledContext := <-runtime.cancelContexts:
		if cancelledContext != nil {
			t.Fatalf("CancelRun received a cancelled context: %v", cancelledContext)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked ACP output delayed cancellation of the native Hecate run")
	}

	close(releaseOutput)
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close ACP input: %v", err)
	}
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("ACP server did not stop after releasing output and closing input")
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

type failingACPOutput struct {
	mu               sync.Mutex
	successfulWrites int
	writes           int
	buffer           lockedBuffer
}

func (w *failingACPOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writes >= w.successfulWrites {
		return 0, errors.New("simulated ACP output failure")
	}
	w.writes++
	return w.buffer.Write(p)
}

type blockingACPOutput struct {
	mu               sync.Mutex
	successfulWrites int
	writes           int
	blocked          chan struct{}
	release          <-chan struct{}
	buffer           lockedBuffer
	blockOnce        sync.Once
}

func (w *blockingACPOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.writes++
	shouldBlock := w.writes == w.successfulWrites+1
	w.mu.Unlock()
	if shouldBlock {
		w.blockOnce.Do(func() { close(w.blocked) })
		<-w.release
	}
	return w.buffer.Write(p)
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.b.Bytes()...)
}

type acpWireResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
}

func writeACPLine(t testing.TB, writer io.Writer, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal ACP request: %v", err)
	}
	if _, err := writer.Write(append(encoded, '\n')); err != nil {
		t.Fatalf("write ACP request: %v", err)
	}
}

func waitACPResponse(t testing.TB, output *lockedBuffer, id string) acpWireResponse {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(string(output.Bytes()), "\n") {
			var response acpWireResponse
			if err := json.Unmarshal([]byte(line), &response); err != nil || response.ID != id {
				continue
			}
			return response
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for ACP response %q; output=%s", id, output.Bytes())
	return acpWireResponse{}
}
