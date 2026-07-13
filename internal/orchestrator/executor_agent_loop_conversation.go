package orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

type agentLoopConversation struct {
	artifactID string
	messages   []types.Message
}

func newAgentLoopConversation(spec ExecutionSpec) agentLoopConversation {
	return agentLoopConversation{
		artifactID: "convo-" + spec.Run.ID,
		messages:   hydrateConversation(spec),
	}
}

func (c *agentLoopConversation) Messages() []types.Message {
	return c.messages
}

func (c *agentLoopConversation) ArtifactID() string {
	return c.artifactID
}

func (c *agentLoopConversation) AppendAssistant(msg types.Message) {
	c.messages = append(c.messages, msg)
}

func (c *agentLoopConversation) AppendToolResult(toolCallID, text string, toolError bool) {
	c.messages = append(c.messages, types.Message{
		Role:       "tool",
		Content:    text,
		ToolCallID: toolCallID,
		ToolError:  toolError,
	})
}

func (c *agentLoopConversation) PendingToolCallsForResume() []types.ToolCall {
	return pendingToolCallsForResume(c.messages)
}

func (c *agentLoopConversation) TailAssistantForResume() (types.Message, bool) {
	if len(c.messages) == 0 {
		return types.Message{}, false
	}
	last := c.messages[len(c.messages)-1]
	if last.Role != "assistant" || len(last.ToolCalls) == 0 {
		return types.Message{}, false
	}
	return last, true
}

func (c *agentLoopConversation) UpsertArtifact(spec ExecutionSpec, turn int, when time.Time) (*types.TaskArtifact, error) {
	return upsertConversationArtifact(spec, c.artifactID, c.messages, turn, when)
}

// pendingToolCallsForResume detects the resume-after-approval state:
// the conversation tail is an assistant message with tool_calls and
// no subsequent tool-role results. Returns the list of tool calls
// that need dispatching. Empty slice = fresh turn (LLM call needed).
func pendingToolCallsForResume(messages []types.Message) []types.ToolCall {
	if len(messages) == 0 {
		return nil
	}
	last := messages[len(messages)-1]
	if last.Role != "assistant" || len(last.ToolCalls) == 0 {
		return nil
	}
	// Tool calls in the trailing assistant message exist; check that
	// none of them have already been resolved by a later tool message.
	// Since we just confirmed `last` is the tail, if tool messages
	// for these calls existed they'd be after `last` — they don't,
	// so all calls are pending.
	return last.ToolCalls
}

// countAssistantTurns returns the number of assistant messages in the
// saved conversation. Each agent_loop turn produces exactly one
// assistant message (with tool_calls or a final answer), so the count
// equals the number of completed turns. Used by the retry-from-turn-N
// codepath to validate the requested turn lies within range.
func countAssistantTurns(messages []types.Message) int {
	n := 0
	for _, m := range messages {
		if m.Role == "assistant" {
			n++
		}
	}
	return n
}

// truncateConversationToTurn drops the Nth assistant message and
// everything that follows it, so the next LLM call re-issues turn N
// against the same prior context. The system message (if present) and
// the user prompt are preserved, as are any prior assistant turns and
// their tool results — the operator gets to explore an alternative
// path from turn N forward.
//
// turn must be >= 1 and <= countAssistantTurns(messages). turn=1
// truncates back to just the prelude (system + user); turn=N for the
// final turn drops only that turn's assistant message.
//
// Returns a fresh slice; the input is not modified.
func truncateConversationToTurn(messages []types.Message, turn int) ([]types.Message, error) {
	if turn < 1 {
		return nil, fmt.Errorf("turn must be >= 1, got %d", turn)
	}
	assistantSeen := 0
	cutIndex := -1
	for i, m := range messages {
		if m.Role != "assistant" {
			continue
		}
		assistantSeen++
		if assistantSeen == turn {
			cutIndex = i
			break
		}
	}
	if cutIndex == -1 {
		return nil, fmt.Errorf("turn %d not found: conversation has %d assistant turn(s)", turn, assistantSeen)
	}
	out := make([]types.Message, cutIndex)
	copy(out, messages[:cutIndex])
	return out, nil
}

