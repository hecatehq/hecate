package api

import "net/http"

const remoteRuntimeLocalOnlyMessage = "local-only endpoint is disabled in remote runtime mode"
const remoteRuntimeEndpointNotAllowedMessage = "endpoint is not enabled in remote runtime mode"

type remoteRuntimeRoutePattern struct {
	method string
	path   string
}

var remoteRuntimeLocalOnlyRoutes = []remoteRuntimeRoutePattern{
	{method: http.MethodPost, path: "/hecate/v1/workspace-dialog"},
	{method: http.MethodPost, path: "/hecate/v1/workspace-open"},
	{method: http.MethodPost, path: "/hecate/v1/system/reset-data"},
	{method: http.MethodPost, path: "/hecate/v1/system/shutdown"},
	{method: http.MethodPost, path: "/hecate/v1/mcp/probe"},
	{method: http.MethodGet, path: "/hecate/v1/mcp/registry/servers"},
	{method: http.MethodGet, path: "/hecate/v1/settings/providers/local-discovery"},
	{method: http.MethodPost, path: "/hecate/v1/agent-adapters/{id}/authenticate"},
	{method: http.MethodGet, path: "/hecate/v1/plugins"},
	{method: http.MethodPost, path: "/hecate/v1/plugins/install-local"},
	{method: http.MethodGet, path: "/hecate/v1/plugins/{id}"},
	{method: http.MethodPatch, path: "/hecate/v1/plugins/{id}"},
	{method: http.MethodGet, path: "/hecate/v1/plugins/{id}/health"},
}

