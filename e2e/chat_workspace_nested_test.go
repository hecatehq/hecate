//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatWorkspaceNestedGitReviewE2E(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "nested")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"outside.txt", "nested/inside.txt"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("before\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	for _, file := range []string{"outside.txt", "nested/inside.txt"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("after\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	baseURL := gatewayServer(t, "HECATE_BACKEND=sqlite")
	created := postJSONDecode[e2eChatSessionResponse](t, baseURL+"/hecate/v1/chat/sessions", fmt.Sprintf(`{
		"agent_id":"hecate", "workspace":%q, "workspace_mode":"in_place"
	}`, workspace))
	type reviewResponse struct {
		Data struct {
			HasChanges bool   `json:"has_changes"`
			Diff       string `json:"diff"`
			Files      []struct {
				Path string `json:"path"`
			} `json:"files"`
		} `json:"data"`
	}
	review := getJSON[reviewResponse](t, baseURL+"/hecate/v1/chat/sessions/"+created.Data.ID+"/workspace-diff")
	if !review.Data.HasChanges || !strings.Contains(review.Data.Diff, "+after") || strings.Contains(review.Data.Diff, "outside.txt") {
		t.Fatalf("nested workspace review = %+v", review.Data)
	}
	if len(review.Data.Files) != 1 || review.Data.Files[0].Path != "inside.txt" {
		t.Fatalf("review files = %+v, want only workspace-relative inside.txt", review.Data.Files)
	}
}
