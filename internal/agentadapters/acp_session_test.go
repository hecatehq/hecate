package agentadapters

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"

	"github.com/hecatehq/hecate/internal/agentcontrols"
)

func TestFakeACPAgentProcess(t *testing.T) {
	if os.Getenv("HECATE_FAKE_ACP_AGENT") != "1" {
		return
	}
	agent := newFakeACPAgent()
	conn := acp.NewAgentSideConnection(agent, os.Stdout, os.Stdin)
	agent.conn = conn
	<-conn.Done()
	os.Exit(0)
}

func TestSessionManagerRunsTurnsThroughACP(t *testing.T) {
	installFakeACPExecutable(t, "codex-acp")
	workspace := t.TempDir()

	manager := NewSessionManager()
	first, err := manager.Run(context.Background(), RunRequest{
		SessionID:      "chat_1",
		AdapterID:      "codex",
		Workspace:      workspace,
		Prompt:         "first turn",
		Timeout:        5 * time.Second,
		MaxOutputBytes: 64 * 1024,
	})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	second, err := manager.Run(context.Background(), RunRequest{
		SessionID:      "chat_1",
		AdapterID:      "codex",
		Workspace:      workspace,
		Prompt:         "second turn",
		Timeout:        5 * time.Second,
		MaxOutputBytes: 64 * 1024,
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if first.DriverKind != DriverKindACP || second.DriverKind != DriverKindACP {
		t.Fatalf("driver kinds = %q / %q, want acp", first.DriverKind, second.DriverKind)
	}
	if first.NativeSessionID == "" || second.NativeSessionID == "" || first.NativeSessionID != second.NativeSessionID {
		t.Fatalf("native sessions = %q / %q, want same non-empty session", first.NativeSessionID, second.NativeSessionID)
	}
	if !first.SessionStarted {
		t.Fatalf("first SessionStarted = false, want true")
	}
	if second.SessionStarted {
		t.Fatalf("second SessionStarted = true, want false for reused ACP session")
	}
	if !strings.Contains(first.Output, "turn 1: first turn") {
		t.Fatalf("first output = %q", first.Output)
	}
	if !strings.Contains(second.Output, "turn 2: second turn") {
		t.Fatalf("second output = %q", second.Output)
	}
	if second.Usage.ContextSize != 200_000 || second.Usage.ContextUsed != 20_000 {
		t.Fatalf("second usage = %+v, want turn 2 context usage", second.Usage)
	}
}

func TestACPSessionConfigOptionsSnapshotPreservesNilAndEmpty(t *testing.T) {
	session := &acpSession{}
	if got := session.configOptionsSnapshot(); got != nil {
		t.Fatalf("initial snapshot = %#v, want nil", got)
	}

	session.setConfigOptions([]agentcontrols.ConfigOption{})
	if got := session.configOptionsSnapshot(); got == nil {
		t.Fatal("empty snapshot = nil, want non-nil empty slice")
	} else if len(got) != 0 {
		t.Fatalf("empty snapshot length = %d, want 0", len(got))
	}

	session.setConfigOptions([]agentcontrols.ConfigOption{{ID: "model", CurrentValue: "fast"}})
	got := session.configOptionsSnapshot()
	if len(got) != 1 || got[0].CurrentValue != "fast" {
		t.Fatalf("snapshot = %#v, want copied option", got)
	}
	got[0].CurrentValue = "mutated"
	got = session.configOptionsSnapshot()
	if len(got) != 1 || got[0].CurrentValue != "fast" {
		t.Fatalf("snapshot after caller mutation = %#v, want stored option unchanged", got)
	}

	input := []agentcontrols.ConfigOption{
		{
			ID:   "reasoning",
			Type: agentcontrols.ConfigOptionTypeSelect,
			Options: []agentcontrols.ConfigSelectOption{
				{Value: "medium", Name: "Medium"},
				{Value: "high", Name: "High"},
			},
		},
	}
	session.setConfigOptions(input)
	input[0].Options[0].Name = "Mutated input"
	got = session.configOptionsSnapshot()
	if got[0].Options[0].Name != "Medium" {
		t.Fatalf("snapshot after input mutation = %#v, want nested options copied", got)
	}
	got[0].Options[1].Name = "Mutated snapshot"
	got = session.configOptionsSnapshot()
	if got[0].Options[1].Name != "High" {
		t.Fatalf("snapshot after nested caller mutation = %#v, want stored nested option unchanged", got)
	}

	session.setConfigOptions(nil)
	if got := session.configOptionsSnapshot(); got != nil {
		t.Fatalf("nil snapshot = %#v, want nil", got)
	}
}

func TestTrimToolSummaryPreservesUTF8(t *testing.T) {
	input := strings.Repeat("界", 121)
	got := trimToolSummary(input)
	if !utf8.ValidString(got) {
		t.Fatalf("trimmed summary is invalid UTF-8: %q", got)
	}
	if utf8.RuneCountInString(got) != 120 {
		t.Fatalf("trimmed summary rune count = %d, want 120", utf8.RuneCountInString(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("trimmed summary = %q, want ellipsis suffix", got)
	}
}

func TestSessionManagerSerializesConcurrentSessionStart(t *testing.T) {
	t.Setenv("HECATE_FAKE_ACP_NEW_SESSION_DELAY", "100ms")
	installFakeACPExecutable(t, "codex-acp")
	workspace := t.TempDir()
	manager := NewSessionManager()

	type result struct {
		run RunResult
		err error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, prompt := range []string{"first concurrent turn", "second concurrent turn"} {
		prompt := prompt
		go func() {
			<-start
			run, err := manager.Run(context.Background(), RunRequest{
				SessionID:      "chat_concurrent",
				AdapterID:      "codex",
				Workspace:      workspace,
				Prompt:         prompt,
				Timeout:        5 * time.Second,
				MaxOutputBytes: 64 * 1024,
			})
			results <- result{run: run, err: err}
		}()
	}
	close(start)

	first := <-results
	second := <-results
	if first.err != nil {
		t.Fatalf("first Run: %v", first.err)
	}
	if second.err != nil {
		t.Fatalf("second Run: %v", second.err)
	}
	if first.run.NativeSessionID == "" || second.run.NativeSessionID == "" {
		t.Fatalf("native session ids = %q / %q, want non-empty", first.run.NativeSessionID, second.run.NativeSessionID)
	}
	if first.run.NativeSessionID != second.run.NativeSessionID {
		t.Fatalf("native session ids = %q / %q, want same session", first.run.NativeSessionID, second.run.NativeSessionID)
	}
	startedCount := 0
	for _, run := range []RunResult{first.run, second.run} {
		if run.SessionStarted {
			startedCount++
		}
	}
	if startedCount != 1 {
		t.Fatalf("SessionStarted count = %d, want exactly one ACP session start", startedCount)
	}
}

func TestSessionManagerLoadsPersistedNativeSession(t *testing.T) {
	installFakeACPExecutable(t, "codex-acp")
	workspace := t.TempDir()

	firstManager := NewSessionManager()
	first, err := firstManager.Run(context.Background(), RunRequest{
		SessionID:      "chat_persisted",
		AdapterID:      "codex",
		Workspace:      workspace,
		Prompt:         "first turn",
		Timeout:        5 * time.Second,
		MaxOutputBytes: 64 * 1024,
	})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.NativeSessionID == "" {
		t.Fatalf("first native session id is empty")
	}
	if err := firstManager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	secondManager := NewSessionManager()
	second, err := secondManager.Run(context.Background(), RunRequest{
		SessionID:               "chat_persisted",
		AdapterID:               "codex",
		Workspace:               workspace,
		PreviousNativeSessionID: first.NativeSessionID,
		Prompt:                  "second turn",
		Timeout:                 5 * time.Second,
		MaxOutputBytes:          64 * 1024,
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.NativeSessionID != first.NativeSessionID {
		t.Fatalf("native session id = %q, want persisted %q", second.NativeSessionID, first.NativeSessionID)
	}
	if !second.SessionStarted || !second.SessionResumed {
		t.Fatalf("session flags = started:%v resumed:%v, want both true", second.SessionStarted, second.SessionResumed)
	}
}

func TestSessionManagerStartsFreshWhenPersistedNativeSessionIsStale(t *testing.T) {
	t.Setenv("HECATE_FAKE_ACP_LOAD_SESSION_FAIL", "1")
	installFakeACPExecutable(t, "codex-acp")
	workspace := t.TempDir()

	manager := NewSessionManager()
	run, err := manager.Run(context.Background(), RunRequest{
		SessionID:               "chat_stale",
		AdapterID:               "codex",
		Workspace:               workspace,
		PreviousNativeSessionID: "fake_session_stale",
		Prompt:                  "fresh turn",
		Timeout:                 5 * time.Second,
		MaxOutputBytes:          64 * 1024,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.NativeSessionID == "" || run.NativeSessionID == "fake_session_stale" {
		t.Fatalf("native session id = %q, want fresh id", run.NativeSessionID)
	}
	if !run.SessionStarted || run.SessionResumed {
		t.Fatalf("session flags = started:%v resumed:%v, want started fresh", run.SessionStarted, run.SessionResumed)
	}
	if !strings.Contains(run.SessionRecovery, "fake_session_stale") {
		t.Fatalf("session recovery = %q, want stale id", run.SessionRecovery)
	}
	if !strings.Contains(run.Output, "fresh turn") {
		t.Fatalf("output = %q, want fresh turn", run.Output)
	}
}

func TestSessionManagerCancelsACPPrompt(t *testing.T) {
	installFakeACPExecutable(t, "codex-acp")
	workspace := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	manager := NewSessionManager()
	done := make(chan error, 1)
	go func() {
		_, err := manager.Run(ctx, RunRequest{
			SessionID:      "chat_cancel",
			AdapterID:      "codex",
			Workspace:      workspace,
			Prompt:         "wait",
			Timeout:        30 * time.Second,
			MaxOutputBytes: 64 * 1024,
			OnOutput: func(chunk string) {
				if strings.Contains(chunk, "waiting") {
					cancel()
				}
			},
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for cancelled ACP prompt")
	}
}

func TestSessionManagerShutdownCancelsActiveACPPrompt(t *testing.T) {
	installFakeACPExecutable(t, "codex-acp")
	workspace := t.TempDir()

	manager := NewSessionManager()
	ready := make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	go func() {
		_, err := manager.Run(context.Background(), RunRequest{
			SessionID:      "chat_shutdown_cancel",
			AdapterID:      "codex",
			Workspace:      workspace,
			Prompt:         "wait",
			Timeout:        30 * time.Second,
			MaxOutputBytes: 64 * 1024,
			OnOutput: func(chunk string) {
				if strings.Contains(chunk, "waiting") {
					once.Do(func() { close(ready) })
				}
			},
		})
		done <- err
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for ACP prompt to start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for active ACP prompt cancellation")
	}
}

func TestSessionManagerShutdownKillsStubbornACPProcess(t *testing.T) {
	installFakeACPExecutable(t, "codex-acp")
	workspace := t.TempDir()

	manager := NewSessionManager()
	ready := make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	go func() {
		_, err := manager.Run(context.Background(), RunRequest{
			SessionID:      "chat_shutdown_kill",
			AdapterID:      "codex",
			Workspace:      workspace,
			Prompt:         "ignore_cancel",
			Timeout:        30 * time.Second,
			MaxOutputBytes: 64 * 1024,
			OnOutput: func(chunk string) {
				if strings.Contains(chunk, "waiting") {
					once.Do(func() { close(ready) })
				}
			},
		})
		done <- err
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for stubborn ACP prompt to start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("Run error = nil, want process termination error")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for stubborn ACP process termination")
	}
}

func TestSessionManagerRejectsRunsAfterShutdown(t *testing.T) {
	installFakeACPExecutable(t, "codex-acp")
	manager := NewSessionManager()
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	_, err := manager.Run(context.Background(), RunRequest{
		SessionID:      "chat_after_shutdown",
		AdapterID:      "codex",
		Workspace:      t.TempDir(),
		Prompt:         "hello",
		Timeout:        5 * time.Second,
		MaxOutputBytes: 64 * 1024,
	})
	if err == nil || !strings.Contains(err.Error(), "shut down") {
		t.Fatalf("Run error = %v, want shut down error", err)
	}
}

func TestACPTurnIgnoresBookkeepingUpdatesInTranscript(t *testing.T) {
	turn := newACPTurn(64*1024, nil)
	sessionID := acp.SessionId("session_1")
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update:    acp.UpdateAgentMessageText("final answer"),
	})
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			UsageUpdate: &acp.SessionUsageUpdate{},
		},
	})
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			SessionInfoUpdate: &acp.SessionSessionInfoUpdate{},
		},
	})
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{Content: acp.TextBlock("private thought")},
		},
	})
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{
				Title:         "git diff --stat",
				Status:        acp.ToolCallStatusInProgress,
				ToolCallId:    acp.ToolCallId("call_1"),
				SessionUpdate: "tool_call",
			},
		},
	})
	status := acp.ToolCallStatusCompleted
	title := "git diff --stat"
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			ToolCallUpdate: &acp.SessionToolCallUpdate{
				ToolCallId: acp.ToolCallId("call_1"),
				Status:     &status,
				Title:      &title,
			},
		},
	})

	output, raw, usage := turn.snapshot()
	if output != "final answer" {
		t.Fatalf("output = %q, want final answer only", output)
	}
	if !strings.Contains(raw, "usage_update") {
		t.Fatalf("raw output = %q, want usage update retained for diagnostics", raw)
	}
	if usage.ContextSize != 0 || usage.ContextUsed != 0 {
		t.Fatalf("usage = %+v, want empty bookkeeping usage", usage)
	}
}

