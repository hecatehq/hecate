package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/taskapp"
	"github.com/hecatehq/hecate/internal/taskstate"
)

// HandleSystemResetData removes local operator state without restarting the
// gateway. Chat sessions are deleted through the normal chat path so live
// external-agent sessions are closed before their rows disappear.
func (h *Handler) HandleSystemResetData(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "system reset") {
		return
	}
	stats, err := h.resetSystemData(r.Context())
	if errors.Is(err, errChatSessionDeleteConflict) || errors.Is(err, errSystemResetConflict) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, SystemResetDataResponse{Object: "system_reset", Data: stats})
}

var errSystemResetConflict = errors.New("system reset conflict")

func (h *Handler) resetSystemData(ctx context.Context) (SystemResetDataResponseItem, error) {
	var stats SystemResetDataResponseItem

	chatDeleted, err := h.resetChatSessions(ctx)
	if err != nil {
		return stats, err
	}
	stats.ChatSessionsDeleted = chatDeleted

	projectsDeleted, err := h.resetProjects(ctx)
	if err != nil {
		return stats, err
	}
	stats.ProjectsDeleted = projectsDeleted

	projectWorkRowsDeleted, err := h.resetProjectWork(ctx)
	if err != nil {
		return stats, err
	}
	stats.ProjectWorkRowsDeleted = projectWorkRowsDeleted

	projectRuntimeRowsDeleted, err := h.resetProjectRuntime(ctx)
	if err != nil {
		return stats, err
	}
	stats.ProjectRuntimeRowsDeleted = projectRuntimeRowsDeleted

	projectSkillsDeleted, err := h.resetProjectSkills(ctx)
	if err != nil {
		return stats, err
	}
	stats.ProjectSkillsDeleted = projectSkillsDeleted

	projectAssistantProposalsDeleted, err := h.resetProjectAssistantProposals(ctx)
	if err != nil {
		return stats, err
	}
	stats.ProjectAssistantProposalsDeleted = projectAssistantProposalsDeleted

	pluginsDeleted, err := h.resetPlugins(ctx)
	if err != nil {
		return stats, err
	}
	stats.PluginsDeleted = pluginsDeleted

	agentProfilesDeleted, err := h.resetAgentProfiles(ctx)
	if err != nil {
		return stats, err
	}
	stats.AgentProfilesDeleted = agentProfilesDeleted

	tasksDeleted, err := h.resetTasks(ctx)
	if err != nil {
		return stats, err
	}
	stats.TasksDeleted = tasksDeleted

	grantsDeleted, err := h.resetAgentApprovalGrants(ctx)
	if err != nil {
		return stats, err
	}
	stats.AgentApprovalGrantsDeleted = grantsDeleted

	settingsStats, err := h.resetSettingsState(ctx)
	if err != nil {
		return stats, err
	}
	stats.ProvidersDeleted = settingsStats.ProvidersDeleted
	stats.PolicyRulesDeleted = settingsStats.PolicyRulesDeleted

	rowsDeleted, err := h.clearRemainingState(ctx)
	if err != nil {
		return stats, err
	}
	stats.DatabaseRowsDeleted = rowsDeleted

	cairnlineMirrorDeleted, err := h.resetCairnlineMirrorDatabase()
	if err != nil {
		return stats, err
	}
	stats.CairnlineMirrorFilesDeleted = cairnlineMirrorDeleted

	return stats, nil
}

func (h *Handler) resetChatSessions(ctx context.Context) (int, error) {
	if h.agentChat == nil {
		return 0, nil
	}
	sessions, err := h.agentChat.List(ctx)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, session := range sessions {
		stopping, err := h.deleteExistingChatSession(ctx, session)
		if err != nil {
			return deleted, err
		}
		if stopping {
			return deleted, fmt.Errorf("%w: chat session %q is still stopping", errChatSessionDeleteConflict, session.ID)
		}
		deleted++
	}
	return deleted, nil
}

