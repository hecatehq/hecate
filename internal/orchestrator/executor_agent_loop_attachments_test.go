package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

type fakeAgentAttachmentReader struct {
	reads       []AgentAttachmentReadRequest
	searches    []AgentAttachmentSearchRequest
	read        func(context.Context, AgentAttachmentReadRequest) (AgentAttachmentReadResult, error)
	search      func(context.Context, AgentAttachmentSearchRequest) (AgentAttachmentSearchResult, error)
	budget      func(context.Context) (int, error)
	budgetCalls int
}

func (f *fakeAgentAttachmentReader) ContextBudget(ctx context.Context) (int, error) {
	f.budgetCalls++
	if f.budget != nil {
		return f.budget(ctx)
	}
	return maxPrivateAttachmentContextBytes, nil
}

func (f *fakeAgentAttachmentReader) Read(ctx context.Context, args AgentAttachmentReadRequest) (AgentAttachmentReadResult, error) {
	f.reads = append(f.reads, args)
	if f.read != nil {
		return f.read(ctx, args)
	}
	const text = "private attachment page 世界\n"
	return AgentAttachmentReadResult{AttachmentRef: args.AttachmentRef, Offset: args.Offset, NextOffset: args.Offset + int64(len(text)), EOF: true, Text: text}, nil
}

func (f *fakeAgentAttachmentReader) Search(ctx context.Context, args AgentAttachmentSearchRequest) (AgentAttachmentSearchResult, error) {
	f.searches = append(f.searches, args)
	if f.search != nil {
		return f.search(ctx, args)
	}
	return AgentAttachmentSearchResult{AttachmentRef: args.AttachmentRef, Matches: []AgentAttachmentSearchMatch{{Offset: args.Offset, Text: "private attachment excerpt"}}, NextOffset: args.Offset + 30, EOF: true}, nil
}

func attachmentLoopSpec(t *testing.T, reader AgentAttachmentReader) ExecutionSpec {
	t.Helper()
	spec := newAgentLoopSpec(t)
	spec.AttachmentReader = reader
	spec.AttachmentContextBytes = 64 << 10
	spec.InputMessage = &types.Message{Role: "user", Content: "Inspect attached source", ContentBlocks: []types.ContentBlock{{Type: "text", Text: "Attached file source.go attachment_ref=att_1 size_bytes=100"}}}
	spec.ChatRequirements = types.ChatRequestRequirements{
		NoProviderFailover: true, ExactProvider: true,
		ProviderInstance: types.ProviderInstanceIdentity{ID: "attachment-generation", Kind: types.ProviderInstanceIdentityConfiguration},
	}
	return spec
}

func TestAgentAttachmentToolsLiveContentIsNotPersisted(t *testing.T) {
	reader := &fakeAgentAttachmentReader{}
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("", agentLoopToolCall("read-1", AgentToolReadAttachment, `{"attachment_ref":"att_1"}`), agentLoopToolCall("search-1", AgentToolSearchAttachment, `{"attachment_ref":"att_1","query":"operator-search"}`))),
		makeChatResp(makeAssistantMsg("Reviewed the source.")),
	}}
	loop := NewAgentLoopExecutor(llm, nil, nil, nil, 3, nil, HTTPRequestPolicy{})
	spec := attachmentLoopSpec(t, reader)
	var events []map[string]any
	spec.EmitRunEvent = func(_ string, data map[string]any) { events = append(events, data) }
	result, err := loop.Execute(t.Context(), spec)
	if err != nil || result.Status != "completed" {
		t.Fatalf("Execute: %+v %v", result, err)
	}
	if len(reader.reads) != 1 || reader.reads[0].MaxBytes != AgentAttachmentDefaultReadBytes || len(reader.searches) != 1 || reader.searches[0].MaxMatches != AgentAttachmentDefaultMatches {
		t.Fatalf("default read/search arguments: %+v %+v", reader.reads, reader.searches)
	}
	if !hasToolDefinition(llm.lastReqs[0].Tools, AgentToolReadAttachment) || !hasToolDefinition(llm.lastReqs[0].Tools, AgentToolSearchAttachment) {
		t.Fatal("scoped attachment tools were not advertised")
	}
	private := 0
	for _, message := range llm.lastReqs[1].Messages {
		if message.Role != "tool" {
			continue
		}
		if message.Content != "" || len(message.ContentBlocks) != 1 || !message.ContentBlocks[0].AttachmentInput || !strings.Contains(message.ContentBlocks[0].Text, "private attachment") {
			t.Fatalf("tool result lost transient provenance: %+v", message)
		}
		private++
	}
	if private != 2 {
		t.Fatalf("private tool messages = %d", private)
	}
	for _, request := range llm.lastReqs {
		if request.Requirements.ImageInput || !request.Requirements.ToolCalling || !request.Requirements.NoProviderFailover || !request.Requirements.ExactProvider || request.Requirements.ProviderInstance != spec.ChatRequirements.ProviderInstance {
			t.Fatalf("attachment route requirements changed: %+v", request.Requirements)
		}
	}
	serialized := mustJSON(t, struct {
		Steps     []types.TaskStep
		Artifacts []types.TaskArtifact
		Events    []map[string]any
	}{result.Steps, result.Artifacts, events})
	if strings.Contains(serialized, "private attachment") {
		t.Fatal("raw attachment tool content reached steps, artifacts, or events")
	}
	if !strings.Contains(serialized, artifactAttachmentToolResultOmission) || !strings.Contains(serialized, "source.go") || !strings.Contains(serialized, "operator-search") {
		t.Fatal("checkpoint lost explicit omission, body-free metadata, or ordinary model-generated query")
	}
}

func TestAgentAttachmentToolsDispatchFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name, tool, args string
		mutate           func(*ExecutionSpec)
	}{
		{"missing reader", AgentToolReadAttachment, `{"attachment_ref":"att_1"}`, func(spec *ExecutionSpec) { spec.AttachmentReader = nil }},
		{"missing budget", AgentToolReadAttachment, `{"attachment_ref":"att_1"}`, func(spec *ExecutionSpec) { spec.AttachmentContextBytes = 0 }},
		{"negative offset", AgentToolReadAttachment, `{"attachment_ref":"att_1","offset":-1}`, nil},
		{"oversized page", AgentToolReadAttachment, `{"attachment_ref":"att_1","max_bytes":32769}`, nil},
		{"unknown arguments", AgentToolReadAttachment, `{"attachment_ref":"att_1","path":"/etc/passwd"}`, nil},
		{"trailing arguments", AgentToolReadAttachment, `{"attachment_ref":"att_1"}{}`, nil},
		{"query too large", AgentToolSearchAttachment, `{"attachment_ref":"att_1","query":"` + strings.Repeat("a", 257) + `"}`, nil},
		{"too many matches", AgentToolSearchAttachment, `{"attachment_ref":"att_1","query":"a","max_matches":21}`, nil},
		{"empty query", AgentToolSearchAttachment, `{"attachment_ref":"att_1","query":""}`, nil},
		{"disabled tools", AgentToolReadAttachment, `{"attachment_ref":"att_1"}`, func(spec *ExecutionSpec) { disabled := false; spec.Task.AgentPresetToolsEnabled = &disabled }},
		{"QA workflow", AgentToolReadAttachment, `{"attachment_ref":"att_1"}`, func(spec *ExecutionSpec) { spec.Run.WorkflowMode = types.WorkflowModeQA }},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &fakeAgentAttachmentReader{}
			spec := attachmentLoopSpec(t, reader)
			if test.mutate != nil {
				test.mutate(&spec)
			}
			result, err := (&agentLoopToolDispatcher{}).Dispatch(t.Context(), spec, agentLoopToolCall("call", test.tool, test.args), 1, nil, nil)
			if err != nil || (!result.ToolError && result.Step != nil && result.Step.Status == "completed") || result.PrivateAttachmentInput || len(reader.reads)+len(reader.searches) != 0 {
				t.Fatalf("unsafe dispatch: %+v %v reads=%d searches=%d", result, err, len(reader.reads), len(reader.searches))
			}
		})
	}
}

