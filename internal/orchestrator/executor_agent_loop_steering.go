package orchestrator

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

// applySteering never interrupts a dispatched tool or provider request. It
// closes only protocol calls that have not started, so the next model request
// can safely incorporate the operator's new direction.
func (e *AgentLoopExecutor) applySteering(spec ExecutionSpec, conversation *agentLoopConversation, runState *agentLoopRunState) (bool, error) {
	if spec.ReadSteering == nil || spec.ApplySteering == nil || spec.GetArtifact == nil || runState.ModelCallCount() >= e.maxModelCalls {
		return false, nil
	}
	artifacts, err := spec.ReadSteering()
	if err != nil {
		return false, err
	}
	items, err := taskstate.PendingRunSteering(artifacts)
	if err != nil || len(items) == 0 {
		return false, err
	}
	current, exists, err := spec.GetArtifact(spec.Task.ID, conversation.ArtifactID())
	if err != nil {
		return false, err
	}
	expected := ""
	if exists {
		expected = taskstate.ConversationDigest(current.ContentText)
		payload, err := json.Marshal(conversationMessagesForArtifact(conversation.Messages()))
		if err != nil {
			return false, err
		}
		if string(payload) != current.ContentText {
			return false, taskstate.ErrSteeringCheckpointConflict
		}
	}
	// Copy the slice before preparing the commit; failure leaves live context
	// untouched and cannot claim an instruction was applied.
	next := *conversation
	next.messages = append([]types.Message(nil), conversation.Messages()...)
	for _, call := range next.PendingToolCallsForResume() {
		next.AppendToolResult(call.ID, "Tool call was not started because new operator guidance superseded it.", true)
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		next.messages = append(next.messages, types.Message{Role: "user", Content: item.Content})
		ids = append(ids, item.ID)
	}
	when := time.Now().UTC()
	var checkpoint types.TaskArtifact
	preparation := spec
	preparation.UpsertArtifact = func(artifact types.TaskArtifact) error { checkpoint = artifact; return nil }
	if _, err := next.UpsertArtifact(preparation, runState.ModelCallCount(), when); err != nil {
		return false, err
	}
	applied, err := spec.ApplySteering(taskstate.RunSteeringApplication{TaskID: spec.Task.ID, RunID: spec.Run.ID,
		IDs: ids, ExpectedConversationDigest: expected, Conversation: checkpoint,
		ModelCallIndex: runState.ModelCallCount() + 1, AppliedAt: when})
	if err != nil {
		return false, err
	}
	if !applied {
		return false, fmt.Errorf("guidance checkpoint was not committed")
	}
	*conversation = next
	runState.TrackConversationArtifact(&checkpoint)
	return true, nil
}