// TestACPTurnAgentThoughtChunkBlockBoundaries exercises the four
// transition cases in appendAgentThoughtChunk's resolver: first
// chunk in a turn, real → different real, real → empty, empty →
// real, and within-block continuation. Each row's expected
// Activity.ID and Detail make the boundary semantics observable.
func TestACPTurnAgentThoughtChunkBlockBoundaries(t *testing.T) {
	t.Parallel()

	idA := "real-a"
	idB := "real-b"
	withID := func(s string) *string { return &s }

	type chunk struct {
		text      string
		messageID *string // nil = ACP omitted messageId on this chunk
	}

	cases := []struct {
		name       string
		chunks     []chunk
		wantIDs    []string
		wantDetail []string
	}{
		{
			name:       "single real-id block: chunks merge under one row",
			chunks:     []chunk{{"alpha", withID(idA)}, {", beta", withID(idA)}},
			wantIDs:    []string{"thinking:" + idA, "thinking:" + idA},
			wantDetail: []string{"alpha", "alpha, beta"},
		},
		{
			name:       "real-A → real-B: explicit ACP boundary, fresh row + buffer",
			chunks:     []chunk{{"alpha", withID(idA)}, {"gamma", withID(idB)}},
			wantIDs:    []string{"thinking:" + idA, "thinking:" + idB},
			wantDetail: []string{"alpha", "gamma"},
		},
		{
			name:       "no messageId in any chunk: all chunks merge under one fallback row",
			chunks:     []chunk{{"a", nil}, {"b", nil}, {"c", nil}},
			wantIDs:    []string{"thinking:" + thoughtFallbackBlockID + "-1", "thinking:" + thoughtFallbackBlockID + "-1", "thinking:" + thoughtFallbackBlockID + "-1"},
			wantDetail: []string{"a", "ab", "abc"},
		},
		{
			name:       "fallback → real: opens fresh row keyed on the real id",
			chunks:     []chunk{{"x", nil}, {"y", withID(idA)}},
			wantIDs:    []string{"thinking:" + thoughtFallbackBlockID + "-1", "thinking:" + idA},
			wantDetail: []string{"x", "y"},
		},
		{
			name:       "real → empty: opens a fallback row with a clean buffer",
			chunks:     []chunk{{"x", withID(idA)}, {"y", nil}},
			wantIDs:    []string{"thinking:" + idA, "thinking:" + thoughtFallbackBlockID + "-1"},
			wantDetail: []string{"x", "y"},
		},
		{
			// Regression: distinct fallback episodes within one turn
			// MUST get distinct Activity.IDs. mergeChatActivity
			// dedupes by id and replaces Detail wholesale on collision,
			// so a shared id would let episode 2's text overwrite
			// episode 1's row in the persisted activities array.
			name: "fallback → real → empty: each fallback episode gets a distinct counter id",
			chunks: []chunk{
				{"alpha", nil},
				{"middle", withID(idA)},
				{"omega", nil},
			},
			wantIDs:    []string{"thinking:" + thoughtFallbackBlockID + "-1", "thinking:" + idA, "thinking:" + thoughtFallbackBlockID + "-2"},
			wantDetail: []string{"alpha", "middle", "omega"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			turn := newACPTurn(64*1024, nil)
			var activities []Activity
			turn.setActivityCallback(func(a Activity) { activities = append(activities, a) })

			for _, c := range tc.chunks {
				turn.recordUpdate(acp.SessionNotification{
					SessionId: acp.SessionId("session_1"),
					Update: acp.SessionUpdate{
						AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
							Content:   acp.TextBlock(c.text),
							MessageId: c.messageID,
						},
					},
				})
			}

			if len(activities) != len(tc.wantIDs) {
				t.Fatalf("emission count = %d, want %d", len(activities), len(tc.wantIDs))
			}
			for i, want := range tc.wantIDs {
				if activities[i].ID != want {
					t.Errorf("activity %d ID = %q, want %q", i, activities[i].ID, want)
				}
				if activities[i].Detail != tc.wantDetail[i] {
					t.Errorf("activity %d Detail = %q, want %q", i, activities[i].Detail, tc.wantDetail[i])
				}
				if activities[i].Type != "thinking" {
					t.Errorf("activity %d Type = %q, want thinking", i, activities[i].Type)
				}
				if activities[i].Title != "Thinking" {
					t.Errorf("activity %d Title = %q, want Thinking", i, activities[i].Title)
				}
			}

			// Output must stay empty — thoughts are not visible transcript text.
			output, _, _ := turn.snapshot()
			if output != "" {
				t.Errorf("output = %q, want empty (thoughts must not leak into the transcript)", output)
			}
		})
	}
}