// hydrateConversation returns the conversation history for this run.
// On a fresh run, it prepends the composed system prompt (from the
// runner's four-layer resolver) before the user prompt. On a resume,
// it returns the JSON-decoded prior conversation from the source
// run's persisted agent_conversation artifact — the loop continues
// exactly where it left off, preserving tool results, prior reasoning,
// AND the original system prompt (it's already in the saved message
// array; we don't re-compose).
//
// If the resume artifact is missing or malformed (corrupt JSON, edited
// out of band) we fall back to the fresh-start state. That degrades
// gracefully: the agent re-plans rather than crashing.
func hydrateConversation(spec ExecutionSpec) []types.Message {
	if spec.ResumeCheckpoint != nil && len(spec.ResumeCheckpoint.AgentConversation) > 0 {
		var saved []types.Message
		if err := json.Unmarshal(spec.ResumeCheckpoint.AgentConversation, &saved); err == nil && len(saved) > 0 {
			if prompt := strings.TrimSpace(spec.ResumeCheckpoint.AppendUserPrompt); prompt != "" {
				saved = append(saved, types.Message{Role: "user", Content: prompt})
			}
			return saved
		}
	}
	// Fresh run: build the prelude as
	//   1. environment system message (workspace path) — always present
	//      when there's a workspace, so the LLM uses the right cwd
	//      and absolute paths in tool calls. Without this the model
	//      reads the user prompt's mention of "/Users/foo/myrepo"
	//      and uses that path verbatim — which lands outside the
	//      sandbox (an isolated clone) and the run fails with
	//      "escapes allowed root".
	//   2. composed operator system prompt (four layers) — global /
	//      tenant / workspace CLAUDE.md|AGENTS.md / per-task. Empty
	//      when none of those layers contributed.
	//   3. user prompt.
	messages := make([]types.Message, 0, 3)
	if env := environmentSystemMessage(spec); env != "" {
		messages = append(messages, types.Message{Role: "system", Content: env})
	}
	if strings.TrimSpace(spec.SystemPrompt) != "" {
		messages = append(messages, types.Message{Role: "system", Content: spec.SystemPrompt})
	}
	messages = append(messages, types.Message{Role: "user", Content: spec.Task.Prompt})
	return messages
}

// environmentSystemMessage produces the machine-generated system
// message that grounds the LLM in its actual sandbox: where the
// workspace lives and what's enforced. This is environmental fact,
// not operator-tunable directive — kept separate from
// spec.SystemPrompt so the operator can't accidentally elide it.
//
// Tools-disabled preset tasks receive policy guidance without a workspace path
// because no tool can use it and sending an absolute local path to the provider
// would disclose unnecessary operator context. Other tasks return "" when no
// workspace path is available.
func environmentSystemMessage(spec ExecutionSpec) string {
	if agentPresetDisablesTools(spec.Task) {
		return "Tools are disabled by the resolved agent preset. Do not make tool calls. " +
			"Answer from the supplied context, and say what workspace inspection would be needed when the context is insufficient."
	}
	workspace := strings.TrimSpace(spec.Task.WorkingDirectory)
	if workspace == "" {
		workspace = strings.TrimSpace(spec.Task.SandboxAllowedRoot)
	}
	if workspace == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("Your workspace is at: ")
	b.WriteString(workspace)
	b.WriteString("\n\n")
	b.WriteString("Use this path (or paths under it) when calling tools. ")
	b.WriteString("`shell_exec` / `git_exec` default their working_directory to the workspace when omitted; ")
	b.WriteString("`read_file` / `list_dir` / `grep` / `glob` / `git_diff` resolve relative paths from the workspace. ")
	b.WriteString("Tool calls that target paths outside this directory are rejected by the sandbox — ")
	b.WriteString("don't reuse paths from the user prompt verbatim if they fall outside the workspace.")
	return b.String()
}

// upsertConversationArtifact writes the current conversation snapshot
// to a stable artifact ID. Returns the artifact when it's newly
// created (or on the first call) so the caller can include it in the
// run's artifact list. Idempotent across turns: the same ID means the
// artifact's content is replaced in place rather than appended.
func upsertConversationArtifact(spec ExecutionSpec, id string, messages []types.Message, turn int, when time.Time) (*types.TaskArtifact, error) {
	if spec.UpsertArtifact == nil {
		return nil, nil
	}
	payload, err := json.Marshal(messages)
	if err != nil {
		// Marshal failures here are fatal — every Message field is
		// JSON-marshalable by construction; a failure would be a
		// runtime corruption we shouldn't paper over.
		return nil, fmt.Errorf("marshal agent conversation: %w", err)
	}
	art := types.TaskArtifact{
		ID:          id,
		TaskID:      spec.Task.ID,
		RunID:       spec.Run.ID,
		Kind:        "agent_conversation",
		Name:        "agent-conversation.json",
		Description: fmt.Sprintf("Agent loop conversation snapshot after turn %d", turn),
		MimeType:    "application/json",
		StorageKind: "inline",
		ContentText: string(payload),
		SizeBytes:   int64(len(payload)),
		Status:      "ready",
		CreatedAt:   when,
		RequestID:   spec.RequestID,
		TraceID:     spec.TraceID,
	}
	if err := spec.UpsertArtifact(art); err != nil {
		return nil, err
	}
	return &art, nil
}
