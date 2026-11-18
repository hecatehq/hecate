//go:build linux

package sandbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/processrunner"
)

func TestBwrapCancellationClosesInheritedOutput(t *testing.T) {
	if DetectWrapper(t.Context()) != WrapperBwrap {
		t.Skip("requires usable bubblewrap")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The finite child bounds cleanup even when the regression leaves it alive.
	argv := wrappedArgv([]string{"sh", "-c", "sleep 5 & printf ready; wait"}, t.TempDir(), false, true)
	ready := make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	go func() {
		_, err := processrunner.NewLocalRunner().RunStreaming(ctx, processrunner.Request{
			Command: argv[0], Args: argv[1:], MaxStdoutBytes: 1024, MaxStderrBytes: 1024,
		}, func(chunk processrunner.Chunk) {
			if chunk.Stream == "stdout" {
				once.Do(func() { close(ready) })
			}
		})
		done <- err
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("wrapper exited before child started: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("wrapper did not start child")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunStreaming error = %v, want cancellation", err)
		}
	case <-time.After(2 * time.Second):
		// Reap the finite fixture before reporting failure.
		select {
		case <-done:
		case <-time.After(6 * time.Second):
		}
		t.Fatal("cancelled bubblewrap left a descendant holding the output pipe open")
	}
}
