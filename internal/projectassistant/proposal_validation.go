package projectassistant

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/projectwork"
)

// ValidateProposalActions validates every action before API composition uses
// its project scope for mutation admission. Durable apply repeats this check at
// its own boundary.
func ValidateProposalActions(proposal Proposal) error {
	for _, action := range proposal.Actions {
		if err := validateActionShape(action); err != nil {
			return err
		}
	}
	return nil
}

// CanonicalizeProposal assigns stable typed identities needed before callers
// derive a proposal's mutation scope. An omitted create_project id is derived
// from the durable proposal id and action position, so a direct Apply retry is
// stable even if an earlier attempt was rejected before the proposal ledger
// could be written. Explicit ids and already-canonical action bytes are left
// unchanged.
func (s *Service) CanonicalizeProposal(proposal Proposal) (Proposal, error) {
	if s == nil {
		return Proposal{}, ErrStoreNotConfigured
	}
	proposal.ID = strings.TrimSpace(proposal.ID)
	if proposal.ID == "" {
		return Proposal{}, fmt.Errorf("%w: proposal id is required", ErrInvalid)
	}
	proposal.Actions = cloneActions(proposal.Actions)
	proposal.Warnings = append([]string(nil), proposal.Warnings...)
	if len(proposal.Actions) == 0 {
		return Proposal{}, fmt.Errorf("%w: actions are required", ErrInvalid)
	}
	if err := ValidateProposalActions(proposal); err != nil {
		return Proposal{}, err
	}
	for index := range proposal.Actions {
		action := proposal.Actions[index]
		if normalizeKind(action.Kind) != ActionCreateProject {
			continue
		}
		var patch projectPatch
		if err := decodePatch(action, &patch); err != nil {
			return Proposal{}, err
		}
		if strings.TrimSpace(patch.ID) != "" {
			continue
		}
		patch.ID = targetValue(action, "project_id")
		if patch.ID == "" {
			patch.ID = canonicalCreateProjectID(proposal.ID, index)
		}
		encoded, err := json.Marshal(patch)
		if err != nil {
			return Proposal{}, fmt.Errorf("%w: encode canonical create_project patch: %v", ErrInvalid, err)
		}
		proposal.Actions[index].Patch = encoded
	}
	return proposal, nil
}

func canonicalCreateProjectID(proposalID string, actionIndex int) string {
	seed, _ := json.Marshal(struct {
		Domain      string `json:"domain"`
		ProposalID  string `json:"proposal_id"`
		ActionIndex int    `json:"action_index"`
	}{
		Domain:      "hecate.project-assistant.create-project.v1",
		ProposalID:  strings.TrimSpace(proposalID),
		ActionIndex: actionIndex,
	})
	digest := sha256.Sum256(seed)
	return fmt.Sprintf("proj_%x", digest[:12])
}

func validateActionShape(action Action) error {
	kind := normalizeKind(action.Kind)
	if _, ok := lookupApplyActionSpec(kind); !ok {
		return fmt.Errorf("%w: %s", ErrUnknownActionKind, action.Kind)
	}
	if len(action.Patch) == 0 && kind != ActionRemoveProjectRoot {
		return fmt.Errorf("%w: action %q patch is required", ErrInvalid, kind)
	}
	targetProjectID := strings.TrimSpace(targetValue(action, "project_id"))
	patchProjectID := actionPatchProjectID(action)
	if targetProjectID != "" && patchProjectID != "" && targetProjectID != patchProjectID {
		return fmt.Errorf("%w: action %q has conflicting target and patch project ids", ErrInvalid, kind)
	}
	if kind != ActionCreateAssignment {
		return nil
	}
	hasRuntimeLinks, err := assignmentPatchHasRuntimeLinks(action.Patch)
	if err != nil {
		return err
	}
	if hasRuntimeLinks {
		return fmt.Errorf("%w: create_assignment proposals cannot bind chats, tasks, runs, messages, or snapshots", ErrInvalid)
	}
	var patch assignmentPatch
	if err := decodePatch(action, &patch); err != nil {
		return err
	}
	return validateAssignmentProposalBoundary(patch)
}

func validateAssignmentProposalBoundary(patch assignmentPatch) error {
	if len(patch.ExecutionRef) > 0 {
		return fmt.Errorf("%w: create_assignment proposals cannot bind chats, tasks, runs, messages, or snapshots", ErrInvalid)
	}
	if patch.Status != "" && patch.Status != projectwork.AssignmentStatusQueued {
		return fmt.Errorf("%w: create_assignment proposals must create queued assignments", ErrInvalid)
	}
	return nil
}

func assignmentPatchHasRuntimeLinks(raw json.RawMessage) (bool, error) {
	if len(raw) == 0 {
		return false, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false, fmt.Errorf("%w: decode assignment patch keys: %v", ErrInvalid, err)
	}
	for _, key := range []string{"task_id", "run_id", "chat_session_id", "message_id", "context_snapshot_id", "execution_ref"} {
		if _, ok := fields[key]; ok {
			return true, nil
		}
	}
	return false, nil
}

func decodePatch(action Action, target any) error {
	if len(action.Patch) == 0 {
		return nil
	}
	if err := json.Unmarshal(action.Patch, target); err != nil {
		return fmt.Errorf("%w: decode %s patch: %v", ErrInvalid, normalizeKind(action.Kind), err)
	}
	return nil
}

func normalizeKind(kind string) string {
	return strings.TrimSpace(kind)
}

func cloneActions(actions []Action) []Action {
	if actions == nil {
		return nil
	}
	cloned := make([]Action, len(actions))
	for idx, action := range actions {
		cloned[idx] = Action{
			Kind:   action.Kind,
			Target: cloneStringMap(action.Target),
			Patch:  append(json.RawMessage(nil), action.Patch...),
			Reason: action.Reason,
		}
	}
	return cloned
}

func actionSetFingerprint(actions []Action) (string, error) {
	items := make([]actionFingerprint, 0, len(actions))
	for _, action := range actions {
		patch, err := compactPatch(action.Patch)
		if err != nil {
			return "", err
		}
		items = append(items, actionFingerprint{
			Kind:   normalizeKind(action.Kind),
			Target: cloneStringMap(action.Target),
			Patch:  patch,
			Reason: strings.TrimSpace(action.Reason),
		})
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("%w: encode action fingerprint: %v", ErrInvalid, err)
	}
	return string(encoded), nil
}

func compactPatch(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, fmt.Errorf("%w: invalid action patch json", ErrInvalid)
	}
	return append(json.RawMessage(nil), buf.Bytes()...), nil
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
