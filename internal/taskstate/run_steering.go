package taskstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/pkg/types"
)

const RunSteeringArtifactKind = "run_steering"
const MaxRunSteeringContentBytes = 8 * 1024
const maxRunSteeringItems = 32
const maxPendingRunSteeringItems = 8

var (
	ErrSteeringUnavailable        = errors.New("live guidance requires a queued or running native agent run")
	ErrSteeringRequestConflict    = errors.New("guidance request id was already used with different content")
	ErrSteeringFull               = errors.New("this run has reached its guidance limit")
	ErrSteeringCheckpointConflict = errors.New("guidance conversation checkpoint changed")
)

// RunSteering is user guidance, not a new turn or a change to runtime policy.
// Its ordinary task artifact owns the text; event payloads contain only ids.
type RunSteering struct {
	ID             string     `json:"id"`
	Content        string     `json:"content"`
	Status         string     `json:"status"`
	Sequence       int        `json:"sequence"`
	Reason         string     `json:"reason,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	AppliedAt      *time.Time `json:"applied_at,omitempty"`
	ModelCallIndex int        `json:"model_call_index,omitempty"`
}

type RunSteeringSubmission struct {
	TaskID, RunID, ClientRequestID, Content, RequestID, TraceID string
	CreatedAt                                                   time.Time
}

type RunSteeringResult struct {
	RunSteering
	Replay bool `json:"replay"`
}

// Applying guidance and checkpointing its messages must be one commit. A crash
// may retry the model request, but may never append the instruction twice.
type RunSteeringApplication struct {
	TaskID, RunID              string
	IDs                        []string
	ExpectedConversationDigest string
	Conversation               types.TaskArtifact
	ModelCallIndex             int
	AppliedAt                  time.Time
}

type RunSteeringStore interface {
	SubmitRunSteering(context.Context, RunSteeringSubmission) (RunSteeringResult, error)
	ApplyRunSteering(context.Context, RunSteeringApplication) (bool, error)
}

func ValidateRunSteeringSubmission(in RunSteeringSubmission) error {
	if strings.TrimSpace(in.TaskID) == "" || strings.TrimSpace(in.RunID) == "" {
		return fmt.Errorf("task and run ids are required")
	}
	if len(in.ClientRequestID) < 1 || len(in.ClientRequestID) > 128 {
		return fmt.Errorf("client_request_id must contain 1 to 128 ASCII characters")
	}
	for _, r := range in.ClientRequestID {
		if r < 33 || r > 126 {
			return fmt.Errorf("client_request_id must contain printable ASCII characters")
		}
	}
	if !utf8.ValidString(in.Content) || strings.TrimSpace(in.Content) == "" || len(in.Content) > MaxRunSteeringContentBytes {
		return fmt.Errorf("guidance must be nonempty UTF-8 text up to 8 KiB")
	}
	return nil
}

func RunSteeringFromArtifact(artifact types.TaskArtifact) (RunSteering, error) {
	var item RunSteering
	if artifact.Kind != RunSteeringArtifactKind {
		return item, fmt.Errorf("not a guidance artifact")
	}
	if err := json.Unmarshal([]byte(artifact.ContentText), &item); err != nil {
		return item, err
	}
	item.ID, item.Status = artifact.ID, artifact.Status
	return item, nil
}

func ConversationDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func steeringArtifact(item RunSteering, taskID, runID, requestID, traceID string) types.TaskArtifact {
	payload, _ := json.Marshal(item)
	return types.TaskArtifact{ID: item.ID, TaskID: taskID, RunID: runID,
		Kind: RunSteeringArtifactKind, Name: "Operator guidance", MimeType: "application/json",
		StorageKind: "inline", ContentText: string(payload), SizeBytes: int64(len(payload)),
		Status: item.Status, CreatedAt: item.CreatedAt, RequestID: requestID, TraceID: traceID}
}

func prepareRunSteering(task types.Task, run types.TaskRun, artifacts []types.TaskArtifact, in RunSteeringSubmission) (types.TaskArtifact, bool, error) {
	id := "steer-" + ConversationDigest(in.TaskID+"\x00"+in.RunID+"\x00"+in.ClientRequestID)
	pending := 0
	for _, artifact := range artifacts {
		if artifact.ID == id {
			item, err := RunSteeringFromArtifact(artifact)
			if err != nil {
				return types.TaskArtifact{}, false, err
			}
			if item.Content != in.Content {
				return types.TaskArtifact{}, false, ErrSteeringRequestConflict
			}
			return artifact, true, nil
		}
		if artifact.Status == "pending" {
			pending++
		}
	}
	if task.ExecutionKind != "agent_loop" || task.WorkflowMode != "" || run.WorkflowMode != "" ||
		(run.Status != "queued" && run.Status != "running") {
		return types.TaskArtifact{}, false, ErrSteeringUnavailable
	}
	if len(artifacts) >= maxRunSteeringItems || pending >= maxPendingRunSteeringItems {
		return types.TaskArtifact{}, false, ErrSteeringFull
	}
	when := in.CreatedAt
	if when.IsZero() {
		when = time.Now().UTC()
	}
	item := RunSteering{ID: id, Content: in.Content, Status: "pending", Sequence: len(artifacts) + 1, CreatedAt: when}
	return steeringArtifact(item, task.ID, run.ID, in.RequestID, in.TraceID), false, nil
}

func prepareAppliedRunSteering(run types.TaskRun, artifacts []types.TaskArtifact, conversation *types.TaskArtifact, in RunSteeringApplication) ([]types.TaskArtifact, error) {
	if run.Status != "running" {
		return nil, ErrSteeringUnavailable
	}
	if len(in.IDs) == 0 || len(in.IDs) > maxPendingRunSteeringItems || in.Conversation.ID != "convo-"+run.ID ||
		in.Conversation.TaskID != run.TaskID || in.Conversation.RunID != run.ID || in.Conversation.Kind != "agent_conversation" {
		return nil, fmt.Errorf("invalid guidance checkpoint")
	}
	digest := ""
	if conversation != nil {
		digest = ConversationDigest(conversation.ContentText)
	}
	if digest != in.ExpectedConversationDigest {
		return nil, ErrSteeringCheckpointConflict
	}
	byID := make(map[string]types.TaskArtifact, len(artifacts))
	for _, artifact := range artifacts {
		byID[artifact.ID] = artifact
	}
	updates := make([]types.TaskArtifact, 0, len(in.IDs))
	seen := make(map[string]bool, len(in.IDs))
	for _, id := range in.IDs {
		artifact, ok := byID[id]
		if !ok || seen[id] || artifact.Status != "pending" {
			return nil, ErrSteeringCheckpointConflict
		}
		seen[id] = true
		item, err := RunSteeringFromArtifact(artifact)
		if err != nil {
			return nil, err
		}
		item.Status = "applied"
		item.AppliedAt = &in.AppliedAt
		item.ModelCallIndex = in.ModelCallIndex
		updates = append(updates, steeringArtifact(item, run.TaskID, run.ID, artifact.RequestID, artifact.TraceID))
	}
	return updates, nil
}

func steeringEvent(artifact types.TaskArtifact, eventType runtimeevents.EventType, when time.Time) types.TaskRunEvent {
	return types.TaskRunEvent{TaskID: artifact.TaskID, RunID: artifact.RunID, EventType: eventType.String(),
		Data:      map[string]any{"instruction_id": artifact.ID, "status": artifact.Status},
		CreatedAt: when, RequestID: artifact.RequestID, TraceID: artifact.TraceID}
}

func settleRunSteering(artifact types.TaskArtifact, reason string) (types.TaskArtifact, error) {
	item, err := RunSteeringFromArtifact(artifact)
	if err != nil {
		return types.TaskArtifact{}, err
	}
	item.Status, item.Reason = "not_applied", reason
	return steeringArtifact(item, artifact.TaskID, artifact.RunID, artifact.RequestID, artifact.TraceID), nil
}

func (s *MemoryStore) SubmitRunSteering(_ context.Context, in RunSteeringSubmission) (RunSteeringResult, error) {
	if err := ValidateRunSteeringSubmission(in); err != nil {
		return RunSteeringResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[in.TaskID]
	if !ok {
		return RunSteeringResult{}, ErrTaskNotFound
	}
	run, ok := s.runs[in.RunID]
	if !ok || run.TaskID != task.ID {
		return RunSteeringResult{}, ErrSteeringUnavailable
	}
	artifacts := s.listArtifactsLocked(ArtifactFilter{TaskID: task.ID, RunID: run.ID, Kind: RunSteeringArtifactKind})
	artifact, replay, err := prepareRunSteering(task, run, artifacts, in)
	if err != nil {
		return RunSteeringResult{}, err
	}
	item, err := RunSteeringFromArtifact(artifact)
	if err != nil {
		return RunSteeringResult{}, err
	}
	if !replay {
		s.artifacts[artifact.ID] = artifact
		s.appendRunEventLocked(steeringEvent(artifact, runtimeevents.EventRunSteeringSubmitted, artifact.CreatedAt))
		s.signalRun(run.ID)
	}
	return RunSteeringResult{RunSteering: item, Replay: replay}, nil
}

func (s *MemoryStore) ApplyRunSteering(_ context.Context, in RunSteeringApplication) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[in.RunID]
	if !ok || run.TaskID != in.TaskID {
		return false, ErrSteeringUnavailable
	}
	artifacts := s.listArtifactsLocked(ArtifactFilter{TaskID: in.TaskID, RunID: in.RunID, Kind: RunSteeringArtifactKind})
	var current *types.TaskArtifact
	if artifact, found := s.artifacts[in.Conversation.ID]; found {
		current = &artifact
	}
	updates, err := prepareAppliedRunSteering(run, artifacts, current, in)
	if err != nil {
		return false, err
	}
	s.artifacts[in.Conversation.ID] = in.Conversation
	for _, artifact := range updates {
		s.artifacts[artifact.ID] = artifact
		s.appendRunEventLocked(steeringEvent(artifact, runtimeevents.EventRunSteeringApplied, in.AppliedAt))
	}
	s.signalRun(run.ID)
	return true, nil
}

func PendingRunSteering(artifacts []types.TaskArtifact) ([]RunSteering, error) {
	items := make([]RunSteering, 0)
	for _, artifact := range artifacts {
		if artifact.Kind != RunSteeringArtifactKind || artifact.Status != "pending" {
			continue
		}
		item, err := RunSteeringFromArtifact(artifact)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Sequence < items[j].Sequence })
	return items, nil
}
