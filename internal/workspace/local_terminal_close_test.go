package workspace

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func TestLocalTerminal_CloseFinalDrainHonorsContext(t *testing.T) {
	for _, tc := range []struct {
		name        string
		gracePeriod time.Duration
		deadline    time.Duration
	}{
		{name: "deadline during grace", gracePeriod: time.Second, deadline: 20 * time.Millisecond},
		{name: "deadline during final drain", gracePeriod: 5 * time.Millisecond, deadline: 40 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tree := newBlockingTerminalProcessTree()
			term := &localTerminal{
				tree:             tree,
				exit:             make(chan struct{}),
				closeGate:        newTerminalCloseGate(),
				closeGracePeriod: tc.gracePeriod,
			}
			ctx, cancel := context.WithTimeout(context.Background(), tc.deadline)
			defer cancel()
			started := time.Now()
			if err := term.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Close error = %v, want context deadline while final drain remains blocked", err)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("Close took %v after its context expired", elapsed)
			}
			select {
			case <-tree.forceKilled:
			default:
				t.Fatal("Close did not escalate the process tree before returning its context error")
			}
		})
	}
}

func TestLocalTerminal_ConcurrentCloseWaitHonorsContext(t *testing.T) {
	t.Parallel()

	tree := newBlockingTerminalProcessTree()
	term := &localTerminal{
		tree:             tree,
		exit:             make(chan struct{}),
		closeGate:        newTerminalCloseGate(),
		closeGracePeriod: time.Second,
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- term.Close(context.Background()) }()
	select {
	case <-tree.terminated:
	case <-time.After(time.Second):
		t.Fatal("first Close did not acquire close ownership")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := term.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent Close error = %v, want context deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("concurrent Close waited %v for close ownership", elapsed)
	}

	close(term.exit)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first Close after drain: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first Close did not finish after drain")
	}
}

type blockingTerminalProcessTree struct {
	terminateOnce sync.Once
	forceKillOnce sync.Once
	terminated    chan struct{}
	forceKilled   chan struct{}
}

func newBlockingTerminalProcessTree() *blockingTerminalProcessTree {
	return &blockingTerminalProcessTree{
		terminated:  make(chan struct{}),
		forceKilled: make(chan struct{}),
	}
}

func (*blockingTerminalProcessTree) attach(*exec.Cmd) error { return nil }
func (t *blockingTerminalProcessTree) terminate() error {
	t.terminateOnce.Do(func() { close(t.terminated) })
	return nil
}
func (t *blockingTerminalProcessTree) forceKill() error {
	t.forceKillOnce.Do(func() { close(t.forceKilled) })
	return nil
}
func (*blockingTerminalProcessTree) wait()  {}
func (*blockingTerminalProcessTree) close() {}
