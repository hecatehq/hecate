package projectassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

const (
	ActionCreateProject         = "create_project"
	ActionUpdateProject         = "update_project"
	ActionAttachProjectRoot     = "attach_project_root"
	ActionRemoveProjectRoot     = "remove_project_root"
	ActionSetProjectDefaults    = "set_project_defaults"
	ActionMoveChatSession       = "move_chat_session"
	ActionCreateRole            = "create_role"
	ActionCreateWorkItem        = "create_work_item"
	ActionUpdateWorkItem        = "update_work_item"
	ActionCreateAssignment      = "create_assignment"
	ActionCreateHandoff         = "create_handoff"
	ActionUpdateHandoff         = "update_handoff"
	ActionCreateMemoryCandidate = "create_memory_candidate"
)

const (
	ApplyStatusApplied                    = "applied"
	ApplyStatusBlockedBeforeApply         = "blocked_before_apply"
	ApplyStatusPartialDueToRuntimeFailure = "partial_due_to_runtime_failure"
)

var (
	ErrInvalid              = errors.New("invalid project assistant proposal")
	ErrUnknownActionKind    = errors.New("unknown project assistant action kind")
	ErrNotFound             = errors.New("project assistant target not found")
	ErrConflict             = errors.New("project assistant conflict")
	ErrConfirmationRequired = errors.New("project assistant confirmation required")
	ErrStoreNotConfigured   = errors.New("project assistant store not configured")
)

type IDGenerator func(prefix string) string

type Service struct {
	mu                       sync.Mutex
	projects                 projects.Store
	projectAuthority         ProjectAuthority
	chats                    chat.Store
	work                     projectwork.Store
	workAuthority            WorkAuthority
	projectSkills            projectskills.Store
	memory                   memory.Store
	memoryCandidates         memory.CandidateStore
	memoryCandidateAuthority MemoryCandidateAuthority
	proposals                ProposalStore
	llm                      LLMClient
	idgen                    IDGenerator
}

type Stores struct {
	Projects                 projects.Store
	ProjectAuthority         ProjectAuthority
	Chats                    chat.Store
	Work                     projectwork.Store
	WorkAuthority            WorkAuthority
	ProjectSkills            projectskills.Store
	Memory                   memory.Store
	MemoryCandidates         memory.CandidateStore
	MemoryCandidateAuthority MemoryCandidateAuthority
	Proposals                ProposalStore
	LLM                      LLMClient
}

type ProposalInput struct {
	ID        string
	ProjectID string
	Source    string
	SourceID  string
	Title     string
	Summary   string
	Actions   []Action
	Warnings  []string
	TraceID   string
}

type DraftInput struct {
	ProjectID        string
	WorkItemID       string
	Request          string
	RoleID           string
	DriverKind       string
	DraftMode        string
	ReviewArtifactID string
	Provider         string
	Model            string
	RequestID        string
	TraceID          string
}

type ContextInput struct {
	ProjectID  string
	WorkItemID string
	Request    string
	RoleID     string
	DriverKind string
}

