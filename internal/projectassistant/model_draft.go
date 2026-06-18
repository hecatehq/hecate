package projectassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	DraftModeDeterministic = "deterministic"
	DraftModeModel         = "model"
	DraftModeBootstrap     = "bootstrap"

	modelDraftMaxTokens = 1600
)

type LLMClient interface {
	Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error)
}

type modelDraftResponse struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Actions []Action `json:"actions"`
}

var modelDraftResponseFormat = json.RawMessage(`{"type":"json_object"}`)

func (s *Service) draftWithModel(ctx context.Context, input DraftInput, draftContext DraftContext) (Proposal, error) {
	if s == nil || s.llm == nil {
		return Proposal{}, fmt.Errorf("%w: model draft client is not configured", ErrStoreNotConfigured)
	}
	model := firstNonEmpty(input.Model, draftContext.Project.DefaultModel)
	if model == "" {
		return Proposal{}, fmt.Errorf("%w: model draft requires a model or project default_model", ErrInvalid)
	}
	provider := firstNonEmpty(input.Provider, draftContext.Project.DefaultProvider)
	messages, err := modelDraftMessages(draftContext)
	if err != nil {
		return Proposal{}, err
	}
	resp, err := s.llm.Chat(ctx, types.ChatRequest{
		RequestID:      strings.TrimSpace(input.RequestID),
		Model:          model,
		Messages:       messages,
		MaxTokens:      modelDraftMaxTokens,
		Temperature:    0.2,
		ResponseFormat: append(json.RawMessage(nil), modelDraftResponseFormat...),
		Scope:          types.RequestScope{ProviderHint: provider},
	})
	if err != nil {
		return Proposal{}, err
	}
	draft, err := decodeModelDraftResponse(resp)
	if err != nil {
		return Proposal{}, err
	}
	if err := validateModelDraftActions(draftContext, draft.Actions); err != nil {
		return Proposal{}, err
	}
	return s.Propose(ctx, ProposalInput{
		Title:   draft.Title,
		Summary: draft.Summary,
		Actions: draft.Actions,
		TraceID: strings.TrimSpace(input.TraceID),
	})
}

func modelDraftMessages(draftContext DraftContext) ([]types.Message, error) {
	contextJSON, err := json.MarshalIndent(draftContext, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%w: encode model draft context: %v", ErrInvalid, err)
	}
	system := strings.TrimSpace(`You are Hecate Project Assistant.
Return JSON only, shaped as {"title": string, "summary": string, "actions": [ProjectAssistantAction]}.
You draft reviewable project proposals; you do not apply them.
Allowed action kinds are create_work_item, update_work_item, create_assignment, create_handoff, and create_memory_candidate.
Every action must target the current project_id from the context.
Do not create chats, tasks, runs, sessions, filesystem changes, shell commands, or durable memory entries.
If selected_work is present, work-scoped actions must use that selected work item.
For create_assignment, use context.selection.role_id, context.selection.driver_kind, status "queued", and selected_work.root_id when present.
Memory actions must create memory candidates only, with suggested_trust_label "generated_summary" and suggested_source_kind "generated". Pending memory candidates in context are lower-trust than accepted memory.`)
	user := fmt.Sprintf("Draft one concise proposal for this Project Assistant context:\n\n%s", string(contextJSON))
	return []types.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}, nil
}

func decodeModelDraftResponse(resp *types.ChatResponse) (modelDraftResponse, error) {
	content := strings.TrimSpace(modelDraftContent(resp))
	if content == "" {
		return modelDraftResponse{}, fmt.Errorf("%w: model draft response is empty", ErrInvalid)
	}
	parseContent := content
	var draft modelDraftResponse
	if err := json.Unmarshal([]byte(content), &draft); err != nil {
		extracted := extractJSONObject(content)
		if extracted == content {
			return modelDraftResponse{}, fmt.Errorf("%w: decode model draft response: %v", ErrInvalid, err)
		}
		parseContent = extracted
		if err := json.Unmarshal([]byte(extracted), &draft); err != nil {
			return modelDraftResponse{}, fmt.Errorf("%w: decode model draft response: %v", ErrInvalid, err)
		}
	}
	if len(draft.Actions) == 0 {
		var wrapped struct {
			Proposal modelDraftResponse `json:"proposal"`
		}
		if err := json.Unmarshal([]byte(parseContent), &wrapped); err == nil && len(wrapped.Proposal.Actions) > 0 {
			draft = wrapped.Proposal
		}
	}
	return draft, nil
}

func modelDraftContent(resp *types.ChatResponse) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	message := resp.Choices[0].Message
	if strings.TrimSpace(message.Content) != "" {
		return message.Content
	}
	var builder strings.Builder
	for _, block := range message.ContentBlocks {
		if strings.TrimSpace(block.Text) == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(block.Text)
	}
	return builder.String()
}

func extractJSONObject(content string) string {
	for start, r := range content {
		if r != '{' {
			continue
		}
		if end, ok := balancedJSONObjectEnd(content[start:]); ok {
			return strings.TrimSpace(content[start : start+end])
		}
	}
	return content
}

func balancedJSONObjectEnd(content string) (int, bool) {
	depth := 0
	inString := false
	escaped := false
	for idx, r := range content {
		if inString {
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == '"':
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return idx + 1, true
			}
			if depth < 0 {
				return 0, false
			}
		}
	}
	return 0, false
}

func validateModelDraftActions(draftContext DraftContext, actions []Action) error {
	if len(actions) == 0 {
		return fmt.Errorf("%w: model draft actions are required", ErrInvalid)
	}
	for idx, action := range actions {
		if err := validateModelDraftAction(draftContext, idx, action); err != nil {
			return err
		}
	}
	return nil
}

