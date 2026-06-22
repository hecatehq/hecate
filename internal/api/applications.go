package api

import (
	"github.com/hecatehq/hecate/internal/chatapp"
	"github.com/hecatehq/hecate/internal/modelapp"
	"github.com/hecatehq/hecate/internal/pluginregistryapp"
	"github.com/hecatehq/hecate/internal/projectapp"
	"github.com/hecatehq/hecate/internal/projectassistantapp"
	"github.com/hecatehq/hecate/internal/projectworkapp"
	"github.com/hecatehq/hecate/internal/providerapp"
	"github.com/hecatehq/hecate/internal/taskapp"
)

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
		Projects:      h.projects,
		SecretCipher:  h.secretCipher,
		MaxMCPServers: h.config.Server.TaskMaxMCPServersPerTask,
		IDGenerator:   newOpaqueTaskResourceID,
	})
}

func (h *Handler) chatApplication() *chatapp.Application {
	if h == nil {
		return chatapp.New(chatapp.Options{})
	}
	return chatapp.New(chatapp.Options{
		Store:               h.agentChat,
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
		Projects:                  h.projects,
		Chats:                     h.agentChat,
		DeleteChat:                h.deleteProjectChatSession,
		ProjectWork:               h.projectWork,
		ProjectSkills:             h.projectSkills,
		ProjectAssistantProposals: h.projectAssistantProposals,
		Memory:                    h.memory,
		MemoryCandidates:          h.memoryCandidates,
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
			Projects:         h.projects,
			Chats:            h.agentChat,
			Work:             h.projectWork,
			WorkApplication:  h.projectWorkApplication(),
			ProjectSkills:    h.projectSkills,
			Memory:           h.memory,
			MemoryCandidates: h.memoryCandidates,
			Proposals:        h.projectAssistantProposals,
			LLM:              gatewayAgentLLMClient{service: h.service},
			IDGenerator:      newOpaqueTaskResourceID,
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
