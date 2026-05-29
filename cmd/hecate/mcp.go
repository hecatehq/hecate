package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hecatehq/hecate/internal/mcp/server"
	"github.com/hecatehq/hecate/internal/version"
)

// runMCPServer is the entry point for `hecate mcp serve`. It runs an
// MCP server on stdio, talking back to a running Hecate gateway over
// HTTP.
//
// Configuration is environment-only:
//   - HECATE_BASE_URL   — gateway URL, e.g. http://127.0.0.1:8765
//     (default: http://127.0.0.1:8765)
//   - HECATE_RUNTIME_TOKEN — optional token for Hecate-native APIs when
//     the gateway was started with the same value.
//
// We deliberately don't read config.LoadFromEnv() — the MCP subprocess
// runs out-of-process from the gateway and shouldn't share its config
// surface. Operators add this to Claude Desktop / Cursor / Zed by
// pointing their `mcpServers` config at the hecate binary.
func runMCPServer(commandName string) {
	baseURL := strings.TrimSpace(os.Getenv("HECATE_BASE_URL"))
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8765"
	}
	srv := server.NewServer("hecate", version.Version)
	srv.SetDescription("Hecate gateway: read-only inspection of tasks, chat sessions, and recent traffic.")
	client := server.NewGatewayClient(baseURL)
	client.SetRuntimeToken(os.Getenv("HECATE_RUNTIME_TOKEN"))
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

	fmt.Fprintln(os.Stderr, commandName+": started on stdio, talking to "+baseURL)
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, commandName+": "+err.Error())
		os.Exit(1)
	}
}