// Keep this table in lockstep with server.go. The coverage test is part of the
// safety boundary because dynamic "{id}" routes can overlap future exact paths.
var remoteRuntimeAllowedRoutes = []remoteRuntimeRoutePattern{
	{method: http.MethodGet, path: "/hecate/v1/whoami"},
	{method: http.MethodGet, path: "/hecate/v1/providers/presets"},
	{method: http.MethodGet, path: "/hecate/v1/providers/status"},
	{method: http.MethodGet, path: "/hecate/v1/providers/history"},

	{method: http.MethodGet, path: "/hecate/v1/projects"},
	{method: http.MethodPost, path: "/hecate/v1/projects"},
	{method: http.MethodPost, path: "/hecate/v1/project-assistant/context"},
	{method: http.MethodPost, path: "/hecate/v1/project-assistant/draft"},
	{method: http.MethodPost, path: "/hecate/v1/project-assistant/propose"},
	{method: http.MethodPost, path: "/hecate/v1/project-assistant/apply"},
	{method: http.MethodGet, path: "/hecate/v1/projects/{id}"},
	{method: http.MethodPatch, path: "/hecate/v1/projects/{id}"},
	{method: http.MethodDelete, path: "/hecate/v1/projects/{id}"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/roots/discover"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/roots/worktrees"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/context-sources/discover"},
	{method: http.MethodGet, path: "/hecate/v1/projects/{id}/skills"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/skills/discover"},
	{method: http.MethodPatch, path: "/hecate/v1/projects/{id}/skills/{skill_id}"},
	{method: http.MethodGet, path: "/hecate/v1/projects/{id}/memory"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/memory"},
	{method: http.MethodGet, path: "/hecate/v1/projects/{id}/memory/candidates"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/memory/candidates"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/memory/candidates/{candidate_id}/promote"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/memory/candidates/{candidate_id}/reject"},
	{method: http.MethodPatch, path: "/hecate/v1/projects/{id}/memory/{memory_id}"},
	{method: http.MethodDelete, path: "/hecate/v1/projects/{id}/memory/{memory_id}"},
	{method: http.MethodGet, path: "/hecate/v1/projects/{id}/activity"},
	{method: http.MethodGet, path: "/hecate/v1/projects/{id}/roles"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/roles"},
	{method: http.MethodPatch, path: "/hecate/v1/projects/{id}/roles/{role_id}"},
	{method: http.MethodDelete, path: "/hecate/v1/projects/{id}/roles/{role_id}"},
	{method: http.MethodGet, path: "/hecate/v1/projects/{id}/work-items"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/work-items"},
	{method: http.MethodGet, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}"},
	{method: http.MethodPatch, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}"},
	{method: http.MethodDelete, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}"},
	{method: http.MethodGet, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}/assignments"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}/assignments"},
	{method: http.MethodPatch, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}"},
	{method: http.MethodDelete, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}"},
	{method: http.MethodGet, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}/preflight"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}/start"},
	{method: http.MethodGet, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}/context"},
	{method: http.MethodGet, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}/artifacts"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}/artifacts"},
	{method: http.MethodGet, path: "/hecate/v1/projects/{id}/handoffs"},
	{method: http.MethodGet, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs"},
	{method: http.MethodPatch, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs/{handoff_id}"},
	{method: http.MethodPost, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs/{handoff_id}/status"},
	{method: http.MethodDelete, path: "/hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs/{handoff_id}"},

	{method: http.MethodGet, path: "/hecate/v1/agent-profiles"},
	{method: http.MethodPost, path: "/hecate/v1/agent-profiles"},
	{method: http.MethodGet, path: "/hecate/v1/agent-profiles/{id}"},
	{method: http.MethodPatch, path: "/hecate/v1/agent-profiles/{id}"},
	{method: http.MethodDelete, path: "/hecate/v1/agent-profiles/{id}"},
	{method: http.MethodGet, path: "/hecate/v1/agent-adapters"},
	{method: http.MethodPost, path: "/hecate/v1/agent-adapters/{id}/probe"},
	{method: http.MethodGet, path: "/hecate/v1/agent-adapters/{id}/health"},
	{method: http.MethodPost, path: "/hecate/v1/agent-adapters/{id}/logout"},
	{method: http.MethodGet, path: "/hecate/v1/chat/sessions"},
	{method: http.MethodPost, path: "/hecate/v1/chat/sessions"},
	{method: http.MethodGet, path: "/hecate/v1/chat/sessions/{id}"},
	{method: http.MethodPatch, path: "/hecate/v1/chat/sessions/{id}"},
	{method: http.MethodDelete, path: "/hecate/v1/chat/sessions/{id}"},
	{method: http.MethodGet, path: "/hecate/v1/chat/sessions/{id}/stream"},
	{method: http.MethodPost, path: "/hecate/v1/chat/sessions/{id}/cancel"},
	{method: http.MethodPost, path: "/hecate/v1/chat/sessions/{id}/close"},
	{method: http.MethodPost, path: "/hecate/v1/chat/sessions/{id}/compact"},
	{method: http.MethodPatch, path: "/hecate/v1/chat/sessions/{id}/settings"},
	{method: http.MethodPost, path: "/hecate/v1/chat/sessions/{id}/config-options/{config_id}"},
	{method: http.MethodPost, path: "/hecate/v1/chat/sessions/{id}/messages"},
	{method: http.MethodPost, path: "/hecate/v1/chat/sessions/{id}/project-assistant/draft"},
	{method: http.MethodGet, path: "/hecate/v1/chat/sessions/{id}/workspace-diff"},
	{method: http.MethodGet, path: "/hecate/v1/chat/sessions/{id}/workspace-files"},
	{method: http.MethodGet, path: "/hecate/v1/chat/sessions/{id}/workspace-diff/files/{path...}"},
	{method: http.MethodPost, path: "/hecate/v1/chat/sessions/{id}/workspace-diff/revert"},
	{method: http.MethodGet, path: "/hecate/v1/chat/sessions/{id}/messages/{message_id}/files"},
	{method: http.MethodGet, path: "/hecate/v1/chat/sessions/{id}/messages/{message_id}/files/{path...}"},
	{method: http.MethodGet, path: "/hecate/v1/chat/sessions/{id}/messages/{message_id}/context"},
	{method: http.MethodPost, path: "/hecate/v1/chat/sessions/{id}/messages/{message_id}/revert"},
	{method: http.MethodGet, path: "/hecate/v1/chat/sessions/{id}/approvals"},
	{method: http.MethodGet, path: "/hecate/v1/chat/sessions/{id}/approvals/{approval_id}"},
	{method: http.MethodPost, path: "/hecate/v1/chat/sessions/{id}/approvals/{approval_id}/resolve"},
	{method: http.MethodPost, path: "/hecate/v1/chat/sessions/{id}/approvals/{approval_id}/cancel"},
	{method: http.MethodGet, path: "/hecate/v1/chat/grants"},
	{method: http.MethodDelete, path: "/hecate/v1/chat/grants/{grant_id}"},

	{method: http.MethodGet, path: "/hecate/v1/tasks"},
	{method: http.MethodPost, path: "/hecate/v1/tasks"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}"},
	{method: http.MethodDelete, path: "/hecate/v1/tasks/{id}"},
	{method: http.MethodPost, path: "/hecate/v1/tasks/{id}/start"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}/approvals"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}/approvals/{approval_id}"},
	{method: http.MethodPost, path: "/hecate/v1/tasks/{id}/approvals/{approval_id}/resolve"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}/runs"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}/runs/{run_id}"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}/runs/{run_id}/context"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}/runs/{run_id}/stream"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}/runs/{run_id}/events"},
	{method: http.MethodPost, path: "/hecate/v1/tasks/{id}/runs/{run_id}/events"},
	{method: http.MethodPost, path: "/hecate/v1/tasks/{id}/runs/{run_id}/retry"},
	{method: http.MethodPost, path: "/hecate/v1/tasks/{id}/runs/{run_id}/retry-from-turn"},
	{method: http.MethodPost, path: "/hecate/v1/tasks/{id}/runs/{run_id}/resume"},
	{method: http.MethodPost, path: "/hecate/v1/tasks/{id}/runs/{run_id}/continue"},
	{method: http.MethodPost, path: "/hecate/v1/tasks/{id}/runs/{run_id}/cancel"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}/runs/{run_id}/steps"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}/runs/{run_id}/steps/{step_id}"},
	{method: http.MethodPost, path: "/hecate/v1/tasks/{id}/runs/{run_id}/patches/{artifact_id}/revert"},
	{method: http.MethodPost, path: "/hecate/v1/tasks/{id}/runs/{run_id}/patches/{artifact_id}/apply"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}/runs/{run_id}/patches/{artifact_id}"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}/runs/{run_id}/patches"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}/runs/{run_id}/artifacts/{artifact_id}"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}/runs/{run_id}/artifacts"},
	{method: http.MethodGet, path: "/hecate/v1/tasks/{id}/artifacts"},
	{method: http.MethodGet, path: "/hecate/v1/events"},
	{method: http.MethodGet, path: "/hecate/v1/events/stream"},

	{method: http.MethodGet, path: "/hecate/v1/traces"},
	{method: http.MethodGet, path: "/hecate/v1/system/retention/runs"},
	{method: http.MethodPost, path: "/hecate/v1/system/retention/run"},
	{method: http.MethodGet, path: "/hecate/v1/system/stats"},
	{method: http.MethodGet, path: "/hecate/v1/system/mcp/cache"},
	{method: http.MethodGet, path: "/hecate/v1/usage/events"},
	{method: http.MethodGet, path: "/hecate/v1/usage/summary"},

	{method: http.MethodGet, path: "/hecate/v1/settings"},
	{method: http.MethodPost, path: "/hecate/v1/settings/providers"},
	{method: http.MethodPatch, path: "/hecate/v1/settings/providers/{id}"},
	{method: http.MethodDelete, path: "/hecate/v1/settings/providers/{id}"},
	{method: http.MethodPut, path: "/hecate/v1/settings/providers/{id}/api-key"},
	{method: http.MethodPost, path: "/hecate/v1/settings/policy-rules"},
	{method: http.MethodDelete, path: "/hecate/v1/settings/policy-rules/{id}"},
}