// TestACPTurnFallbackDetectionResistsAdapterSpoofingTheFallbackPrefix
// pins that boundary detection treats the fallback property as a
// turn-local flag, not a string-prefix check on Activity.ID. A
// non-spec adapter that ignores the ACP "messageId MUST be a UUID"
// rule and sends a real messageId shaped like the Hecate fallback
// prefix (e.g. `__fallback-7`) must NOT flip boundary detection
// into "I am in a fallback block" semantics — that would mis-route
// a subsequent real → empty transition as a continuation, and the
// next thought would silently glue onto the spoofed one.
//
// With the boolean flag (and not prefix-sniffing) as the source of
// truth, the spoofed real id is treated as a real id; the empty
// id that follows trips a boundary and Hecate mints a
// counter-suffixed fallback that does not collide with the
// spoofed shape.
func TestACPTurnFallbackDetectionResistsAdapterSpoofingTheFallbackPrefix(t *testing.T) {
	t.Parallel()
	turn := newACPTurn(64*1024, nil)
	var activities []Activity
	turn.setActivityCallback(func(a Activity) { activities = append(activities, a) })

	spoofed := thoughtFallbackBlockID + "-7"
	turn.recordUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("session_1"),
		Update: acp.SessionUpdate{
			AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
				Content:   acp.TextBlock("first thought"),
				MessageId: &spoofed,
			},
		},
	})
	turn.recordUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("session_1"),
		Update: acp.SessionUpdate{
			AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
				Content: acp.TextBlock("second thought"),
			},
		},
	})

	if len(activities) != 2 {
		t.Fatalf("activity count = %d, want 2", len(activities))
	}
	// The spoofed real id was adopted under flag=false. The next
	// (empty) chunk hits the real → empty branch and mints a
	// distinct counter-suffixed fallback id that does NOT match
	// the spoofed shape — so the two rows survive merge.
	if activities[0].ID == activities[1].ID {
		t.Fatalf("real → empty transition was misclassified as a continuation because boundary detection trusted the spoofed id prefix; both rows share %q", activities[0].ID)
	}
	if activities[0].Detail != "first thought" {
		t.Fatalf("first emission Detail = %q, want %q", activities[0].Detail, "first thought")
	}
	if activities[1].Detail != "second thought" {
		t.Fatalf("second emission Detail = %q, want %q (the boolean flag must trip a boundary on real → empty even when the real id starts with the fallback prefix)", activities[1].Detail, "second thought")
	}
}

