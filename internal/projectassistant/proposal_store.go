package projectassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	ProposalStatusProposed = "proposed"
	ProposalStatusApplying = "applying"

	ProposalSourceAPI            = "api"
	ProposalSourceApplyRequest   = "apply_request"
	ProposalSourceBootstrap      = "bootstrap"
	ProposalSourceDraft          = "draft"
	ProposalSourceModelDraft     = "model_draft"
	ProposalSourceReviewFollowUp = "review_follow_up"
)

type ProposalStore interface {
	Backend() string
	UpsertProposal(ctx context.Context, record ProposalRecord) (ProposalRecord, error)
	ListProposals(ctx context.Context, projectID string) ([]ProposalRecord, error)
	GetProposal(ctx context.Context, id string) (ProposalRecord, bool, error)
	UpdateProposalApplyState(ctx context.Context, proposalID string, result ApplyResult) (ProposalRecord, error)
	RecordApplyAttempt(ctx context.Context, attempt ApplyAttempt) (ProposalRecord, error)
	DeleteProject(ctx context.Context, projectID string) (int, error)
	Clear(ctx context.Context) (int, error)
}

type ProposalRecord struct {
	ID            string         `json:"id"`
	ProjectID     string         `json:"project_id,omitempty"`
	Source        string         `json:"source,omitempty"`
	SourceID      string         `json:"source_id,omitempty"`
	Proposal      Proposal       `json:"proposal"`
	Status        string         `json:"status"`
	LatestResult  *ApplyResult   `json:"latest_result,omitempty"`
	ApplyAttempts []ApplyAttempt `json:"apply_attempts,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	AppliedAt     *time.Time     `json:"applied_at,omitempty"`

	Fingerprint string `json:"-"`
}

type ApplyAttempt struct {
	ID           string      `json:"id"`
	ProposalID   string      `json:"proposal_id"`
	Status       string      `json:"status"`
	Confirmed    bool        `json:"confirmed"`
	Result       ApplyResult `json:"result"`
	ErrorType    string      `json:"error_type,omitempty"`
	ErrorMessage string      `json:"error_message,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
}

type MemoryProposalStore struct {
	mu        sync.Mutex
	proposals map[string]ProposalRecord
	attempts  map[string][]ApplyAttempt
}

func NewMemoryProposalStore() *MemoryProposalStore {
	return &MemoryProposalStore{
		proposals: make(map[string]ProposalRecord),
		attempts:  make(map[string][]ApplyAttempt),
	}
}

func (s *MemoryProposalStore) Backend() string { return "memory" }

func (s *MemoryProposalStore) UpsertProposal(_ context.Context, record ProposalRecord) (ProposalRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if existing, ok := s.proposals[strings.TrimSpace(record.ID)]; ok && !existing.CreatedAt.IsZero() {
		record.CreatedAt = existing.CreatedAt
		if record.LatestResult == nil {
			record.LatestResult = cloneApplyResultPtr(existing.LatestResult)
		}
		if record.AppliedAt == nil {
			record.AppliedAt = cloneTimePtr(existing.AppliedAt)
		}
		if strings.TrimSpace(record.Status) == "" {
			record.Status = existing.Status
		}
	}
	record = normalizeProposalRecord(record, now)
	if err := validateProposalRecord(record); err != nil {
		return ProposalRecord{}, err
	}
	s.proposals[record.ID] = cloneProposalRecord(record)
	return s.proposalWithAttemptsLocked(record.ID), nil
}

func (s *MemoryProposalStore) GetProposal(_ context.Context, id string) (ProposalRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	if _, ok := s.proposals[id]; !ok {
		return ProposalRecord{}, false, nil
	}
	return s.proposalWithAttemptsLocked(id), true, nil
}

func (s *MemoryProposalStore) ListProposals(_ context.Context, projectID string) ([]ProposalRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	projectID = strings.TrimSpace(projectID)
	records := make([]ProposalRecord, 0, len(s.proposals))
	for id, record := range s.proposals {
		if projectID != "" && strings.TrimSpace(record.ProjectID) != projectID {
			continue
		}
		records = append(records, s.proposalWithAttemptsLocked(id))
	}
	sortProposalRecords(records)
	return records, nil
}

