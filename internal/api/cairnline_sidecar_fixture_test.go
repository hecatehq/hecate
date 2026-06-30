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

func mustRawJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}
