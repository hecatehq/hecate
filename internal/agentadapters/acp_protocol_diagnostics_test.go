package agentadapters

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

type synchronizedLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedLogBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *synchronizedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type signalingReader struct {
	reader  io.Reader
	entered chan struct{}
	once    sync.Once
}

func (r *signalingReader) Read(data []byte) (int, error) {
	r.once.Do(func() { close(r.entered) })
	return r.reader.Read(data)
}

func TestACPStructuralDiagnosticLoggerDropsPeerControlledFields(t *testing.T) {
	const sentinel = "private-file-body-and-stage-path"
	var output bytes.Buffer
	base := slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})).With(
		slog.String("component", "safe-component"),
	)
	logger := newACPStructuralDiagnosticLogger(base)
	logger.Error(
		"failed to parse incoming message",
		slog.String("raw", sentinel),
		slog.String("id", sentinel),
		slog.String("method", sentinel),
		slog.Any("err", errors.New(sentinel)),
		slog.Int("capacity", 64),
	)

	logged := output.String()
	if strings.Contains(logged, sentinel) {
		t.Fatalf("structural diagnostic leaked peer-controlled data: %q", logged)
	}
	for _, expected := range []string{"failed to parse incoming message", "safe-component", "capacity=64"} {
		if !strings.Contains(logged, expected) {
			t.Fatalf("structural diagnostic = %q, want %q", logged, expected)
		}
	}
}

func TestACPStructuralDiagnosticLoggerCollapsesUnknownMessagesAndGroups(t *testing.T) {
	const sentinel = "private-data-in-future-diagnostic"
	var output bytes.Buffer
	logger := newACPStructuralDiagnosticLogger(slog.New(slog.NewTextHandler(&output, nil)))
	logger.WithGroup(sentinel).Error(sentinel, slog.Int("capacity", 64))

	logged := output.String()
	if strings.Contains(logged, sentinel) {
		t.Fatalf("structural diagnostic leaked an unknown message or group: %q", logged)
	}
	for _, expected := range []string{"ACP protocol diagnostic", "capacity=64"} {
		if !strings.Contains(logged, expected) {
			t.Fatalf("structural diagnostic = %q, want %q", logged, expected)
		}
	}
}

func TestLogACPControlRPCFailureDropsPeerMessageAndData(t *testing.T) {
	t.Parallel()

	const sentinel = "private-staged-uri-and-attachment-body"
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "request error",
			err: &acp.RequestError{
				Code:    -32042,
				Message: sentinel,
				Data:    map[string]any{"private": sentinel},
			},
			want: "error_kind=rpc_error",
		},
		{name: "canceled", err: context.Canceled, want: "error_kind=canceled"},
		{name: "deadline", err: context.DeadlineExceeded, want: "error_kind=deadline_exceeded"},
		{name: "transport", err: errors.New(sentinel), want: "error_kind=transport_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&output, nil))
			logACPControlRPCFailure(logger, "close ACP session RPC failed", "safe-native-id", test.err)

			logged := output.String()
			if strings.Contains(logged, sentinel) {
				t.Fatalf("control RPC diagnostic leaked peer data: %q", logged)
			}
			for _, expected := range []string{"close ACP session RPC failed", "native_session_id=safe-native-id", test.want} {
				if !strings.Contains(logged, expected) {
					t.Fatalf("control RPC diagnostic = %q, want %q", logged, expected)
				}
			}
			if _, ok := test.err.(*acp.RequestError); ok && !strings.Contains(logged, "rpc_code=-32042") {
				t.Fatalf("request diagnostic = %q, want numeric RPC code", logged)
			}
		})
	}
}

func TestACPProtocolOutputWaitsForStructuralLogger(t *testing.T) {
	const sentinel = "private-body-before-logger-install"
	var output synchronizedLogBuffer
	var defaultOutput synchronizedLogBuffer
	previousDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&defaultOutput, nil)))
	t.Cleanup(func() { slog.SetDefault(previousDefault) })

	base := slog.New(slog.NewTextHandler(&output, nil))
	peerReader, peerWriter := io.Pipe()
	gatedReader := newACPProtocolReaderGate(peerReader)
	observedReader := &signalingReader{reader: gatedReader, entered: make(chan struct{})}
	conn := acp.NewClientSideConnection(&acpChatClient{}, io.Discard, observedReader)
	select {
	case <-observedReader.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("ACP receive loop did not reach the protocol reader gate")
	}

	writeDone := make(chan error, 1)
	writeStarted := make(chan struct{})
	go func() {
		close(writeStarted)
		_, err := io.WriteString(peerWriter, "not-json-"+sentinel+"\n")
		if closeErr := peerWriter.Close(); err == nil {
			err = closeErr
		}
		writeDone <- err
	}()
	<-writeStarted

	conn.SetLogger(newACPStructuralDiagnosticLogger(base))
	gatedReader.release()
	select {
	case <-conn.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ACP connection did not settle after malformed peer output")
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write malformed peer output: %v", err)
	}
	logged := output.String()
	if strings.Contains(logged, sentinel) {
		t.Fatalf("early ACP peer output leaked before logger installation: %q", logged)
	}
	if leaked := defaultOutput.String(); strings.Contains(leaked, sentinel) {
		t.Fatalf("early ACP peer output leaked through slog.Default: %q", leaked)
	}
	if !strings.Contains(logged, "failed to parse incoming message") {
		t.Fatalf("structural ACP parse diagnostic missing: %q", logged)
	}
}

func TestACPProbeAndAuthConnectionDiscardEarlyPeerDiagnostics(t *testing.T) {
	const sentinel = "private-probe-output-before-logger-install"
	var defaultOutput synchronizedLogBuffer
	previousDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&defaultOutput, nil)))
	t.Cleanup(func() { slog.SetDefault(previousDefault) })

	peerReader, peerWriter := io.Pipe()
	writeDone := make(chan error, 1)
	writeStarted := make(chan struct{})
	go func() {
		close(writeStarted)
		_, err := io.WriteString(peerWriter, "not-json-"+sentinel+"\n")
		if closeErr := peerWriter.Close(); err == nil {
			err = closeErr
		}
		writeDone <- err
	}()
	<-writeStarted

	conn := newGuardedACPProbeConnection(t.TempDir(), io.Discard, peerReader)
	select {
	case <-conn.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("guarded ACP probe connection did not settle after malformed peer output")
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write malformed probe output: %v", err)
	}
	if leaked := defaultOutput.String(); strings.Contains(leaked, sentinel) {
		t.Fatalf("probe/auth ACP output leaked through slog.Default: %q", leaked)
	}
}
