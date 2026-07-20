package api

import (
	"context"
	"fmt"

	"github.com/hecatehq/hecate/internal/chatapp"
	"github.com/hecatehq/hecate/internal/dictationapp"
	"github.com/hecatehq/hecate/internal/modelapp"
	"github.com/hecatehq/hecate/internal/pluginregistryapp"
	"github.com/hecatehq/hecate/internal/projectapp"
	"github.com/hecatehq/hecate/internal/projectassistantapp"
	"github.com/hecatehq/hecate/internal/projectworkapp"
	"github.com/hecatehq/hecate/internal/providerapp"
	"github.com/hecatehq/hecate/internal/taskapp"
	"github.com/hecatehq/hecate/internal/taskruncoord"
	"github.com/hecatehq/hecate/internal/taskschedule"
	"github.com/hecatehq/hecate/internal/taskstate"
)

func (h *Handler) dictationApplication() *dictationapp.Application {
	if h == nil {
		return dictationapp.New(dictationapp.Options{})
	}
	return dictationapp.New(dictationapp.Options{Registry: h.dictationRegistry})
}

func (h *Handler) taskApplication() *taskapp.Application {
	if h == nil {
		return taskapp.New(taskapp.Options{})
	}
	var runner taskapp.Runner
	if h.taskRunner != nil {
		runner = h.taskRunner
	}
	return taskapp.New(taskapp.Options{
		Store:         h.taskStore,
		Runner:        runner,
		Projects:      h.taskProjectStore(),
		SecretCipher:  h.secretCipher,
		MaxMCPServers: h.config.Server.TaskMaxMCPServersPerTask,
		IDGenerator:   newOpaqueTaskResourceID,
		OriginRunGate: h.taskRunOriginGate(),
	})
}

func (h *Handler) taskscheduleApplication() *taskschedule.Application {
	if h == nil {
		return taskschedule.New(taskschedule.Options{})
	}
	scheduleStore, _ := h.taskStore.(taskstate.ScheduleStore)
	return taskschedule.New(taskschedule.Options{
		Store:       scheduleStore,
		Tasks:       h.taskStore,
		IDGenerator: newOpaqueTaskResourceID,
	})
}

func (h *Handler) taskRunOriginGate() *taskruncoord.Gate {
	if h == nil {
		return taskruncoord.NewOriginGate()
	}
	h.taskOriginRunGateMu.Lock()
	defer h.taskOriginRunGateMu.Unlock()
	if h.taskOriginRunGate == nil {
		h.taskOriginRunGate = taskruncoord.NewOriginGate()
		h.taskOriginRunGate.SetValidator("chat", h.validateTaskRunOrigin)
		if h.taskRunner != nil {
			h.taskRunner.SetOriginRunGate(h.taskOriginRunGate)
		}
	}
	return h.taskOriginRunGate
}

func (h *Handler) validateTaskRunOrigin(ctx context.Context, origin taskruncoord.Origin) error {
	if origin.Kind != "chat" {
		return nil
	}
	if h == nil || h.agentChat == nil {
		return chatapp.ErrStoreNotConfigured
	}
	_, found, err := h.agentChat.Get(ctx, origin.ID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: %w", taskruncoord.ErrOriginNotFound, chatapp.ErrSessionNotFound)
	}
	return nil
}

func (h *Handler) chatApplication() *chatapp.Application {
	if h == nil {
		return chatapp.New(chatapp.Options{})
	}
	return chatapp.New(chatapp.Options{
		Store:               h.agentChat,
		Messages:            h.agentChat,
		Attachments:         h.chatAttachments,
		TaskStore:           h.taskStore,
		Runner:              h.agentChatRunner,
		PrepareTimeout:      agentChatPrepareTimeout,
		ConfigOptionTimeout: agentChatConfigOptionTimeout,
	})
}

func (h *Handler) providerApplication() *providerapp.Application {
	if h == nil {
		return providerapp.New(providerapp.Options{})
	}
	return providerapp.New(providerapp.Options{
		ControlPlane: h.controlPlane,
		Runtime:      h.providerRuntime,
		Config:       h.config,
	})
}

func (h *Handler) projectApplication() *projectapp.Application {
	if h == nil {
		return projectapp.New(projectapp.Options{})
	}
	return projectapp.New(projectapp.Options{
		Projects:                     h.projects,
		Chats:                        h.agentChat,
		DeleteChat:                   h.deleteProjectChatSession,
		SweepOrphanedChatAttachments: h.chatApplication().SweepOrphanedAttachments,
		ProjectWork:                  h.projectWork,
		ProjectRuntime:               h.projectRuntime,
		ProjectSkills:                h.projectSkills,
		ProjectAssistantProposals:    h.projectAssistantProposals,
		Memory:                       h.memory,
		MemoryCandidates:             h.memoryCandidates,
	})
}

func (h *Handler) projectWorkApplication() *projectworkapp.Application {
	if h == nil {
		return projectworkapp.New(projectworkapp.Options{})
	}
	return projectworkapp.New(projectworkapp.Options{
		Store:               h.projectWork,
		TaskStore:           h.taskStore,
		Runner:              h.taskRunner,
		ChatStore:           h.agentChat,
		AgentRunner:         h.agentChatRunner,
		ProfileStore:        h.agentProfiles,
		MemoryStore:         h.memory,
		SkillStore:          h.projectSkills,
		RuntimeStore:        h.projectRuntime,
		PrepareTimeout:      agentChatPrepareTimeout,
		RuntimeDefaultModel: h.config.Router.DefaultModel,
		IDGenerator:         newOpaqueTaskResourceID,
	})
}

func (h *Handler) projectAssistantApplication() *projectassistantapp.Application {
	if h == nil {
		return projectassistantapp.New(projectassistantapp.Options{})
	}
	h.projectAssistantMu.Lock()
	defer h.projectAssistantMu.Unlock()
	if h.projectAssistant == nil {
		h.projectAssistant = projectassistantapp.New(projectassistantapp.Options{
			Projects:                 h.projects,
			ProjectAuthority:         h.projectAssistantProjectAuthorityForApplication(),
			Chats:                    h.agentChat,
			Work:                     h.projectAssistantWorkStoreForApplication(),
			WorkAuthority:            h.projectAssistantWorkAuthorityForApplication(),
			WorkApplication:          h.projectWorkApplication(),
			ProjectSkills:            h.projectSkills,
			Memory:                   h.memory,
			MemoryCandidates:         h.memoryCandidates,
			MemoryCandidateAuthority: h.projectAssistantMemoryCandidateAuthorityForApplication(),
			Proposals:                h.projectAssistantProposalStoreForApplication(),
			LLM:                      gatewayAgentLLMClient{service: h.service},
			IDGenerator:              newOpaqueTaskResourceID,
		})
	}
	return h.projectAssistant
}

func (h *Handler) pluginRegistryApplication() *pluginregistryapp.Application {
	if h == nil {
		return pluginregistryapp.New(pluginregistryapp.Options{})
	}
	return pluginregistryapp.New(pluginregistryapp.Options{Store: h.pluginRegistry})
}

func (h *Handler) modelApplication() *modelapp.Application {
	if h == nil {
		return modelapp.New(modelapp.Options{})
	}
	return modelapp.New(modelapp.Options{Service: h.service})
}
