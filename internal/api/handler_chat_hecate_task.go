package api

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatcontext"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/pkg/types"
)

type hecateAgentTaskStore interface {
	CreateTask(ctx context.Context, task types.Task) (types.Task, error)
	GetTask(ctx context.Context, id string) (types.Task, bool, error)
	GetRun(ctx context.Context, taskID, runID string) (types.TaskRun, bool, error)
}

type hecateAgentTaskRunner interface {
	StartTaskWithRunInitializer(ctx context.Context, task types.Task, idgen func(string) string, init func(*types.TaskRun)) (*orchestrator.StartTaskResult, error)
	ContinueAgentTaskWithRunInitializer(ctx context.Context, task types.Task, run types.TaskRun, prompt string, idgen func(string) string, init func(*types.TaskRun)) (*orchestrator.StartTaskResult, error)
}

type hecateAgentTaskOrchestrator struct {
	store      hecateAgentTaskStore
	runner     hecateAgentTaskRunner
	taskID     func() string
	resourceID func(string) string
	now        func() time.Time
}

type hecateAgentTaskRunCommand struct {
	Session               chat.Session
	Prompt                string
	InputRef              string
	InputProviderInstance types.ProviderInstanceIdentity
	SystemPrompt          string
	ForceNewTask          bool
	MCPServers            []types.MCPServerConfig
	ContextPacket         chat.ContextPacket
}

