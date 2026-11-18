//go:build !windows

package processrunner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestLocalRunner_CancellationKillsProcessGroup(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		name := "cancel"
		if timeout {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			req := Request{
				Command: "sh", Args: []string{"-c", "sleep 5 & printf ready; wait"},
				KillProcessGroup: true,
			}
			want := context.Canceled
			if timeout {
				req.Timeout = 100 * time.Millisecond
				want = context.DeadlineExceeded
			}
			var once sync.Once
			done := make(chan error, 1)
			go func() {
				_, err := NewLocalRunner().RunStreaming(ctx, req, func(Chunk) {
					if !timeout {
						once.Do(cancel)
					}
				})
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, want) {
					t.Fatalf("RunStreaming error = %v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				cancel()
				<-done // The finite fixture drains even without group cancellation.
				t.Fatal("cancelled command left a child holding its output pipe")
			}
		})
	}
}
