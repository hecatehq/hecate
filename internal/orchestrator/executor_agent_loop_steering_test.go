package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type steeringLLM struct {
	chat func(types.ChatRequest) (*types.ChatResponse, error)
}

type steeringTool func(ExecutionSpec) (*ExecutionResult, error)

func (tool steeringTool) Execute(_ context.Context, spec ExecutionSpec) (*ExecutionResult, error) {
	return tool(spec)
}

func TestAgentLoop_SteeringFinishesCurrentToolAndSkipsRemainingBatch(t *testing.T) {
	spec, store := steeringSpec(t)
	modelCalls, toolCalls := 0, 0
	tool := steeringTool(func(ExecutionSpec) (*ExecutionResult, error) {
		toolCalls++
		if _, err := store.SubmitRunSteering(t.Context(), taskstate.RunSteeringSubmission{TaskID: spec.Task.ID, RunID: spec.Run.ID, ClientRequestID: "during-tool", Content: "Stop after this command"}); err != nil {
			t.Fatal(err)
		}
		return &ExecutionResult{Status: "completed"}, nil
	})
	llm := steeringLLM{chat: func(req types.ChatRequest) (*types.ChatResponse, error) {
		modelCalls++
		if modelCalls == 1 {
			return makeChatResp(makeAssistantMsg("", types.ToolCall{ID: "first", Type: "function", Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"first"}`}}, types.ToolCall{ID: "second", Type: "function", Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"second"}`}})), nil
		}
		results := map[string]types.Message{}
		for _, message := range req.Messages {
			if message.Role == "tool" {
				results[message.ToolCallID] = message
			}
		}
		if results["first"].ToolError || !results["second"].ToolError {
			t.Fatalf("tool outcomes=%+v", results)
		}
		if req.Messages[len(req.Messages)-1].Content != "Stop after this command" {
			t.Fatal("guidance missing")
		}
		return makeChatResp(makeAssistantMsg("Stopped")), nil
	}}
	loop := NewAgentLoopExecutor(llm, tool, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	result, err := loop.Execute(t.Context(), spec)
	if err != nil || result.Status != "completed" || modelCalls != 2 || toolCalls != 1 {
		t.Fatalf("result=%+v err=%v model=%d tool=%d", result, err, modelCalls, toolCalls)
	}
}

