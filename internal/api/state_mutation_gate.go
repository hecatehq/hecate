package api

import (
	"context"
	"errors"
	"sync"
)

var errDestructiveStateMutationInProgress = errors.New("destructive state mutation is already in progress")

// stateMutationGate coordinates chat session creation with destructive state
// cleanup that spans multiple Hecate applications and the embedded Cairnline
// authority. It belongs to the API composition layer because no individual
// store owns the complete mutation.
//
// A destructive mutation closes admission before waiting for existing chat
// creates to finish. The epoch advances at both edges of the mutation so a
// request that arrived before or during cleanup cannot begin creating a chat
// after cleanup has completed.
type stateMutationGate struct {
	mu                sync.Mutex
	epoch             uint64
	destructiveActive bool
	chatCreates       int
	chatCreatesDone   chan struct{}
}

func (g *stateMutationGate) snapshot() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.epoch
}

func (g *stateMutationGate) beginChatCreate(expectedEpoch uint64) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.destructiveActive || g.epoch != expectedEpoch {
		return nil, errDestructiveStateMutationInProgress
	}
	if g.chatCreates == 0 {
		g.chatCreatesDone = make(chan struct{})
	}
	g.chatCreates++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.chatCreates--
			if g.chatCreates == 0 {
				close(g.chatCreatesDone)
				g.chatCreatesDone = nil
			}
		})
	}, nil
}

func (g *stateMutationGate) beginDestructive(ctx context.Context) (func(), error) {
	g.mu.Lock()
	if g.destructiveActive {
		g.mu.Unlock()
		return nil, errDestructiveStateMutationInProgress
	}
	g.destructiveActive = true
	g.epoch++
	wait := g.chatCreatesDone
	g.mu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			g.mu.Lock()
			g.destructiveActive = false
			g.epoch++
			g.mu.Unlock()
		})
	}
	if wait == nil {
		return release, nil
	}
	select {
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	case <-wait:
		return release, nil
	}
}