func remoteRuntimeEndpointBlockReason(method, path string) string {
	if !isHecateAPIPath(path) {
		return ""
	}
	if routePatternsMatch(remoteRuntimeLocalOnlyRoutes, method, path) {
		return remoteRuntimeLocalOnlyMessage
	}
	if routePatternsMatch(remoteRuntimeAllowedRoutes, method, path) {
		return ""
	}
	return remoteRuntimeEndpointNotAllowedMessage
}

func remoteRuntimeRoutePatternKnown(pattern string) bool {
	method, path, ok := splitRoutePattern(pattern)
	if !ok || !isHecateAPIPath(path) {
		return true
	}
	return routePatternDeclared(remoteRuntimeAllowedRoutes, method, path) ||
		routePatternDeclared(remoteRuntimeLocalOnlyRoutes, method, path)
}

func routePatternDeclared(patterns []remoteRuntimeRoutePattern, method, path string) bool {
	for _, pattern := range patterns {
		if pattern.method == method && pattern.path == path {
			return true
		}
	}
	return false
}

func routePatternsMatch(patterns []remoteRuntimeRoutePattern, method, path string) bool {
	for _, pattern := range patterns {
		if pattern.method == method && routePathMatches(pattern.path, path) {
			return true
		}
	}
	return false
}

func splitRoutePattern(pattern string) (string, string, bool) {
	for i, ch := range pattern {
		if ch == ' ' || ch == '\t' {
			method := pattern[:i]
			path := pattern[i+1:]
			for len(path) > 0 && (path[0] == ' ' || path[0] == '\t') {
				path = path[1:]
			}
			return method, path, method != "" && path != ""
		}
	}
	return "", "", false
}

func routePathMatches(pattern, path string) bool {
	patternSegments := splitRoutePath(pattern)
	pathSegments := splitRoutePath(path)
	for i := 0; i < len(patternSegments); i++ {
		if i >= len(pathSegments) {
			return routeSegmentIsRestWildcard(patternSegments[i])
		}
		segment := patternSegments[i]
		if routeSegmentIsRestWildcard(segment) {
			return true
		}
		if routeSegmentIsWildcard(segment) {
			continue
		}
		if segment != pathSegments[i] {
			return false
		}
	}
	return len(pathSegments) == len(patternSegments)
}

func splitRoutePath(path string) []string {
	for len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	for len(path) > 0 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	if path == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' {
			out = append(out, path[start:i])
			start = i + 1
		}
	}
	return out
}

func routeSegmentIsWildcard(segment string) bool {
	return len(segment) >= 2 && segment[0] == '{' && segment[len(segment)-1] == '}'
}

func routeSegmentIsRestWildcard(segment string) bool {
	return routeSegmentIsWildcard(segment) && len(segment) >= len("{x...}") && segment[len(segment)-4:len(segment)-1] == "..."
}
