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
	state := &cairnlineSidecarFixtureState{
		assignmentStatus:   "queued",
		projects:           make(map[string]ProjectCairnlineSidecarProjectItem),
		deletedProjects:    make(map[string]struct{}),
		roots:              make(map[string]map[string]ProjectCairnlineSidecarRootItem),
		contextSources:     make(map[string]map[string]ProjectCairnlineSidecarSourceItem),
		roles:              make(map[string]map[string]ProjectCairnlineSidecarRoleItem),
		workItems:          make(map[string]map[string]ProjectCairnlineSidecarWorkItem),
		assignments:        make(map[string]map[string]ProjectCairnlineSidecarAssignmentItem),
		artifacts:          make(map[string]map[string]ProjectCairnlineSidecarArtifactItem),
		evidence:           make(map[string]map[string]ProjectCairnlineSidecarEvidenceItem),
		reviews:            make(map[string]map[string]ProjectCairnlineSidecarReviewItem),
		handoffs:           make(map[string]map[string]ProjectCairnlineSidecarHandoffItem),
		memoryEntries:      make(map[string]map[string]ProjectCairnlineSidecarMemoryEntryItem),
		memoryCandidates:   make(map[string]map[string]ProjectCairnlineSidecarMemoryCandidateItem),
		assistantProposals: make(map[string]ProjectCairnlineSidecarAssistantProposalRecordItem),
	}
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
				Capabilities: mcp.ServerCapabilities{
					Tools:     &mcp.ToolsCapability{},
					Resources: &mcp.ResourcesCapability{},
				},
				ServerInfo: mcp.ServerInfo{Name: "cairnline-fixture", Version: "test"},
			}
		case "tools/list":
			result = mcp.ListToolsResult{Tools: cairnlineSidecarFixtureTools(mode)}
		case "resources/templates/list":
			if cairnlineSidecarFixtureModeHas(mode, "resource-template-error") {
				rpcErr = mcp.NewError(mcp.ErrCodeInternalError, "resource template fixture failure")
				break
			}
			result = mcp.ListResourceTemplatesResult{ResourceTemplates: cairnlineSidecarFixtureResourceTemplates(mode)}
		case "resources/read":
			var params mcp.ReadResourceParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				rpcErr = mcp.NewError(mcp.ErrCodeInvalidParams, "invalid resources/read params")
				break
			}
			result, rpcErr = cairnlineSidecarFixtureReadResource(mode, state, params.URI)
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
	assignmentStatus   string
	claimedBy          string
	executionRef       string
	projectSequence    int
	projects           map[string]ProjectCairnlineSidecarProjectItem
	deletedProjects    map[string]struct{}
	roots              map[string]map[string]ProjectCairnlineSidecarRootItem
	contextSources     map[string]map[string]ProjectCairnlineSidecarSourceItem
	roleSequence       int
	workSequence       int
	assignmentSequence int
	artifactSequence   int
	evidenceSequence   int
	reviewSequence     int
	handoffSequence    int
	memorySequence     int
	candidateSequence  int
	roles              map[string]map[string]ProjectCairnlineSidecarRoleItem
	workItems          map[string]map[string]ProjectCairnlineSidecarWorkItem
	assignments        map[string]map[string]ProjectCairnlineSidecarAssignmentItem
	artifacts          map[string]map[string]ProjectCairnlineSidecarArtifactItem
	evidence           map[string]map[string]ProjectCairnlineSidecarEvidenceItem
	reviews            map[string]map[string]ProjectCairnlineSidecarReviewItem
	handoffs           map[string]map[string]ProjectCairnlineSidecarHandoffItem
	memoryEntries      map[string]map[string]ProjectCairnlineSidecarMemoryEntryItem
	memoryCandidates   map[string]map[string]ProjectCairnlineSidecarMemoryCandidateItem
	assistantProposals map[string]ProjectCairnlineSidecarAssistantProposalRecordItem
}

func cairnlineSidecarFixtureTools(mode string) []mcp.Tool {
	names := append([]string(nil), projectCairnlineSidecarRequiredTools...)
	if cairnlineSidecarFixtureModeHas(mode, "missing") {
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

func cairnlineSidecarFixtureResourceTemplates(mode string) []mcp.ResourceTemplate {
	if cairnlineSidecarFixtureModeHas(mode, "missing-resource-template") {
		return []mcp.ResourceTemplate{{
			URITemplate: "cairnline://projects/{project_id}",
			Name:        "project",
			MIMEType:    "application/json",
		}}
	}
	templates := make([]mcp.ResourceTemplate, 0, len(projectCairnlineSidecarRequiredResourceTemplates))
	for _, uri := range projectCairnlineSidecarRequiredResourceTemplates {
		templates = append(templates, mcp.ResourceTemplate{
			URITemplate: uri,
			Name:        strings.NewReplacer("cairnline://", "", "/", "_", "{", "", "}", "", "-", "_").Replace(uri),
			MIMEType:    "application/json",
		})
	}
	return templates
}

func cairnlineSidecarFixtureReadResource(mode string, state *cairnlineSidecarFixtureState, uri string) (mcp.ReadResourceResult, *mcp.RPCError) {
	if cairnlineSidecarFixtureModeHas(mode, "resource-read-error") {
		return mcp.ReadResourceResult{}, mcp.NewError(mcp.ErrCodeInternalError, "resource read fixture failure")
	}
	const prefix = "cairnline://projects/"
	projectID := strings.TrimPrefix(strings.TrimSpace(uri), prefix)
	if projectID == uri || projectID == "" || strings.Contains(projectID, "/") {
		return mcp.ReadResourceResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "unsupported fixture resource uri")
	}
	project := ProjectCairnlineSidecarProjectItem{
		ID:   "proj_fixture",
		Name: "Fixture Project",
	}
	if items := cairnlineSidecarFixtureProjects(mode, state); len(items) > 0 {
		for _, item := range items {
			if item.ID == projectID {
				project = item
				break
			}
		}
	}
	if project.ID != projectID {
		return mcp.ReadResourceResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "fixture project not found")
	}
	raw, err := json.MarshalIndent(map[string]any{
		"project": project,
	}, "", "  ")
	if err != nil {
		return mcp.ReadResourceResult{}, mcp.NewError(mcp.ErrCodeInternalError, err.Error())
	}
	return mcp.ReadResourceResult{Contents: []mcp.ResourceContents{{
		URI:      uri,
		MIMEType: "application/json",
		Text:     string(raw),
	}}}, nil
}