type Proposal struct {
	ID                   string   `json:"id"`
	Title                string   `json:"title"`
	Summary              string   `json:"summary"`
	Actions              []Action `json:"actions"`
	Warnings             []string `json:"warnings,omitempty"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
	TraceID              string   `json:"trace_id,omitempty"`
}

type Action struct {
	Kind   string            `json:"kind"`
	Target map[string]string `json:"target,omitempty"`
	Patch  json.RawMessage   `json:"patch,omitempty"`
	Reason string            `json:"reason,omitempty"`
}

type ApplyResult struct {
	ProposalID           string         `json:"proposal_id"`
	Status               string         `json:"status"`
	Applied              bool           `json:"applied"`
	Actions              []ActionResult `json:"actions"`
	TotalActionCount     int            `json:"total_action_count"`
	CommittedActionCount int            `json:"committed_action_count"`
	FailedActionIndex    *int           `json:"failed_action_index,omitempty"`
	ResumeActionIndex    int            `json:"resume_action_index"`
}

type ActionResult struct {
	Kind string            `json:"kind"`
	ID   string            `json:"id,omitempty"`
	Data map[string]string `json:"data,omitempty"`
}

type ApplyError struct {
	ProposalID        string
	FailedActionIndex int
	Result            ApplyResult
	Err               error
}

func (e *ApplyError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return fmt.Sprintf("project assistant apply failed at action %d", e.FailedActionIndex)
	}
	return fmt.Sprintf("project assistant apply failed at action %d: %v", e.FailedActionIndex, e.Err)
}

func (e *ApplyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type actionFingerprint struct {
	Kind   string            `json:"kind"`
	Target map[string]string `json:"target,omitempty"`
	Patch  json.RawMessage   `json:"patch,omitempty"`
	Reason string            `json:"reason,omitempty"`
}

var defaultIDCounter atomic.Uint64

func NewService(stores Stores, idgen IDGenerator) *Service {
	if idgen == nil {
		idgen = func(prefix string) string {
			return fmt.Sprintf("%s_%d", strings.TrimSpace(prefix), defaultIDCounter.Add(1))
		}
	}
	proposals := stores.Proposals
	if proposals == nil {
		proposals = NewMemoryProposalStore()
	}
	return &Service{
		projects:                 stores.Projects,
		projectAuthority:         projectAuthorityForStores(stores),
		chats:                    stores.Chats,
		work:                     stores.Work,
		workAuthority:            workAuthorityForStores(stores),
		projectSkills:            stores.ProjectSkills,
		memory:                   stores.Memory,
		memoryCandidates:         stores.MemoryCandidates,
		memoryCandidateAuthority: memoryCandidateAuthorityForStores(stores),
		proposals:                proposals,
		llm:                      stores.LLM,
		idgen:                    idgen,
	}
}

func (s *Service) Propose(ctx context.Context, input ProposalInput) (Proposal, error) {
	proposal, err := s.buildProposal(input)
	if err != nil {
		return Proposal{}, err
	}
	record, err := s.storeProposal(ctx, proposal, input.ProjectID, input.Source, input.SourceID)
	if err != nil {
		return Proposal{}, err
	}
	return record.Proposal, nil
}

func (s *Service) Proposal(ctx context.Context, id string) (ProposalRecord, bool, error) {
	if s == nil || s.proposals == nil {
		return ProposalRecord{}, false, ErrStoreNotConfigured
	}
	return s.proposals.GetProposal(ctx, id)
}

func (s *Service) buildProposal(input ProposalInput) (Proposal, error) {
	if s == nil {
		return Proposal{}, ErrStoreNotConfigured
	}
	actions := cloneActions(input.Actions)
	if len(actions) == 0 {
		return Proposal{}, fmt.Errorf("%w: actions are required", ErrInvalid)
	}
	for _, action := range actions {
		if err := validateActionShape(action); err != nil {
			return Proposal{}, err
		}
	}
	id := strings.TrimSpace(input.ID)
	if id == "" {
		id = s.idgen("pa")
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = "Project operation proposal"
	}
	return Proposal{
		ID:                   id,
		Title:                title,
		Summary:              strings.TrimSpace(input.Summary),
		Actions:              actions,
		Warnings:             append([]string(nil), input.Warnings...),
		RequiresConfirmation: true,
		TraceID:              strings.TrimSpace(input.TraceID),
	}, nil
}

func (s *Service) storeProposal(ctx context.Context, proposal Proposal, projectID, source, sourceID string) (ProposalRecord, error) {
	if s == nil || s.proposals == nil {
		return ProposalRecord{}, ErrStoreNotConfigured
	}
	fingerprint, err := actionSetFingerprint(proposal.Actions)
	if err != nil {
		return ProposalRecord{}, err
	}
	return s.proposals.UpsertProposal(ctx, ProposalRecord{
		ID:          proposal.ID,
		ProjectID:   strings.TrimSpace(projectID),
		Source:      strings.TrimSpace(source),
		SourceID:    strings.TrimSpace(sourceID),
		Proposal:    proposal,
		Status:      ProposalStatusProposed,
		Fingerprint: fingerprint,
	})
}

func (s *Service) Draft(ctx context.Context, input DraftInput) (Proposal, error) {
	mode := normalizeDraftMode(input.DraftMode)
	if mode == DraftModeReviewFollowUp && strings.TrimSpace(input.RoleID) == "" {
		roleID, err := s.defaultReviewFollowUpRoleID(ctx, input)
		if err != nil {
			return Proposal{}, err
		}
		input.RoleID = roleID
	}
	draftContext, err := s.Context(ctx, ContextInput{
		ProjectID:  input.ProjectID,
		WorkItemID: input.WorkItemID,
		Request:    input.Request,
		RoleID:     input.RoleID,
		DriverKind: input.DriverKind,
	})
	if err != nil {
		return Proposal{}, err
	}
	switch mode {
	case "", DraftModeDeterministic:
		return s.draftDeterministic(ctx, input, draftContext)
	case DraftModeBootstrap:
		return s.draftBootstrap(ctx, input, draftContext)
	case DraftModeReviewFollowUp:
		return s.draftReviewFollowUp(ctx, input, draftContext)
	case DraftModeModel:
		return s.draftWithModel(ctx, input, draftContext)
	default:
		return Proposal{}, fmt.Errorf("%w: unsupported draft_mode %q", ErrInvalid, input.DraftMode)
	}
}

func (s *Service) draftDeterministic(ctx context.Context, input DraftInput, draftContext DraftContext) (Proposal, error) {
	request := draftRequestParts(input.Request)
	roleLabel := firstNonEmpty(draftContext.Selection.RoleName, draftContext.Selection.RoleID, "role")
	var proposalInput ProposalInput
	if draftContext.SelectedWork != nil {
		if draftContext.Selection.RoleID == "" {
			return Proposal{}, fmt.Errorf("%w: role_id is required for assignment drafts", ErrInvalid)
		}
		patch := map[string]any{
			"project_id":   draftContext.Project.ID,
			"work_item_id": draftContext.SelectedWork.ID,
			"role_id":      draftContext.Selection.RoleID,
			"driver_kind":  draftContext.Selection.DriverKind,
			"status":       projectwork.AssignmentStatusQueued,
		}
		if rootID := strings.TrimSpace(draftContext.SelectedWork.RootID); rootID != "" {
			patch["root_id"] = rootID
		}
		proposalInput = ProposalInput{
			ProjectID: draftContext.Project.ID,
			Source:    ProposalSourceDraft,
			SourceID:  DraftModeDeterministic,
			Title:     firstNonEmpty(request.title, fmt.Sprintf("Queue %s for %s", roleLabel, draftContext.SelectedWork.Title)),
			Summary:   fmt.Sprintf("Create a queued %s assignment on the selected work item.", draftContext.Selection.DriverKind),
			Actions: []Action{{
				Kind:   ActionCreateAssignment,
				Target: map[string]string{"project_id": draftContext.Project.ID},
				Patch:  mustRawJSON(patch),
				Reason: "Queue a reviewable assignment without starting execution.",
			}},
			TraceID: strings.TrimSpace(input.TraceID),
		}
	} else {
		patch := map[string]any{
			"project_id": draftContext.Project.ID,
			"title":      firstNonEmpty(request.title, "Untitled project work"),
			"brief":      request.brief,
			"status":     projectwork.WorkItemStatusReady,
			"priority":   "normal",
		}
		if draftContext.Selection.RoleID != "" {
			patch["owner_role_id"] = draftContext.Selection.RoleID
		}
		proposalInput = ProposalInput{
			ProjectID: draftContext.Project.ID,
			Source:    ProposalSourceDraft,
			SourceID:  DraftModeDeterministic,
			Title:     firstNonEmpty(request.title, "Create project work item"),
			Summary:   "Create a ready work item from the current assistant draft.",
			Actions: []Action{{
				Kind:   ActionCreateWorkItem,
				Target: map[string]string{"project_id": draftContext.Project.ID},
				Patch:  mustRawJSON(patch),
				Reason: "Create a reviewable project work item.",
			}},
			TraceID: strings.TrimSpace(input.TraceID),
		}
	}
	return s.Propose(ctx, proposalInput)
}
