package projectworkapp

import (
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/projectwork"
)

type ActivityLinkedChat struct {
	ID           string
	Status       string
	LatestRole   string
	LatestStatus string
	LatestError  string
	MessageCount int
	Missing      bool
}

type ActivityAssignmentInput struct {
	Status        string
	TaskID        string
	RunID         string
	ChatSessionID string
	Execution     *AssignmentExecutionSummary
	ExecutionRef  *AssignmentExecutionRef
	LinkedChat    *ActivityLinkedChat
	ArtifactCount int
}

type ActivityAssignmentState struct {
	Status         string
	BlockingSignal string
	StatusSummary  string
	LinkedTaskID   string
	LinkedRunID    string
	LinkedChatID   string
	Bucket         string
}

func ProjectActivityAssignmentState(input ActivityAssignmentInput) ActivityAssignmentState {
	status := strings.TrimSpace(ProjectActivityProjectedStatus(input))
	if status == "" {
		status = "unknown"
	}
	signal := ProjectActivityBlockingSignal(input)
	return ActivityAssignmentState{
		Status:         status,
		BlockingSignal: signal,
		StatusSummary:  ProjectActivityStatusSummary(input, signal),
		LinkedTaskID:   activityExecutionTaskID(input),
		LinkedRunID:    activityExecutionRunID(input),
		LinkedChatID:   activityExecutionChatID(input),
		Bucket:         ProjectActivityBucket(signal),
	}
}

func ProjectActivityBucket(signal string) string {
	switch strings.TrimSpace(signal) {
	case "awaiting_approval", "failed", "cancelled", "not_started", "stale_unknown":
		return "blocked"
	case "completed":
		return "completed"
	case "running":
		return "active"
	default:
		return "active"
	}
}

func ProjectActivityBlockingSignal(input ActivityAssignmentInput) string {
	if input.LinkedChat != nil && input.LinkedChat.Missing {
		return "stale_unknown"
	}
	status := strings.TrimSpace(ProjectActivityProjectedStatus(input))
	switch status {
	case projectwork.AssignmentStatusAwaitingApproval:
		return "awaiting_approval"
	case projectwork.AssignmentStatusFailed:
		return "failed"
	case projectwork.AssignmentStatusCancelled, "closed":
		return "cancelled"
	case projectwork.AssignmentStatusCompleted:
		return "completed"
	case projectwork.AssignmentStatusRunning, projectwork.AssignmentStatusQueued:
		if input.Execution == nil && strings.TrimSpace(input.TaskID) == "" && strings.TrimSpace(input.RunID) != "" {
			return "stale_unknown"
		}
		if input.Execution != nil && input.Execution.Missing {
			return "stale_unknown"
		}
		if status == projectwork.AssignmentStatusQueued &&
			strings.TrimSpace(input.TaskID) == "" &&
			strings.TrimSpace(input.RunID) == "" &&
			strings.TrimSpace(input.ChatSessionID) == "" {
			return "not_started"
		}
		return "running"
	default:
		if input.Execution != nil && input.Execution.Missing {
			return "stale_unknown"
		}
		return "stale_unknown"
	}
}

func ProjectActivityStatusSummary(input ActivityAssignmentInput, signal string) string {
	switch strings.TrimSpace(signal) {
	case "awaiting_approval":
		count := 0
		if input.Execution != nil {
			count = input.Execution.PendingApprovalCount
		}
		if input.ExecutionRef != nil && input.ExecutionRef.PendingApprovalCount > count {
			count = input.ExecutionRef.PendingApprovalCount
		}
		if count > 0 {
			return fmt.Sprintf("%d approval pending", count)
		}
		return "awaiting approval"
	case "failed":
		if input.LinkedChat != nil && input.LinkedChat.LatestError != "" {
			return input.LinkedChat.LatestError
		}
		if input.Execution != nil && input.Execution.LastError != "" {
			return input.Execution.LastError
		}
		if input.LinkedChat != nil {
			return "linked chat failed"
		}
		return "failed run"
	case "cancelled":
		if input.LinkedChat != nil {
			return "linked chat cancelled"
		}
		return "cancelled"
	case "not_started":
		return "not started"
	case "running":
		if input.LinkedChat != nil {
			return ProjectActivityLinkedChatSummary(input.LinkedChat)
		}
		return "running"
	case "completed":
		if input.ArtifactCount > 0 {
			return fmt.Sprintf("completed with %d artifact%s", input.ArtifactCount, pluralSuffix(input.ArtifactCount))
		}
		return "completed"
	default:
		if input.LinkedChat != nil && input.LinkedChat.Missing {
			return "linked chat missing"
		}
		return "stale or unknown"
	}
}

func ProjectActivityProjectedStatus(input ActivityAssignmentInput) string {
	if input.LinkedChat != nil {
		if input.LinkedChat.Missing {
			return "stale_unknown"
		}
		if status := strings.TrimSpace(input.LinkedChat.Status); status != "" && status != "idle" {
			return status
		}
		if status := strings.TrimSpace(input.LinkedChat.LatestStatus); status != "" {
			return status
		}
		if strings.TrimSpace(input.LinkedChat.Status) == "idle" {
			return firstNonEmpty(activityExecutionStatus(input), input.Status, projectwork.AssignmentStatusRunning)
		}
	}
	return firstNonEmpty(activityExecutionStatus(input), input.Status)
}

func ProjectActivityLinkedChatSummary(linkedChat *ActivityLinkedChat) string {
	if linkedChat == nil {
		return ""
	}
	parts := []string{"linked chat"}
	if linkedChat.Status != "" {
		parts = append(parts, linkedChat.Status)
	}
	if linkedChat.LatestRole != "" && linkedChat.LatestStatus != "" {
		parts = append(parts, linkedChat.LatestRole+" "+linkedChat.LatestStatus)
	}
	if linkedChat.MessageCount > 0 {
		parts = append(parts, fmt.Sprintf("%d message%s", linkedChat.MessageCount, pluralSuffix(linkedChat.MessageCount)))
	}
	return strings.Join(parts, " · ")
}

func activityExecutionStatus(input ActivityAssignmentInput) string {
	if input.ExecutionRef != nil {
		return input.ExecutionRef.Status
	}
	if input.Execution == nil {
		return ""
	}
	return input.Execution.Status
}

func activityExecutionTaskID(input ActivityAssignmentInput) string {
	if input.ExecutionRef == nil {
		return ""
	}
	return input.ExecutionRef.TaskID
}

func activityExecutionRunID(input ActivityAssignmentInput) string {
	if input.ExecutionRef == nil {
		return ""
	}
	return input.ExecutionRef.RunID
}

func activityExecutionChatID(input ActivityAssignmentInput) string {
	if input.ExecutionRef == nil {
		return ""
	}
	return input.ExecutionRef.ChatSessionID
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
