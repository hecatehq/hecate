package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/mcp"
)

const cairnlineSidecarFixtureArgPrefix = "--hecate-cairnline-sidecar-fixture="

func TestMain(m *testing.M) {
	for _, arg := range os.Args[1:] {
		if mode, ok := strings.CutPrefix(arg, cairnlineSidecarFixtureArgPrefix); ok {
			cairnlineSidecarFixtureMain(mode)
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func cairnlineSidecarFixtureMain(mode string) {
	in := bufio.NewReader(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			fmt.Fprintln(os.Stderr, "cairnline sidecar fixture: read:", err)
			return
		}
		var req mcp.Request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if req.IsNotification() {
			continue
		}
		var (
			result any
			rpcErr *mcp.RPCError
		)
		switch req.Method {
		case "initialize":
			result = mcp.InitializeResult{
				ProtocolVersion: mcp.DeclaredProtocolVersion,
				Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
				ServerInfo:      mcp.ServerInfo{Name: "cairnline-fixture", Version: "test"},
			}
		case "tools/list":
			result = mcp.ListToolsResult{Tools: cairnlineSidecarFixtureTools(mode)}
		case "tools/call":
			var params mcp.CallToolParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				rpcErr = mcp.NewError(mcp.ErrCodeInvalidParams, "invalid tools/call params")
				break
			}
			result, rpcErr = cairnlineSidecarFixtureCallTool(mode, params)
		default:
			rpcErr = mcp.NewError(mcp.ErrCodeMethodNotFound, req.Method)
		}

		resp := mcp.Response{JSONRPC: "2.0", ID: req.ID}
		if rpcErr != nil {
			resp.Error = rpcErr
		} else {
			raw, err := json.Marshal(result)
			if err != nil {
				fmt.Fprintln(os.Stderr, "cairnline sidecar fixture: marshal:", err)
				continue
			}
			resp.Result = raw
		}
		if err := enc.Encode(&resp); err != nil {
			fmt.Fprintln(os.Stderr, "cairnline sidecar fixture: write:", err)
			return
		}
	}
}

func cairnlineSidecarFixtureTools(mode string) []mcp.Tool {
	names := append([]string(nil), projectCairnlineSidecarRequiredTools...)
	if mode == "missing" {
		names = []string{"projects.list"}
	}
	tools := make([]mcp.Tool, 0, len(names))
	for _, name := range names {
		tools = append(tools, mcp.Tool{
			Name:        name,
			Description: "Cairnline fixture tool " + name,
			InputSchema: json.RawMessage(`{"type":"object"}`),
		})
	}
	return tools
}

func cairnlineSidecarFixtureCallTool(mode string, params mcp.CallToolParams) (mcp.CallToolResult, *mcp.RPCError) {
	switch params.Name {
	case "projects.list":
		if mode == "tool-error" {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture projects.list failed"),
				IsError: true,
			}, nil
		}
		result := mcp.CallToolResult{Content: mcp.TextContent("Projects (1):\n- proj_fixture: Fixture Project")}
		if mode != "text-only" {
			result.StructuredContent = mustRawJSON([]ProjectCairnlineSidecarProjectItem{{
				ID:          "proj_fixture",
				Name:        "Fixture Project",
				Description: "Structured fixture project",
				Roots: []ProjectCairnlineSidecarRootItem{{
					ID:     "root_fixture",
					Path:   "/workspace/fixture",
					Kind:   "local",
					Active: true,
				}},
				ContextSources: []ProjectCairnlineSidecarSourceItem{{
					ID:      "src_fixture",
					Kind:    "workspace_instruction",
					Title:   "AGENTS.md",
					Locator: "AGENTS.md",
					Enabled: true,
				}},
			}})
		}
		return result, nil
	case "profiles.list":
		return cairnlineSidecarFixtureListResult(mode, "Profiles (1):\n- profile_fixture: Fixture Profile", []map[string]any{{
			"id":          "profile_fixture",
			"name":        "Fixture Profile",
			"description": "Portable fixture profile",
			"skill_ids":   []string{"skill_fixture"},
		}})
	case "execution_profiles.list":
		return cairnlineSidecarFixtureListResult(mode, "Execution profiles (1):\n- exec_fixture: Fixture Execution", []map[string]any{{
			"id":           "exec_fixture",
			"name":         "Fixture Execution",
			"agent_kind":   "any",
			"model_hint":   "fixture-model",
			"tools_policy": "readonly",
		}})
	case "skills.list":
		if mode == "coordination-tool-error" {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture skills.list failed"),
				IsError: true,
			}, nil
		}
		return cairnlineSidecarFixtureListResult(mode, "Skills (1):\n- skill_fixture: Fixture Skill", []map[string]any{{
			"project_id":  cairnlineSidecarFixtureProjectID(params.Arguments),
			"id":          "skill_fixture",
			"title":       "Fixture Skill",
			"path":        ".agents/skills/fixture/SKILL.md",
			"enabled":     true,
			"status":      "available",
			"trust_label": "workspace_skill",
		}})
	case "roles.list":
		return cairnlineSidecarFixtureListResult(mode, "Roles (1):\n- role_fixture: Fixture Reviewer", []map[string]any{{
			"project_id":                   cairnlineSidecarFixtureProjectID(params.Arguments),
			"id":                           "role_fixture",
			"name":                         "Fixture Reviewer",
			"default_profile_id":           "profile_fixture",
			"default_execution_mode":       "mcp_pull",
			"default_skill_ids":            []string{"skill_fixture"},
			"default_execution_profile_id": "exec_fixture",
		}})
	case "work_items.list":
		return cairnlineSidecarFixtureListResult(mode, "Work items (1):\n- work_fixture: Fixture Work", []map[string]any{{
			"project_id": cairnlineSidecarFixtureProjectID(params.Arguments),
			"id":         "work_fixture",
			"title":      "Fixture Work",
			"status":     "open",
			"priority":   "normal",
		}})
	case "assignments.list":
		if mode == "assignment-list-empty" {
			return cairnlineSidecarFixtureListResult(mode, "No assignments yet.", []map[string]any{})
		}
		return cairnlineSidecarFixtureListResult(mode, "Assignments (1):\n- asg_fixture: Fixture Assignment", []map[string]any{{
			"project_id":     cairnlineSidecarFixtureProjectID(params.Arguments),
			"id":             "asg_fixture",
			"work_item_id":   "work_fixture",
			"role_id":        "role_fixture",
			"profile_id":     "profile_fixture",
			"execution_mode": "mcp_pull",
			"status":         "queued",
		}})
	case "assignments.context":
		if mode == "context-tool-error" {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture assignments.context failed"),
				IsError: true,
			}, nil
		}
		var input struct {
			ProjectID    string `json:"project_id"`
			AssignmentID string `json:"assignment_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid assignments.context arguments")
		}
		if input.ProjectID == "" || input.AssignmentID == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing assignment context ids")
		}
		result := mcp.CallToolResult{Content: mcp.TextContent("Assignment context " + input.AssignmentID + " for project " + input.ProjectID)}
		if mode != "text-only" {
			result.StructuredContent = mustRawJSON(map[string]any{
				"assignment": map[string]any{
					"id":           input.AssignmentID,
					"project_id":   input.ProjectID,
					"work_item_id": "work_fixture",
					"role_id":      "role_fixture",
					"status":       "queued",
				},
				"work_item": map[string]any{
					"id":    "work_fixture",
					"title": "Fixture Work",
				},
				"role": map[string]any{
					"id":   "role_fixture",
					"name": "Fixture Reviewer",
				},
			})
		}
		return result, nil
	case "projects.get":
		if mode == "get-tool-error" {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture projects.get failed"),
				IsError: true,
			}, nil
		}
		var input struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid projects.get arguments")
		}
		if input.ID == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing project id")
		}
		project := ProjectCairnlineSidecarProjectItem{
			ID:          input.ID,
			Name:        "Fixture Project",
			Description: "Structured fixture project detail",
			Roots: []ProjectCairnlineSidecarRootItem{{
				ID:     "root_fixture",
				Path:   "/workspace/fixture",
				Kind:   "local",
				Active: true,
			}},
			ContextSources: []ProjectCairnlineSidecarSourceItem{{
				ID:      "src_fixture",
				Kind:    "workspace_instruction",
				Title:   "AGENTS.md",
				Locator: "AGENTS.md",
				Enabled: true,
			}},
		}
		result := mcp.CallToolResult{Content: mcp.TextContent("Project " + input.ID + ": Fixture Project")}
		if mode != "text-only" {
			result.StructuredContent = mustRawJSON(project)
		}
		return result, nil
	default:
		return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeMethodNotFound, params.Name)
	}
}

func cairnlineSidecarFixtureListResult(mode, text string, structured any) (mcp.CallToolResult, *mcp.RPCError) {
	result := mcp.CallToolResult{Content: mcp.TextContent(text)}
	if mode != "text-only" {
		result.StructuredContent = mustRawJSON(structured)
	}
	return result, nil
}

func cairnlineSidecarFixtureProjectID(raw json.RawMessage) string {
	var input struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(raw, &input)
	return input.ProjectID
}

func mustRawJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}
