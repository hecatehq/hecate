package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/hecatehq/hecate/internal/mcp"
)

const (
	resourceMIMEJSON = "application/json"

	resourceRecentTasksURI  = "hecate://tasks/recent"
	resourceRecentTracesURI = "hecate://traces/recent"
)

// RegisterDefaultResources exposes read-only Hecate snapshots as MCP
// resources so editor clients can attach gateway state as context
// without asking the model to run a tool first.
func RegisterDefaultResources(s *Server, client *GatewayClient) {
	s.RegisterResource(mcp.Resource{
		URI:         resourceRecentTasksURI,
		Name:        "recent_tasks",
		Title:       "Recent Hecate tasks",
		Description: "Recent task records with id, title, prompt, status, execution kind, latest-Run step count, latest Run id, and creation time.",
		MIMEType:    resourceMIMEJSON,
	}, recentTasksResourceHandler(client))

	s.RegisterResource(mcp.Resource{
		URI:         resourceRecentTracesURI,
		Name:        "recent_traces",
		Title:       "Recent Hecate traces",
		Description: "Recent gateway trace summaries with request id, trace id, status, latency, and route metadata.",
		MIMEType:    resourceMIMEJSON,
	}, recentTracesResourceHandler(client))

	s.RegisterResourceTemplate(mcp.ResourceTemplate{
		URITemplate: "hecate://tasks/{task_id}",
		Name:        "task_detail",
		Title:       "Hecate task detail",
		Description: "Detailed status for one Hecate task by task id.",
		MIMEType:    resourceMIMEJSON,
	}, taskDetailResourceHandler(client))

	s.RegisterResourceTemplate(mcp.ResourceTemplate{
		URITemplate: "hecate://traces/{request_id}",
		Name:        "trace_detail",
		Title:       "Hecate trace detail",
		Description: "Detailed trace spans and route metadata for one Hecate request id.",
		MIMEType:    resourceMIMEJSON,
	}, traceDetailResourceHandler(client))
}

func recentTasksResourceHandler(client *GatewayClient) ResourceHandler {
	return func(ctx context.Context, uri string) (mcp.ReadResourceResult, error) {
		q := url.Values{}
		q.Set("limit", "30")
		var resp listTasksResponse
		if err := client.Get(ctx, "/hecate/v1/tasks", q, &resp); err != nil {
			return mcp.ReadResourceResult{}, err
		}
		return jsonResource(uri, resp)
	}
}

func recentTracesResourceHandler(client *GatewayClient) ResourceHandler {
	return func(ctx context.Context, uri string) (mcp.ReadResourceResult, error) {
		q := url.Values{}
		q.Set("limit", "100")
		var resp traceListResponse
		if err := client.Get(ctx, "/hecate/v1/traces", q, &resp); err != nil {
			return mcp.ReadResourceResult{}, err
		}
		return jsonResource(uri, resp)
	}
}

func taskDetailResourceHandler(client *GatewayClient) ResourceHandler {
	return func(ctx context.Context, uri string) (mcp.ReadResourceResult, error) {
		taskID, ok := resourceID(uri, "tasks")
		if !ok || taskID == "recent" {
			return mcp.ReadResourceResult{}, errResourceNoMatch
		}
		var resp getTaskStatusResponse
		if err := client.Get(ctx, "/hecate/v1/tasks/"+url.PathEscape(taskID), nil, &resp); err != nil {
			return mcp.ReadResourceResult{}, err
		}
		return jsonResource(uri, resp)
	}
}

func traceDetailResourceHandler(client *GatewayClient) ResourceHandler {
	return func(ctx context.Context, uri string) (mcp.ReadResourceResult, error) {
		requestID, ok := resourceID(uri, "traces")
		if !ok || requestID == "recent" {
			return mcp.ReadResourceResult{}, errResourceNoMatch
		}
		q := url.Values{}
		q.Set("request_id", requestID)
		var resp traceDetailResponse
		if err := client.Get(ctx, "/hecate/v1/traces", q, &resp); err != nil {
			return mcp.ReadResourceResult{}, err
		}
		return jsonResource(uri, resp)
	}
}

func resourceID(rawURI, host string) (string, bool) {
	u, err := url.Parse(rawURI)
	if err != nil || u.Scheme != "hecate" || u.Host != host {
		return "", false
	}
	id := strings.TrimPrefix(u.EscapedPath(), "/")
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	unescaped, err := url.PathUnescape(id)
	if err != nil {
		return "", false
	}
	return unescaped, true
}

func jsonResource(uri string, v any) (mcp.ReadResourceResult, error) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.ReadResourceResult{}, fmt.Errorf("marshal resource %s: %w", uri, err)
	}
	return mcp.ReadResourceResult{
		Contents: []mcp.ResourceContents{{
			URI:      uri,
			MIMEType: resourceMIMEJSON,
			Text:     string(body),
		}},
	}, nil
}
