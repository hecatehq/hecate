//go:build e2e

package e2e

import (
	"context"
	"net/http"
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

type e2eChatSessionResponse struct {
	Object string             `json:"object"`
	Data   e2eChatSessionItem `json:"data"`
}

type e2eChatSessionItem struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	AgentID string `json:"agent_id"`
	Status  string `json:"status"`
}
