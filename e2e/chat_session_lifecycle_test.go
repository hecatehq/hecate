//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

type e2eChatSessionResponse struct {
	Object string             `json:"object"`
	Data   e2eChatSessionItem `json:"data"`
}

type e2eChatSessionItem struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	AgentID         string `json:"agent_id"`
	Status          string `json:"status"`
	NativeSessionID string `json:"native_session_id"`
}
