package agentadapters

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"

	acp "github.com/coder/acp-go-sdk"
)

// acpProtocolReaderGate prevents the ACP SDK receive loop from observing peer
// output before Hecate installs its structural diagnostic logger. The SDK
// starts that loop in its connection constructor and otherwise falls back to
// slog.Default, which can record complete malformed protocol lines.
type acpProtocolReaderGate struct {
	reader io.Reader
	ready  chan struct{}
	once   sync.Once
}

func newACPProtocolReaderGate(reader io.Reader) *acpProtocolReaderGate {
	return &acpProtocolReaderGate{reader: reader, ready: make(chan struct{})}
}

func (g *acpProtocolReaderGate) Read(buffer []byte) (int, error) {
	<-g.ready
	return g.reader.Read(buffer)
}

func (g *acpProtocolReaderGate) release() {
	g.once.Do(func() { close(g.ready) })
}

func newGuardedACPClientSideConnection(
	client acp.Client,
	peerInput io.Writer,
	peerOutput io.Reader,
	logger *slog.Logger,
) *acp.ClientSideConnection {
	protocolOutput := newACPProtocolReaderGate(peerOutput)
	connection := acp.NewClientSideConnection(client, peerInput, protocolOutput)
	connection.SetLogger(newACPStructuralDiagnosticLogger(logger))
	protocolOutput.release()
	return connection
}

func newGuardedACPProbeConnection(
	workspace string,
	peerInput io.Writer,
	peerOutput io.Reader,
) *acp.ClientSideConnection {
	// Probe, authentication, and logout flows do not have a component logger.
	// Install the same structural boundary with a discard sink so their early
	// peer output can never fall back to slog.Default.
	return newGuardedACPClientSideConnection(
		probeClient{workspace: workspace},
		peerInput,
		peerOutput,
		nil,
	)
}

// acpStructuralDiagnosticHandler forwards the SDK's fixed diagnostic message
// and bounded queue counters, but never forwards peer-controlled protocol
// payloads, identifiers, methods, or errors. Those values can contain staged
// paths or attachment bodies before Hecate's turn redactor can inspect them.
type acpStructuralDiagnosticHandler struct {
	next slog.Handler
}

func newACPStructuralDiagnosticLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return slog.New(acpStructuralDiagnosticHandler{next: logger.Handler()})
}

// logACPControlRPCFailure records only structural information from an ACP
// request failure. RequestError messages and data are peer-controlled and can
// repeat staged paths or attachment content after the originating turn's
// redactor is gone.
func logACPControlRPCFailure(logger *slog.Logger, message, nativeSessionID string, err error) {
	if logger == nil || err == nil {
		return
	}
	kind := "transport_error"
	attributes := []any{slog.String("native_session_id", nativeSessionID)}
	switch {
	case errors.Is(err, context.Canceled):
		kind = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		kind = "deadline_exceeded"
	default:
		var requestErr *acp.RequestError
		if errors.As(err, &requestErr) {
			kind = "rpc_error"
			attributes = append(attributes, slog.Int("rpc_code", requestErr.Code))
		}
	}
	attributes = append(attributes, slog.String("error_kind", kind))
	logger.Warn(message, attributes...)
}

func (h acpStructuralDiagnosticHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h acpStructuralDiagnosticHandler) Handle(ctx context.Context, record slog.Record) error {
	sanitized := slog.NewRecord(
		record.Time,
		record.Level,
		safeACPStructuralDiagnosticMessage(record.Message),
		record.PC,
	)
	record.Attrs(func(attribute slog.Attr) bool {
		if safe, ok := safeACPStructuralDiagnosticAttribute(attribute); ok {
			sanitized.AddAttrs(safe)
		}
		return true
	})
	return h.next.Handle(ctx, sanitized)
}

func (h acpStructuralDiagnosticHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	safe := make([]slog.Attr, 0, len(attributes))
	for _, attribute := range attributes {
		if sanitized, ok := safeACPStructuralDiagnosticAttribute(attribute); ok {
			safe = append(safe, sanitized)
		}
	}
	return acpStructuralDiagnosticHandler{next: h.next.WithAttrs(safe)}
}

func (h acpStructuralDiagnosticHandler) WithGroup(string) slog.Handler {
	// The SDK does not currently group diagnostics. Drop future group names so
	// an upstream change cannot turn peer-controlled data into a log key.
	return acpStructuralDiagnosticHandler{next: h.next}
}

func safeACPStructuralDiagnosticMessage(message string) string {
	// Keep this allowlist in step with the pinned SDK. Unknown future messages
	// are collapsed to a fixed event so this boundary remains fail-closed if an
	// upstream diagnostic starts interpolating peer-controlled data.
	switch message {
	case "failed to parse incoming message",
		"failed to canonicalize inbound request id",
		"failed to queue notification; closing connection",
		"received message with neither id nor method",
		"connection closed",
		"failed to canonicalize response id",
		"failed to parse $/cancel_request params",
		"received $/cancel_request without requestId",
		"failed to canonicalize $/cancel_request requestId",
		"failed to handle notification",
		"failed to send $/cancel_request",
		"dropping $/cancel_request due to full queue":
		return message
	default:
		return "ACP protocol diagnostic"
	}
}

func safeACPStructuralDiagnosticAttribute(attribute slog.Attr) (slog.Attr, bool) {
	attribute.Value = attribute.Value.Resolve()
	switch attribute.Key {
	case "capacity", "queued", "queue_len":
		switch attribute.Value.Kind() {
		case slog.KindInt64, slog.KindUint64:
			return attribute, true
		}
	}
	return slog.Attr{}, false
}