func (h *Handler) resetProjects(ctx context.Context) (int, error) {
	if h.projects == nil {
		return 0, nil
	}
	items, err := h.projects.List(ctx)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, item := range items {
		if h.memory != nil {
			if _, err := h.memory.DeleteByProjectID(ctx, item.ID); err != nil {
				return deleted, err
			}
		}
		if h.memoryCandidates != nil {
			if _, err := h.memoryCandidates.DeleteCandidatesByProjectID(ctx, item.ID); err != nil {
				return deleted, err
			}
		}
		if err := h.projects.Delete(ctx, item.ID); err != nil {
			if errors.Is(err, projects.ErrNotFound) {
				continue
			}
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func (h *Handler) resetProjectWork(ctx context.Context) (int, error) {
	if h.projectWork == nil {
		return 0, nil
	}
	return h.projectWork.Clear(ctx)
}

func (h *Handler) resetProjectRuntime(ctx context.Context) (int, error) {
	if h.projectRuntime == nil {
		return 0, nil
	}
	return h.projectRuntime.Clear(ctx)
}

func (h *Handler) resetProjectSkills(ctx context.Context) (int, error) {
	if h.projectSkills == nil {
		return 0, nil
	}
	return h.projectSkills.Clear(ctx)
}

func (h *Handler) resetProjectAssistantProposals(ctx context.Context) (int, error) {
	if h.projectAssistantProposals == nil {
		return 0, nil
	}
	return h.projectAssistantProposals.Clear(ctx)
}

func (h *Handler) resetPlugins(ctx context.Context) (int, error) {
	if h.pluginRegistry == nil {
		return 0, nil
	}
	return h.pluginRegistry.Clear(ctx)
}

func (h *Handler) resetAgentProfiles(ctx context.Context) (int, error) {
	if h.agentProfiles == nil {
		return 0, nil
	}
	items, err := h.agentProfiles.List(ctx)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, item := range items {
		if item.BuiltIn {
			continue
		}
		if err := h.agentProfiles.Delete(ctx, item.ID); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func (h *Handler) resetTasks(ctx context.Context) (int, error) {
	if h.taskStore == nil {
		return 0, nil
	}
	tasks, err := h.taskStore.ListTasks(ctx, taskstate.TaskFilter{})
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, task := range tasks {
		active, err := taskapp.HasActiveRun(ctx, h.taskStore, task)
		if err != nil {
			return deleted, err
		}
		if active {
			return deleted, fmt.Errorf("%w: task %q has an active run; cancel it first", errSystemResetConflict, task.ID)
		}
		if err := h.taskStore.DeleteTask(ctx, task.ID); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func (h *Handler) resetCairnlineMirrorDatabase() (int, error) {
	if h == nil {
		return 0, nil
	}
	dbPath := h.cairnlineEmbeddedDatabasePath()
	deleted := 0
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Remove(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func (h *Handler) resetAgentApprovalGrants(ctx context.Context) (int, error) {
	coord := h.agentApprovalCoordinator()
	if coord == nil {
		return 0, nil
	}
	grants, err := coord.ListGrants(ctx, agentadapters.GrantFilter{})
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, grant := range grants {
		if err := coord.DeleteGrant(ctx, grant.ID); err != nil {
			if errors.Is(err, agentadapters.ErrApprovalNotFound) {
				continue
			}
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

type resetSettingsStats struct {
	ProvidersDeleted   int
	PolicyRulesDeleted int
}

func (h *Handler) resetSettingsState(ctx context.Context) (resetSettingsStats, error) {
	var stats resetSettingsStats
	if h.controlPlane == nil {
		return stats, nil
	}
	state, err := h.controlPlane.Snapshot(ctx)
	if err != nil {
		return stats, err
	}
	for _, provider := range state.Providers {
		if err := h.deleteSettingsProvider(ctx, provider.ID); err != nil {
			return stats, err
		}
		stats.ProvidersDeleted++
	}
	for _, rule := range state.PolicyRules {
		if err := h.controlPlane.DeletePolicyRule(ctx, rule.ID); err != nil {
			return stats, err
		}
		stats.PolicyRulesDeleted++
	}
	if h.providerRuntime != nil && stats.ProvidersDeleted > 0 {
		if err := h.providerRuntime.Reload(ctx); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

func (h *Handler) deleteSettingsProvider(ctx context.Context, id string) error {
	if h.providerRuntime != nil {
		return h.providerRuntime.Delete(ctx, id)
	}
	return h.controlPlane.DeleteProvider(ctx, id)
}

func (h *Handler) clearRemainingState(ctx context.Context) (int, error) {
	if h.stateCleaner == nil {
		return 0, nil
	}
	return h.stateCleaner.ClearData(ctx)
}