func TestAgentAttachmentToolsWithholdReaderFailuresAndOversizedResults(t *testing.T) {
	for _, test := range []struct {
		name       string
		reader     *fakeAgentAttachmentReader
		budget     int
		tool, args string
	}{
		{"reader error", &fakeAgentAttachmentReader{read: func(context.Context, AgentAttachmentReadRequest) (AgentAttachmentReadResult, error) {
			return AgentAttachmentReadResult{}, errors.New("private failure body")
		}}, 4096, AgentToolReadAttachment, `{"attachment_ref":"att_1"}`},
		{"invalid result", &fakeAgentAttachmentReader{read: func(context.Context, AgentAttachmentReadRequest) (AgentAttachmentReadResult, error) {
			return AgentAttachmentReadResult{AttachmentRef: "other", Text: "private failure body"}, nil
		}}, 4096, AgentToolReadAttachment, `{"attachment_ref":"att_1"}`},
		{"context budget", &fakeAgentAttachmentReader{}, 32, AgentToolReadAttachment, `{"attachment_ref":"att_1"}`},
		{"search context budget", &fakeAgentAttachmentReader{}, 32, AgentToolSearchAttachment, `{"attachment_ref":"att_1","query":"a"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := attachmentLoopSpec(t, test.reader)
			spec.AttachmentContextBytes = test.budget
			result := dispatchAttachmentTool(t.Context(), spec, agentLoopToolCall("call", test.tool, test.args), 1, time.Now())
			if !result.ToolError || result.PrivateAttachmentInput || result.Step != nil || strings.Contains(result.Text, "private") {
				t.Fatalf("unsafe result: %+v", result)
			}
		})
	}
}

func TestAgentAttachmentToolsCancellationDiscardsCompletedReaderOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	reader := &fakeAgentAttachmentReader{read: func(_ context.Context, req AgentAttachmentReadRequest) (AgentAttachmentReadResult, error) {
		cancel()
		return AgentAttachmentReadResult{AttachmentRef: req.AttachmentRef, Offset: req.Offset, NextOffset: req.Offset + 7, EOF: true, Text: "private"}, nil
	}}
	result := dispatchAttachmentTool(ctx, attachmentLoopSpec(t, reader), agentLoopToolCall("call", AgentToolReadAttachment, `{"attachment_ref":"att_1"}`), 1, time.Now())
	if !result.ToolError || result.PrivateAttachmentInput || strings.Contains(result.Text, "private") {
		t.Fatalf("cancelled result disclosed content: %+v", result)
	}
	result = dispatchAttachmentTool(ctx, attachmentLoopSpec(t, reader), agentLoopToolCall("call2", AgentToolReadAttachment, `{"attachment_ref":"att_1"}`), 2, time.Now())
	if !result.ToolError || len(reader.reads) != 1 {
		t.Fatal("cancelled context called reader again")
	}
}

func TestAgentAttachmentToolContextPrunesOldestWithoutMutatingSnapshot(t *testing.T) {
	conversation := agentLoopConversation{messages: []types.Message{{Role: "user", Content: "file metadata remains"}}}
	conversation.AppendPrivateAttachmentToolResult("old", "older body", false)
	conversation.AppendPrivateAttachmentToolResult("new", "newer body", false)
	snapshot := conversation.Messages()
	conversation.pruneAttachmentToolResults(len("newer body"))
	if snapshot[1].ContentBlocks[0].Text != "older body" || !snapshot[1].ContentBlocks[0].AttachmentInput {
		t.Fatal("pruning mutated a previous request snapshot")
	}
	if conversation.Messages()[1].ContentBlocks[0].Text != attachmentToolContextOmission || conversation.Messages()[1].ContentBlocks[0].AttachmentInput || conversation.Messages()[2].ContentBlocks[0].Text != "newer body" || conversation.Messages()[0].Content != "file metadata remains" {
		t.Fatalf("incorrect pruning: %+v", conversation.Messages())
	}
	conversation.pruneAttachmentToolResults(0)
	if privateAttachmentToolBytes(conversation.Messages()[2]) != 0 {
		t.Fatal("zero budget retained private bytes")
	}
}

func TestAgentAttachmentToolCompletedCheckpointNeverReplaysRead(t *testing.T) {
	reader := &fakeAgentAttachmentReader{}
	spec := attachmentLoopSpec(t, reader)
	conversation := agentLoopConversation{messages: []types.Message{*spec.InputMessage, makeAssistantMsg("", agentLoopToolCall("completed-read", AgentToolReadAttachment, `{"attachment_ref":"att_1"}`))}}
	conversation.AppendPrivateAttachmentToolResult("completed-read", "private completed file body", false)
	raw, err := json.Marshal(conversationMessagesForArtifact(conversation.Messages()))
	if err != nil {
		t.Fatal(err)
	}
	spec.ResumeCheckpoint = &ResumeCheckpoint{AgentConversation: raw}
	llm := &scriptedLLM{responses: []*types.ChatResponse{makeChatResp(makeAssistantMsg("Finished."))}}
	loop := NewAgentLoopExecutor(llm, nil, nil, nil, 3, nil, HTTPRequestPolicy{})
	result, err := loop.Execute(t.Context(), spec)
	if err != nil || result.Status != "completed" || len(reader.reads) != 0 || len(llm.lastReqs) != 1 {
		t.Fatalf("resume replayed private read: %+v %v reads=%d", result, err, len(reader.reads))
	}
	request := mustJSON(t, llm.lastReqs[0])
	if strings.Contains(request, "private completed file body") || !strings.Contains(request, artifactAttachmentToolResultOmission) {
		t.Fatal("resume did not retain explicit private-result omission")
	}
}

func TestAgentAttachmentToolsRespectApprovalAndCatalogPolicy(t *testing.T) {
	for _, tool := range []string{AgentToolReadAttachment, AgentToolSearchAttachment} {
		if hasToolDefinition(agentToolDefinitions(), tool) {
			t.Fatalf("%s advertised without reader", tool)
		}
	}
	reader := &fakeAgentAttachmentReader{}
	spec := attachmentLoopSpec(t, reader)
	spec.Task.AgentPresetApprovalPolicy = types.AgentPresetApprovalRequire
	spec.Task.AgentPresetID = "require-review"
	spec.Task.AgentPresetToolsEnabled = enabledAgentPresetToolsSnapshot()
	llm := &scriptedLLM{responses: []*types.ChatResponse{makeChatResp(makeAssistantMsg("", agentLoopToolCall("read", AgentToolReadAttachment, `{"attachment_ref":"att_1"}`)))}}
	loop := NewAgentLoopExecutor(llm, nil, nil, nil, 3, nil, HTTPRequestPolicy{})
	result, err := loop.Execute(t.Context(), spec)
	if err != nil || result.Status != "awaiting_approval" || len(reader.reads) != 0 {
		t.Fatalf("read bypassed approval: %+v %v", result, err)
	}
	if got := attachmentContextBudget(ExecutionSpec{AttachmentContextBytes: 256 << 10}); got != 64<<10 {
		t.Fatalf("runtime budget cap = %d", got)
	}
}

func TestAgentAttachmentToolsReadApprovalFamiliesPreserveQAExclusion(t *testing.T) {
	for _, policy := range []string{"read_file", "all_tools"} {
		for _, tool := range []string{AgentToolReadAttachment, AgentToolSearchAttachment} {
			t.Run(policy+"/"+tool, func(t *testing.T) {
				spec := attachmentLoopSpec(t, &fakeAgentAttachmentReader{})
				gate := newAgentLoopApprovalGate(agentLoopGatedTools(map[string]struct{}{policy: {}}))
				calls := []types.ToolCall{agentLoopToolCall("call", tool, `{"attachment_ref":"att_1","query":"needle"}`)}
				if _, pause := gate.EvaluateAdvertised(spec, 1, 1, time.Now(), calls, attachmentToolDefinitions()); !pause {
					t.Fatal("attachment inspection bypassed configured approval family")
				}
				spec.Run.WorkflowMode = types.WorkflowModeQA
				if _, pause := gate.EvaluateAdvertised(spec, 1, 1, time.Now(), calls, attachmentToolDefinitions()); pause {
					t.Fatal("QA-excluded attachment inspection incorrectly asked for approval")
				}
			})
		}
	}
}

func TestAgentAttachmentToolsResumePendingCallsWithoutReplayingCompletedRead(t *testing.T) {
	reader := &fakeAgentAttachmentReader{}
	spec := attachmentLoopSpec(t, reader)
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("", agentLoopToolCall("completed-read", AgentToolReadAttachment, `{"attachment_ref":"att_1"}`))),
		makeChatResp(makeAssistantMsg("", agentLoopToolCall("pending-read", AgentToolReadAttachment, `{"attachment_ref":"att_1","offset":10}`), agentLoopToolCall("pending-shell", "shell_exec", `{"command":"pwd"}`))),
		makeChatResp(makeAssistantMsg("Finished reviewing.")),
	}}
	shell := &stubExecutor{}
	loop := NewAgentLoopExecutor(llm, shell, nil, nil, 5, []string{"shell_exec"}, HTTPRequestPolicy{})
	paused, err := loop.Execute(t.Context(), spec)
	if err != nil || paused.Status != "awaiting_approval" || len(reader.reads) != 1 {
		t.Fatalf("first execution: %+v %v reads=%d", paused, err, len(reader.reads))
	}
	var checkpoint []byte
	for _, artifact := range paused.Artifacts {
		if artifact.Kind == "agent_conversation" {
			checkpoint = []byte(artifact.ContentText)
		}
	}
	if len(checkpoint) == 0 || strings.Contains(string(checkpoint), "private attachment page") || !strings.Contains(string(checkpoint), artifactAttachmentToolResultOmission) || !strings.Contains(string(checkpoint), "source.go") {
		t.Fatalf("checkpoint did not preserve metadata and omit the actual read body: %s", checkpoint)
	}
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SameRun: true, AgentConversation: checkpoint, ThisRunModelCallCount: 2,
		PendingToolCallsApproved: true, PendingToolCallsOriginRunID: spec.Run.ID, PendingToolCallsOriginModelCallIndex: 2,
	}
	resumed, err := loop.Execute(t.Context(), spec)
	if err != nil || resumed.Status != "completed" || len(reader.reads) != 2 || reader.reads[1].Offset != 10 || len(shell.calls) != 1 || len(llm.lastReqs) != 3 {
		t.Fatalf("resume execution: %+v %v reads=%+v shell calls=%d model calls=%d", resumed, err, reader.reads, len(shell.calls), len(llm.lastReqs))
	}
	request := llm.lastReqs[2]
	var sawCompleted, sawPending bool
	for _, message := range request.Messages {
		if message.ToolCallID == "completed-read" {
			sawCompleted = len(message.ContentBlocks) == 1 && message.ContentBlocks[0].Text == artifactAttachmentToolResultOmission && !message.ContentBlocks[0].AttachmentInput
		}
		if message.ToolCallID == "pending-read" {
			sawPending = message.Content == "" && len(message.ContentBlocks) == 1 && message.ContentBlocks[0].AttachmentInput && strings.Contains(message.ContentBlocks[0].Text, "private attachment page")
		}
	}
	if !sawCompleted || !sawPending || !strings.Contains(mustJSON(t, request), "source.go") {
		t.Fatalf("resumed request lost completed omission, fresh private output, or metadata: %+v", request.Messages)
	}
}

func TestAgentAttachmentToolsPruneBeforeEveryModelCall(t *testing.T) {
	reader := &fakeAgentAttachmentReader{}
	spec := attachmentLoopSpec(t, reader)
	spec.AttachmentContextBytes = 160
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("", agentLoopToolCall("first-page", AgentToolReadAttachment, `{"attachment_ref":"att_1"}`))),
		makeChatResp(makeAssistantMsg("", agentLoopToolCall("second-page", AgentToolReadAttachment, `{"attachment_ref":"att_1","offset":30}`))),
		makeChatResp(makeAssistantMsg("Finished reviewing.")),
	}}
	loop := NewAgentLoopExecutor(llm, nil, nil, nil, 4, nil, HTTPRequestPolicy{})
	result, err := loop.Execute(t.Context(), spec)
	if err != nil || result.Status != "completed" || len(reader.reads) != 2 || len(llm.lastReqs) != 3 {
		t.Fatalf("Execute: %+v %v reads=%d calls=%d", result, err, len(reader.reads), len(llm.lastReqs))
	}
	for _, request := range llm.lastReqs {
		bytes := 0
		for _, message := range request.Messages {
			bytes += privateAttachmentToolBytes(message)
		}
		if bytes > spec.AttachmentContextBytes {
			t.Fatalf("model request exceeded private context budget: %d", bytes)
		}
	}
	for _, message := range llm.lastReqs[2].Messages {
		switch message.ToolCallID {
		case "first-page":
			if len(message.ContentBlocks) != 1 || message.ContentBlocks[0].Text != attachmentToolContextOmission {
				t.Fatal("older complete page was not explicitly omitted")
			}
		case "second-page":
			if privateAttachmentToolBytes(message) == 0 {
				t.Fatal("newest page was not retained")
			}
		}
	}
}

func TestAgentAttachmentToolsDynamicBudgetWithholdsResults(t *testing.T) {
	for _, scenario := range []string{"smaller", "error", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reader := &fakeAgentAttachmentReader{budget: func(context.Context) (int, error) {
				switch scenario {
				case "smaller":
					return 16, nil
				case "error":
					return 0, errors.New("private provider budget diagnostic")
				default:
					cancel()
					return 64 << 10, nil
				}
			}}
			result := dispatchAttachmentTool(ctx, attachmentLoopSpec(t, reader), agentLoopToolCall("call", AgentToolReadAttachment, `{"attachment_ref":"att_1"}`), 1, time.Now())
			if !result.ToolError || result.PrivateAttachmentInput || result.Step != nil || strings.Contains(result.Text, "private") || len(reader.reads) != 1 {
				t.Fatalf("dynamic budget disclosed output: %+v", result)
			}
		})
	}
}

func TestAgentAttachmentToolsInitialMetadataNeedsNoFinalBudget(t *testing.T) {
	reader := &fakeAgentAttachmentReader{budget: func(context.Context) (int, error) { return 0, errors.New("route not recorded before first dispatch") }}
	llm := &scriptedLLM{responses: []*types.ChatResponse{makeChatResp(makeAssistantMsg("No file read needed."))}}
	loop := NewAgentLoopExecutor(llm, nil, nil, nil, 2, nil, HTTPRequestPolicy{})
	result, err := loop.Execute(t.Context(), attachmentLoopSpec(t, reader))
	if err != nil || result.Status != "completed" || len(llm.lastReqs) != 1 || reader.budgetCalls != 0 {
		t.Fatalf("metadata-only call asked for final budget: %+v %v budget calls=%d", result, err, reader.budgetCalls)
	}
}

func TestAgentAttachmentToolsRefreshBudgetBeforePrivateModelCall(t *testing.T) {
	for _, scenario := range []string{"smaller", "error", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			reader := &fakeAgentAttachmentReader{budget: func(context.Context) (int, error) {
				calls++
				if calls == 1 {
					return 64 << 10, nil
				}
				switch scenario {
				case "smaller":
					return 16, nil
				case "error":
					return 0, errors.New("private provider budget diagnostic")
				default:
					cancel()
					return 64 << 10, nil
				}
			}}
			llm := &scriptedLLM{responses: []*types.ChatResponse{
				makeChatResp(makeAssistantMsg("", agentLoopToolCall("read", AgentToolReadAttachment, `{"attachment_ref":"att_1"}`))),
				makeChatResp(makeAssistantMsg("Done.")),
			}}
			loop := NewAgentLoopExecutor(llm, nil, nil, nil, 3, nil, HTTPRequestPolicy{})
			result, err := loop.Execute(ctx, attachmentLoopSpec(t, reader))
			if err != nil || calls != 2 {
				t.Fatalf("Execute=%+v %v, budget calls=%d", result, err, calls)
			}
			if scenario == "smaller" {
				if result.Status != "completed" || len(llm.lastReqs) != 2 {
					t.Fatalf("shrink execution=%+v", result)
				}
				request := mustJSON(t, llm.lastReqs[1])
				if strings.Contains(request, "private attachment page") || !strings.Contains(request, attachmentToolContextOmission) {
					t.Fatal("final smaller route retained private result")
				}
			} else if result.Status != "failed" || len(llm.lastReqs) != 1 || strings.Contains(result.LastError, "private") {
				t.Fatalf("unvalidated context reached provider or failure details: %+v calls=%d", result, len(llm.lastReqs))
			}
		})
	}
}
