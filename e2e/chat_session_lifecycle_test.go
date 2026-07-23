//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChatSessionApplicationLayerLifecycleE2E(t *testing.T) {
	baseURL := gatewayServer(t, "HECATE_BACKEND=sqlite")

	created := postJSONDecode[e2eChatSessionResponse](t, baseURL+"/hecate/v1/chat/sessions", `{
		"agent_id": "hecate",
		"title": "chat application layer e2e"
	}`)
	if created.Object != "chat_session" {
		t.Fatalf("created object = %q, want chat_session", created.Object)
	}
	if created.Data.ID == "" {
		t.Fatal("created session id is empty")
	}
	if created.Data.AgentID != "hecate" || created.Data.Title != "chat application layer e2e" || created.Data.Status != "idle" {
		t.Fatalf("created session = %+v, want hecate idle session with title", created.Data)
	}

	got := getJSON[e2eChatSessionResponse](t, baseURL+"/hecate/v1/chat/sessions/"+created.Data.ID)
	if got.Data.ID != created.Data.ID || got.Data.AgentID != "hecate" {
		t.Fatalf("fetched session = %+v, want created hecate session", got.Data)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, baseURL+"/hecate/v1/chat/sessions/"+created.Data.ID, nil)
	if err != nil {
		t.Fatalf("NewRequest DELETE chat session: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE chat session: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE chat session status = %d, want 204; body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	req, err = http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+"/hecate/v1/chat/sessions/"+created.Data.ID, nil)
	if err != nil {
		t.Fatalf("NewRequest GET deleted chat session: %v", err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET deleted chat session: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted chat session status = %d, want 404; body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

func TestExternalAgentChatDeleteDeletesNativeACPSessionE2E(t *testing.T) {
	fakeACP := buildFakeACPAgentTestBinary(t)
	adapterDir := t.TempDir()
	deleteFile := filepath.Join(t.TempDir(), "deleted-native-session.txt")
	installFakeACPAdapterExecutable(t, adapterDir, "codex-acp-adapter", fakeACP, map[string]string{
		"HECATE_FAKE_ACP_DELETE_FILE": deleteFile,
	})

	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_AGENT_ADAPTER_TEST_PROCESS_OVERRIDES=codex",
		"HOME="+t.TempDir(),
		"PATH="+adapterDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	workspace := t.TempDir()
	created := postJSONDecode[e2eChatSessionResponse](t, baseURL+"/hecate/v1/chat/sessions", fmt.Sprintf(`{
		"agent_id": "codex",
		"workspace": %q,
		"title": "external delete e2e"
	}`, workspace))
	if created.Data.NativeSessionID == "" {
		t.Fatalf("created external session native id is empty: %+v", created.Data)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, baseURL+"/hecate/v1/chat/sessions/"+created.Data.ID, nil)
	if err != nil {
		t.Fatalf("NewRequest DELETE external chat session: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE external chat session: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE external chat session status = %d, want 204; body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	got, err := os.ReadFile(deleteFile)
	if err != nil {
		t.Fatalf("read fake ACP delete file: %v", err)
	}
	if strings.TrimSpace(string(got)) != created.Data.NativeSessionID {
		t.Fatalf("native session deleted = %q, want %q", strings.TrimSpace(string(got)), created.Data.NativeSessionID)
	}

	req, err = http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+"/hecate/v1/chat/sessions/"+created.Data.ID, nil)
	if err != nil {
		t.Fatalf("NewRequest GET deleted external chat session: %v", err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET deleted external chat session: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted external chat session status = %d, want 404; body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

func TestExternalAgentChatTurnSurvivesDisconnectAndKeyedReplayE2E(t *testing.T) {
	fakeACP := buildFakeACPAgentTestBinary(t)
	adapterDir := t.TempDir()
	promptSessions := filepath.Join(t.TempDir(), "prompt-sessions.txt")
	releaseFile := filepath.Join(t.TempDir(), "release-prompt")
	installFakeACPAdapterExecutable(t, adapterDir, "codex-acp-adapter", fakeACP, map[string]string{
		"HECATE_FAKE_ACP_PROMPT_RELEASE_FILE":    releaseFile,
		"HECATE_FAKE_ACP_PROMPT_SESSION_CAPTURE": promptSessions,
	})

	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_AGENT_ADAPTER_TEST_PROCESS_OVERRIDES=codex",
		"HOME="+t.TempDir(),
		"PATH="+adapterDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	created := postJSONDecode[e2eChatSessionResponse](t, baseURL+"/hecate/v1/chat/sessions", fmt.Sprintf(`{
		"agent_id": "codex",
		"workspace": %q,
		"title": "external disconnect e2e"
	}`, t.TempDir()))

	messageURL := baseURL + "/hecate/v1/chat/sessions/" + created.Data.ID + "/messages"
	messageBody := `{
		"content": "survive disconnect",
		"execution_mode": "external_agent",
		"client_request_id": "external-disconnect-e2e"
	}`
	requestCtx, disconnect := context.WithCancel(context.Background())
	t.Cleanup(func() {
		disconnect()
		_ = os.WriteFile(releaseFile, []byte("cleanup"), 0o600)
	})
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, messageURL, strings.NewReader(messageBody))
	if err != nil {
		t.Fatalf("NewRequest external agent message: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	requestDone := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(request)
		if resp != nil {
			resp.Body.Close()
		}
		requestDone <- err
	}()

	waitForFileLineCount(t, promptSessions, 1)
	disconnect()
	select {
	case err := <-requestDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("disconnected message request error = %v, want context cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("message client did not observe its disconnect")
	}

	replay := postJSONDecode[e2eChatSessionResponse](t, messageURL, messageBody)
	if replay.MessageRequest == nil || !replay.MessageRequest.Replay || replay.MessageRequest.CommittedMessageID == "" {
		t.Fatalf("keyed replay metadata = %+v, want committed replay", replay.MessageRequest)
	}
	if len(replay.Data.Messages) != 2 || replay.Data.Messages[1].Role != "assistant" || replay.Data.Messages[1].Status != "running" {
		t.Fatalf("keyed replay transcript = %+v, want one user and one running assistant", replay.Data.Messages)
	}
	if got := readFileLines(t, promptSessions); len(got) != 1 {
		t.Fatalf("fake ACP prompt dispatches = %d (%v), want exactly one", len(got), got)
	}

	if err := os.WriteFile(releaseFile, []byte("continue"), 0o600); err != nil {
		t.Fatalf("release fake ACP prompt: %v", err)
	}
	settled := waitForExternalAgentChatCompletion(t, baseURL, created.Data.ID)
	if settled.Data.TurnsUsed != 1 || len(settled.Data.Messages) != 2 {
		t.Fatalf("settled session turns=%d messages=%+v, want one completed turn", settled.Data.TurnsUsed, settled.Data.Messages)
	}
	assistant := settled.Data.Messages[1]
	if assistant.Role != "assistant" || assistant.Status != "completed" || assistant.Content != "turn 1: survive disconnect" || assistant.CompletedAt == "" {
		t.Fatalf("settled assistant = %+v, want completed fake ACP response", assistant)
	}
	if got := readFileLines(t, promptSessions); len(got) != 1 {
		t.Fatalf("fake ACP prompt dispatches after settlement = %d (%v), want exactly one", len(got), got)
	}
}

func buildFakeACPAgentTestBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-acp-agent.test")
	cmd := exec.Command("go", "test", "-c", "-o", bin, "./internal/agentadapters")
	cmd.Dir = moduleRootDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake ACP agent test binary: %v\n%s", err, out)
	}
	return bin
}

func installFakeACPAdapterExecutable(t *testing.T, dir, name, testBinary string, env map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fake adapter dir: %v", err)
	}
	exe := filepath.Join(dir, name)
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("HECATE_FAKE_ACP_AGENT=1")
	for key, value := range env {
		b.WriteString(" ")
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(shellQuote(value))
	}
	b.WriteString(" exec ")
	b.WriteString(shellQuote(testBinary))
	b.WriteString(" -test.run '^TestFakeACPAgentProcess$'\n")
	if err := os.WriteFile(exe, []byte(b.String()), 0o755); err != nil {
		t.Fatalf("write fake adapter executable: %v", err)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func waitForFileLineCount(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lines, err := fileLines(path)
		if err == nil && len(lines) >= want {
			return
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read %s: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d lines in %s; got %d", want, path, len(lines))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readFileLines(t *testing.T, path string) []string {
	t.Helper()
	lines, err := fileLines(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return lines
}

func fileLines(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

func waitForExternalAgentChatCompletion(t *testing.T, baseURL, sessionID string) e2eChatSessionResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		session := getJSON[e2eChatSessionResponse](t, baseURL+"/hecate/v1/chat/sessions/"+sessionID)
		if len(session.Data.Messages) == 2 &&
			session.Data.Messages[1].Status == "completed" &&
			session.Data.TurnsUsed == 1 {
			return session
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for external agent session %q to complete; last session=%+v", sessionID, session.Data)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type e2eChatSessionResponse struct {
	Object         string                         `json:"object"`
	Data           e2eChatSessionItem             `json:"data"`
	MessageRequest *e2eChatMessageRequestResponse `json:"message_request,omitempty"`
}

type e2eChatSessionItem struct {
	ID              string               `json:"id"`
	Title           string               `json:"title"`
	AgentID         string               `json:"agent_id"`
	Status          string               `json:"status"`
	NativeSessionID string               `json:"native_session_id"`
	TurnsUsed       int                  `json:"turns_used"`
	Messages        []e2eChatMessageItem `json:"messages"`
}

type e2eChatMessageItem struct {
	Role        string `json:"role"`
	Content     string `json:"content"`
	Status      string `json:"status"`
	CompletedAt string `json:"completed_at"`
}

type e2eChatMessageRequestResponse struct {
	Replay             bool   `json:"replay"`
	CommittedMessageID string `json:"committed_message_id"`
}