func validateModelDraftAction(draftContext DraftContext, index int, action Action) error {
	kind := normalizeKind(action.Kind)
	switch kind {
	case ActionCreateWorkItem, ActionUpdateWorkItem, ActionCreateAssignment, ActionCreateHandoff, ActionCreateMemoryCandidate:
	default:
		return fmt.Errorf("%w: model draft action %d kind %q is not allowed", ErrInvalid, index+1, kind)
	}
	if err := validateActionShape(action); err != nil {
		return err
	}
	projectID := strings.TrimSpace(draftContext.Project.ID)
	if targetProjectID := targetValue(action, "project_id"); targetProjectID == "" {
		return fmt.Errorf("%w: model draft action %d target.project_id is required", ErrInvalid, index+1)
	} else if targetProjectID != projectID {
		return fmt.Errorf("%w: model draft action %d targets project %q outside current project %q", ErrInvalid, index+1, targetProjectID, projectID)
	}
	selectedWorkID := ""
	if draftContext.SelectedWork != nil {
		selectedWorkID = strings.TrimSpace(draftContext.SelectedWork.ID)
	}
	switch kind {
	case ActionCreateWorkItem:
		var patch workItemPatch
		if err := decodePatch(action, &patch); err != nil {
			return err
		}
		return validateOptionalProjectID(index, "patch.project_id", patch.ProjectID, projectID)
	case ActionUpdateWorkItem:
		if selectedWorkID == "" {
			return fmt.Errorf("%w: model draft action %d cannot update work without selected_work", ErrInvalid, index+1)
		}
		if workItemID := targetValue(action, "work_item_id"); workItemID != selectedWorkID {
			return fmt.Errorf("%w: model draft action %d must target selected work item %q", ErrInvalid, index+1, selectedWorkID)
		}
	case ActionCreateAssignment:
		if selectedWorkID == "" {
			return fmt.Errorf("%w: model draft action %d cannot create assignment without selected_work", ErrInvalid, index+1)
		}
		hasRuntimeLinks, err := assignmentPatchHasRuntimeLinks(action.Patch)
		if err != nil {
			return err
		}
		if hasRuntimeLinks {
			return fmt.Errorf("%w: model draft action %d assignment cannot bind chats, tasks, runs, messages, or snapshots", ErrInvalid, index+1)
		}
		var patch assignmentPatch
		if err := decodePatch(action, &patch); err != nil {
			return err
		}
		if err := validateOptionalProjectID(index, "patch.project_id", patch.ProjectID, projectID); err != nil {
			return err
		}
		if patch.WorkItemID != selectedWorkID {
			return fmt.Errorf("%w: model draft action %d assignment work_item_id must be selected work item %q", ErrInvalid, index+1, selectedWorkID)
		}
		if patch.RoleID != draftContext.Selection.RoleID || patch.RoleID == "" {
			return fmt.Errorf("%w: model draft action %d assignment role_id must use selected role %q", ErrInvalid, index+1, draftContext.Selection.RoleID)
		}
		if patch.DriverKind != draftContext.Selection.DriverKind || patch.DriverKind == "" {
			return fmt.Errorf("%w: model draft action %d assignment driver_kind must use selected driver %q", ErrInvalid, index+1, draftContext.Selection.DriverKind)
		}
		if patch.Status != "" && patch.Status != projectwork.AssignmentStatusQueued {
			return fmt.Errorf("%w: model draft action %d assignment status must be queued", ErrInvalid, index+1)
		}
	case ActionCreateHandoff:
		if selectedWorkID == "" {
			return fmt.Errorf("%w: model draft action %d cannot create handoff without selected_work", ErrInvalid, index+1)
		}
		var patch handoffPatch
		if err := decodePatch(action, &patch); err != nil {
			return err
		}
		if err := validateOptionalProjectID(index, "patch.project_id", patch.ProjectID, projectID); err != nil {
			return err
		}
		if patch.WorkItemID != selectedWorkID {
			return fmt.Errorf("%w: model draft action %d handoff work_item_id must be selected work item %q", ErrInvalid, index+1, selectedWorkID)
		}
		if patch.Status != "" && patch.Status != projectwork.HandoffStatusPending {
			return fmt.Errorf("%w: model draft action %d handoff status must be pending", ErrInvalid, index+1)
		}
	case ActionCreateMemoryCandidate:
		var patch memoryCandidatePatch
		if err := decodePatch(action, &patch); err != nil {
			return err
		}
		if err := validateOptionalProjectID(index, "patch.project_id", patch.ProjectID, projectID); err != nil {
			return err
		}
		return validateModelMemoryCandidateProvenance(index, patch)
	}
	return nil
}

func validateModelMemoryCandidateProvenance(index int, patch memoryCandidatePatch) error {
	trustLabel := strings.TrimSpace(patch.SuggestedTrustLabel)
	if trustLabel != "" && trustLabel != memory.TrustLabelGenerated {
		return fmt.Errorf("%w: model draft action %d memory candidate suggested_trust_label must be %q", ErrInvalid, index+1, memory.TrustLabelGenerated)
	}
	sourceKind := strings.TrimSpace(patch.SuggestedSourceKind)
	if sourceKind != "" && sourceKind != memory.SourceKindGenerated {
		return fmt.Errorf("%w: model draft action %d memory candidate suggested_source_kind must be %q", ErrInvalid, index+1, memory.SourceKindGenerated)
	}
	return nil
}

func validateOptionalProjectID(index int, field, value, projectID string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if value != projectID {
		return fmt.Errorf("%w: model draft action %d %s must be current project %q", ErrInvalid, index+1, field, projectID)
	}
	return nil
}

func normalizeDraftMode(mode string) string {
	return strings.TrimSpace(mode)
}