// TestACPTurnCapsThinkingActivityDetailToProtectActivityRowSize
// pins the per-block accumulator cap. Each chunk re-emits the
// full accumulated Detail (mergeChatActivity replaces the
// row's Detail wholesale by Activity.ID), so an unbounded
// accumulator would inflate the persisted activities JSON and
// websocket payload with every chunk. The cap holds the
// worst-case row to thoughtMaxBytesPerBlock plus the truncation
// suffix; further chunks for the same block do NOT lengthen the
// payload.
func TestACPTurnCapsThinkingActivityDetailToProtectActivityRowSize(t *testing.T) {
	turn := newACPTurn(64*1024, nil)
	var activities []Activity
	turn.setActivityCallback(func(a Activity) { activities = append(activities, a) })

	sessionID := acp.SessionId("session_1")
	messageID := "long-thought"

	// One chunk that already exceeds the cap on its own. UTF-8 safe
	// (single-byte ASCII) — a separate test covers the rune-boundary
	// rollback.
	bigChunk := strings.Repeat("a", thoughtMaxBytesPerBlock+1024)
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
				Content:   acp.TextBlock(bigChunk),
				MessageId: &messageID,
			},
		},
	})
	// A second chunk for the same block — the row should NOT keep
	// growing; the truncation marker is sticky once tripped.
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
				Content:   acp.TextBlock(strings.Repeat("b", 4096)),
				MessageId: &messageID,
			},
		},
	})

	if len(activities) != 2 {
		t.Fatalf("activity count = %d, want 2 (one per chunk)", len(activities))
	}
	for i, a := range activities {
		if !strings.HasSuffix(a.Detail, thoughtTruncationSuffix) {
			t.Fatalf("activity %d missing truncation suffix; Detail = %q", i, a.Detail[:min(len(a.Detail), 80)])
		}
		body := strings.TrimSuffix(a.Detail, thoughtTruncationSuffix)
		if len(body) > thoughtMaxBytesPerBlock {
			t.Fatalf("activity %d Detail body = %d bytes, want ≤ %d (cap exceeded)", i, len(body), thoughtMaxBytesPerBlock)
		}
		if strings.Contains(body, "b") {
			t.Fatalf("activity %d Detail body contains text from the post-cap chunk; further bytes must be dropped, not appended", i)
		}
	}
	if activities[0].Detail != activities[1].Detail {
		t.Fatalf("Detail diverged between chunks after the cap; once truncated the row should be stable.\nfirst:  %d bytes\nsecond: %d bytes", len(activities[0].Detail), len(activities[1].Detail))
	}
}

// TestACPTurnTruncatesThinkingDetailOnUTF8RuneBoundary protects
// against slicing a multi-byte rune mid-sequence; the resulting
// Activity.Detail is JSON-serialized into the chat row, and
// stray UTF-8 continuation bytes would corrupt the payload (or be
// replaced with U+FFFD by lenient decoders, losing data).
func TestACPTurnTruncatesThinkingDetailOnUTF8RuneBoundary(t *testing.T) {
	turn := newACPTurn(64*1024, nil)
	var activities []Activity
	turn.setActivityCallback(func(a Activity) { activities = append(activities, a) })

	// Build a payload whose cut point falls inside a 3-byte rune. The
	// Japanese "あ" is 3 bytes in UTF-8 (E3 81 82); pad with ASCII so
	// the cap lands two bytes into one of the runes.
	prefix := strings.Repeat("a", thoughtMaxBytesPerBlock-1)
	payload := prefix + "あ" + "tail"
	messageID := "rune-cut"
	turn.recordUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("session_1"),
		Update: acp.SessionUpdate{
			AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
				Content:   acp.TextBlock(payload),
				MessageId: &messageID,
			},
		},
	})

	if len(activities) != 1 {
		t.Fatalf("activity count = %d, want 1", len(activities))
	}
	body := strings.TrimSuffix(activities[0].Detail, thoughtTruncationSuffix)
	if !utf8.ValidString(body) {
		t.Fatalf("Detail body is not valid UTF-8 — rune-boundary rollback failed.\nbody (last 8 bytes): % x", body[max(len(body)-8, 0):])
	}
	if strings.HasSuffix(body, "あ") {
		t.Fatalf("Detail body unexpectedly retained the truncated rune; the cut should have rolled back PAST it")
	}
}

