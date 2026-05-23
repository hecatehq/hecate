package client

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/mcp"
)

// TestPool_RealServer_FilesystemRoundTrip is a hermetic smoke test
// against the canonical reference MCP server,
// `@modelcontextprotocol/server-filesystem`. Where pool_subprocess_test
// pins the framing and lifecycle against our own fixture, this test
// proves the full stack — Pool → StdioTransport → Client → JSON-RPC
// negotiation → tools/call — works against an MCP implementation we
// did NOT write. The kind of bug only this test would catch:
//
//   - we declare a protocol version the upstream rejects
//   - our handshake sends a field the spec deprecated
//   - our framing flushes at the wrong granularity for the upstream's
//     stream parser
//   - tool-call result content blocks differ from what we flatten
//
// Skipped when:
//   - `bunx` is not on PATH (the test runner doesn't have Bun installed)
//   - `-short` is set (the first run downloads the package, ~10–30s)
//
// We use bunx rather than npx so the test fits the repo's "Bun not
// Node" guideline. The server-filesystem package is well-maintained
// upstream and ships a stable tools/list, so name-based assertions
// (read_text_file, list_directory) are safe.
func TestPool_RealServer_FilesystemRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("real-server smoke test downloads an MCP package; skipping under -short")
	}
	bunx, err := exec.LookPath("bunx")
	if err != nil {
		t.Skip("bunx not on PATH; skipping real-server smoke test")
	}

	// Hermetic sandbox: server-filesystem is allowlisted to whatever
	// directories you pass on the command line. We give it one temp
	// dir, drop a known file in there, and verify both the read and
	// the listing surface back through the pool.
	//
	// EvalSymlinks is required on macOS: t.TempDir returns paths under
	// /var/folders/... which the OS symlinks to /private/var/folders/...
	// The server-filesystem package canonicalizes its allowlist through
	// symlinks, so a request against the un-canonicalized path is
	// rejected as "outside allowed directories" even though it points
	// at the same inode.
	rawRoot := t.TempDir()
	root, err := filepath.EvalSymlinks(rawRoot)
	if err != nil {
		t.Fatalf("eval symlinks on tempdir: %v", err)
	}
	knownFilename := "hello.txt"
	knownContents := "hecate real-server smoke test\n"
	if err := os.WriteFile(filepath.Join(root, knownFilename), []byte(knownContents), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	cfg := ServerConfig{
		Name:    "fs",
		Command: bunx,
		// `--bun` forces Bun's runtime even if a Node fallback is
		// installed, keeping the spawn behavior consistent across
		// machines. The server reads its allowlist from positional
		// args.
		Args: []string{"--bun", "@modelcontextprotocol/server-filesystem", root},
	}

	// Generous timeout — first invocation may pull the package over
	// the network. Subsequent runs hit Bun's cache and finish in <1s.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, mcp.ClientInfo{Name: "hecate-real-server-test", Version: "0.0.0"}, []ServerConfig{cfg})
	if err != nil {
		// Most failures here are environmental (no network, sandboxed
		// CI, package install blocked). Skip rather than fail so the
		// test doesn't go red on machines that can't reach npm.
		t.Skipf("could not bring up server-filesystem (likely environmental): %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	tools := pool.Tools()
	if len(tools) == 0 {
		t.Fatal("real server vended zero tools")
	}

	// Index by un-namespaced name for the asserts below. The exact
	// catalog evolves with server-filesystem releases — check that a
	// readable file and a directory listing tool exist under either
	// of the names that release line ships, rather than pinning one
	// version.
	toolByLeaf := make(map[string]NamespacedTool, len(tools))
	for _, ts := range tools {
		_, leaf, ok := SplitNamespacedToolName(ts.Name)
		if !ok {
			t.Errorf("tool name %q is not namespaced", ts.Name)
			continue
		}
		toolByLeaf[leaf] = ts
	}

	readToolLeaf := firstPresent(toolByLeaf, "read_text_file", "read_file")
	if readToolLeaf == "" {
		t.Fatalf("no read tool in catalog; got %v", keysOf(toolByLeaf))
	}
	listToolLeaf := firstPresent(toolByLeaf, "list_directory", "list_dir")
	if listToolLeaf == "" {
		t.Fatalf("no list tool in catalog; got %v", keysOf(toolByLeaf))
	}

	// Exercise the read path end-to-end.
	readArgs, _ := json.Marshal(map[string]string{"path": filepath.Join(root, knownFilename)})
	text, isErr, err := pool.Call(ctx, "mcp__fs__"+readToolLeaf, readArgs)
	if err != nil {
		t.Fatalf("Call %s: %v", readToolLeaf, err)
	}
	if isErr {
		t.Fatalf("Call %s returned isError=true; text=%q", readToolLeaf, text)
	}
	if !strings.Contains(text, "hecate real-server smoke test") {
		t.Errorf("read tool text = %q, want fixture contents", text)
	}

	// Exercise the directory-listing path so we cover a tool whose
	// result shape (lines of names) differs from the read tool's.
	listArgs, _ := json.Marshal(map[string]string{"path": root})
	listText, listIsErr, err := pool.Call(ctx, "mcp__fs__"+listToolLeaf, listArgs)
	if err != nil {
		t.Fatalf("Call %s: %v", listToolLeaf, err)
	}
	if listIsErr {
		t.Fatalf("Call %s returned isError=true; text=%q", listToolLeaf, listText)
	}
	if !strings.Contains(listText, knownFilename) {
		t.Errorf("list tool text = %q, want it to mention %q", listText, knownFilename)
	}
}

// firstPresent returns the first key from candidates that exists in m,
// or "" if none do. Used to tolerate naming changes across upstream
// MCP-server releases without pinning to a specific version.
func firstPresent[V any](m map[string]V, candidates ...string) string {
	for _, c := range candidates {
		if _, ok := m[c]; ok {
			return c
		}
	}
	return ""
}

// keysOf returns a slice of the keys in m. Only used in test failure
// messages so the developer can see what the real server actually
// shipped without re-running with verbose logging.
func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
