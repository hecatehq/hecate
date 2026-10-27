package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestAgentLoop_WorkspaceSkillsOnlyEnterTransientRequest(t *testing.T) {
	skill := types.WorkspaceSkillSnapshot{WorkspaceSkillInfo: types.WorkspaceSkillInfo{Path: ".agents/skills/review/SKILL.md", SHA256: "reviewed"}, Body: "private-skill-sentinel"}
	messages := []types.Message{{Role: "user", Content: "Review changes"}}
	req := agentLoopChatRequest(ExecutionSpec{Task: types.Task{WorkspaceSkills: []types.WorkspaceSkillSnapshot{skill}}}, messages, nil)
	if len(req.Messages) != 2 || !strings.Contains(req.Messages[0].ContentBlocks[0].Text, skill.Body) {
		t.Fatal("request lacks skill")
	}
	for _, checkpoint := range [][]types.Message{messages, req.Messages} {
		raw, err := json.Marshal(conversationMessagesForArtifact(checkpoint))
		if err != nil || strings.Contains(string(raw), skill.Body) {
			t.Fatal("checkpoint leaked skill body")
		}
	}
}
