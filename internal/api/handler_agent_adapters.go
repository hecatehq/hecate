package api

import (
	"net/http"

	"github.com/hecate/agent-runtime/internal/agentadapters"
)

func (h *Handler) HandleAgentAdapters(w http.ResponseWriter, r *http.Request) {
	items := agentadapters.List(r.Context())
	data := make([]AgentAdapterResponseItem, 0, len(items))
	for _, item := range items {
		data = append(data, AgentAdapterResponseItem{
			ID:                  item.ID,
			Name:                item.Name,
			Kind:                item.Kind,
			Command:             item.Command,
			Args:                item.Args,
			Managed:             item.Managed.Package != "",
			ManagedPackage:      item.Managed.Package,
			Available:           item.Available,
			Status:              item.Status,
			Path:                item.Path,
			Error:               item.Error,
			Description:         item.Description,
			CostMode:            item.CostMode,
			DocsURL:             item.DocsURL,
			Version:             item.Version,
			SupportedRange:      item.SupportedRange,
			VersionOutsideRange: item.VersionOutsideRange,
		})
	}

	WriteJSON(w, http.StatusOK, AgentAdapterResponse{
		Object: "agent_adapters",
		Data:   data,
	})
}
