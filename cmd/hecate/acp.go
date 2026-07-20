package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hecatehq/hecate/internal/acpserver"
	"github.com/hecatehq/hecate/internal/version"
)

// runACPServer is the entry point for `hecate acp serve`. It makes Hecate a
// local ACP agent for an editor such as Zed while keeping all execution in
// Hecate's durable task runtime.
//
// Configuration is environment-only so editors can start the binary without
// inheriting the runtime's broader configuration surface:
//   - HECATE_BASE_URL       — local Hecate runtime URL
//     (default: http://127.0.0.1:8765)
//   - HECATE_RUNTIME_TOKEN  — optional native runtime API token
//
// The ACP bridge accepts only literal loopback base URLs. Editor workspaces
// are local filesystem paths, so forwarding them to a remote runtime would
// imply an unsafe and incorrect workspace contract.
func runACPServer(parent context.Context, input io.Reader, output, diagnostics io.Writer) error {
	baseURL := strings.TrimSpace(os.Getenv("HECATE_BASE_URL"))
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8765"
	}
	runtime, err := acpserver.NewHTTPRuntime(baseURL, os.Getenv("HECATE_RUNTIME_TOKEN"))
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	fmt.Fprintln(diagnostics, "hecate acp serve: started on stdio, talking to "+baseURL)
	err = acpserver.Serve(ctx, input, output, runtime, acpserver.Config{Version: version.Version})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