// TestACPTurnResetsThinkingTruncationStateOnBlockBoundary ensures
// the truncation flag is per-block, not per-turn. A long thought
// hitting the cap must NOT leave the next block pre-marked
// truncated when it has only emitted a few bytes.
func TestACPTurnResetsThinkingTruncationStateOnBlockBoundary(t *testing.T) {
	turn := newACPTurn(64*1024, nil)
	var activities []Activity
	turn.setActivityCallback(func(a Activity) { activities = append(activities, a) })

	first := "block-1"
	second := "block-2"
	turn.recordUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("session_1"),
		Update: acp.SessionUpdate{
			AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
				Content:   acp.TextBlock(strings.Repeat("a", thoughtMaxBytesPerBlock+1)),
				MessageId: &first,
			},
		},
	})
	turn.recordUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("session_1"),
		Update: acp.SessionUpdate{
			AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
				Content:   acp.TextBlock("short follow-up"),
				MessageId: &second,
			},
		},
	})

	if len(activities) != 2 {
		t.Fatalf("activity count = %d, want 2", len(activities))
	}
	if !strings.HasSuffix(activities[0].Detail, thoughtTruncationSuffix) {
		t.Fatalf("first block should be marked truncated; Detail = %q", activities[0].Detail[:min(len(activities[0].Detail), 80)])
	}
	if strings.HasSuffix(activities[1].Detail, thoughtTruncationSuffix) {
		t.Fatalf("second (short) block carries truncation suffix from prior block; truncation state must reset on block boundary.\nDetail = %q", activities[1].Detail)
	}
	if activities[1].Detail != "short follow-up" {
		t.Fatalf("second block Detail = %q, want plain follow-up text", activities[1].Detail)
	}
}

func TestACPTurnEmitsFileChangeActivitiesForMutatingToolCallsOnCompletion(t *testing.T) {
	turn := newACPTurn(64*1024, nil)
	var activities []Activity
	turn.setActivityCallback(func(a Activity) { activities = append(activities, a) })

	sessionID := acp.SessionId("session_1")
	line := 42

	// In-progress edit: tool_call activity emits, no file_change yet
	// (the file isn't actually changed until the call completes).
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{
				ToolCallId: acp.ToolCallId("call_1"),
				Title:      "edit",
				Status:     acp.ToolCallStatusInProgress,
				Kind:       acp.ToolKindEdit,
				Locations: []acp.ToolCallLocation{
					{Path: "internal/example.go", Line: &line},
				},
			},
		},
	})
	// Same call, completed, with two locations: should emit two file_change activities.
	completedStatus := acp.ToolCallStatusCompleted
	completedTitle := "edit"
	editKind := acp.ToolKindEdit
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			ToolCallUpdate: &acp.SessionToolCallUpdate{
				ToolCallId: acp.ToolCallId("call_1"),
				Status:     &completedStatus,
				Title:      &completedTitle,
				Kind:       &editKind,
				Locations: []acp.ToolCallLocation{
					{Path: "internal/example.go", Line: &line},
					{Path: "docs/example.md"},
				},
			},
		},
	})

	// A read tool call must NOT emit file_change activities even on completion.
	readKind := acp.ToolKindRead
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{
				ToolCallId: acp.ToolCallId("call_2"),
				Title:      "read",
				Status:     acp.ToolCallStatusCompleted,
				Kind:       readKind,
				Locations:  []acp.ToolCallLocation{{Path: "internal/other.go"}},
			},
		},
	})

	// Expected emissions, in order:
	//   1. tool_call (call_1, in_progress)
	//   2. tool_call (call_1, completed)
	//   3. file_change (call_1, internal/example.go:42)
	//   4. file_change (call_1, docs/example.md)
	//   5. tool_call (call_2, completed; read kind, no file_change)
	if len(activities) != 5 {
		t.Fatalf("activity count = %d, want 5; got types %v", len(activities), activityTypes(activities))
	}

	fileChanges := filterActivities(activities, "file_change")
	if len(fileChanges) != 2 {
		t.Fatalf("file_change count = %d, want 2 (one per location on the completed edit); got %v", len(fileChanges), activityTitles(fileChanges))
	}
	if fileChanges[0].Title != "internal/example.go:42" {
		t.Fatalf("first file_change title = %q, want path with line number", fileChanges[0].Title)
	}
	if fileChanges[1].Title != "docs/example.md" {
		t.Fatalf("second file_change title = %q, want plain path", fileChanges[1].Title)
	}
	if fileChanges[0].ID == fileChanges[1].ID {
		t.Fatalf("file_change activities for distinct paths share an ID %q; they must differ so the UI can render them separately", fileChanges[0].ID)
	}
	for _, fc := range fileChanges {
		if fc.Status != "completed" {
			t.Fatalf("file_change status = %q, want completed", fc.Status)
		}
		if fc.Kind != "edit" {
			t.Fatalf("file_change kind = %q, want edit", fc.Kind)
		}
	}
}

// TestACPTurnAggregatesFileChangeLocationsBySharedPath pins the
// dedupe-by-path behavior. ACP `Locations` may carry multiple
// entries for the same file (e.g. several edited line ranges in
// one call). Without aggregation, each entry would emit an activity
// with the same `file_change:<toolCallID>:<path>` Activity.ID and
// downstream mergeChatActivity would let later emissions
// overwrite earlier ones — the operator would see the last range
// only, with the others silently lost. We collapse same-path
// entries into one row, summarizing the line numbers in the title.
func TestACPTurnAggregatesFileChangeLocationsBySharedPath(t *testing.T) {
	turn := newACPTurn(64*1024, nil)
	var activities []Activity
	turn.setActivityCallback(func(a Activity) { activities = append(activities, a) })

	line1, line2, line3, line4 := 42, 100, 200, 250
	completed := acp.ToolCallStatusCompleted
	editKind := acp.ToolKindEdit
	title := "edit"

	turn.recordUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("session_1"),
		Update: acp.SessionUpdate{
			ToolCallUpdate: &acp.SessionToolCallUpdate{
				ToolCallId: acp.ToolCallId("call_1"),
				Status:     &completed,
				Title:      &title,
				Kind:       &editKind,
				Locations: []acp.ToolCallLocation{
					{Path: "internal/example.go", Line: &line1},
					{Path: "internal/example.go", Line: &line2},
					{Path: "internal/example.go", Line: &line3},
					{Path: "internal/example.go", Line: &line4},
					{Path: "docs/example.md"}, // separate file, no line
				},
			},
		},
	})

	// Expected emissions: 1 tool_call activity + 2 file_change rows
	// (one per unique path), NOT 1+5.
	fileChanges := filterActivities(activities, "file_change")
	if len(fileChanges) != 2 {
		t.Fatalf("file_change count = %d, want 2 (one row per unique path); got titles %v", len(fileChanges), activityTitles(fileChanges))
	}
	if fileChanges[0].Title != "internal/example.go (42, 100, 200, +1 more)" {
		t.Fatalf("first row title = %q, want path with summarized lines and overflow tail", fileChanges[0].Title)
	}
	if fileChanges[1].Title != "docs/example.md" {
		t.Fatalf("second row title = %q, want plain path (no line info on the source location)", fileChanges[1].Title)
	}
	// Activity.IDs must be unique per path so mergeChatActivity
	// keeps both rows; the per-path collapse must NOT extend across
	// distinct files.
	if fileChanges[0].ID == fileChanges[1].ID {
		t.Fatalf("file_change IDs collide across distinct paths: %q", fileChanges[0].ID)
	}
}

