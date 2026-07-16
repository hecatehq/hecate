package agentadapters

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMultiWorkspaceConcurrency drives two SessionManager sessions in
// parallel, against the same ACP fake adapter but with distinct
// workspaces, sharing one coordinator + storage. It verifies that:
//
//   - The two sessions get distinct native ACP session IDs.
//   - Approval rows attributed to one session are not visible from the
//     other via the shared coordinator's ListApprovals view (per-session
//     scoping holds under concurrency).
//   - Closing session A leaves session B's adapter process group intact
//     — a follow-up turn against B still succeeds.
func TestMultiWorkspaceConcurrency(t *testing.T) {
	installFakeACPExecutable(t, "codex-acp-adapter")

	workspaceA := newConcurrentWorkspace(t)
	workspaceB := newConcurrentWorkspace(t)

	store := NewMemoryApprovalStore()
	coord := NewApprovalCoordinator(CoordinatorOptions{
		Mode:    ModeAuto,
		Store:   store,
		Timeout: 2 * time.Second,
	})

	manager := NewSessionManager()
	manager.SetApprovalCoordinator(coord)
	t.Cleanup(func() {
		_ = manager.Shutdown(context.Background())
	})

	const sessionA = "chat_concurrent_A"
	const sessionB = "chat_concurrent_B"

	type result struct {
		run RunResult
		err error
	}

	results := make(chan result, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for _, args := range []struct {
		sessionID string
		workspace string
		prompt    string
	}{
		{sessionA, workspaceA, "first parallel turn"},
		{sessionB, workspaceB, "second parallel turn"},
	} {
		args := args
		go func() {
			defer wg.Done()
			run, err := manager.Run(context.Background(), RunRequest{
				SessionID:      args.sessionID,
				AdapterID:      "codex",
				Workspace:      args.workspace,
				Prompt:         PromptInput{Text: args.prompt},
				Timeout:        5 * time.Second,
				MaxOutputBytes: 64 * 1024,
			})
			results <- result{run: run, err: err}
		}()
	}
	wg.Wait()
	close(results)

	gotA, gotB := result{}, result{}
	for r := range results {
		if r.err == nil {
			switch r.run.Output {
			case "":
				// fallthrough — tag by output below
			}
			if strings.Contains(r.run.Output, "first parallel turn") {
				gotA = r
			} else if strings.Contains(r.run.Output, "second parallel turn") {
				gotB = r
			}
		} else {
			t.Fatalf("concurrent run failed: %v", r.err)
		}
	}
	if gotA.run.NativeSessionID == "" || gotB.run.NativeSessionID == "" {
		t.Fatalf("native session ids = %q / %q, want non-empty", gotA.run.NativeSessionID, gotB.run.NativeSessionID)
	}
	if gotA.run.NativeSessionID == gotB.run.NativeSessionID {
		t.Fatalf("native session ids should differ across workspaces, both = %q", gotA.run.NativeSessionID)
	}
	if !gotA.run.SessionStarted || !gotB.run.SessionStarted {
		t.Fatalf("expected both runs to start fresh sessions, got A.started=%v B.started=%v", gotA.run.SessionStarted, gotB.run.SessionStarted)
	}

	// Per-session approval scoping: drive the coordinator with two
	// approvals tagged with session A and one tagged with session B.
	// ListApprovals must filter cleanly by session id.
	ctx := context.Background()
	now := time.Now().UTC()
	for _, row := range []Approval{
		{SessionID: sessionA, AdapterID: "codex", Workspace: workspaceA, ToolKind: "file_write", Status: ApprovalStatusPending, CreatedAt: now, ExpiresAt: now.Add(time.Minute)},
		{SessionID: sessionA, AdapterID: "codex", Workspace: workspaceA, ToolKind: "execute", Status: ApprovalStatusPending, CreatedAt: now.Add(time.Millisecond), ExpiresAt: now.Add(time.Minute)},
		{SessionID: sessionB, AdapterID: "codex", Workspace: workspaceB, ToolKind: "file_write", Status: ApprovalStatusPending, CreatedAt: now.Add(2 * time.Millisecond), ExpiresAt: now.Add(time.Minute)},
	} {
		if _, err := store.CreateApproval(ctx, row); err != nil {
			t.Fatalf("CreateApproval(%s): %v", row.SessionID, err)
		}
	}

	listA, err := coord.ListApprovals(ctx, sessionA, "")
	if err != nil {
		t.Fatalf("ListApprovals(A): %v", err)
	}
	if len(listA) != 2 {
		t.Fatalf("ListApprovals(A) returned %d rows, want 2", len(listA))
	}
	for _, row := range listA {
		if row.SessionID != sessionA {
			t.Fatalf("ListApprovals(A) leaked row for session %q", row.SessionID)
		}
		if row.Workspace != workspaceA {
			t.Fatalf("ListApprovals(A) row workspace = %q, want %q", row.Workspace, workspaceA)
		}
	}
	listB, err := coord.ListApprovals(ctx, sessionB, "")
	if err != nil {
		t.Fatalf("ListApprovals(B): %v", err)
	}
	if len(listB) != 1 {
		t.Fatalf("ListApprovals(B) returned %d rows, want 1", len(listB))
	}
	if listB[0].SessionID != sessionB || listB[0].Workspace != workspaceB {
		t.Fatalf("ListApprovals(B) row = %+v, want session=%s workspace=%s", listB[0], sessionB, workspaceB)
	}

	// Close session A; verify session B is still alive by sending a
	// follow-up turn (which would fail if session B's adapter process
	// group had been torn down).
	if err := manager.CloseSession(context.Background(), sessionA); err != nil {
		t.Fatalf("CloseSession(A): %v", err)
	}
	follow, err := manager.Run(context.Background(), RunRequest{
		SessionID:      sessionB,
		AdapterID:      "codex",
		Workspace:      workspaceB,
		Prompt:         PromptInput{Text: "third turn after A closed"},
		Timeout:        5 * time.Second,
		MaxOutputBytes: 64 * 1024,
	})
	if err != nil {
		t.Fatalf("follow-up Run(B) after Close(A): %v", err)
	}
	if follow.NativeSessionID != gotB.run.NativeSessionID {
		t.Fatalf("session B native id changed after Close(A): %q -> %q (process likely restarted)", gotB.run.NativeSessionID, follow.NativeSessionID)
	}
	if follow.SessionStarted {
		t.Fatalf("session B reported SessionStarted=true on follow-up; existing process should have been reused")
	}
	if !strings.Contains(follow.Output, "third turn after A closed") {
		t.Fatalf("follow-up output = %q, want third turn payload", follow.Output)
	}
}

// TestMidTurnCancel verifies that cancelling the request context mid-
// turn returns promptly, surfaces context.Canceled, and doesn't leak
// goroutines from the in-flight ACP plumbing. The fake adapter sleeps
// indefinitely on the "wait" prompt until its context is cancelled, so
// any cancel that doesn't reach the adapter would block until the
// outer Timeout (30s) fires.
func TestMidTurnCancel(t *testing.T) {
	installFakeACPExecutable(t, "codex-acp-adapter")
	workspace := newConcurrentWorkspace(t)

	manager := NewSessionManager()
	t.Cleanup(func() {
		_ = manager.Shutdown(context.Background())
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const sessionID = "chat_midturn_cancel"

	startedTurn := make(chan struct{})
	var once sync.Once
	done := make(chan error, 1)

	preGoroutines := runtime.NumGoroutine()
	turnStart := time.Now()

	go func() {
		_, err := manager.Run(ctx, RunRequest{
			SessionID:      sessionID,
			AdapterID:      "codex",
			Workspace:      workspace,
			Prompt:         PromptInput{Text: "wait"},
			Timeout:        30 * time.Second,
			MaxOutputBytes: 64 * 1024,
			OnOutput: func(chunk string) {
				if strings.Contains(chunk, "waiting") {
					once.Do(func() { close(startedTurn) })
				}
			},
		})
		done <- err
	}()

	select {
	case <-startedTurn:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for fake adapter to begin streaming")
	}

	cancel()

	select {
	case err := <-done:
		elapsed := time.Since(turnStart)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
		if elapsed > 10*time.Second {
			t.Fatalf("cancellation took %s, expected to return well under fake's 30s sleep budget", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("Run did not return after Cancel; the cancel signal isn't reaching the in-flight ACP prompt")
	}

	// Drain any cleanup goroutines spawned for the cancelled turn.
	if err := manager.CloseSession(context.Background(), sessionID); err != nil {
		t.Fatalf("CloseSession after cancel: %v", err)
	}

	// Allow goroutine accounting to settle. Background goroutines from
	// the ACP SDK / cmd plumbing exit asynchronously after Close, so
	// poll a short window before declaring a leak.
	deadline := time.Now().Add(3 * time.Second)
	var post int
	for {
		runtime.Gosched()
		post = runtime.NumGoroutine()
		if post <= preGoroutines+2 {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if post > preGoroutines+4 {
		t.Fatalf("goroutine leak after cancel: pre=%d post=%d (delta=%d)", preGoroutines, post, post-preGoroutines)
	}
}

// TestFreshPromptAfterCancel verifies that after a mid-turn cancel,
// the same logical session can run a brand-new prompt successfully and
// that the second turn's response carries no residue from the
// cancelled first.
//
// NOTE: The cancellation path in TestMidTurnCancel ends up tearing
// down the underlying ACP subprocess via CloseSession; the second
// turn here therefore creates a fresh ACP session under the same
// SessionManager session id. That's the intended behavior per the
// existing TestSessionManagerCancelsACPPrompt fixture.
func TestFreshPromptAfterCancel(t *testing.T) {
	installFakeACPExecutable(t, "codex-acp-adapter")
	workspace := newConcurrentWorkspace(t)

	manager := NewSessionManager()
	t.Cleanup(func() {
		_ = manager.Shutdown(context.Background())
	})

	const sessionID = "chat_fresh_after_cancel"

	// Start a slow turn and cancel it.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	startedTurn := make(chan struct{})
	var once sync.Once
	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Run(cancelledCtx, RunRequest{
			SessionID:      sessionID,
			AdapterID:      "codex",
			Workspace:      workspace,
			Prompt:         PromptInput{Text: "wait"},
			Timeout:        30 * time.Second,
			MaxOutputBytes: 64 * 1024,
			OnOutput: func(chunk string) {
				if strings.Contains(chunk, "waiting") {
					once.Do(func() { close(startedTurn) })
				}
			},
		})
		firstDone <- err
	}()

	select {
	case <-startedTurn:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for first turn to start streaming")
	}
	cancel()

	select {
	case err := <-firstDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first Run error = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("first Run did not return after Cancel")
	}

	// Force teardown of the cancelled session so the next turn starts
	// from a known state. The session manager allows reusing the same
	// session id afterwards.
	if err := manager.CloseSession(context.Background(), sessionID); err != nil {
		t.Fatalf("CloseSession after first turn: %v", err)
	}

	// Send a fresh prompt against the same session id.
	freshCtx, freshCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer freshCancel()
	second, err := manager.Run(freshCtx, RunRequest{
		SessionID:      sessionID,
		AdapterID:      "codex",
		Workspace:      workspace,
		Prompt:         PromptInput{Text: "fresh prompt body"},
		Timeout:        5 * time.Second,
		MaxOutputBytes: 64 * 1024,
	})
	if err != nil {
		t.Fatalf("second Run after cancel: %v", err)
	}
	if second.NativeSessionID == "" {
		t.Fatalf("second Run native session id is empty")
	}
	if !strings.Contains(second.Output, "fresh prompt body") {
		t.Fatalf("second output = %q, want it to echo the fresh prompt", second.Output)
	}
	if strings.Contains(second.Output, "waiting") {
		t.Fatalf("second output = %q, leaked residue from cancelled first turn", second.Output)
	}
	if second.ExitCode != 0 {
		t.Fatalf("second ExitCode = %d, want 0 (cancellation residue)", second.ExitCode)
	}
}

// newConcurrentWorkspace returns a freshly created absolute workspace
// path. Uses os.MkdirTemp directly (not t.TempDir) per the test plan
// to demonstrate distinct workspace roots; t.Cleanup removes them.
func newConcurrentWorkspace(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hecate-agentadapters-concurrency-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}
