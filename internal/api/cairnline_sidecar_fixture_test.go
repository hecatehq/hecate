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
