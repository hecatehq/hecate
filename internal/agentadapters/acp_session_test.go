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

	acp "github.com/coder/acp-go-sdk"
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

func TestSessionManagerSerializesConcurrentSessionStart(t *testing.T) {
	installFakeACPExecutable(t, "codex-acp")
	t.Setenv("HECATE_FAKE_ACP_NEW_SESSION_DELAY", "100ms")
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

func installFakeACPExecutable(t *testing.T, name string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	exe := filepath.Join(bin, name)
	script := fmt.Sprintf("#!/bin/sh\nHECATE_FAKE_ACP_AGENT=1 exec %q -test.run '^TestFakeACPAgentProcess$'\n", os.Args[0])
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
