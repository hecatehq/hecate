package acpserver

import (
	"context"
	"errors"
	"io"
	"sync"
)

// Serve runs Hecate's ACP agent on a local stdio transport. Protocol bytes are
// written only to output; callers own all diagnostics and must write them to
// stderr. On disconnect or cancellation, active Hecate runs are explicitly
// cancelled while their durable task records remain available to operators.
func Serve(ctx context.Context, input io.Reader, output io.Writer, runtime Runtime, config Config) error {
	if ctx == nil {
		ctx = context.Background()
	}
	agent, err := NewAgent(runtime, config)
	if err != nil {
		return err
	}
	server := agent.Server()
	// Server.Serve waits for concurrent protocol handlers after input ends.
	// Detect the reader's terminal result here so Hecate can cancel owned runs
	// immediately rather than waiting for an event poll to time out.
	monitoredInput := &disconnectReader{reader: input, disconnected: agent.CloseAll}
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(monitoredInput, output)
	}()
	awaitCancellation := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), agent.config.CancelTimeout)
		defer cancel()
		agent.waitForCancellations(shutdownCtx)
	}

	select {
	case err := <-done:
		agent.CloseAll()
		awaitCancellation()
		return err
	case <-ctx.Done():
		agent.CloseAll()
		// The kit's scanner is intentionally blocking while the client is
		// connected. Closing stdio is the correct subprocess shutdown signal
		// and lets Serve wait for the protocol goroutine before returning.
		if closer, ok := input.(io.Closer); ok {
			_ = closer.Close()
		}
		err := <-done
		agent.CloseAll()
		awaitCancellation()
		if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
			return ctx.Err()
		}
		return err
	}
}

type disconnectReader struct {
	reader       io.Reader
	disconnected func()
	once         sync.Once
}

func (r *disconnectReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil && n == 0 {
		r.once.Do(func() {
			if r.disconnected != nil {
				r.disconnected()
			}
		})
	}
	return n, err
}