// TestACPTurnRetainsToolKindAcrossUpdatesThatOmitIt pins the
// per-call kind cache. SessionToolCallUpdate.Kind is optional;
// adapters routinely emit Kind on the initial ToolCall and drop
// it on the matching completion update. Without the cache, the
// completion update would compute kind == "" and skip
// emitFileChangeActivities — silently losing every per-file row
// for an edit that actually happened.
func TestACPTurnRetainsToolKindAcrossUpdatesThatOmitIt(t *testing.T) {
	turn := newACPTurn(64*1024, nil)
	var activities []Activity
	turn.setActivityCallback(func(a Activity) { activities = append(activities, a) })

	// Initial ToolCall carries kind = edit.
	turn.recordUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("session_1"),
		Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{
				ToolCallId: acp.ToolCallId("call_1"),
				Title:      "edit",
				Status:     acp.ToolCallStatusInProgress,
				Kind:       acp.ToolKindEdit,
			},
		},
	})
	// Completion update OMITS Kind. Locations are present.
	completed := acp.ToolCallStatusCompleted
	completedTitle := "edit"
	turn.recordUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("session_1"),
		Update: acp.SessionUpdate{
			ToolCallUpdate: &acp.SessionToolCallUpdate{
				ToolCallId: acp.ToolCallId("call_1"),
				Status:     &completed,
				Title:      &completedTitle,
				// Kind: nil — adapter omitted it on completion.
				Locations: []acp.ToolCallLocation{
					{Path: "internal/example.go"},
				},
			},
		},
	})

	fileChanges := filterActivities(activities, "file_change")
	if len(fileChanges) != 1 {
		t.Fatalf("file_change count = %d, want 1 (the cached kind=edit must drive emission even though the completion update omitted Kind); got types %v", len(fileChanges), activityTypes(activities))
	}
	if fileChanges[0].Kind != "edit" {
		t.Fatalf("file_change kind = %q, want edit (resolved from the per-call cache)", fileChanges[0].Kind)
	}
	if fileChanges[0].Title != "internal/example.go" {
		t.Fatalf("file_change title = %q, want path", fileChanges[0].Title)
	}

	// The completion tool_call activity must also surface the
	// resolved kind so the timeline doesn't render a blank kind
	// label after the cache lookup.
	toolCalls := filterActivities(activities, "tool_call")
	if len(toolCalls) < 2 {
		t.Fatalf("tool_call count = %d, want ≥ 2 (initial + completion)", len(toolCalls))
	}
	if toolCalls[len(toolCalls)-1].Kind != "edit" {
		t.Fatalf("completion tool_call kind = %q, want edit (cache-resolved)", toolCalls[len(toolCalls)-1].Kind)
	}
}

// TestACPTurnRecordToolCallUpdateDefaultsTitleToToolCallId pins the
// fix for an mergeChatActivity-drop edge case. ACP's
// SessionToolCallUpdate.Title is optional. mergeChatActivity
// silently discards an emission whose Title is empty when there is
// no prior row with a matching Activity.ID to merge into — and a
// ToolCallUpdate without a preceding ToolCall is rare but legal
// (e.g., adapter resumed mid-stream). recordToolCallUpdate
// defaults Title to the ToolCallId so the activity always carries
// something renderable and survives the merge path.
func TestACPTurnRecordToolCallUpdateDefaultsTitleToToolCallId(t *testing.T) {
	t.Parallel()
	turn := newACPTurn(64*1024, nil)
	var activities []Activity
	turn.setActivityCallback(func(a Activity) { activities = append(activities, a) })

	completed := acp.ToolCallStatusCompleted
	turn.recordUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("session_1"),
		Update: acp.SessionUpdate{
			ToolCallUpdate: &acp.SessionToolCallUpdate{
				ToolCallId: acp.ToolCallId("call_orphan"),
				Status:     &completed,
				// Title omitted — adapter sent only a status update.
			},
		},
	})

	toolCalls := filterActivities(activities, "tool_call")
	if len(toolCalls) != 1 {
		t.Fatalf("tool_call count = %d, want 1 (the activity must survive merge with no preceding ToolCall)", len(toolCalls))
	}
	if toolCalls[0].Title != "call_orphan" {
		t.Fatalf("tool_call Title = %q, want %q (defaulted to ToolCallId so mergeChatActivity does not drop the row)", toolCalls[0].Title, "call_orphan")
	}
}

func TestACPTurnSkipsFileChangeForInProgressMutatingToolCalls(t *testing.T) {
	turn := newACPTurn(64*1024, nil)
	var activities []Activity
	turn.setActivityCallback(func(a Activity) { activities = append(activities, a) })

	turn.recordUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("session_1"),
		Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{
				ToolCallId: acp.ToolCallId("call_1"),
				Title:      "delete",
				Status:     acp.ToolCallStatusInProgress,
				Kind:       acp.ToolKindDelete,
				Locations:  []acp.ToolCallLocation{{Path: "doomed.txt"}},
			},
		},
	})

	// Only the tool_call activity should fire — no file_change yet:
	// emitting one before the delete actually completes would tell the
	// operator a file is gone when the adapter is still mid-call (and
	// the call could fail).
	if len(activities) != 1 {
		t.Fatalf("activity count = %d, want 1 (no file_change before completion); got types %v", len(activities), activityTypes(activities))
	}
	if activities[0].Type != "tool_call" {
		t.Fatalf("only emission should be tool_call, got %q", activities[0].Type)
	}
}

func filterActivities(items []Activity, want string) []Activity {
	out := make([]Activity, 0, len(items))
	for _, a := range items {
		if a.Type == want {
			out = append(out, a)
		}
	}
	return out
}

func activityTypes(items []Activity) []string {
	out := make([]string, 0, len(items))
	for _, a := range items {
		out = append(out, a.Type)
	}
	return out
}

func activityTitles(items []Activity) []string {
	out := make([]string, 0, len(items))
	for _, a := range items {
		out = append(out, a.Title)
	}
	return out
}

