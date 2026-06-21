package projectassistant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/projectwork"
)

func validateActionShape(action Action) error {
	kind := normalizeKind(action.Kind)
	switch kind {
	case ActionCreateProject, ActionUpdateProject, ActionAttachProjectRoot, ActionRemoveProjectRoot,
		ActionSetProjectDefaults, ActionMoveChatSession, ActionCreateRole, ActionCreateWorkItem, ActionUpdateWorkItem,
		ActionCreateAssignment, ActionCreateHandoff, ActionUpdateHandoff, ActionCreateMemoryCandidate:
		if len(action.Patch) == 0 && kind != ActionRemoveProjectRoot {
			return fmt.Errorf("%w: action %q patch is required", ErrInvalid, kind)
		}
		if kind == ActionCreateAssignment {
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
			if err := validateAssignmentProposalBoundary(patch); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnknownActionKind, action.Kind)
	}
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
