package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hecate/agent-runtime/internal/mcp/server"
	"github.com/hecate/agent-runtime/internal/version"
)

// runMCPServer is the entry point for `gateway mcp-server`. It runs an
// MCP server on stdio, talking back to a running Hecate gateway over
// HTTP — the same auth token an operator uses in the UI.
//
// Configuration is environment-only:
//   - HECATE_BASE_URL   — gateway URL, e.g. http://127.0.0.1:8765
//     (default: http://127.0.0.1:8765)
//   - HECATE_AUTH_TOKEN — bearer token (required for any non-public
//     endpoint; surfaced in the gateway's first-run
//     banner or under /data/hecate.bootstrap.json)
//
// We deliberately don't read config.LoadFromEnv() — the MCP subprocess
// runs out-of-process from the gateway and shouldn't share its config
// surface. Operators add this to Claude Desktop / Cursor / Zed by
// pointing their `mcpServers` config at the gateway binary.
func runMCPServer() {
	baseURL := strings.TrimSpace(os.Getenv("HECATE_BASE_URL"))
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8765"
	}
	authToken := strings.TrimSpace(os.Getenv("HECATE_AUTH_TOKEN"))
	if authToken == "" {
		// Stderr (not stdout) — stdout is the JSON-RPC channel. A line
		// of plain text on stdout would corrupt the wire and the
		// client would disconnect. Mirroring this on every error path
		// in this file.
		fmt.Fprintln(os.Stderr, "gateway mcp-server: HECATE_AUTH_TOKEN is required")
		fmt.Fprintln(os.Stderr, "  the bearer token is printed in the gateway's first-run banner,")
		fmt.Fprintln(os.Stderr, "  or readable from /data/hecate.bootstrap.json (key: admin_token).")
		os.Exit(2)
	}

	srv := server.NewServer("hecate", version.Version)
	srv.SetDescription("Hecate gateway: read-only inspection of tasks, chat sessions, and recent traffic.")
	client := server.NewGatewayClient(baseURL, authToken)
	server.RegisterDefaultTools(srv, client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// SIGINT / SIGTERM cancels the context so Serve unwinds cleanly
	// when the parent process kills us. Most MCP-aware editors send
	// SIGTERM on subprocess shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	fmt.Fprintln(os.Stderr, "gateway mcp-server: started on stdio, talking to "+baseURL)
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "gateway mcp-server: "+err.Error())
		os.Exit(1)
	}
}