func TestACPTurnReplacesProgressNarrationWhenAgentMessageIDChanges(t *testing.T) {
	var snapshots []string
	turn := newACPTurn(64*1024, func(text string) {
		snapshots = append(snapshots, text)
	})
	sessionID := acp.SessionId("session_1")
	progressID := "019df226-progress"
	finalID := "019df226-final"

	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content:   acp.TextBlock("I’ll inspect the current git diff and summarize it."),
				MessageId: &progressID,
			},
		},
	})
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content:   acp.TextBlock("There’s no current tracked-file diff."),
				MessageId: &finalID,
			},
		},
	})

	output, _, _ := turn.snapshot()
	if output != "There’s no current tracked-file diff." {
		t.Fatalf("output = %q, want latest agent message only", output)
	}
	if len(snapshots) != 2 || snapshots[0] != "I’ll inspect the current git diff and summarize it." || snapshots[1] != output {
		t.Fatalf("snapshots = %#v, want progress then replacement final", snapshots)
	}
}

func TestACPTurnReplacesPreToolNarrationWhenAnswerContinuesSameMessage(t *testing.T) {
	var snapshots []string
	turn := newACPTurn(64*1024, func(text string) {
		snapshots = append(snapshots, text)
	})
	sessionID := acp.SessionId("session_1")
	messageID := "019df226-same-message"

	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content:   acp.TextBlock("I’ll check the current worktree diff and summarize the changed files plus the important hunks."),
				MessageId: &messageID,
			},
		},
	})
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{
				SessionUpdate: "tool_call",
				ToolCallId:    acp.ToolCallId("call_diff"),
				Title:         "git diff --stat",
				Status:        acp.ToolCallStatusInProgress,
				Kind:          acp.ToolKindExecute,
			},
		},
	})
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content:   acp.TextBlock("There are 11 modified files."),
				MessageId: &messageID,
			},
		},
	})

	output, _, _ := turn.snapshot()
	if output != "There are 11 modified files." {
		t.Fatalf("output = %q, want final answer without pre-tool narration", output)
	}
	wantSnapshots := []string{
		"I’ll check the current worktree diff and summarize the changed files plus the important hunks.",
		"",
		"There are 11 modified files.",
	}
	if len(snapshots) != len(wantSnapshots) {
		t.Fatalf("snapshots = %#v, want %#v", snapshots, wantSnapshots)
	}
	for i := range wantSnapshots {
		if snapshots[i] != wantSnapshots[i] {
			t.Fatalf("snapshots[%d] = %q, want %q in %#v", i, snapshots[i], wantSnapshots[i], snapshots)
		}
	}
}

func TestACPTurnConcatenatesChunksWithSameAgentMessageID(t *testing.T) {
	turn := newACPTurn(64*1024, nil)
	sessionID := acp.SessionId("session_1")
	messageID := "019df226-final"

	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content:   acp.TextBlock("There’s no "),
				MessageId: &messageID,
			},
		},
	})
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content:   acp.TextBlock("current diff."),
				MessageId: &messageID,
			},
		},
	})

	output, _, _ := turn.snapshot()
	if output != "There’s no current diff." {
		t.Fatalf("output = %q, want same-message chunks concatenated", output)
	}
}

func TestACPTurnCapturesUsageUpdate(t *testing.T) {
	turn := newACPTurn(64*1024, nil)
	turn.recordUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("session_1"),
		Update: acp.SessionUpdate{
			UsageUpdate: &acp.SessionUsageUpdate{
				SessionUpdate: "usage_update",
				Size:          200_000,
				Used:          42_000,
				Cost:          &acp.Cost{Amount: 0.1234, Currency: "usd"},
			},
		},
	})

	output, _, usage := turn.snapshot()
	if output != "" {
		t.Fatalf("output = %q, want usage update excluded from transcript", output)
	}
	if usage.ContextSize != 200_000 || usage.ContextUsed != 42_000 {
		t.Fatalf("usage context = %d/%d, want 42000/200000", usage.ContextUsed, usage.ContextSize)
	}
	if usage.ReportedCostAmount != "0.1234" || usage.ReportedCostCurrency != "USD" {
		t.Fatalf("usage cost = %s %s, want 0.1234 USD", usage.ReportedCostAmount, usage.ReportedCostCurrency)
	}
}

func TestACPTurnEmitsToolAndPlanActivities(t *testing.T) {
	var activities []Activity
	turn := newACPTurn(64*1024, nil)
	turn.setActivityCallback(func(activity Activity) {
		activities = append(activities, activity)
	})
	sessionID := acp.SessionId("session_1")
	line := 42
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{
				SessionUpdate: "tool_call",
				ToolCallId:    acp.ToolCallId("call_1"),
				Title:         "git diff --stat",
				Status:        acp.ToolCallStatusInProgress,
				Kind:          acp.ToolKindExecute,
				Locations:     []acp.ToolCallLocation{{Path: "README.md", Line: &line}},
			},
		},
	})
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			Plan: &acp.SessionUpdatePlan{
				SessionUpdate: "plan",
				Entries: []acp.PlanEntry{
					{Content: "Inspect changes", Status: acp.PlanEntryStatusCompleted, Priority: acp.PlanEntryPriorityHigh},
					{Content: "Summarize result", Status: acp.PlanEntryStatusInProgress, Priority: acp.PlanEntryPriorityMedium},
				},
			},
		},
	})

	if len(activities) != 3 {
		t.Fatalf("activities = %#v, want 3", activities)
	}
	if got := activities[0]; got.ID != "tool:call_1" || got.Type != "tool_call" || got.Status != "running" || got.Kind != "execute" || got.Title != "git diff --stat" || !strings.Contains(got.Detail, "README.md:42") {
		t.Fatalf("tool activity = %#v", got)
	}
	if got := activities[1]; got.Type != "plan" || got.Status != "completed" || got.Kind != "high" || got.Title != "Inspect changes" {
		t.Fatalf("first plan activity = %#v", got)
	}
	if got := activities[2]; got.Type != "plan" || got.Status != "in_progress" || got.Kind != "medium" || got.Title != "Summarize result" {
		t.Fatalf("second plan activity = %#v", got)
	}
}

