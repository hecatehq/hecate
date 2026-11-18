package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/taskstate"
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

func TestAgentLoop_WorkspaceSkillsSurviveLiveGuidanceAndResumeWithoutArtifactDisclosure(t *testing.T) {
	spec, store := steeringSpec(t)
	const private = "PRIVATE-FROZEN-SKILL-INSTRUCTIONS"
	sum := sha256.Sum256([]byte(private))
	spec.Task.WorkspaceSkills = []types.WorkspaceSkillSnapshot{{
		WorkspaceSkillInfo: types.WorkspaceSkillInfo{Path: ".agents/skills/review/SKILL.md", SHA256: hex.EncodeToString(sum[:]), Bytes: len(private)},
		Body:               private,
	}}
	const guidance = "Include the risks"
	const resumedGuidance = "Also include mitigations"
	calls := 0
	llm := steeringLLM{chat: func(req types.ChatRequest) (*types.ChatResponse, error) {
		calls++
		privateBlocks, guidanceMessages, resumedMessages := 0, 0, 0
		for _, message := range req.Messages {
			for _, block := range message.ContentBlocks {
				if strings.Contains(block.Text, private) {
					if !block.WorkspaceSkillInput {
						t.Fatal("skill instruction lost its private input marker")
					}
					privateBlocks++
				}
			}
			if message.Role == "user" && message.Content == guidance {
				guidanceMessages++
			}
			if message.Role == "user" && message.Content == resumedGuidance {
				resumedMessages++
			}
		}
		if privateBlocks != 1 || (calls > 1 && guidanceMessages != 1) || (calls == 3 && resumedMessages != 1) {
			t.Fatalf("call %d: private blocks=%d guidance=%d resumed guidance=%d", calls, privateBlocks, guidanceMessages, resumedMessages)
		}
		if calls == 1 {
			if _, err := store.SubmitRunSteering(t.Context(), taskstate.RunSteeringSubmission{TaskID: spec.Task.ID, RunID: spec.Run.ID, ClientRequestID: "during-model", Content: guidance}); err != nil {
				t.Fatal(err)
			}
		}
		return makeChatResp(makeAssistantMsg("Reviewed")), nil
	}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	result, err := loop.Execute(t.Context(), spec)
	if err != nil || result.Status != "completed" || calls != 2 {
		t.Fatalf("first execution: result=%+v err=%v calls=%d", result, err, calls)
	}
	checkpoint, found, err := spec.GetArtifact(spec.Task.ID, "convo-"+spec.Run.ID)
	if err != nil || !found || strings.Contains(checkpoint.ContentText, private) {
		t.Fatalf("guidance checkpoint disclosed skills or was missing: found=%t err=%v", found, err)
	}
	// Recover from the durable conversation, not the transient request. The
	// frozen Task must restore its skill once without duplicating prior guidance.
	spec.ResumeCheckpoint = &ResumeCheckpoint{SourceRunID: spec.Run.ID, SameRun: true, AgentConversation: []byte(checkpoint.ContentText), ThisRunModelCallCount: 2, LastCompletedStepID: "model-second"}
	if _, err := store.SubmitRunSteering(t.Context(), taskstate.RunSteeringSubmission{TaskID: spec.Task.ID, RunID: spec.Run.ID, ClientRequestID: "after-checkpoint", Content: resumedGuidance}); err != nil {
		t.Fatal(err)
	}
	result, err = loop.Execute(t.Context(), spec)
	if err != nil || result.Status != "completed" || result.ModelCallCount != 3 || calls != 3 {
		t.Fatalf("recovered execution: result=%+v err=%v calls=%d", result, err, calls)
	}
	artifacts, err := store.ListArtifacts(t.Context(), taskstate.ArtifactFilter{TaskID: spec.Task.ID, RunID: spec.Run.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range artifacts {
		if strings.Contains(artifact.ContentText, private) {
			t.Fatalf("artifact %s disclosed frozen skill instructions", artifact.Kind)
		}
	}
}