func TestAgentLoop_SteeringDoesNotResetModelCallLimit(t *testing.T) {
	spec, store := steeringSpec(t)
	calls := 0
	llm := steeringLLM{chat: func(types.ChatRequest) (*types.ChatResponse, error) {
		calls++
		if _, err := store.SubmitRunSteering(t.Context(), taskstate.RunSteeringSubmission{TaskID: spec.Task.ID, RunID: spec.Run.ID, ClientRequestID: "limit", Content: "One more thing"}); err != nil {
			t.Fatal(err)
		}
		return makeChatResp(makeAssistantMsg("Done")), nil
	}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 1, nil, HTTPRequestPolicy{})
	result, err := loop.Execute(t.Context(), spec)
	if err != nil || calls != 1 || result.Status != "completed" {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
	artifacts, _ := spec.ReadSteering()
	if len(artifacts) != 1 || artifacts[0].Status != "pending" {
		t.Fatal("guidance must remain unconsumed for terminal settlement")
	}
}

func TestAgentLoop_SteeringAppliedCheckpointResumesWithoutDuplication(t *testing.T) {
	spec, store := steeringSpec(t)
	item, err := store.SubmitRunSteering(t.Context(), taskstate.RunSteeringSubmission{TaskID: spec.Task.ID, RunID: spec.Run.ID, ClientRequestID: "resume", Content: "Include risks"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal([]types.Message{{Role: "user", Content: spec.Task.Prompt}, {Role: "assistant", Content: "Initial answer"}, {Role: "user", Content: "Include risks"}})
	checkpoint := types.TaskArtifact{ID: "convo-" + spec.Run.ID, TaskID: spec.Task.ID, RunID: spec.Run.ID, Kind: "agent_conversation", Status: "ready", ContentText: string(payload)}
	if _, err := store.ApplyRunSteering(t.Context(), taskstate.RunSteeringApplication{TaskID: spec.Task.ID, RunID: spec.Run.ID, IDs: []string{item.ID}, Conversation: checkpoint, ModelCallIndex: 2}); err != nil {
		t.Fatal(err)
	}
	spec.ResumeCheckpoint = &ResumeCheckpoint{SourceRunID: spec.Run.ID, SameRun: true, AgentConversation: payload, ThisRunModelCallCount: 1, LastCompletedStepID: "model-first"}
	calls := 0
	llm := steeringLLM{chat: func(req types.ChatRequest) (*types.ChatResponse, error) {
		calls++
		count := 0
		for _, message := range req.Messages {
			if message.Role == "user" && message.Content == "Include risks" {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("guidance duplicated on resume: %d", count)
		}
		return makeChatResp(makeAssistantMsg("Risks included")), nil
	}}
	result, err := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{}).Execute(t.Context(), spec)
	if err != nil || result.Status != "completed" || calls != 1 || result.ModelCallCount != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
}

func TestAgentLoop_SteeringPreservesPrivateAttachmentInputWithoutPersistingBytes(t *testing.T) {
	spec, store := steeringSpec(t)
	const private = "PRIVATE-ATTACHMENT-BODY"
	spec.InputMessage = &types.Message{Role: "user", Content: spec.Task.Prompt, ContentBlocks: []types.ContentBlock{{Type: "text", Text: private, AttachmentInput: true}}}
	calls := 0
	llm := steeringLLM{chat: func(req types.ChatRequest) (*types.ChatResponse, error) {
		calls++
		found := false
		for _, message := range req.Messages {
			for _, block := range message.ContentBlocks {
				found = found || block.Text == private
			}
		}
		if !found {
			t.Fatal("live attachment input lost")
		}
		if calls == 1 {
			if _, err := store.SubmitRunSteering(t.Context(), taskstate.RunSteeringSubmission{TaskID: spec.Task.ID, RunID: spec.Run.ID, ClientRequestID: "private", Content: "Include risks"}); err != nil {
				t.Fatal(err)
			}
		}
		return makeChatResp(makeAssistantMsg("Done")), nil
	}}
	result, err := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{}).Execute(t.Context(), spec)
	if err != nil || result.Status != "completed" || calls != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	checkpoint, _, err := spec.GetArtifact(spec.Task.ID, "convo-"+spec.Run.ID)
	if err != nil || strings.Contains(checkpoint.ContentText, private) {
		t.Fatal("private attachment persisted in guidance checkpoint")
	}
}

func (llm steeringLLM) Chat(_ context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	return llm.chat(req)
}

func steeringSpec(t *testing.T) (ExecutionSpec, *taskstate.MemoryStore) {
	t.Helper()
	spec := newAgentLoopSpec(t)
	spec.Task.ExecutionKind = "agent_loop"
	spec.Run.TaskID, spec.Run.Status = spec.Task.ID, "running"
	store := taskstate.NewMemoryStore()
	if _, err := store.CreateTask(context.Background(), spec.Task); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRun(context.Background(), spec.Run); err != nil {
		t.Fatal(err)
	}
	spec.UpsertArtifact = func(a types.TaskArtifact) error { _, err := store.CreateArtifact(context.Background(), a); return err }
	spec.GetArtifact = func(taskID, id string) (types.TaskArtifact, bool, error) {
		return store.GetArtifact(context.Background(), taskID, id)
	}
	spec.ReadSteering = func() ([]types.TaskArtifact, error) {
		return store.ListArtifacts(context.Background(), taskstate.ArtifactFilter{TaskID: spec.Task.ID, RunID: spec.Run.ID, Kind: taskstate.RunSteeringArtifactKind})
	}
	spec.ApplySteering = func(in taskstate.RunSteeringApplication) (bool, error) {
		return store.ApplyRunSteering(context.Background(), in)
	}
	return spec, store
}

func TestAgentLoop_SteeringSupersedesUnstartedTools(t *testing.T) {
	spec, store := steeringSpec(t)
	calls := 0
	llm := steeringLLM{chat: func(req types.ChatRequest) (*types.ChatResponse, error) {
		calls++
		if calls == 1 {
			_, err := store.SubmitRunSteering(context.Background(), taskstate.RunSteeringSubmission{TaskID: spec.Task.ID, RunID: spec.Run.ID, ClientRequestID: "guide", Content: "Inspect only; do not write"})
			if err != nil {
				t.Fatal(err)
			}
			return makeChatResp(makeAssistantMsg("", types.ToolCall{ID: "old-call", Type: "function", Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"touch forbidden"}`}})), nil
		}
		if got := req.Messages[len(req.Messages)-1]; got.Role != "user" || got.Content != "Inspect only; do not write" {
			t.Fatalf("last=%+v", got)
		}
		previous := req.Messages[len(req.Messages)-2]
		if previous.Role != "tool" || !previous.ToolError || previous.ToolCallID != "old-call" {
			t.Fatalf("unclosed tool=%+v", previous)
		}
		return makeChatResp(makeAssistantMsg("Inspected without changes")), nil
	}}
	shell := &stubExecutor{}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	result, err := loop.Execute(context.Background(), spec)
	if err != nil || result.Status != "completed" || calls != 2 || len(shell.calls) != 0 {
		t.Fatalf("result=%+v err=%v calls=%d tools=%d", result, err, calls, len(shell.calls))
	}
	artifacts, _ := spec.ReadSteering()
	if len(artifacts) != 1 || artifacts[0].Status != "applied" {
		t.Fatalf("artifacts=%+v", artifacts)
	}
	checkpoint, _, _ := spec.GetArtifact(spec.Task.ID, "convo-"+spec.Run.ID)
	var messages []types.Message
	if err := json.Unmarshal([]byte(checkpoint.ContentText), &messages); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, m := range messages {
		if m.Role == "user" && strings.Contains(m.Content, "Inspect only") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("instruction applied %d times", count)
	}
}

func TestAgentLoop_SteeringAfterFinalAnswerNeedsAnotherModelCall(t *testing.T) {
	spec, store := steeringSpec(t)
	calls := 0
	llm := steeringLLM{chat: func(req types.ChatRequest) (*types.ChatResponse, error) {
		calls++
		if calls == 1 {
			_, err := store.SubmitRunSteering(context.Background(), taskstate.RunSteeringSubmission{TaskID: spec.Task.ID, RunID: spec.Run.ID, ClientRequestID: "guide", Content: "Include risks"})
			if err != nil {
				t.Fatal(err)
			}
		}
		return makeChatResp(makeAssistantMsg("Answer")), nil
	}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	result, err := loop.Execute(context.Background(), spec)
	if err != nil || result.Status != "completed" || calls != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
}