func TestACPTurnSurfacesToolRawInputCommand(t *testing.T) {
	var activities []Activity
	turn := newACPTurn(64*1024, nil)
	turn.setActivityCallback(func(activity Activity) {
		activities = append(activities, activity)
	})
	sessionID := acp.SessionId("session_1")
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{
				SessionUpdate: "tool_call",
				ToolCallId:    acp.ToolCallId("call_shell"),
				Title:         "call_shell",
				Status:        acp.ToolCallStatusInProgress,
				Kind:          acp.ToolKindExecute,
				RawInput: map[string]any{
					"command": "/bin/zsh -lc 'go test ./internal/agentadapters'",
				},
			},
		},
	})

	if len(activities) != 1 {
		t.Fatalf("activities = %#v, want 1", activities)
	}
	if got := activities[0].Detail; got != "execute · /bin/zsh -lc 'go test ./internal/agentadapters'" {
		t.Fatalf("activity detail = %q", got)
	}
}

func TestACPTurnSurfacesToolContentPreview(t *testing.T) {
	var activities []Activity
	turn := newACPTurn(64*1024, nil)
	turn.setActivityCallback(func(activity Activity) {
		activities = append(activities, activity)
	})
	sessionID := acp.SessionId("session_1")
	status := acp.ToolCallStatusCompleted
	turn.recordUpdate(acp.SessionNotification{
		SessionId: sessionID,
		Update: acp.SessionUpdate{
			ToolCallUpdate: &acp.SessionToolCallUpdate{
				ToolCallId: acp.ToolCallId("call_shell"),
				Status:     &status,
				Kind:       acp.Ptr(acp.ToolKindExecute),
				Content: []acp.ToolCallContent{
					acp.ToolContent(acp.TextBlock("ok\nPASS ./internal/agentadapters")),
				},
			},
		},
	})

	if len(activities) != 1 {
		t.Fatalf("activities = %#v, want 1", activities)
	}
	if got := activities[0].Detail; got != "execute · output: ok PASS ./internal/agentadapters" {
		t.Fatalf("activity detail = %q", got)
	}
}

func installFakeACPExecutable(t *testing.T, name string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	exe := filepath.Join(bin, name)
	script := fmt.Sprintf(
		"#!/bin/sh\nHECATE_FAKE_ACP_AGENT=1 HECATE_FAKE_ACP_LOAD_SESSION_FAIL=%q HECATE_FAKE_ACP_NEW_SESSION_DELAY=%q exec %q -test.run '^TestFakeACPAgentProcess$'\n",
		os.Getenv("HECATE_FAKE_ACP_LOAD_SESSION_FAIL"),
		os.Getenv("HECATE_FAKE_ACP_NEW_SESSION_DELAY"),
		os.Args[0],
	)
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ACP executable: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type fakeACPAgent struct {
	conn *acp.AgentSideConnection

	mu       sync.Mutex
	sessions map[string]*fakeACPSession
}

type fakeACPSession struct {
	turns  int
	cancel context.CancelFunc
}

func newFakeACPAgent() *fakeACPAgent {
	return &fakeACPAgent{sessions: make(map[string]*fakeACPSession)}
}

func (a *fakeACPAgent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (a *fakeACPAgent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession:         true,
			SessionCapabilities: acp.SessionCapabilities{Close: &acp.SessionCloseCapabilities{}},
		},
	}, nil
}

func (a *fakeACPAgent) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	if delay, err := time.ParseDuration(os.Getenv("HECATE_FAKE_ACP_NEW_SESSION_DELAY")); err == nil && delay > 0 {
		time.Sleep(delay)
	}
	id := fmt.Sprintf("fake_session_%d", time.Now().UnixNano())
	a.mu.Lock()
	a.sessions[id] = &fakeACPSession{}
	a.mu.Unlock()
	return acp.NewSessionResponse{SessionId: acp.SessionId(id)}, nil
}

func (a *fakeACPAgent) LoadSession(_ context.Context, params acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	if os.Getenv("HECATE_FAKE_ACP_LOAD_SESSION_FAIL") == "1" {
		return acp.LoadSessionResponse{}, fmt.Errorf("fake persisted session %s not found", params.SessionId)
	}
	a.mu.Lock()
	a.sessions[string(params.SessionId)] = &fakeACPSession{}
	a.mu.Unlock()
	return acp.LoadSessionResponse{}, nil
}

func (a *fakeACPAgent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	session, err := a.session(params.SessionId)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	prompt := promptText(params.Prompt)
	turnCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	session.turns++
	turn := session.turns
	session.cancel = cancel
	a.mu.Unlock()
	defer cancel()

	if prompt == "wait" {
		if err := a.conn.SessionUpdate(turnCtx, acp.SessionNotification{
			SessionId: params.SessionId,
			Update:    acp.UpdateAgentMessageText("waiting"),
		}); err != nil {
			return acp.PromptResponse{}, err
		}
		<-turnCtx.Done()
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	if prompt == "ignore_cancel" {
		if err := a.conn.SessionUpdate(turnCtx, acp.SessionNotification{
			SessionId: params.SessionId,
			Update:    acp.UpdateAgentMessageText("waiting"),
		}); err != nil {
			return acp.PromptResponse{}, err
		}
		select {}
	}

	if err := a.conn.SessionUpdate(turnCtx, acp.SessionNotification{
		SessionId: params.SessionId,
		Update:    acp.UpdateAgentMessageText(fmt.Sprintf("turn %d: %s", turn, prompt)),
	}); err != nil {
		return acp.PromptResponse{}, err
	}
	if err := a.conn.SessionUpdate(turnCtx, acp.SessionNotification{
		SessionId: params.SessionId,
		Update: acp.SessionUpdate{
			UsageUpdate: &acp.SessionUsageUpdate{
				SessionUpdate: "usage_update",
				Size:          200_000,
				Used:          turn * 10_000,
			},
		},
	}); err != nil {
		return acp.PromptResponse{}, err
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (a *fakeACPAgent) Cancel(_ context.Context, params acp.CancelNotification) error {
	session, err := a.session(params.SessionId)
	if err == nil && session.cancel != nil {
		session.cancel()
	}
	return nil
}

func (a *fakeACPAgent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}

func (a *fakeACPAgent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionList)
}

func (a *fakeACPAgent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionResume)
}

func (a *fakeACPAgent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionSetConfigOption)
}

func (a *fakeACPAgent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionSetMode)
}

func (a *fakeACPAgent) session(id acp.SessionId) (*fakeACPSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session := a.sessions[string(id)]
	if session == nil {
		return nil, fmt.Errorf("session %q not found", id)
	}
	return session, nil
}

func promptText(blocks []acp.ContentBlock) string {
	var b strings.Builder
	for _, block := range blocks {
		if block.Text != nil {
			b.WriteString(block.Text.Text)
		}
	}
	return b.String()
}