func cairnlineSidecarFixtureCallTool(mode string, state *cairnlineSidecarFixtureState, params mcp.CallToolParams) (mcp.CallToolResult, *mcp.RPCError) {
	switch params.Name {
	case "coordination.capabilities":
		if cairnlineSidecarFixtureModeHas(mode, "coordination-capabilities-tool-error") {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture coordination.capabilities failed"),
				IsError: true,
			}, nil
		}
		result := mcp.CallToolResult{Content: mcp.TextContent("Cairnline coordinates project work; it does not launch or authorize agents.")}
		if !cairnlineSidecarFixtureTextOnly(mode, "coordination.capabilities") {
			result.StructuredContent = mustRawJSON(ProjectCairnlineCoordinationCapabilities{
				ServerName:    "cairnline",
				ServerVersion: "fixture",
				Product:       "local-first project coordination server for operators and AI agents",
				CoreRule:      "Assignment is coordination. Execution is capability-dependent.",
				ExecutionModes: []string{
					"manual",
					"mcp_pull",
					"external_adapter",
					"orchestrated",
				},
				AssignmentStatuses: []string{
					"queued",
					"claimed",
					"running",
					"awaiting_review",
					"completed",
					"failed",
					"cancelled",
				},
				DesiredAgentKindHints: []string{
					"any",
					"human",
					"claude",
					"cursor",
					"hecate",
				},
				SkillMetadataPaths: []string{
					".agents/skills",
					".cairnline/skills",
					".claude/skills",
					".gemini/skills",
					".hecate/skills",
				},
				AgentHostOwns: []string{
					"agent launch and supervision",
					"provider and model selection",
					"tool, write, network, and sandbox permissions",
					"mapping desired_agent hints to host-specific agents or presets",
				},
				RecommendedMCPPullFlow: []string{
					"assignments.next",
					"assignments.claim",
					"assignments.context",
					"assignments.launch_packet",
					"evidence.record",
					"assignments.complete",
				},
			})
		}
		return result, nil
	case "projects.list":
		if cairnlineSidecarFixtureModeHas(mode, "tool-error") {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture projects.list failed"),
				IsError: true,
			}, nil
		}
		result := mcp.CallToolResult{Content: mcp.TextContent("Projects (1):\n- proj_fixture: Fixture Project")}
		if !cairnlineSidecarFixtureTextOnly(mode, "projects.list") {
			result.StructuredContent = mustRawJSON(cairnlineSidecarFixtureProjects(mode, state))
		}
		return result, nil
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
		projectID := cairnlineSidecarFixtureProjectID(params.Arguments)
		if roles := cairnlineSidecarFixtureProjectRoles(state, projectID); len(roles) > 0 {
			return cairnlineSidecarFixtureListResult(mode, fmt.Sprintf("Roles (%d)", len(roles)), roles)
		}
		return cairnlineSidecarFixtureListResult(mode, "Roles (1):\n- role_fixture: Fixture Reviewer", []ProjectCairnlineSidecarRoleItem{{
			ProjectID:            projectID,
			ID:                   "role_fixture",
			Name:                 "Fixture Reviewer",
			DefaultExecutionMode: "mcp_pull",
			DefaultSkillIDs:      []string{"skill_fixture"},
		}})
	case "work_items.list":
		projectID := cairnlineSidecarFixtureProjectID(params.Arguments)
		if items := cairnlineSidecarFixtureProjectWorkItems(state, projectID); len(items) > 0 {
			return cairnlineSidecarFixtureListResult(mode, fmt.Sprintf("Work items (%d)", len(items)), items)
		}
		return cairnlineSidecarFixtureListResult(mode, "Work items (1):\n- work_fixture: Fixture Work", []ProjectCairnlineSidecarWorkItem{{
			ProjectID: projectID,
			ID:        "work_fixture",
			Title:     "Fixture Work",
			Status:    "open",
			Priority:  "normal",
		}})
	case "assignments.list":
		if mode == "assignment-list-empty" {
			return cairnlineSidecarFixtureListResult(mode, "No assignments yet.", []map[string]any{})
		}
		projectID := cairnlineSidecarFixtureProjectID(params.Arguments)
		if assignments := cairnlineSidecarFixtureProjectAssignments(state, projectID); len(assignments) > 0 {
			return cairnlineSidecarFixtureListResult(mode, fmt.Sprintf("Assignments (%d)", len(assignments)), assignments)
		}
		return cairnlineSidecarFixtureListResult(mode, "Assignments (1):\n- asg_fixture: Fixture Assignment", []ProjectCairnlineSidecarAssignmentItem{{
			ProjectID:     projectID,
			ID:            "asg_fixture",
			WorkItemID:    "work_fixture",
			RoleID:        "role_fixture",
			ExecutionMode: "mcp_pull",
			DesiredAgent: ProjectCairnlineSidecarDesiredAgentItem{
				Kind:     "any",
				SkillIDs: []string{"skill_fixture"},
			},
			Status:       state.assignmentStatus,
			ClaimedBy:    state.claimedBy,
			ExecutionRef: cairnlineSidecarFixtureExecutionRef(state.executionRef),
		}})
	case "projects.activity":
		projectID := cairnlineSidecarFixtureProjectID(params.Arguments)
		activity := cairnlineSidecarFixtureProjectActivity(state, projectID)
		result := mcp.CallToolResult{Content: mcp.TextContent("Project activity " + projectID)}
		if !cairnlineSidecarFixtureTextOnly(mode, "projects.activity") {
			result.StructuredContent = mustRawJSON(activity)
		}
		return result, nil
	case "projects.operations_brief":
		projectID := cairnlineSidecarFixtureProjectID(params.Arguments)
		brief := cairnlineSidecarFixtureProjectOperationsBrief(state, projectID)
		result := mcp.CallToolResult{Content: mcp.TextContent("Operations brief " + projectID)}
		if !cairnlineSidecarFixtureTextOnly(mode, "projects.operations_brief") {
			result.StructuredContent = mustRawJSON(brief)
		}
		return result, nil
	case "assignments.next":
		if mode == "assignment-list-empty" || state.assignmentStatus != "queued" {
			return cairnlineSidecarFixtureListResult(mode, "No compatible assignments.", []map[string]any{})
		}
		return cairnlineSidecarFixtureListResult(mode, "Compatible assignments (1):\n- asg_fixture: Fixture Assignment", []map[string]any{{
			"project_id":     cairnlineSidecarFixtureProjectID(params.Arguments),
			"id":             "asg_fixture",
			"work_item_id":   "work_fixture",
			"role_id":        "role_fixture",
			"execution_mode": "mcp_pull",
			"desired_agent": map[string]any{
				"kind":      "any",
				"skill_ids": []string{"skill_fixture"},
			},
			"status": state.assignmentStatus,
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
		assignment := ProjectCairnlineSidecarAssignmentItem{
			ID:            input.AssignmentID,
			ProjectID:     input.ProjectID,
			WorkItemID:    "work_fixture",
			RoleID:        "role_fixture",
			Status:        state.assignmentStatus,
			ClaimedBy:     state.claimedBy,
			ExecutionRef:  cairnlineSidecarFixtureExecutionRef(state.executionRef),
			ExecutionMode: "mcp_pull",
		}
		if stored, ok := state.assignments[input.ProjectID][input.AssignmentID]; ok {
			assignment = stored
		}
		projectID := input.ProjectID
		if cairnlineSidecarFixtureModeHas(mode, "context-project-mismatch") {
			projectID = "proj_other"
		}
		workItemID := assignment.WorkItemID
		if cairnlineSidecarFixtureModeHas(mode, "context-route-mismatch") {
			workItemID = "work_other"
		}
		result := mcp.CallToolResult{Content: mcp.TextContent("Assignment context " + input.AssignmentID + " for project " + input.ProjectID)}
		if !cairnlineSidecarFixtureTextOnly(mode, "assignments.context") {
			result.StructuredContent = mustRawJSON(map[string]any{
				"id": "ctx_fixture",
				"project": map[string]any{
					"id":              projectID,
					"name":            "Fixture Project",
					"default_root_id": "root_fixture",
					"roots": []map[string]any{{
						"id":     "root_fixture",
						"path":   "/workspace/fixture",
						"kind":   "git",
						"active": true,
					}},
				},
				"assignment": map[string]any{
					"id":             assignment.ID,
					"project_id":     projectID,
					"work_item_id":   workItemID,
					"role_id":        assignment.RoleID,
					"status":         assignment.Status,
					"claimed_by":     assignment.ClaimedBy,
					"execution_ref":  assignment.ExecutionRef,
					"execution_mode": assignment.ExecutionMode,
				},
				"work_item": map[string]any{
					"id":         workItemID,
					"project_id": projectID,
					"title":      firstNonEmpty(cairnlineSidecarFixtureWorkItemTitle(state, input.ProjectID, workItemID), "Fixture Work"),
					"status":     "open",
					"priority":   "normal",
				},
				"role": map[string]any{
					"id":         assignment.RoleID,
					"project_id": projectID,
					"name":       firstNonEmpty(cairnlineSidecarFixtureRoleName(state, input.ProjectID, assignment.RoleID), "Fixture Reviewer"),
				},
				"warnings": []string{"fixture context warning"},
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
		assignment := ProjectCairnlineSidecarAssignmentItem{
			ID:            input.AssignmentID,
			ProjectID:     input.ProjectID,
			WorkItemID:    "work_fixture",
			RoleID:        "role_fixture",
			ExecutionMode: "mcp_pull",
			DesiredAgent: ProjectCairnlineSidecarDesiredAgentItem{
				Kind:     "any",
				SkillIDs: []string{"skill_fixture"},
			},
			Status:       state.assignmentStatus,
			ClaimedBy:    state.claimedBy,
			ExecutionRef: cairnlineSidecarFixtureExecutionRef(state.executionRef),
		}
		if stored, ok := state.assignments[input.ProjectID][input.AssignmentID]; ok {
			assignment = stored
		}
		projectID := input.ProjectID
		if cairnlineSidecarFixtureModeHas(mode, "launch-packet-project-mismatch") {
			projectID = "proj_other"
		}
		workItemID := assignment.WorkItemID
		if cairnlineSidecarFixtureModeHas(mode, "launch-packet-route-mismatch") {
			workItemID = "work_other"
		}
		result := mcp.CallToolResult{Content: mcp.TextContent("Launch packet launch_fixture for " + input.AssignmentID)}
		if !cairnlineSidecarFixtureTextOnly(mode, "assignments.launch_packet") {
			result.StructuredContent = mustRawJSON(map[string]any{
				"id":   "launch_fixture",
				"kind": "assignment_launch_packet",
				"project": map[string]any{
					"id":   projectID,
					"name": "Fixture Project",
				},
				"work_item": map[string]any{
					"id":         workItemID,
					"project_id": projectID,
					"title":      firstNonEmpty(cairnlineSidecarFixtureWorkItemTitle(state, input.ProjectID, workItemID), "Fixture Work"),
				},
				"role": map[string]any{
					"id":   assignment.RoleID,
					"name": firstNonEmpty(cairnlineSidecarFixtureRoleName(state, input.ProjectID, assignment.RoleID), "Fixture Reviewer"),
				},
				"skills": []map[string]any{{
					"id":    "skill_fixture",
					"title": "Fixture Skill",
				}},
				"assignment": map[string]any{
					"id":             assignment.ID,
					"project_id":     projectID,
					"work_item_id":   workItemID,
					"role_id":        assignment.RoleID,
					"status":         assignment.Status,
					"claimed_by":     assignment.ClaimedBy,
					"execution_ref":  assignment.ExecutionRef,
					"execution_mode": assignment.ExecutionMode,
					"desired_agent":  assignment.DesiredAgent,
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
		if cairnlineSidecarFixtureModeHas(mode, "get-tool-error") {
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
		if _, ok := state.deletedProjects[input.ID]; ok {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture project not found: " + input.ID),
				IsError: true,
			}, nil
		}
		if cairnlineSidecarFixtureModeHas(mode, "strict-projects") && input.ID != "proj_fixture" {
			if _, ok := state.projects[input.ID]; !ok {
				return mcp.CallToolResult{
					Content: mcp.TextContent("fixture project not found: " + input.ID),
					IsError: true,
				}, nil
			}
		}
		project := cairnlineSidecarFixtureProject(mode, input.ID)
		if cairnlineSidecarFixtureModeHas(mode, "project-route-mismatch") {
			project.ID = "proj_other"
		}
		if stored, ok := state.projects[input.ID]; ok {
			project = stored
			project.Roots = cairnlineSidecarFixtureProjectRoots(state, input.ID)
			project.ContextSources = cairnlineSidecarFixtureProjectSources(state, input.ID)
		}
		result := mcp.CallToolResult{Content: mcp.TextContent("Project " + input.ID + ": " + project.Name)}
		if !cairnlineSidecarFixtureTextOnly(mode, "projects.get") {
			result.StructuredContent = mustRawJSON(project)
		}
		return result, nil
	case "projects.create":
		var input struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid projects.create arguments")
		}
		if strings.TrimSpace(input.Name) == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing project name")
		}
		state.projectSequence++
		id := fmt.Sprintf("proj_write_fixture_%d", state.projectSequence)
		state.projects[id] = ProjectCairnlineSidecarProjectItem{
			ID:          id,
			Name:        input.Name,
			Description: input.Description,
		}
		delete(state.deletedProjects, id)
		return mcp.CallToolResult{Content: mcp.TextContent("Created project " + id + ": " + input.Name)}, nil
	case "projects.update":
		if mode == "project-update-tool-error" {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture projects.update failed"),
				IsError: true,
			}, nil
		}
		var input struct {
			ID          string  `json:"id"`
			Name        *string `json:"name"`
			Description *string `json:"description"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid projects.update arguments")
		}
		project, ok := state.projects[input.ID]
		if !ok {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture project not found: " + input.ID),
				IsError: true,
			}, nil
		}
		if input.Name != nil {
			project.Name = *input.Name
		}
		if input.Description != nil {
			project.Description = *input.Description
		}
		state.projects[input.ID] = project
		return mcp.CallToolResult{Content: mcp.TextContent("Updated project " + input.ID + ": " + project.Name)}, nil
	case "projects.delete":
		var input struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid projects.delete arguments")
		}
		if input.ID == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing project id")
		}
		delete(state.projects, input.ID)
		state.deletedProjects[input.ID] = struct{}{}
		delete(state.roots, input.ID)
		delete(state.contextSources, input.ID)
		delete(state.roles, input.ID)
		delete(state.workItems, input.ID)
		delete(state.assignments, input.ID)
		delete(state.artifacts, input.ID)
		delete(state.evidence, input.ID)
		delete(state.reviews, input.ID)
		delete(state.handoffs, input.ID)
		delete(state.memoryEntries, input.ID)
		delete(state.memoryCandidates, input.ID)
		for proposalID, proposal := range state.assistantProposals {
			if proposal.ProjectID == input.ID {
				delete(state.assistantProposals, proposalID)
			}
		}
		return mcp.CallToolResult{Content: mcp.TextContent("Deleted project " + input.ID)}, nil
	case "assistant.propose":
		var input ProjectCairnlineSidecarAssistantProposalItem
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid assistant.propose arguments")
		}
		if input.ID == "" || input.ProjectID == "" || input.Title == "" || len(input.Actions) == 0 {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing assistant proposal arguments")
		}
		input.RequiresConfirmation = true
		record := ProjectCairnlineSidecarAssistantProposalRecordItem{
			ID:        input.ID,
			ProjectID: input.ProjectID,
			Source:    firstNonEmpty(input.Source, "assistant"),
			Proposal:  input,
			Status:    "proposed",
		}
		state.assistantProposals[record.ID] = record
		return mcp.CallToolResult{Content: mcp.TextContent("Assistant proposal " + record.ID + ": [proposed] " + input.Title), StructuredContent: mustRawJSON(record)}, nil
	case "assistant.proposals.list":
		var input struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid assistant.proposals.list arguments")
		}
		items := cairnlineSidecarFixtureProjectAssistantProposals(state, input.ProjectID)
		return cairnlineSidecarFixtureListResult(mode, fmt.Sprintf("Assistant proposals (%d)", len(items)), items)
	case "assistant.proposals.get":
		var input struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid assistant.proposals.get arguments")
		}
		record, ok := state.assistantProposals[input.ID]
		if !ok {
			record, ok = cairnlineSidecarFixtureDefaultAssistantProposal(input.ID)
		}
		if !ok {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture assistant proposal not found: " + input.ID), IsError: true}, nil
		}
		if cairnlineSidecarFixtureModeHas(mode, "assistant.proposals.get-id-mismatch") {
			record.ID = "pa_fixture_other"
			record.Proposal.ID = "pa_fixture_other"
		}
		result := mcp.CallToolResult{Content: mcp.TextContent("Assistant proposal " + input.ID + ": [" + record.Status + "] " + record.Proposal.Title)}
		if !cairnlineSidecarFixtureTextOnly(mode, "assistant.proposals.get") {
			result.StructuredContent = mustRawJSON(record)
		}
		return result, nil
	case "assistant.apply":
		var input struct {
			ProposalID string                                       `json:"proposal_id"`
			Proposal   ProjectCairnlineSidecarAssistantProposalItem `json:"proposal"`
			Confirm    bool                                         `json:"confirm"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid assistant.apply arguments")
		}
		if mode == "assistant-apply-tool-error" && input.Confirm {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture assistant.apply failed"), IsError: true}, nil
		}
		record, ok := state.assistantProposals[input.ProposalID]
		if !ok && input.Proposal.ID != "" {
			record = ProjectCairnlineSidecarAssistantProposalRecordItem{
				ID:        input.Proposal.ID,
				ProjectID: input.Proposal.ProjectID,
				Source:    firstNonEmpty(input.Proposal.Source, "assistant"),
				Proposal:  input.Proposal,
				Status:    "proposed",
			}
			input.ProposalID = record.ID
			ok = true
		}
		if !ok {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture assistant proposal not found: " + input.ProposalID), IsError: true}, nil
		}
		if !input.Confirm {
			result := ProjectCairnlineSidecarAssistantApplyResultItem{
				ProposalID:       record.ID,
				Status:           "needs_confirmation",
				Applied:          false,
				Confirmed:        false,
				TotalActionCount: len(record.Proposal.Actions),
			}
			record.Status = "needs_confirmation"
			record.LatestResult = &result
			record.ApplyAttempts = append(record.ApplyAttempts, ProjectCairnlineSidecarAssistantApplyAttemptItem{ID: "attempt_" + record.ID + "_needs_confirmation", ProposalID: record.ID, Status: result.Status, Confirmed: false, Result: result})
			state.assistantProposals[record.ID] = record
			return mcp.CallToolResult{Content: mcp.TextContent("Assistant apply " + record.ID + ": needs_confirmation"), StructuredContent: mustRawJSON(result), IsError: true}, nil
		}
		result := cairnlineSidecarFixtureApplyAssistantProposal(state, record)
		record.Status = result.Status
		record.LatestResult = &result
		record.ApplyAttempts = append(record.ApplyAttempts, ProjectCairnlineSidecarAssistantApplyAttemptItem{ID: "attempt_" + record.ID + "_applied", ProposalID: record.ID, Status: result.Status, Confirmed: true, Result: result})
		if result.Applied {
			record.AppliedAt = "fixture-applied-at"
		}
		state.assistantProposals[record.ID] = record
		return mcp.CallToolResult{Content: mcp.TextContent(fmt.Sprintf("Assistant apply %s: %s actions=%d/%d", record.ID, result.Status, result.AppliedActionCount, result.TotalActionCount)), StructuredContent: mustRawJSON(result)}, nil
	case "roles.create":
		var input struct {
			ProjectID            string   `json:"project_id"`
			Name                 string   `json:"name"`
			Description          string   `json:"description"`
			Instructions         string   `json:"instructions"`
			DefaultSkillIDs      []string `json:"default_skill_ids"`
			DefaultExecutionMode string   `json:"default_execution_mode"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid roles.create arguments")
		}
		if input.ProjectID == "" || input.Name == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing role create arguments")
		}
		state.roleSequence++
		id := fmt.Sprintf("role_write_fixture_%d", state.roleSequence)
		role := ProjectCairnlineSidecarRoleItem{
			ProjectID:            input.ProjectID,
			ID:                   id,
			Name:                 input.Name,
			Description:          input.Description,
			Instructions:         input.Instructions,
			DefaultSkillIDs:      input.DefaultSkillIDs,
			DefaultExecutionMode: input.DefaultExecutionMode,
		}
		cairnlineSidecarFixtureEnsureRoles(state, input.ProjectID)[id] = role
		return mcp.CallToolResult{Content: mcp.TextContent("Created role " + id + ": " + input.Name)}, nil
	case "work_items.create":
		var input struct {
			ProjectID   string `json:"project_id"`
			Title       string `json:"title"`
			Brief       string `json:"brief"`
			OwnerRoleID string `json:"owner_role_id"`
			RootID      string `json:"root_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid work_items.create arguments")
		}
		if input.ProjectID == "" || input.Title == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing work item create arguments")
		}
		state.workSequence++
		id := fmt.Sprintf("work_write_fixture_%d", state.workSequence)
		item := ProjectCairnlineSidecarWorkItem{
			ProjectID:   input.ProjectID,
			ID:          id,
			Title:       input.Title,
			Brief:       input.Brief,
			Status:      "open",
			Priority:    "normal",
			OwnerRoleID: input.OwnerRoleID,
			RootID:      input.RootID,
		}
		cairnlineSidecarFixtureEnsureWorkItems(state, input.ProjectID)[id] = item
		return mcp.CallToolResult{Content: mcp.TextContent("Created work item " + id + ": " + input.Title)}, nil
	case "assignments.create":
		var input struct {
			ProjectID        string   `json:"project_id"`
			WorkItemID       string   `json:"work_item_id"`
			RoleID           string   `json:"role_id"`
			RootID           string   `json:"root_id"`
			ExecutionMode    string   `json:"execution_mode"`
			DesiredAgentKind string   `json:"desired_agent_kind"`
			SkillIDs         []string `json:"skill_ids"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid assignments.create arguments")
		}
		if input.ProjectID == "" || input.WorkItemID == "" || input.RoleID == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing assignment create arguments")
		}
		state.assignmentSequence++
		id := fmt.Sprintf("asg_write_fixture_%d", state.assignmentSequence)
		assignment := ProjectCairnlineSidecarAssignmentItem{
			ProjectID:     input.ProjectID,
			ID:            id,
			WorkItemID:    input.WorkItemID,
			RoleID:        input.RoleID,
			RootID:        input.RootID,
			ExecutionMode: input.ExecutionMode,
			DesiredAgent: ProjectCairnlineSidecarDesiredAgentItem{
				Kind:     input.DesiredAgentKind,
				SkillIDs: append([]string(nil), input.SkillIDs...),
			},
			Status: "queued",
		}
		cairnlineSidecarFixtureEnsureAssignments(state, input.ProjectID)[id] = assignment
		return mcp.CallToolResult{Content: mcp.TextContent("Created assignment " + id)}, nil
	case "roots.list":
		var input struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid roots.list arguments")
		}
		roots := cairnlineSidecarFixtureProjectRoots(state, input.ProjectID)
		return cairnlineSidecarFixtureListResult(mode, fmt.Sprintf("Roots for %s (%d)", input.ProjectID, len(roots)), roots)
	case "roots.create":
		var input struct {
			ProjectID string `json:"project_id"`
			ID        string `json:"id"`
			Path      string `json:"path"`
			Kind      string `json:"kind"`
			GitRemote string `json:"git_remote"`
			GitBranch string `json:"git_branch"`
			Active    *bool  `json:"active"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid roots.create arguments")
		}
		if input.ProjectID == "" || input.ID == "" || input.Path == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing root create arguments")
		}
		active := true
		if input.Active != nil {
			active = *input.Active
		}
		root := ProjectCairnlineSidecarRootItem{
			ID:        input.ID,
			Path:      input.Path,
			Kind:      input.Kind,
			GitRemote: input.GitRemote,
			GitBranch: input.GitBranch,
			Active:    active,
		}
		cairnlineSidecarFixtureEnsureRoots(state, input.ProjectID)[input.ID] = root
		return mcp.CallToolResult{Content: mcp.TextContent("Created root " + input.ID), StructuredContent: mustRawJSON(root)}, nil
	case "roots.update":
		if mode == "root-update-tool-error" {
			return mcp.CallToolResult{
				Content: mcp.TextContent("fixture roots.update failed"),
				IsError: true,
			}, nil
		}
		var input struct {
			ProjectID string  `json:"project_id"`
			RootID    string  `json:"root_id"`
			Path      *string `json:"path"`
			Kind      *string `json:"kind"`
			GitRemote *string `json:"git_remote"`
			GitBranch *string `json:"git_branch"`
			Active    *bool   `json:"active"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid roots.update arguments")
		}
		roots := cairnlineSidecarFixtureEnsureRoots(state, input.ProjectID)
		root, ok := roots[input.RootID]
		if !ok {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture root not found: " + input.RootID), IsError: true}, nil
		}
		if input.Path != nil {
			root.Path = *input.Path
		}
		if input.Kind != nil {
			root.Kind = *input.Kind
		}
		if input.GitRemote != nil {
			root.GitRemote = *input.GitRemote
		}
		if input.GitBranch != nil {
			root.GitBranch = *input.GitBranch
		}
		if input.Active != nil {
			root.Active = *input.Active
		}
		roots[input.RootID] = root
		return mcp.CallToolResult{Content: mcp.TextContent("Updated root " + input.RootID), StructuredContent: mustRawJSON(root)}, nil
	case "roots.delete":
		var input struct {
			ProjectID string `json:"project_id"`
			RootID    string `json:"root_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid roots.delete arguments")
		}
		roots := cairnlineSidecarFixtureEnsureRoots(state, input.ProjectID)
		root, ok := roots[input.RootID]
		if !ok {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture root not found: " + input.RootID), IsError: true}, nil
		}
		delete(roots, input.RootID)
		return mcp.CallToolResult{Content: mcp.TextContent("Deleted root " + input.RootID), StructuredContent: mustRawJSON(root)}, nil
	case "context_sources.list":
		var input struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid context_sources.list arguments")
		}
		sources := cairnlineSidecarFixtureProjectSources(state, input.ProjectID)
		return cairnlineSidecarFixtureListResult(mode, fmt.Sprintf("Context sources for %s (%d)", input.ProjectID, len(sources)), sources)
	case "context_sources.create":
		var input struct {
			ProjectID      string            `json:"project_id"`
			ID             string            `json:"id"`
			Kind           string            `json:"kind"`
			Title          string            `json:"title"`
			Locator        string            `json:"locator"`
			Enabled        *bool             `json:"enabled"`
			Format         string            `json:"format"`
			Scope          string            `json:"scope"`
			TrustLabel     string            `json:"trust_label"`
			SourceCategory string            `json:"source_category"`
			Metadata       map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid context_sources.create arguments")
		}
		if input.ProjectID == "" || input.ID == "" || input.Title == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing context source create arguments")
		}
		enabled := true
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		source := ProjectCairnlineSidecarSourceItem{
			ID:             input.ID,
			Kind:           input.Kind,
			Title:          input.Title,
			Locator:        input.Locator,
			Enabled:        enabled,
			Format:         input.Format,
			Scope:          input.Scope,
			TrustLabel:     input.TrustLabel,
			SourceCategory: input.SourceCategory,
			Metadata:       input.Metadata,
		}
		cairnlineSidecarFixtureEnsureSources(state, input.ProjectID)[input.ID] = source
		return mcp.CallToolResult{Content: mcp.TextContent("Created context source " + input.ID), StructuredContent: mustRawJSON(source)}, nil
	case "context_sources.update":
		var input struct {
			ProjectID      string            `json:"project_id"`
			SourceID       string            `json:"source_id"`
			Kind           *string           `json:"kind"`
			Title          *string           `json:"title"`
			Locator        *string           `json:"locator"`
			Enabled        *bool             `json:"enabled"`
			Format         *string           `json:"format"`
			Scope          *string           `json:"scope"`
			TrustLabel     *string           `json:"trust_label"`
			SourceCategory *string           `json:"source_category"`
			Metadata       map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid context_sources.update arguments")
		}
		sources := cairnlineSidecarFixtureEnsureSources(state, input.ProjectID)
		source, ok := sources[input.SourceID]
		if !ok {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture context source not found: " + input.SourceID), IsError: true}, nil
		}
		if input.Kind != nil {
			source.Kind = *input.Kind
		}
		if input.Title != nil {
			source.Title = *input.Title
		}
		if input.Locator != nil {
			source.Locator = *input.Locator
		}
		if input.Enabled != nil {
			source.Enabled = *input.Enabled
		}
		if input.Format != nil {
			source.Format = *input.Format
		}
		if input.Scope != nil {
			source.Scope = *input.Scope
		}
		if input.TrustLabel != nil {
			source.TrustLabel = *input.TrustLabel
		}
		if input.SourceCategory != nil {
			source.SourceCategory = *input.SourceCategory
		}
		if input.Metadata != nil {
			source.Metadata = input.Metadata
		}
		sources[input.SourceID] = source
		return mcp.CallToolResult{Content: mcp.TextContent("Updated context source " + input.SourceID), StructuredContent: mustRawJSON(source)}, nil
	case "context_sources.delete":
		var input struct {
			ProjectID string `json:"project_id"`
			SourceID  string `json:"source_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid context_sources.delete arguments")
		}
		sources := cairnlineSidecarFixtureEnsureSources(state, input.ProjectID)
		source, ok := sources[input.SourceID]
		if !ok {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture context source not found: " + input.SourceID), IsError: true}, nil
		}
		delete(sources, input.SourceID)
		return mcp.CallToolResult{Content: mcp.TextContent("Deleted context source " + input.SourceID), StructuredContent: mustRawJSON(source)}, nil
	case "artifacts.create":
		var input struct {
			ProjectID      string `json:"project_id"`
			WorkItemID     string `json:"work_item_id"`
			AssignmentID   string `json:"assignment_id"`
			Kind           string `json:"kind"`
			Title          string `json:"title"`
			Body           string `json:"body"`
			AuthorRoleID   string `json:"author_role_id"`
			ProvenanceKind string `json:"provenance_kind"`
			TrustLabel     string `json:"trust_label"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid artifacts.create arguments")
		}
		if input.ProjectID == "" || input.WorkItemID == "" || input.Kind == "" || input.Body == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing artifact create arguments")
		}
		state.artifactSequence++
		id := fmt.Sprintf("artifact_write_fixture_%d", state.artifactSequence)
		artifact := ProjectCairnlineSidecarArtifactItem{
			ProjectID:      input.ProjectID,
			ID:             id,
			WorkItemID:     input.WorkItemID,
			AssignmentID:   input.AssignmentID,
			Kind:           input.Kind,
			Title:          input.Title,
			Body:           input.Body,
			AuthorRoleID:   input.AuthorRoleID,
			ProvenanceKind: input.ProvenanceKind,
			TrustLabel:     input.TrustLabel,
		}
		cairnlineSidecarFixtureEnsureArtifacts(state, input.ProjectID)[id] = artifact
		return mcp.CallToolResult{Content: mcp.TextContent("Created artifact " + id), StructuredContent: mustRawJSON(artifact)}, nil
	case "artifacts.list":
		projectID, workItemID := cairnlineSidecarFixtureProjectWorkIDs(params.Arguments)
		items := cairnlineSidecarFixtureProjectArtifacts(state, projectID, workItemID)
		if len(items) == 0 && cairnlineSidecarFixtureModeHas(mode, "collaboration-fixture") {
			items = cairnlineSidecarFixtureDefaultArtifacts(projectID, workItemID)
		}
		return cairnlineSidecarFixtureListResult(mode, fmt.Sprintf("Artifacts for %s (%d)", workItemID, len(items)), items)
	case "artifacts.get":
		var input struct {
			ProjectID  string `json:"project_id"`
			WorkItemID string `json:"work_item_id"`
			ArtifactID string `json:"artifact_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid artifacts.get arguments")
		}
		artifact, ok := state.artifacts[input.ProjectID][input.ArtifactID]
		if !ok || artifact.WorkItemID != input.WorkItemID {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture artifact not found: " + input.ArtifactID), IsError: true}, nil
		}
		return mcp.CallToolResult{Content: mcp.TextContent("Artifact " + input.ArtifactID), StructuredContent: mustRawJSON(artifact)}, nil
	case "evidence.record":
		if mode == "evidence-record-tool-error" {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture evidence.record failed"), IsError: true}, nil
		}
		var input struct {
			ProjectID    string `json:"project_id"`
			WorkItemID   string `json:"work_item_id"`
			AssignmentID string `json:"assignment_id"`
			Title        string `json:"title"`
			Body         string `json:"body"`
			Locator      string `json:"locator"`
			SourceKind   string `json:"source_kind"`
			ExternalID   string `json:"external_id"`
			Provider     string `json:"provider"`
			TrustLabel   string `json:"trust_label"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid evidence.record arguments")
		}
		if input.ProjectID == "" || input.WorkItemID == "" || input.Title == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing evidence record arguments")
		}
		state.evidenceSequence++
		id := fmt.Sprintf("evidence_write_fixture_%d", state.evidenceSequence)
		item := ProjectCairnlineSidecarEvidenceItem{
			ProjectID:    input.ProjectID,
			ID:           id,
			WorkItemID:   input.WorkItemID,
			AssignmentID: input.AssignmentID,
			Title:        input.Title,
			Body:         input.Body,
			Locator:      input.Locator,
			SourceKind:   input.SourceKind,
			ExternalID:   input.ExternalID,
			Provider:     input.Provider,
			TrustLabel:   input.TrustLabel,
		}
		cairnlineSidecarFixtureEnsureEvidence(state, input.ProjectID)[id] = item
		return mcp.CallToolResult{Content: mcp.TextContent("Recorded evidence " + id)}, nil
	case "evidence.list":
		projectID, workItemID := cairnlineSidecarFixtureProjectWorkIDs(params.Arguments)
		items := cairnlineSidecarFixtureProjectEvidence(state, projectID, workItemID)
		if len(items) == 0 && cairnlineSidecarFixtureModeHas(mode, "collaboration-fixture") {
			items = cairnlineSidecarFixtureDefaultEvidence(projectID, workItemID)
		}
		return cairnlineSidecarFixtureListResult(mode, fmt.Sprintf("Evidence for %s (%d)", workItemID, len(items)), items)
	case "evidence.get":
		var input struct {
			ProjectID  string `json:"project_id"`
			WorkItemID string `json:"work_item_id"`
			EvidenceID string `json:"evidence_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid evidence.get arguments")
		}
		item, ok := state.evidence[input.ProjectID][input.EvidenceID]
		if !ok || item.WorkItemID != input.WorkItemID {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture evidence not found: " + input.EvidenceID), IsError: true}, nil
		}
		return mcp.CallToolResult{Content: mcp.TextContent("Evidence " + input.EvidenceID), StructuredContent: mustRawJSON(item)}, nil
	case "reviews.record":
		if mode == "review-record-tool-error" {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture reviews.record failed"), IsError: true}, nil
		}
		var input struct {
			ProjectID      string `json:"project_id"`
			WorkItemID     string `json:"work_item_id"`
			AssignmentID   string `json:"assignment_id"`
			ReviewerRoleID string `json:"reviewer_role_id"`
			Title          string `json:"title"`
			Body           string `json:"body"`
			Verdict        string `json:"verdict"`
			Risk           string `json:"risk"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid reviews.record arguments")
		}
		if input.ProjectID == "" || input.WorkItemID == "" || input.Body == "" || input.Verdict == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing review record arguments")
		}
		state.reviewSequence++
		id := fmt.Sprintf("review_write_fixture_%d", state.reviewSequence)
		review := ProjectCairnlineSidecarReviewItem{
			ProjectID:      input.ProjectID,
			ID:             id,
			WorkItemID:     input.WorkItemID,
			AssignmentID:   input.AssignmentID,
			ReviewerRoleID: input.ReviewerRoleID,
			Title:          input.Title,
			Body:           input.Body,
			Verdict:        input.Verdict,
			Risk:           input.Risk,
			Status:         "open",
		}
		cairnlineSidecarFixtureEnsureReviews(state, input.ProjectID)[id] = review
		return mcp.CallToolResult{Content: mcp.TextContent("Recorded review " + id)}, nil
	case "reviews.list":
		projectID, workItemID := cairnlineSidecarFixtureProjectWorkIDs(params.Arguments)
		items := cairnlineSidecarFixtureProjectReviews(state, projectID, workItemID)
		if len(items) == 0 && cairnlineSidecarFixtureModeHas(mode, "collaboration-fixture") {
			items = cairnlineSidecarFixtureDefaultReviews(projectID, workItemID)
		}
		return cairnlineSidecarFixtureListResult(mode, fmt.Sprintf("Reviews for %s (%d)", workItemID, len(items)), items)
	case "reviews.get":
		var input struct {
			ProjectID  string `json:"project_id"`
			WorkItemID string `json:"work_item_id"`
			ReviewID   string `json:"review_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid reviews.get arguments")
		}
		review, ok := state.reviews[input.ProjectID][input.ReviewID]
		if !ok || review.WorkItemID != input.WorkItemID {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture review not found: " + input.ReviewID), IsError: true}, nil
		}
		return mcp.CallToolResult{Content: mcp.TextContent("Review " + input.ReviewID), StructuredContent: mustRawJSON(review)}, nil
	case "handoffs.create":
		var input struct {
			ProjectID             string   `json:"project_id"`
			WorkItemID            string   `json:"work_item_id"`
			SourceAssignmentID    string   `json:"source_assignment_id"`
			SourceRunID           string   `json:"source_run_id"`
			SourceChatSessionID   string   `json:"source_chat_session_id"`
			SourceMessageID       string   `json:"source_message_id"`
			FromRoleID            string   `json:"from_role_id"`
			ToRoleID              string   `json:"to_role_id"`
			TargetAssignmentID    string   `json:"target_assignment_id"`
			TargetWorkItemID      string   `json:"target_work_item_id"`
			Title                 string   `json:"title"`
			Body                  string   `json:"body"`
			RecommendedNextAction string   `json:"recommended_next_action"`
			LinkedArtifactIDs     []string `json:"linked_artifact_ids"`
			LinkedMemoryIDs       []string `json:"linked_memory_ids"`
			ContextRefs           []string `json:"context_refs"`
			Status                string   `json:"status"`
			ProvenanceKind        string   `json:"provenance_kind"`
			TrustLabel            string   `json:"trust_label"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid handoffs.create arguments")
		}
		if input.ProjectID == "" || input.WorkItemID == "" || input.Title == "" || input.Body == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing handoff create arguments")
		}
		state.handoffSequence++
		id := fmt.Sprintf("handoff_write_fixture_%d", state.handoffSequence)
		status := firstNonEmpty(input.Status, "open")
		handoff := ProjectCairnlineSidecarHandoffItem{
			ProjectID:             input.ProjectID,
			ID:                    id,
			WorkItemID:            input.WorkItemID,
			SourceAssignmentID:    input.SourceAssignmentID,
			SourceRunID:           input.SourceRunID,
			SourceChatSessionID:   input.SourceChatSessionID,
			SourceMessageID:       input.SourceMessageID,
			FromRoleID:            input.FromRoleID,
			ToRoleID:              input.ToRoleID,
			TargetAssignmentID:    input.TargetAssignmentID,
			TargetWorkItemID:      input.TargetWorkItemID,
			Title:                 input.Title,
			Body:                  input.Body,
			RecommendedNextAction: input.RecommendedNextAction,
			LinkedArtifactIDs:     input.LinkedArtifactIDs,
			LinkedMemoryIDs:       input.LinkedMemoryIDs,
			ContextRefs:           input.ContextRefs,
			Status:                status,
			ProvenanceKind:        input.ProvenanceKind,
			TrustLabel:            input.TrustLabel,
		}
		cairnlineSidecarFixtureEnsureHandoffs(state, input.ProjectID)[id] = handoff
		return mcp.CallToolResult{Content: mcp.TextContent("Created handoff " + id)}, nil
	case "handoffs.list":
		projectID, workItemID := cairnlineSidecarFixtureProjectWorkIDs(params.Arguments)
		items := cairnlineSidecarFixtureProjectHandoffs(state, projectID, workItemID)
		if len(items) == 0 && cairnlineSidecarFixtureModeHas(mode, "collaboration-fixture") {
			items = cairnlineSidecarFixtureDefaultHandoffs(projectID, workItemID)
		}
		return cairnlineSidecarFixtureListResult(mode, fmt.Sprintf("Handoffs for %s (%d)", workItemID, len(items)), items)
	case "handoffs.get":
		var input struct {
			ProjectID  string `json:"project_id"`
			WorkItemID string `json:"work_item_id"`
			HandoffID  string `json:"handoff_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid handoffs.get arguments")
		}
		handoff, ok := state.handoffs[input.ProjectID][input.HandoffID]
		if !ok || handoff.WorkItemID != input.WorkItemID {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture handoff not found: " + input.HandoffID), IsError: true}, nil
		}
		return mcp.CallToolResult{Content: mcp.TextContent("Handoff " + input.HandoffID), StructuredContent: mustRawJSON(handoff)}, nil
	case "memory_entries.create":
		var input struct {
			ProjectID  string `json:"project_id"`
			Title      string `json:"title"`
			Body       string `json:"body"`
			TrustLabel string `json:"trust_label"`
			SourceKind string `json:"source_kind"`
			SourceID   string `json:"source_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid memory_entries.create arguments")
		}
		if input.ProjectID == "" || input.Title == "" || input.Body == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing memory entry create arguments")
		}
		state.memorySequence++
		id := fmt.Sprintf("mem_write_fixture_%d", state.memorySequence)
		entry := ProjectCairnlineSidecarMemoryEntryItem{
			ProjectID:  input.ProjectID,
			ID:         id,
			Title:      input.Title,
			Body:       input.Body,
			TrustLabel: firstNonEmpty(input.TrustLabel, "operator_memory"),
			SourceKind: input.SourceKind,
			SourceID:   input.SourceID,
			Enabled:    true,
		}
		cairnlineSidecarFixtureEnsureMemoryEntries(state, input.ProjectID)[id] = entry
		return mcp.CallToolResult{Content: mcp.TextContent("Created memory entry " + id + ": " + input.Title)}, nil
	case "memory_entries.list":
		var input struct {
			ProjectID       string `json:"project_id"`
			IncludeDisabled bool   `json:"include_disabled"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid memory_entries.list arguments")
		}
		items := cairnlineSidecarFixtureProjectMemoryEntries(state, input.ProjectID, input.IncludeDisabled)
		if mode == "memory-fixture" && len(items) == 0 {
			items = cairnlineSidecarFixtureDefaultMemoryEntries(input.ProjectID, input.IncludeDisabled)
		}
		return cairnlineSidecarFixtureListResult(mode, fmt.Sprintf("Memory entries for %s (%d)", input.ProjectID, len(items)), items)
	case "memory_entries.get":
		var input struct {
			ProjectID string `json:"project_id"`
			MemoryID  string `json:"memory_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid memory_entries.get arguments")
		}
		entry, ok := state.memoryEntries[input.ProjectID][input.MemoryID]
		if !ok {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture memory entry not found: " + input.MemoryID), IsError: true}, nil
		}
		return mcp.CallToolResult{Content: mcp.TextContent("Memory entry " + input.MemoryID), StructuredContent: mustRawJSON(entry)}, nil
	case "memory_entries.update":
		var input struct {
			ProjectID  string  `json:"project_id"`
			MemoryID   string  `json:"memory_id"`
			Title      *string `json:"title"`
			Body       *string `json:"body"`
			TrustLabel *string `json:"trust_label"`
			SourceKind *string `json:"source_kind"`
			SourceID   *string `json:"source_id"`
			Enabled    *bool   `json:"enabled"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid memory_entries.update arguments")
		}
		entries := cairnlineSidecarFixtureEnsureMemoryEntries(state, input.ProjectID)
		entry, ok := entries[input.MemoryID]
		if !ok {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture memory entry not found: " + input.MemoryID), IsError: true}, nil
		}
		if input.Title != nil {
			entry.Title = *input.Title
		}
		if input.Body != nil {
			entry.Body = *input.Body
		}
		if input.TrustLabel != nil {
			entry.TrustLabel = *input.TrustLabel
		}
		if input.SourceKind != nil {
			entry.SourceKind = *input.SourceKind
		}
		if input.SourceID != nil {
			entry.SourceID = *input.SourceID
		}
		if input.Enabled != nil {
			entry.Enabled = *input.Enabled
		}
		entries[input.MemoryID] = entry
		return mcp.CallToolResult{Content: mcp.TextContent("Updated memory entry " + input.MemoryID + ": " + entry.Title)}, nil
	case "memory_entries.delete":
		var input struct {
			ProjectID string `json:"project_id"`
			MemoryID  string `json:"memory_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid memory_entries.delete arguments")
		}
		delete(cairnlineSidecarFixtureEnsureMemoryEntries(state, input.ProjectID), input.MemoryID)
		return mcp.CallToolResult{Content: mcp.TextContent("Deleted memory entry " + input.MemoryID)}, nil
	case "memory_candidates.create":
		var input struct {
			ProjectID           string                                            `json:"project_id"`
			Title               string                                            `json:"title"`
			Body                string                                            `json:"body"`
			SuggestedKind       string                                            `json:"suggested_kind"`
			SuggestedTrustLabel string                                            `json:"suggested_trust_label"`
			SuggestedSourceKind string                                            `json:"suggested_source_kind"`
			SuggestedSourceID   string                                            `json:"suggested_source_id"`
			SourceRefs          []ProjectCairnlineSidecarMemoryCandidateSourceRef `json:"source_refs"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid memory_candidates.create arguments")
		}
		if input.ProjectID == "" || input.Title == "" || input.Body == "" {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "missing memory candidate create arguments")
		}
		state.candidateSequence++
		id := fmt.Sprintf("memcand_write_fixture_%d", state.candidateSequence)
		candidate := ProjectCairnlineSidecarMemoryCandidateItem{
			ProjectID:           input.ProjectID,
			ID:                  id,
			Title:               input.Title,
			Body:                input.Body,
			SuggestedKind:       input.SuggestedKind,
			SuggestedTrustLabel: firstNonEmpty(input.SuggestedTrustLabel, "generated_summary"),
			SuggestedSourceKind: firstNonEmpty(input.SuggestedSourceKind, "generated"),
			SuggestedSourceID:   input.SuggestedSourceID,
			SourceRefs:          input.SourceRefs,
			Status:              "pending",
		}
		cairnlineSidecarFixtureEnsureMemoryCandidates(state, input.ProjectID)[id] = candidate
		return mcp.CallToolResult{Content: mcp.TextContent("Created memory candidate " + id + ": " + input.Title)}, nil
	case "memory_candidates.list":
		var input struct {
			ProjectID       string `json:"project_id"`
			IncludeResolved bool   `json:"include_resolved"`
			Status          string `json:"status"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid memory_candidates.list arguments")
		}
		items := cairnlineSidecarFixtureProjectMemoryCandidates(state, input.ProjectID, input.IncludeResolved, input.Status)
		if mode == "memory-fixture" && len(items) == 0 {
			items = cairnlineSidecarFixtureDefaultMemoryCandidates(input.ProjectID, input.IncludeResolved, input.Status)
		}
		return cairnlineSidecarFixtureListResult(mode, fmt.Sprintf("Memory candidates for %s (%d)", input.ProjectID, len(items)), items)
	case "memory_candidates.get":
		var input struct {
			ProjectID   string `json:"project_id"`
			CandidateID string `json:"candidate_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid memory_candidates.get arguments")
		}
		candidate, ok := state.memoryCandidates[input.ProjectID][input.CandidateID]
		if !ok {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture memory candidate not found: " + input.CandidateID), IsError: true}, nil
		}
		return mcp.CallToolResult{Content: mcp.TextContent("Memory candidate " + input.CandidateID), StructuredContent: mustRawJSON(candidate)}, nil
	case "memory_candidates.promote":
		if mode == "memory-candidate-promote-tool-error" {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture memory_candidates.promote failed"), IsError: true}, nil
		}
		var input struct {
			ProjectID   string `json:"project_id"`
			CandidateID string `json:"candidate_id"`
			Title       string `json:"title"`
			Body        string `json:"body"`
			TrustLabel  string `json:"trust_label"`
			SourceKind  string `json:"source_kind"`
			SourceID    string `json:"source_id"`
			Enabled     *bool  `json:"enabled"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid memory_candidates.promote arguments")
		}
		candidates := cairnlineSidecarFixtureEnsureMemoryCandidates(state, input.ProjectID)
		candidate, ok := candidates[input.CandidateID]
		if !ok {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture memory candidate not found: " + input.CandidateID), IsError: true}, nil
		}
		enabled := true
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		state.memorySequence++
		entryID := fmt.Sprintf("mem_write_fixture_%d", state.memorySequence)
		entry := ProjectCairnlineSidecarMemoryEntryItem{
			ProjectID:  input.ProjectID,
			ID:         entryID,
			Title:      firstNonEmpty(input.Title, candidate.Title),
			Body:       firstNonEmpty(input.Body, candidate.Body),
			TrustLabel: firstNonEmpty(input.TrustLabel, candidate.SuggestedTrustLabel, "generated_summary"),
			SourceKind: firstNonEmpty(input.SourceKind, candidate.SuggestedSourceKind, "generated"),
			SourceID:   firstNonEmpty(input.SourceID, candidate.SuggestedSourceID),
			Enabled:    enabled,
		}
		cairnlineSidecarFixtureEnsureMemoryEntries(state, input.ProjectID)[entryID] = entry
		candidate.Status = "promoted"
		candidate.PromotedMemoryID = entryID
		candidates[input.CandidateID] = candidate
		return mcp.CallToolResult{Content: mcp.TextContent("Promoted memory candidate " + input.CandidateID + " to memory entry " + entryID), StructuredContent: mustRawJSON(candidate)}, nil
	case "memory_candidates.reject":
		var input struct {
			ProjectID   string `json:"project_id"`
			CandidateID string `json:"candidate_id"`
			Reason      string `json:"reason"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid memory_candidates.reject arguments")
		}
		candidates := cairnlineSidecarFixtureEnsureMemoryCandidates(state, input.ProjectID)
		candidate, ok := candidates[input.CandidateID]
		if !ok {
			return mcp.CallToolResult{Content: mcp.TextContent("fixture memory candidate not found: " + input.CandidateID), IsError: true}, nil
		}
		candidate.Status = "rejected"
		candidate.StatusReason = input.Reason
		candidates[input.CandidateID] = candidate
		return mcp.CallToolResult{Content: mcp.TextContent("Rejected memory candidate " + input.CandidateID), StructuredContent: mustRawJSON(candidate)}, nil
	case "memory_candidates.delete":
		var input struct {
			ProjectID   string `json:"project_id"`
			CandidateID string `json:"candidate_id"`
		}
		if err := json.Unmarshal(params.Arguments, &input); err != nil {
			return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid memory_candidates.delete arguments")
		}
		delete(cairnlineSidecarFixtureEnsureMemoryCandidates(state, input.ProjectID), input.CandidateID)
		return mcp.CallToolResult{Content: mcp.TextContent("Deleted memory candidate " + input.CandidateID)}, nil
	default:
		return mcp.CallToolResult{}, mcp.NewError(mcp.ErrCodeMethodNotFound, params.Name)
	}
}

func cairnlineSidecarFixtureProject(mode, id string) ProjectCairnlineSidecarProjectItem {
	project := ProjectCairnlineSidecarProjectItem{
		ID:            id,
		Name:          "Fixture Project",
		Description:   "Structured fixture project",
		DefaultRootID: "root_fixture",
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
	if cairnlineSidecarFixtureModeHas(mode, "temp-root") {
		for i := range project.Roots {
			project.Roots[i].Path = os.TempDir()
		}
	}
	return project
}

func cairnlineSidecarFixtureProjects(mode string, state *cairnlineSidecarFixtureState) []ProjectCairnlineSidecarProjectItem {
	projects := []ProjectCairnlineSidecarProjectItem{cairnlineSidecarFixtureProject(mode, "proj_fixture")}
	for _, project := range state.projects {
		project.Roots = cairnlineSidecarFixtureProjectRoots(state, project.ID)
		project.ContextSources = cairnlineSidecarFixtureProjectSources(state, project.ID)
		projects = append(projects, project)
	}
	return projects
}

func cairnlineSidecarFixtureEnsureRoots(state *cairnlineSidecarFixtureState, projectID string) map[string]ProjectCairnlineSidecarRootItem {
	if state.roots[projectID] == nil {
		state.roots[projectID] = make(map[string]ProjectCairnlineSidecarRootItem)
	}
	return state.roots[projectID]
}

func cairnlineSidecarFixtureProjectRoots(state *cairnlineSidecarFixtureState, projectID string) []ProjectCairnlineSidecarRootItem {
	rootsByID := state.roots[projectID]
	roots := make([]ProjectCairnlineSidecarRootItem, 0, len(rootsByID))
	for _, root := range rootsByID {
		roots = append(roots, root)
	}
	return roots
}

func cairnlineSidecarFixtureEnsureSources(state *cairnlineSidecarFixtureState, projectID string) map[string]ProjectCairnlineSidecarSourceItem {
	if state.contextSources[projectID] == nil {
		state.contextSources[projectID] = make(map[string]ProjectCairnlineSidecarSourceItem)
	}
	return state.contextSources[projectID]
}

func cairnlineSidecarFixtureProjectSources(state *cairnlineSidecarFixtureState, projectID string) []ProjectCairnlineSidecarSourceItem {
	sourcesByID := state.contextSources[projectID]
	sources := make([]ProjectCairnlineSidecarSourceItem, 0, len(sourcesByID))
	for _, source := range sourcesByID {
		sources = append(sources, source)
	}
	return sources
}

func cairnlineSidecarFixtureEnsureRoles(state *cairnlineSidecarFixtureState, projectID string) map[string]ProjectCairnlineSidecarRoleItem {
	if state.roles[projectID] == nil {
		state.roles[projectID] = make(map[string]ProjectCairnlineSidecarRoleItem)
	}
	return state.roles[projectID]
}

func cairnlineSidecarFixtureProjectRoles(state *cairnlineSidecarFixtureState, projectID string) []ProjectCairnlineSidecarRoleItem {
	rolesByID := state.roles[projectID]
	roles := make([]ProjectCairnlineSidecarRoleItem, 0, len(rolesByID))
	for _, role := range rolesByID {
		roles = append(roles, role)
	}
	return roles
}

func cairnlineSidecarFixtureRoleName(state *cairnlineSidecarFixtureState, projectID, roleID string) string {
	if role, ok := state.roles[projectID][roleID]; ok {
		return role.Name
	}
	return ""
}

func cairnlineSidecarFixtureEnsureWorkItems(state *cairnlineSidecarFixtureState, projectID string) map[string]ProjectCairnlineSidecarWorkItem {
	if state.workItems[projectID] == nil {
		state.workItems[projectID] = make(map[string]ProjectCairnlineSidecarWorkItem)
	}
	return state.workItems[projectID]
}

func cairnlineSidecarFixtureProjectWorkItems(state *cairnlineSidecarFixtureState, projectID string) []ProjectCairnlineSidecarWorkItem {
	itemsByID := state.workItems[projectID]
	items := make([]ProjectCairnlineSidecarWorkItem, 0, len(itemsByID))
	for _, item := range itemsByID {
		items = append(items, item)
	}
	return items
}

func cairnlineSidecarFixtureWorkItemTitle(state *cairnlineSidecarFixtureState, projectID, workItemID string) string {
	if item, ok := state.workItems[projectID][workItemID]; ok {
		return item.Title
	}
	return ""
}

func cairnlineSidecarFixtureEnsureAssignments(state *cairnlineSidecarFixtureState, projectID string) map[string]ProjectCairnlineSidecarAssignmentItem {
	if state.assignments[projectID] == nil {
		state.assignments[projectID] = make(map[string]ProjectCairnlineSidecarAssignmentItem)
	}
	return state.assignments[projectID]
}

func cairnlineSidecarFixtureProjectAssignments(state *cairnlineSidecarFixtureState, projectID string) []ProjectCairnlineSidecarAssignmentItem {
	assignmentsByID := state.assignments[projectID]
	assignments := make([]ProjectCairnlineSidecarAssignmentItem, 0, len(assignmentsByID))
	for _, assignment := range assignmentsByID {
		assignments = append(assignments, assignment)
	}
	return assignments
}

func cairnlineSidecarFixtureEnsureArtifacts(state *cairnlineSidecarFixtureState, projectID string) map[string]ProjectCairnlineSidecarArtifactItem {
	if state.artifacts[projectID] == nil {
		state.artifacts[projectID] = make(map[string]ProjectCairnlineSidecarArtifactItem)
	}
	return state.artifacts[projectID]
}

func cairnlineSidecarFixtureDefaultArtifacts(projectID, workItemID string) []ProjectCairnlineSidecarArtifactItem {
	workItemID, ok := cairnlineSidecarFixtureDefaultWorkItemID(workItemID)
	if !ok {
		return nil
	}
	return []ProjectCairnlineSidecarArtifactItem{{
		ProjectID:    projectID,
		ID:           "artifact_fixture",
		WorkItemID:   workItemID,
		AssignmentID: "asg_fixture",
		Kind:         "decision_note",
		Title:        "Fixture decision",
		Body:         "Portable fixture artifact.",
		AuthorRoleID: "role_fixture",
		CreatedAt:    "2026-06-01T10:00:00Z",
		UpdatedAt:    "2026-06-01T10:00:00Z",
	}}
}

func cairnlineSidecarFixtureProjectArtifacts(state *cairnlineSidecarFixtureState, projectID, workItemID string) []ProjectCairnlineSidecarArtifactItem {
	artifactsByID := state.artifacts[projectID]
	artifacts := make([]ProjectCairnlineSidecarArtifactItem, 0, len(artifactsByID))
	for _, artifact := range artifactsByID {
		if workItemID == "" || artifact.WorkItemID == workItemID {
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts
}

func cairnlineSidecarFixtureEnsureEvidence(state *cairnlineSidecarFixtureState, projectID string) map[string]ProjectCairnlineSidecarEvidenceItem {
	if state.evidence[projectID] == nil {
		state.evidence[projectID] = make(map[string]ProjectCairnlineSidecarEvidenceItem)
	}
	return state.evidence[projectID]
}

func cairnlineSidecarFixtureDefaultEvidence(projectID, workItemID string) []ProjectCairnlineSidecarEvidenceItem {
	workItemID, ok := cairnlineSidecarFixtureDefaultWorkItemID(workItemID)
	if !ok {
		return nil
	}
	return []ProjectCairnlineSidecarEvidenceItem{{
		ProjectID:    projectID,
		ID:           "evidence_fixture",
		WorkItemID:   workItemID,
		AssignmentID: "asg_fixture",
		Title:        "Fixture evidence",
		Body:         "Portable fixture evidence.",
		Locator:      "https://example.com/evidence",
		SourceKind:   "pull_request",
		ExternalID:   "PR 632",
		Provider:     "github",
		TrustLabel:   "operator_provided",
		CreatedAt:    "2026-06-01T11:00:00Z",
		UpdatedAt:    "2026-06-01T11:00:00Z",
	}}
}

func cairnlineSidecarFixtureProjectEvidence(state *cairnlineSidecarFixtureState, projectID, workItemID string) []ProjectCairnlineSidecarEvidenceItem {
	evidenceByID := state.evidence[projectID]
	items := make([]ProjectCairnlineSidecarEvidenceItem, 0, len(evidenceByID))
	for _, item := range evidenceByID {
		if workItemID == "" || item.WorkItemID == workItemID {
			items = append(items, item)
		}
	}
	return items
}

func cairnlineSidecarFixtureEnsureReviews(state *cairnlineSidecarFixtureState, projectID string) map[string]ProjectCairnlineSidecarReviewItem {
	if state.reviews[projectID] == nil {
		state.reviews[projectID] = make(map[string]ProjectCairnlineSidecarReviewItem)
	}
	return state.reviews[projectID]
}

func cairnlineSidecarFixtureDefaultReviews(projectID, workItemID string) []ProjectCairnlineSidecarReviewItem {
	workItemID, ok := cairnlineSidecarFixtureDefaultWorkItemID(workItemID)
	if !ok {
		return nil
	}
	return []ProjectCairnlineSidecarReviewItem{{
		ProjectID:      projectID,
		ID:             "review_fixture",
		WorkItemID:     workItemID,
		AssignmentID:   "asg_fixture",
		ReviewerRoleID: "role_fixture",
		Title:          "Fixture review",
		Body:           "Portable fixture review.",
		Verdict:        "changes_requested",
		Risk:           "medium",
		Status:         "open",
		CreatedAt:      "2026-06-01T12:00:00Z",
		UpdatedAt:      "2026-06-01T12:00:00Z",
	}}
}

func cairnlineSidecarFixtureProjectReviews(state *cairnlineSidecarFixtureState, projectID, workItemID string) []ProjectCairnlineSidecarReviewItem {
	reviewsByID := state.reviews[projectID]
	reviews := make([]ProjectCairnlineSidecarReviewItem, 0, len(reviewsByID))
	for _, review := range reviewsByID {
		if workItemID == "" || review.WorkItemID == workItemID {
			reviews = append(reviews, review)
		}
	}
	return reviews
}

func cairnlineSidecarFixtureEnsureHandoffs(state *cairnlineSidecarFixtureState, projectID string) map[string]ProjectCairnlineSidecarHandoffItem {
	if state.handoffs[projectID] == nil {
		state.handoffs[projectID] = make(map[string]ProjectCairnlineSidecarHandoffItem)
	}
	return state.handoffs[projectID]
}

func cairnlineSidecarFixtureDefaultHandoffs(projectID, workItemID string) []ProjectCairnlineSidecarHandoffItem {
	workItemID, ok := cairnlineSidecarFixtureDefaultWorkItemID(workItemID)
	if !ok {
		return nil
	}
	return []ProjectCairnlineSidecarHandoffItem{{
		ProjectID:             projectID,
		ID:                    "handoff_fixture",
		WorkItemID:            workItemID,
		SourceAssignmentID:    "asg_fixture",
		FromRoleID:            "role_fixture",
		ToRoleID:              "role_fixture",
		Title:                 "Fixture handoff",
		Body:                  "Portable fixture handoff.",
		RecommendedNextAction: "Review fixture follow-up.",
		LinkedArtifactIDs:     []string{"artifact_fixture"},
		Status:                "open",
		ProvenanceKind:        "agent_draft",
		TrustLabel:            "operator_reviewed",
		CreatedAt:             "2026-06-01T13:00:00Z",
		UpdatedAt:             "2026-06-01T13:00:00Z",
		StatusChangedAt:       "2026-06-01T13:00:00Z",
	}}
}

func cairnlineSidecarFixtureDefaultWorkItemID(workItemID string) (string, bool) {
	workItemID = strings.TrimSpace(workItemID)
	if workItemID == "" {
		return "work_fixture", true
	}
	if workItemID == "work_fixture" {
		return workItemID, true
	}
	return "", false
}

func cairnlineSidecarFixtureProjectHandoffs(state *cairnlineSidecarFixtureState, projectID, workItemID string) []ProjectCairnlineSidecarHandoffItem {
	handoffsByID := state.handoffs[projectID]
	handoffs := make([]ProjectCairnlineSidecarHandoffItem, 0, len(handoffsByID))
	for _, handoff := range handoffsByID {
		if workItemID == "" || handoff.WorkItemID == workItemID {
			handoffs = append(handoffs, handoff)
		}
	}
	return handoffs
}

func cairnlineSidecarFixtureEnsureMemoryEntries(state *cairnlineSidecarFixtureState, projectID string) map[string]ProjectCairnlineSidecarMemoryEntryItem {
	if state.memoryEntries[projectID] == nil {
		state.memoryEntries[projectID] = make(map[string]ProjectCairnlineSidecarMemoryEntryItem)
	}
	return state.memoryEntries[projectID]
}

func cairnlineSidecarFixtureProjectMemoryEntries(state *cairnlineSidecarFixtureState, projectID string, includeDisabled bool) []ProjectCairnlineSidecarMemoryEntryItem {
	entriesByID := state.memoryEntries[projectID]
	entries := make([]ProjectCairnlineSidecarMemoryEntryItem, 0, len(entriesByID))
	for _, entry := range entriesByID {
		if !includeDisabled && !entry.Enabled {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func cairnlineSidecarFixtureDefaultMemoryEntries(projectID string, includeDisabled bool) []ProjectCairnlineSidecarMemoryEntryItem {
	entries := []ProjectCairnlineSidecarMemoryEntryItem{
		{
			ProjectID:  projectID,
			ID:         "mem_fixture",
			Title:      "Fixture memory",
			Body:       "Remember the fixture sidecar memory contract.",
			TrustLabel: "operator_memory",
			SourceKind: "test_fixture",
			SourceID:   "fixture-memory",
			Enabled:    true,
		},
		{
			ProjectID:  projectID,
			ID:         "mem_disabled_fixture",
			Title:      "Disabled fixture memory",
			Body:       "This memory entry is disabled.",
			TrustLabel: "operator_memory",
			SourceKind: "test_fixture",
			SourceID:   "fixture-memory-disabled",
			Enabled:    false,
		},
	}
	if includeDisabled {
		return entries
	}
	return entries[:1]
}

func cairnlineSidecarFixtureEnsureMemoryCandidates(state *cairnlineSidecarFixtureState, projectID string) map[string]ProjectCairnlineSidecarMemoryCandidateItem {
	if state.memoryCandidates[projectID] == nil {
		state.memoryCandidates[projectID] = make(map[string]ProjectCairnlineSidecarMemoryCandidateItem)
	}
	return state.memoryCandidates[projectID]
}

func cairnlineSidecarFixtureProjectMemoryCandidates(state *cairnlineSidecarFixtureState, projectID string, includeResolved bool, status string) []ProjectCairnlineSidecarMemoryCandidateItem {
	candidatesByID := state.memoryCandidates[projectID]
	candidates := make([]ProjectCairnlineSidecarMemoryCandidateItem, 0, len(candidatesByID))
	for _, candidate := range candidatesByID {
		if status != "" && candidate.Status != status {
			continue
		}
		if status == "" && !includeResolved && candidate.Status != "pending" {
			continue
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func cairnlineSidecarFixtureDefaultMemoryCandidates(projectID string, includeResolved bool, status string) []ProjectCairnlineSidecarMemoryCandidateItem {
	candidates := []ProjectCairnlineSidecarMemoryCandidateItem{
		{
			ProjectID:           projectID,
			ID:                  "memcand_fixture",
			Title:               "Fixture candidate",
			Body:                "Promote this fixture candidate when it becomes durable.",
			SuggestedKind:       "project_guidance",
			SuggestedTrustLabel: "generated_summary",
			SuggestedSourceKind: "test_fixture",
			SuggestedSourceID:   "fixture-candidate",
			SourceRefs: []ProjectCairnlineSidecarMemoryCandidateSourceRef{{
				Kind:  "evidence",
				ID:    "evidence_fixture",
				Title: "Fixture evidence",
				URL:   "https://example.test/evidence",
			}},
			Status: "pending",
		},
		{
			ProjectID:           projectID,
			ID:                  "memcand_rejected_fixture",
			Title:               "Rejected fixture candidate",
			Body:                "This candidate was rejected.",
			SuggestedKind:       "project_guidance",
			SuggestedTrustLabel: "generated_summary",
			SuggestedSourceKind: "test_fixture",
			SuggestedSourceID:   "fixture-candidate-rejected",
			Status:              "rejected",
			StatusReason:        "Not durable.",
		},
	}
	out := make([]ProjectCairnlineSidecarMemoryCandidateItem, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(status) != "" && candidate.Status != status {
			continue
		}
		if strings.TrimSpace(status) == "" && !includeResolved && candidate.Status != "pending" {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

func cairnlineSidecarFixtureProjectAssistantProposals(state *cairnlineSidecarFixtureState, projectID string) []ProjectCairnlineSidecarAssistantProposalRecordItem {
	items := make([]ProjectCairnlineSidecarAssistantProposalRecordItem, 0, len(state.assistantProposals))
	for _, item := range state.assistantProposals {
		if projectID == "" || item.ProjectID == projectID {
			items = append(items, item)
		}
	}
	return items
}

func cairnlineSidecarFixtureDefaultAssistantProposal(id string) (ProjectCairnlineSidecarAssistantProposalRecordItem, bool) {
	if id != "pa_fixture" {
		return ProjectCairnlineSidecarAssistantProposalRecordItem{}, false
	}
	return ProjectCairnlineSidecarAssistantProposalRecordItem{
		ID:        "pa_fixture",
		ProjectID: "proj_fixture",
		Source:    "assistant",
		Proposal: ProjectCairnlineSidecarAssistantProposalItem{
			ID:                   "pa_fixture",
			ProjectID:            "proj_fixture",
			Title:                "Queue fixture work",
			Summary:              "Create one reviewable work item from the sidecar assistant proposal fixture.",
			RequiresConfirmation: true,
			Actions: []ProjectCairnlineSidecarAssistantActionItem{{
				Kind:    "create_work_item",
				Summary: "Capture the next sidecar-backed project task.",
				Target:  ProjectCairnlineSidecarAssistantTargetItem{ProjectID: "proj_fixture"},
				WorkItem: &ProjectCairnlineSidecarWorkItem{
					ID:        "work_from_sidecar_proposal",
					ProjectID: "proj_fixture",
					Title:     "Sidecar proposal work",
					Brief:     "Read the proposal from the Cairnline sidecar.",
					Status:    "open",
					Priority:  "normal",
				},
			}},
		},
		Status: "proposed",
	}, true
}

func cairnlineSidecarFixtureApplyAssistantProposal(state *cairnlineSidecarFixtureState, record ProjectCairnlineSidecarAssistantProposalRecordItem) ProjectCairnlineSidecarAssistantApplyResultItem {
	result := ProjectCairnlineSidecarAssistantApplyResultItem{
		ProposalID:       record.ID,
		Status:           "applied",
		Applied:          true,
		Confirmed:        true,
		TotalActionCount: len(record.Proposal.Actions),
		Actions:          make([]ProjectCairnlineSidecarAssistantActionResultItem, 0, len(record.Proposal.Actions)),
	}
	for idx, action := range record.Proposal.Actions {
		actionResult := ProjectCairnlineSidecarAssistantActionResultItem{
			Kind:   action.Kind,
			Status: "applied",
		}
		switch action.Kind {
		case "create_role":
			if action.Role == nil || action.Role.ProjectID == "" || action.Role.ID == "" {
				result.Status = "partial"
				result.Applied = false
				result.FailedActionIndex = &idx
				actionResult.Status = "failed"
				actionResult.Error = "missing role payload"
				result.Actions = append(result.Actions, actionResult)
				return result
			}
			cairnlineSidecarFixtureEnsureRoles(state, action.Role.ProjectID)[action.Role.ID] = *action.Role
			actionResult.ProjectID = action.Role.ProjectID
			actionResult.RoleID = action.Role.ID
		case "create_work_item":
			if action.WorkItem == nil || action.WorkItem.ProjectID == "" || action.WorkItem.ID == "" {
				result.Status = "partial"
				result.Applied = false
				result.FailedActionIndex = &idx
				actionResult.Status = "failed"
				actionResult.Error = "missing work item payload"
				result.Actions = append(result.Actions, actionResult)
				return result
			}
			cairnlineSidecarFixtureEnsureWorkItems(state, action.WorkItem.ProjectID)[action.WorkItem.ID] = *action.WorkItem
			actionResult.ProjectID = action.WorkItem.ProjectID
			actionResult.WorkItemID = action.WorkItem.ID
		case "create_assignment":
			if action.Assignment == nil || action.Assignment.ProjectID == "" || action.Assignment.ID == "" {
				result.Status = "partial"
				result.Applied = false
				result.FailedActionIndex = &idx
				actionResult.Status = "failed"
				actionResult.Error = "missing assignment payload"
				result.Actions = append(result.Actions, actionResult)
				return result
			}
			assignment := *action.Assignment
			if assignment.Status == "" {
				assignment.Status = "queued"
			}
			cairnlineSidecarFixtureEnsureAssignments(state, assignment.ProjectID)[assignment.ID] = assignment
			actionResult.ProjectID = assignment.ProjectID
			actionResult.WorkItemID = assignment.WorkItemID
			actionResult.RoleID = assignment.RoleID
			actionResult.AssignmentID = assignment.ID
		default:
			result.Status = "partial"
			result.Applied = false
			result.FailedActionIndex = &idx
			actionResult.Status = "failed"
			actionResult.Error = "unsupported action kind"
			result.Actions = append(result.Actions, actionResult)
			return result
		}
		result.AppliedActionCount++
		result.Actions = append(result.Actions, actionResult)
	}
	return result
}

func cairnlineSidecarFixtureProjectActivity(state *cairnlineSidecarFixtureState, projectID string) map[string]any {
	assignments := cairnlineSidecarFixtureProjectAssignments(state, projectID)
	if len(assignments) == 0 {
		assignments = []ProjectCairnlineSidecarAssignmentItem{{
			ProjectID:     projectID,
			ID:            "asg_fixture",
			WorkItemID:    "work_fixture",
			RoleID:        "role_fixture",
			ExecutionMode: "mcp_pull",
			DesiredAgent: ProjectCairnlineSidecarDesiredAgentItem{
				Kind:     "any",
				SkillIDs: []string{"skill_fixture"},
			},
			Status:       state.assignmentStatus,
			ClaimedBy:    state.claimedBy,
			ExecutionRef: cairnlineSidecarFixtureExecutionRef(state.executionRef),
		}}
	}
	items := make([]map[string]any, 0, len(assignments))
	buckets := map[string][]map[string]any{
		"active":    {},
		"blocked":   {},
		"completed": {},
		"other":     {},
		"recent":    {},
	}
	counts := map[string]int{"assignments": len(assignments)}
	for _, assignment := range assignments {
		bucket := cairnlineSidecarFixtureActivityBucket(assignment.Status)
		counts[bucket]++
		counts[assignment.Status]++
		item := map[string]any{
			"bucket":             bucket,
			"assignment_id":      assignment.ID,
			"work_item_id":       assignment.WorkItemID,
			"work_item_title":    firstNonEmpty(cairnlineSidecarFixtureWorkItemTitle(state, projectID, assignment.WorkItemID), "Fixture Work"),
			"role_id":            assignment.RoleID,
			"role_name":          firstNonEmpty(cairnlineSidecarFixtureRoleName(state, projectID, assignment.RoleID), "Fixture Reviewer"),
			"root_id":            assignment.RootID,
			"status":             assignment.Status,
			"execution_mode":     assignment.ExecutionMode,
			"desired_agent_kind": "any",
			"execution_ref":      assignment.ExecutionRef,
			"created_at":         "2026-06-30T00:00:00Z",
			"updated_at":         "2026-06-30T00:01:00Z",
		}
		items = append(items, item)
		buckets[bucket] = append(buckets[bucket], item)
		buckets["recent"] = append(buckets["recent"], item)
	}
	return map[string]any{
		"project_id": projectID,
		"counts":     counts,
		"buckets":    buckets,
		"items":      items,
		"created_at": "2026-06-30T00:02:00Z",
	}
}

func cairnlineSidecarFixtureProjectOperationsBrief(state *cairnlineSidecarFixtureState, projectID string) map[string]any {
	activity := cairnlineSidecarFixtureProjectActivity(state, projectID)
	activityItems, _ := activity["items"].([]map[string]any)
	items := make([]map[string]any, 0, len(activityItems)+2)
	for _, item := range activityItems {
		assignmentID, _ := item["assignment_id"].(string)
		workItemID, _ := item["work_item_id"].(string)
		status, _ := item["status"].(string)
		items = append(items, map[string]any{
			"kind":          "assignment",
			"severity":      cairnlineSidecarFixtureOperationSeverity(status),
			"status":        status,
			"title":         "Review assignment " + assignmentID,
			"detail":        firstNonEmpty(status, "queued"),
			"work_item_id":  workItemID,
			"assignment_id": assignmentID,
			"updated_at":    "2026-06-30T00:03:00Z",
		})
	}
	handoffs := cairnlineSidecarFixtureProjectHandoffs(state, projectID, "")
	if len(handoffs) == 0 {
		handoffs = cairnlineSidecarFixtureDefaultHandoffs(projectID, "")
	}
	openHandoffs := 0
	for _, handoff := range handoffs {
		if handoff.Status != "pending" {
			continue
		}
		openHandoffs++
		items = append(items, map[string]any{
			"kind":         "handoff",
			"severity":     "action",
			"status":       handoff.Status,
			"title":        firstNonEmpty(handoff.Title, "Review handoff"),
			"detail":       firstNonEmpty(handoff.RecommendedNextAction, handoff.Body),
			"work_item_id": handoff.WorkItemID,
			"artifact_id":  handoff.ID,
			"updated_at":   "2026-06-30T00:04:00Z",
		})
	}
	reviews := cairnlineSidecarFixtureProjectReviews(state, projectID, "")
	if len(reviews) == 0 {
		reviews = cairnlineSidecarFixtureDefaultReviews(projectID, "")
	}
	reviewFollowUps := 0
	for _, review := range reviews {
		if review.Verdict != "changes_requested" {
			continue
		}
		reviewFollowUps++
		items = append(items, map[string]any{
			"kind":         "review_follow_up",
			"severity":     "action",
			"status":       review.Status,
			"title":        firstNonEmpty(review.Title, "Review follow-up"),
			"detail":       firstNonEmpty(review.Body, "Review requested changes."),
			"work_item_id": review.WorkItemID,
			"artifact_id":  review.ID,
			"updated_at":   "2026-06-30T00:05:00Z",
		})
	}
	counts := map[string]any{
		"work_items":                1,
		"open_work_items":           1,
		"assignments":               len(activityItems),
		"active_assignments":        cairnlineSidecarFixtureInt(activity, "counts", "active"),
		"blocked_assignments":       cairnlineSidecarFixtureInt(activity, "counts", "blocked"),
		"pending_memory_candidates": 0,
		"review_follow_ups":         reviewFollowUps,
		"missing_evidence":          0,
		"open_handoffs":             openHandoffs,
		"closeout_ready":            0,
	}
	return map[string]any{
		"project_id": projectID,
		"status":     "attention",
		"title":      "Project operations",
		"detail":     "Fixture operations brief.",
		"counts":     counts,
		"items":      items,
		"created_at": "2026-06-30T00:06:00Z",
	}
}

func cairnlineSidecarFixtureActivityBucket(status string) string {
	switch status {
	case "claimed", "running", "awaiting_review":
		return "active"
	case "completed":
		return "completed"
	case "queued", "failed", "cancelled":
		return "blocked"
	default:
		return "other"
	}
}

func cairnlineSidecarFixtureOperationSeverity(status string) string {
	switch status {
	case "queued", "failed", "cancelled":
		return "blocked"
	case "claimed", "running", "awaiting_review":
		return "active"
	default:
		return "info"
	}
}

func cairnlineSidecarFixtureInt(value map[string]any, keys ...string) int {
	var current any = value
	for _, key := range keys {
		next, ok := current.(map[string]int)
		if ok {
			return next[key]
		}
		generic, ok := current.(map[string]any)
		if !ok {
			return 0
		}
		current = generic[key]
	}
	if v, ok := current.(int); ok {
		return v
	}
	return 0
}

func cairnlineSidecarFixtureModeHas(mode, flag string) bool {
	for _, part := range strings.FieldsFunc(mode, func(r rune) bool {
		return r == '+' || r == ','
	}) {
		if strings.TrimSpace(part) == flag {
			return true
		}
	}
	return false
}

func cairnlineSidecarFixtureTextOnly(mode, tool string) bool {
	return cairnlineSidecarFixtureModeHas(mode, "text-only") ||
		cairnlineSidecarFixtureModeHas(mode, tool+"-text-only")
}

func cairnlineSidecarFixtureListResult(mode, text string, structured any) (mcp.CallToolResult, *mcp.RPCError) {
	result := mcp.CallToolResult{Content: mcp.TextContent(text)}
	if !cairnlineSidecarFixtureModeHas(mode, "text-only") {
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

func cairnlineSidecarFixtureProjectWorkIDs(raw json.RawMessage) (string, string) {
	var input struct {
		ProjectID  string `json:"project_id"`
		WorkItemID string `json:"work_item_id"`
	}
	_ = json.Unmarshal(raw, &input)
	return input.ProjectID, input.WorkItemID
}

func mustRawJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

// cairnlineSidecarFixtureExecutionRef models the sidecar's legacy tolerance:
// a bare string execution ref (what the lifecycle smoke sends over MCP)
// decodes as the run id in the structured portable ref.
func cairnlineSidecarFixtureExecutionRef(value string) ProjectCairnlineSidecarExecutionRef {
	value = strings.TrimSpace(value)
	if value == "" {
		return ProjectCairnlineSidecarExecutionRef{}
	}
	return ProjectCairnlineSidecarExecutionRef{RunID: value}
}
