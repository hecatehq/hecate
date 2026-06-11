package api

import (
	"github.com/hecatehq/hecate/internal/chatapp"
	"github.com/hecatehq/hecate/internal/modelapp"
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

func (h *Handler) projectWorkApplication() *projectworkapp.Application {
	if h == nil {
		return projectworkapp.New(projectworkapp.Options{})
	}
	return projectworkapp.New(projectworkapp.Options{
		Store:          h.projectWork,
		TaskStore:      h.taskStore,
		Runner:         h.taskRunner,
		ChatStore:      h.agentChat,
		AgentRunner:    h.agentChatRunner,
		PrepareTimeout: agentChatPrepareTimeout,
		IDGenerator:    newOpaqueTaskResourceID,
	})
}

func (h *Handler) modelApplication() *modelapp.Application {
	if h == nil {
		return modelapp.New(modelapp.Options{})
	}
	return modelapp.New(modelapp.Options{Service: h.service})
}