func (h *Handler) hecateAgentTaskOrchestrator() hecateAgentTaskOrchestrator {
	return hecateAgentTaskOrchestrator{
		store:      h.taskStore,
		runner:     h.taskRunner,
		taskID:     newTaskID,
		resourceID: newOpaqueTaskResourceID,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func (o hecateAgentTaskOrchestrator) StartOrContinue(ctx context.Context, cmd hecateAgentTaskRunCommand) (types.Task, types.TaskRun, error) {
	if o.store == nil || o.runner == nil {
		return types.Task{}, types.TaskRun{}, fmt.Errorf("task runtime is not configured")
	}
	if o.resourceID == nil {
		o.resourceID = newOpaqueTaskResourceID
	}
	if o.taskID == nil {
		o.taskID = newTaskID
	}
	if o.now == nil {
		o.now = func() time.Time { return time.Now().UTC() }
	}
	if cmd.Session.TaskID == "" || cmd.ForceNewTask {
		return o.startNewTask(ctx, cmd)
	}
	return o.continueTask(ctx, cmd)
}

func (o hecateAgentTaskOrchestrator) startNewTask(ctx context.Context, cmd hecateAgentTaskRunCommand) (types.Task, types.TaskRun, error) {
	now := o.now()
	title := strings.TrimSpace(cmd.Session.Title)
	if title == "" {
		title = "Hecate Chat"
	}
	workspaceMode := chat.EffectiveWorkspaceMode(cmd.Session.WorkspaceMode)
	reuseWorkspace := cmd.ForceNewTask && strings.TrimSpace(cmd.Session.TaskID) != "" && workspaceMode != chat.WorkspaceModeInPlace
	if reuseWorkspace {
		if strings.TrimSpace(cmd.Session.LatestRunID) == "" {
			return types.Task{}, types.TaskRun{}, fmt.Errorf("managed chat workspace cannot be reused without a latest run")
		}
		priorRun, found, err := o.store.GetRun(ctx, cmd.Session.TaskID, cmd.Session.LatestRunID)
		if err != nil {
			return types.Task{}, types.TaskRun{}, err
		}
		if !found {
			return types.Task{}, types.TaskRun{}, fmt.Errorf("latest task run %q not found", cmd.Session.LatestRunID)
		}
		priorWorkspace := strings.TrimSpace(priorRun.WorkspacePath)
		currentWorkspace := strings.TrimSpace(cmd.Session.Workspace)
		if priorWorkspace == "" || currentWorkspace == "" || filepath.Clean(priorWorkspace) != filepath.Clean(currentWorkspace) {
			return types.Task{}, types.TaskRun{}, fmt.Errorf("managed chat workspace does not match the latest task run")
		}
	}
	task := types.Task{
		ID:                 o.taskID(),
		Title:              title,
		Prompt:             cmd.Prompt,
		ProjectID:          cmd.Session.ProjectID,
		SystemPrompt:       strings.TrimSpace(cmd.SystemPrompt),
		ExecutionKind:      "agent_loop",
		ExecutionProfile:   hecateChatExecutionProfile(cmd.Session),
		OriginKind:         "chat",
		OriginID:           cmd.Session.ID,
		WorkspaceMode:      workspaceMode,
		WorkspaceReuse:     reuseWorkspace,
		WorkingDirectory:   cmd.Session.Workspace,
		SandboxAllowedRoot: cmd.Session.Workspace,
		RTKEnabled:         cmd.Session.RTKEnabled,
		Status:             types.TaskStatusNotStarted,
		Priority:           "normal",
		RequestedProvider:  cmd.Session.Provider,
		RequestedModel:     cmd.Session.Model,
		MCPServers:         append([]types.MCPServerConfig(nil), cmd.MCPServers...),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if preset := cmd.Session.AgentPreset; !preset.Empty() {
		toolsEnabled := preset.ToolsEnabled
		task.AgentPresetID = preset.ID
		task.AgentPresetToolsEnabled = &toolsEnabled
		task.SandboxReadOnly = !preset.WritesAllowed
		task.SandboxNetwork = preset.NetworkAllowed
	}
	task, err := o.store.CreateTask(ctx, task)
	if err != nil {
		return types.Task{}, types.TaskRun{}, err
	}
	result, err := o.runner.StartTaskWithRunInitializer(ctx, task, o.resourceID, func(run *types.TaskRun) {
		run.InputRef = strings.TrimSpace(cmd.InputRef)
		run.InputProviderDispatchRecorded = false
		if run.InputRef != "" {
			run.InputProviderInstance = cmd.InputProviderInstance
		}
		packet := cmd.ContextPacket
		packet.Workspace = run.WorkspacePath
		run.ContextPacket = chatcontext.Marshal(chatcontext.Normalize(packet, chatcontext.MergeRefs(
			chatcontext.ChatMessageRefs(cmd.Session.ID, "", "", cmd.Session.ProjectID),
			chatcontext.TaskRunRefs(task.ID, run.ID, cmd.Session.ProjectID),
		)))
	})
	if err != nil {
		return types.Task{}, types.TaskRun{}, err
	}
	return result.Task, result.Run, nil
}

func hecateChatExecutionProfile(session chat.Session) string {
	if !session.AgentPreset.Empty() && strings.TrimSpace(session.AgentPreset.ExecutionProfile) != "" {
		return strings.TrimSpace(session.AgentPreset.ExecutionProfile)
	}
	return "chat_agent"
}

func (o hecateAgentTaskOrchestrator) continueTask(ctx context.Context, cmd hecateAgentTaskRunCommand) (types.Task, types.TaskRun, error) {
	task, found, err := o.store.GetTask(ctx, cmd.Session.TaskID)
	if err != nil {
		return types.Task{}, types.TaskRun{}, err
	}
	if !found {
		return types.Task{}, types.TaskRun{}, fmt.Errorf("backing task %q not found", cmd.Session.TaskID)
	}
	run, found, err := o.store.GetRun(ctx, task.ID, cmd.Session.LatestRunID)
	if err != nil {
		return types.Task{}, types.TaskRun{}, err
	}
	if !found {
		return types.Task{}, types.TaskRun{}, fmt.Errorf("latest task run %q not found", cmd.Session.LatestRunID)
	}
	result, err := o.runner.ContinueAgentTaskWithRunInitializer(ctx, task, run, cmd.Prompt, o.resourceID, func(nextRun *types.TaskRun) {
		nextRun.InputRef = strings.TrimSpace(cmd.InputRef)
		nextRun.InputProviderDispatchRecorded = false
		if nextRun.InputRef != "" {
			nextRun.InputProviderInstance = cmd.InputProviderInstance
		} else {
			nextRun.InputProviderInstance = types.ProviderInstanceIdentity{}
		}
		packet := cmd.ContextPacket
		packet.Workspace = nextRun.WorkspacePath
		nextRun.ContextPacket = chatcontext.Marshal(chatcontext.Normalize(packet, chatcontext.MergeRefs(
			chatcontext.ChatMessageRefs(cmd.Session.ID, "", "", cmd.Session.ProjectID),
			chatcontext.TaskRunRefs(task.ID, nextRun.ID, cmd.Session.ProjectID),
		)))
	})
	if err != nil {
		return types.Task{}, types.TaskRun{}, err
	}
	return result.Task, result.Run, nil
}
