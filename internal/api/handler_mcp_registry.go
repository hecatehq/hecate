package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	mcpregistry "github.com/hecatehq/hecate/internal/mcp/registry"
)

const (
	mcpRegistryDefaultLimit = 30
	mcpRegistryMaxLimit     = 100
	mcpRegistryTimeout      = 10 * time.Second
)

func (h *Handler) HandleMCPRegistryServers(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "MCP registry discovery") {
		return
	}

	baseURL, query, err := parseMCPRegistryServersQuery(r)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	client, err := mcpregistry.NewClient(baseURL, &http.Client{Timeout: mcpRegistryTimeout})
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), mcpRegistryTimeout)
	defer cancel()

	list, err := client.ListServers(ctx, query)
	if err != nil {
		WriteError(w, http.StatusBadGateway, errCodeGatewayError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, MCPRegistryServersResponse{
		Object: "mcp_registry_servers",
		Data:   renderMCPRegistryServers(client.BaseURL(), list),
	})
}

func parseMCPRegistryServersQuery(r *http.Request) (string, mcpregistry.ListServersQuery, error) {
	values := r.URL.Query()
	limit := mcpRegistryDefaultLimit
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return "", mcpregistry.ListServersQuery{}, fmt.Errorf("limit query parameter must be a positive integer")
		}
		if parsed > mcpRegistryMaxLimit {
			parsed = mcpRegistryMaxLimit
		}
		limit = parsed
	}
	includeDeleted := false
	if raw := strings.TrimSpace(values.Get("include_deleted")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return "", mcpregistry.ListServersQuery{}, fmt.Errorf("include_deleted query parameter must be a boolean")
		}
		includeDeleted = parsed
	}
	updatedSince := strings.TrimSpace(values.Get("updated_since"))
	if updatedSince != "" {
		if _, err := time.Parse(time.RFC3339Nano, updatedSince); err != nil {
			return "", mcpregistry.ListServersQuery{}, fmt.Errorf("updated_since query parameter must be RFC3339")
		}
	}
	return strings.TrimSpace(values.Get("registry_url")), mcpregistry.ListServersQuery{
		Cursor:         strings.TrimSpace(values.Get("cursor")),
		Limit:          limit,
		Search:         strings.TrimSpace(values.Get("search")),
		UpdatedSince:   updatedSince,
		Version:        strings.TrimSpace(values.Get("version")),
		IncludeDeleted: includeDeleted,
	}, nil
}