func (s *MemoryProposalStore) UpdateProposalApplyState(_ context.Context, proposalID string, result ApplyResult) (ProposalRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	proposalID = strings.TrimSpace(proposalID)
	record, ok := s.proposals[proposalID]
	if !ok {
		return ProposalRecord{}, ErrNotFound
	}
	record = applyResultToProposalRecord(record, result, time.Now().UTC())
	s.proposals[proposalID] = cloneProposalRecord(record)
	return s.proposalWithAttemptsLocked(proposalID), nil
}

func (s *MemoryProposalStore) RecordApplyAttempt(_ context.Context, attempt ApplyAttempt) (ProposalRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	proposalID := strings.TrimSpace(attempt.ProposalID)
	record, ok := s.proposals[proposalID]
	if !ok {
		return ProposalRecord{}, ErrNotFound
	}
	now := time.Now().UTC()
	attempt = normalizeApplyAttempt(attempt, now)
	if err := validateApplyAttempt(attempt); err != nil {
		return ProposalRecord{}, err
	}
	record = applyResultToProposalRecord(record, attempt.Result, now)
	s.proposals[proposalID] = cloneProposalRecord(record)
	s.attempts[proposalID] = append(s.attempts[proposalID], cloneApplyAttempt(attempt))
	return s.proposalWithAttemptsLocked(proposalID), nil
}

func (s *MemoryProposalStore) DeleteProject(_ context.Context, projectID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	projectID = strings.TrimSpace(projectID)
	deleted := 0
	for id, record := range s.proposals {
		if strings.TrimSpace(record.ProjectID) != projectID {
			continue
		}
		delete(s.proposals, id)
		delete(s.attempts, id)
		deleted++
	}
	return deleted, nil
}

func (s *MemoryProposalStore) Clear(_ context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := len(s.proposals)
	s.proposals = make(map[string]ProposalRecord)
	s.attempts = make(map[string][]ApplyAttempt)
	return count, nil
}

func (s *MemoryProposalStore) proposalWithAttemptsLocked(id string) ProposalRecord {
	record := cloneProposalRecord(s.proposals[id])
	record.ApplyAttempts = cloneApplyAttempts(s.attempts[id])
	return record
}

