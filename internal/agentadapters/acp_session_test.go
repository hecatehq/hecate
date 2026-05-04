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

	output, raw := turn.snapshot()
	if output != "final answer" {
		t.Fatalf("output = %q, want final answer only", output)
	}
	if !strings.Contains(raw, "usage_update") {
		t.Fatalf("raw output = %q, want usage update retained for diagnostics", raw)
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
			SessionCapabilities: acp.SessionCapabilities{Close: &acp.SessionCloseCapabilities{}},
		},
	}, nil
}

func (a *fakeACPAgent) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	id := fmt.Sprintf("fake_session_%d", time.Now().UnixNano())
	a.mu.Lock()
	a.sessions[id] = &fakeACPSession{}
	a.mu.Unlock()
	return acp.NewSessionResponse{SessionId: acp.SessionId(id)}, nil
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
