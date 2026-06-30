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
	state := &cairnlineSidecarFixtureState{assignmentStatus: "queued"}
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
			result, rpcErr = cairnlineSidecarFixtureCallTool(mode, state, params)
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

type cairnlineSidecarFixtureState struct {
	assignmentStatus string
	claimedBy        string
	executionRef     string
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

func cairnlineSidecarFixtureCallTool(mode string, state *cairnlineSidecarFixtureState, params mcp.CallToolParams) (mcp.CallToolResult, *mcp.RPCError) {
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
			"status":         state.assignmentStatus,
			"claimed_by":     state.claimedBy,
			"execution_ref":  state.executionRef,
		}})
	case "assignments.next":
		if mode == "assignment-list-empty" || state.assignmentStatus != "queued" {
			return cairnlineSidecarFixtureListResult(mode, "No compatible assignments.", []map[string]any{})
		}
		return cairnlineSidecarFixtureListResult(mode, "Compatible assignments (1):\n- asg_fixture: Fixture Assignment", []map[string]any{{
			"project_id":     cairnlineSidecarFixtureProjectID(params.Arguments),
			"id":             "asg_fixture",
			"work_item_id":   "work_fixture",
			"role_id":        "role_fixture",
			"profile_id":     "profile_fixture",
			"execution_mode": "mcp_pull",
			"status":         state.assignmentStatus,
		}})
	case "assignments.claim":
		if mode == "claim-tool-error" {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture assignments.claim failed"),
				IsError: true,
			}, nil
		}
		var input struct {
			ProjectID    string `json:"project_id"`
			AssignmentID string `json:"assignment_id"`
			ClaimedBy    string `json:"claimed_by"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid assignments.claim arguments")
		}
		if input.ProjectID == "" || input.AssignmentID == "" || input.ClaimedBy == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing claim arguments")
		}
		if state.assignmentStatus != "queued" {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture assignment is not queued"),
				IsError: true,
			}, nil
		}
		state.assignmentStatus = "claimed"
		state.claimedBy = input.ClaimedBy
		return mcp.CallToolResult{Content: mcp.TextContent("Claimed assignment " + input.AssignmentID + " by " + input.ClaimedBy)}, nil
	case "assignments.release":
		var input struct {
			ProjectID    string `json:"project_id"`
			AssignmentID string `json:"assignment_id"`
			ClaimedBy    string `json:"claimed_by"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid assignments.release arguments")
		}
		state.assignmentStatus = "queued"
		state.claimedBy = ""
		state.executionRef = ""
		return mcp.CallToolResult{Content: mcp.TextContent("Released assignment " + input.AssignmentID)}, nil
	case "assignments.update_status":
		if mode == "update-status-tool-error" {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture assignments.update_status failed"),
				IsError: true,
			}, nil
		}
		var input struct {
			ProjectID    string `json:"project_id"`
			AssignmentID string `json:"assignment_id"`
			Status       string `json:"status"`
			ExecutionRef string `json:"execution_ref"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid assignments.update_status arguments")
		}
		if input.ProjectID == "" || input.AssignmentID == "" || input.Status == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing update_status arguments")
		}
		state.assignmentStatus = input.Status
		state.executionRef = input.ExecutionRef
		return mcp.CallToolResult{Content: mcp.TextContent("Updated assignment " + input.AssignmentID + ": " + input.Status)}, nil
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
					"id":            input.AssignmentID,
					"project_id":    input.ProjectID,
					"work_item_id":  "work_fixture",
					"role_id":       "role_fixture",
					"status":        state.assignmentStatus,
					"claimed_by":    state.claimedBy,
					"execution_ref": state.executionRef,
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
	case "assignments.launch_packet":
		if mode == "launch-packet-tool-error" {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture assignments.launch_packet failed"),
				IsError: true,
			}, nil
		}
		var input struct {
			ProjectID    string `json:"project_id"`
			AssignmentID string `json:"assignment_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid assignments.launch_packet arguments")
		}
		if input.ProjectID == "" || input.AssignmentID == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing launch packet ids")
		}
		result := mcp.CallToolResult{Content: mcp.TextContent("Launch packet launch_fixture for " + input.AssignmentID)}
		if mode != "text-only" {
			result.StructuredContent = mustRawJSON(map[string]any{
				"id":   "launch_fixture",
				"kind": "assignment_launch_packet",
				"project": map[string]any{
					"id":   input.ProjectID,
					"name": "Fixture Project",
				},
				"work_item": map[string]any{
					"id":    "work_fixture",
					"title": "Fixture Work",
				},
				"role": map[string]any{
					"id":   "role_fixture",
					"name": "Fixture Reviewer",
				},
				"profile": map[string]any{
					"id":   "profile_fixture",
					"name": "Fixture Profile",
				},
				"execution_profile": map[string]any{
					"id":   "exec_fixture",
					"name": "Fixture Execution",
				},
				"skills": []map[string]any{{
					"id":    "skill_fixture",
					"title": "Fixture Skill",
				}},
				"assignment": map[string]any{
					"id":            input.AssignmentID,
					"project_id":    input.ProjectID,
					"work_item_id":  "work_fixture",
					"role_id":       "role_fixture",
					"status":        state.assignmentStatus,
					"claimed_by":    state.claimedBy,
					"execution_ref": state.executionRef,
				},
				"artifacts":         []map[string]any{{"id": "artifact_fixture"}},
				"evidence":          []map[string]any{{"id": "evidence_fixture"}},
				"reviews":           []map[string]any{{"id": "review_fixture"}},
				"handoffs":          []map[string]any{{"id": "handoff_fixture"}},
				"memory":            []map[string]any{{"id": "memory_fixture"}},
				"memory_candidates": []map[string]any{{"id": "candidate_fixture"}},
				"warnings":          []string{"fixture warning"},
			})
		}
		return result, nil
	case "assignments.complete":
		if mode == "complete-tool-error" {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture assignments.complete failed"),
				IsError: true,
			}, nil
		}
		var input struct {
			ProjectID    string `json:"project_id"`
			AssignmentID string `json:"assignment_id"`
			Status       string `json:"status"`
			ExecutionRef string `json:"execution_ref"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid assignments.complete arguments")
		}
		if input.ProjectID == "" || input.AssignmentID == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing complete arguments")
		}
		if input.Status == "" {
			input.Status = "completed"
		}
		state.assignmentStatus = input.Status
		state.executionRef = input.ExecutionRef
		return mcp.CallToolResult{Content: mcp.TextContent("Updated assignment " + input.AssignmentID + ": " + input.Status)}, nil
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