func normalizeProposalRecord(record ProposalRecord, now time.Time) ProposalRecord {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	record.ID = strings.TrimSpace(firstNonEmpty(record.ID, record.Proposal.ID))
	record.ProjectID = strings.TrimSpace(firstNonEmpty(record.ProjectID, proposalProjectID(record.Proposal)))
	record.Source = strings.TrimSpace(record.Source)
	record.SourceID = strings.TrimSpace(record.SourceID)
	record.Status = strings.TrimSpace(record.Status)
	if record.Status == "" {
		record.Status = ProposalStatusProposed
	}
	record.Proposal = cloneProposal(record.Proposal)
	record.Proposal.ID = record.ID
	if record.Proposal.RequiresConfirmation == false {
		record.Proposal.RequiresConfirmation = true
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() || !record.UpdatedAt.After(record.CreatedAt) {
		record.UpdatedAt = now
	}
	if record.LatestResult != nil {
		record.LatestResult = cloneApplyResultPtr(record.LatestResult)
		if record.LatestResult.ProposalID == "" {
			record.LatestResult.ProposalID = record.ID
		}
		if record.LatestResult.TotalActionCount == 0 {
			record.LatestResult.TotalActionCount = len(record.Proposal.Actions)
		}
		record.Status = strings.TrimSpace(firstNonEmpty(record.Status, record.LatestResult.Status))
	}
	record.AppliedAt = cloneTimePtr(record.AppliedAt)
	record.ApplyAttempts = cloneApplyAttempts(record.ApplyAttempts)
	record.Fingerprint = strings.TrimSpace(record.Fingerprint)
	if record.Fingerprint == "" {
		fingerprint, err := actionSetFingerprint(record.Proposal.Actions)
		if err == nil {
			record.Fingerprint = fingerprint
		}
	}
	return record
}

func validateProposalRecord(record ProposalRecord) error {
	if record.ID == "" {
		return fmt.Errorf("%w: proposal id is required", ErrInvalid)
	}
	if record.Proposal.ID != record.ID {
		return fmt.Errorf("%w: proposal record id mismatch", ErrInvalid)
	}
	projectID, err := ProposalRecordProjectID(record.Proposal, record.ProjectID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(record.ProjectID) != projectID {
		return fmt.Errorf("%w: proposal record project_id must match its canonical action scope", ErrInvalid)
	}
	if len(record.Proposal.Actions) == 0 {
		return fmt.Errorf("%w: actions are required", ErrInvalid)
	}
	for _, action := range record.Proposal.Actions {
		if err := validateActionShape(action); err != nil {
			return err
		}
	}
	if record.Fingerprint == "" {
		return fmt.Errorf("%w: proposal fingerprint is required", ErrInvalid)
	}
	return nil
}

func normalizeApplyAttempt(attempt ApplyAttempt, now time.Time) ApplyAttempt {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	attempt.ID = strings.TrimSpace(attempt.ID)
	attempt.ProposalID = strings.TrimSpace(firstNonEmpty(attempt.ProposalID, attempt.Result.ProposalID))
	attempt.Status = strings.TrimSpace(firstNonEmpty(attempt.Status, attempt.Result.Status))
	attempt.ErrorType = strings.TrimSpace(attempt.ErrorType)
	attempt.ErrorMessage = strings.TrimSpace(attempt.ErrorMessage)
	if attempt.Result.ProposalID == "" {
		attempt.Result.ProposalID = attempt.ProposalID
	}
	if attempt.Result.Status == "" {
		attempt.Result.Status = attempt.Status
	}
	if attempt.CreatedAt.IsZero() {
		attempt.CreatedAt = now
	}
	return cloneApplyAttempt(attempt)
}

func validateApplyAttempt(attempt ApplyAttempt) error {
	if attempt.ID == "" {
		return fmt.Errorf("%w: apply attempt id is required", ErrInvalid)
	}
	if attempt.ProposalID == "" {
		return fmt.Errorf("%w: apply attempt proposal_id is required", ErrInvalid)
	}
	if attempt.Status == "" {
		return fmt.Errorf("%w: apply attempt status is required", ErrInvalid)
	}
	return nil
}

func applyResultToProposalRecord(record ProposalRecord, result ApplyResult, now time.Time) ProposalRecord {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result = cloneApplyResult(result)
	if result.ProposalID == "" {
		result.ProposalID = record.ID
	}
	if result.TotalActionCount == 0 {
		result.TotalActionCount = len(record.Proposal.Actions)
	}
	record.LatestResult = &result
	record.Status = firstNonEmpty(result.Status, record.Status, ProposalStatusProposed)
	record.UpdatedAt = now
	if result.Applied || result.Status == ApplyStatusApplied {
		appliedAt := now
		record.AppliedAt = &appliedAt
	}
	return cloneProposalRecord(record)
}

func cloneProposalRecord(record ProposalRecord) ProposalRecord {
	record.Proposal = cloneProposal(record.Proposal)
	record.LatestResult = cloneApplyResultPtr(record.LatestResult)
	record.ApplyAttempts = cloneApplyAttempts(record.ApplyAttempts)
	record.AppliedAt = cloneTimePtr(record.AppliedAt)
	return record
}

func sortProposalRecords(records []ProposalRecord) {
	slices.SortFunc(records, func(a, b ProposalRecord) int {
		if cmp := b.UpdatedAt.Compare(a.UpdatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID, b.ID)
	})
}

func cloneProposal(proposal Proposal) Proposal {
	return Proposal{
		ID:                   proposal.ID,
		Title:                proposal.Title,
		Summary:              proposal.Summary,
		Actions:              cloneActions(proposal.Actions),
		Warnings:             append([]string(nil), proposal.Warnings...),
		RequiresConfirmation: proposal.RequiresConfirmation,
		TraceID:              proposal.TraceID,
	}
}

func cloneApplyResult(result ApplyResult) ApplyResult {
	result.Actions = cloneActionResults(result.Actions)
	result.FailedActionIndex = cloneIntPtr(result.FailedActionIndex)
	return result
}

func cloneApplyResultPtr(result *ApplyResult) *ApplyResult {
	if result == nil {
		return nil
	}
	cloned := cloneApplyResult(*result)
	return &cloned
}

func cloneApplyAttempt(attempt ApplyAttempt) ApplyAttempt {
	attempt.Result = cloneApplyResult(attempt.Result)
	return attempt
}

func cloneApplyAttempts(attempts []ApplyAttempt) []ApplyAttempt {
	if attempts == nil {
		return nil
	}
	out := make([]ApplyAttempt, len(attempts))
	for idx, attempt := range attempts {
		out[idx] = cloneApplyAttempt(attempt)
	}
	return out
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func proposalProjectID(proposal Proposal) string {
	projectIDs := ProposalProjectIDs(proposal)
	if len(projectIDs) > 0 {
		return projectIDs[0]
	}
	return ""
}

// ProposalProjectIDs returns every explicit portable project scope in action
// order. Callers that need a complete mutation boundary should admit the full
// returned set atomically; the first ID is the proposal record's canonical
// project scope.
func ProposalProjectIDs(proposal Proposal) []string {
	projectIDs := make([]string, 0, len(proposal.Actions))
	seen := make(map[string]struct{}, len(proposal.Actions))
	appendProjectID := func(projectID string) {
		projectID = strings.TrimSpace(projectID)
		if projectID == "" {
			return
		}
		if _, ok := seen[projectID]; ok {
			return
		}
		seen[projectID] = struct{}{}
		projectIDs = append(projectIDs, projectID)
	}
	for _, action := range proposal.Actions {
		appendProjectID(targetValue(action, "project_id"))
		appendProjectID(actionPatchProjectID(action))
	}
	return projectIDs
}

func actionPatchProjectID(action Action) string {
	switch normalizeKind(action.Kind) {
	case ActionCreateProject:
		var patch projectPatch
		if err := decodePatch(action, &patch); err == nil {
			return strings.TrimSpace(patch.ID)
		}
	case ActionMoveChatSession:
		var patch moveChatSessionPatch
		if err := decodePatch(action, &patch); err == nil {
			return strings.TrimSpace(patch.ProjectID)
		}
	case ActionCreateRole:
		var patch rolePatch
		if err := decodePatch(action, &patch); err == nil {
			return strings.TrimSpace(patch.ProjectID)
		}
	case ActionCreateWorkItem:
		var patch workItemPatch
		if err := decodePatch(action, &patch); err == nil {
			return strings.TrimSpace(patch.ProjectID)
		}
	case ActionCreateAssignment:
		var patch assignmentPatch
		if err := decodePatch(action, &patch); err == nil {
			return strings.TrimSpace(patch.ProjectID)
		}
	case ActionCreateHandoff:
		var patch handoffPatch
		if err := decodePatch(action, &patch); err == nil {
			return strings.TrimSpace(patch.ProjectID)
		}
	case ActionCreateMemoryCandidate:
		var patch memoryCandidatePatch
		if err := decodePatch(action, &patch); err == nil {
			return strings.TrimSpace(patch.ProjectID)
		}
	}
	return ""
}

func applyAttemptForResult(id string, confirmed bool, result ApplyResult, err error) ApplyAttempt {
	attempt := ApplyAttempt{
		ID:         strings.TrimSpace(id),
		ProposalID: strings.TrimSpace(result.ProposalID),
		Status:     strings.TrimSpace(result.Status),
		Confirmed:  confirmed,
		Result:     cloneApplyResult(result),
		CreatedAt:  time.Now().UTC(),
	}
	if err != nil {
		attempt.ErrorType = applyAttemptErrorType(err)
		attempt.ErrorMessage = err.Error()
	}
	return attempt
}

func applyAttemptErrorType(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrConflict):
		return "conflict"
	case errors.Is(err, ErrConfirmationRequired):
		return "confirmation_required"
	case errors.Is(err, ErrStoreNotConfigured):
		return "store_not_configured"
	default:
		return "error"
	}
}

func encodeProposalJSON(proposal Proposal) ([]byte, error) {
	raw, err := json.Marshal(cloneProposal(proposal))
	if err != nil {
		return nil, fmt.Errorf("%w: encode proposal: %v", ErrInvalid, err)
	}
	return raw, nil
}

func encodeApplyResultJSON(result ApplyResult) ([]byte, error) {
	raw, err := json.Marshal(cloneApplyResult(result))
	if err != nil {
		return nil, fmt.Errorf("%w: encode apply result: %v", ErrInvalid, err)
	}
	return raw, nil
}
