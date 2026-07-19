//go:build !windows

package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadFileToolRejectsFIFOWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "events.pipe"), 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	done := make(chan string, 1)
	go func() {
		text, _, _, _ := readFileTool(spec, readFileArgs{Path: "events.pipe"}, 1, time.Now().UTC(), "read_file")
		done <- text
	}()
	select {
	case text := <-done:
		if !strings.Contains(text, "not a regular file") {
			t.Fatalf("readFileTool() text = %q, want non-regular-file rejection", text)
		}
	case <-time.After(time.Second):
		t.Fatal("readFileTool() blocked opening a FIFO")
	}
}

func TestGrepToolSkipsFIFOWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "events.pipe"), 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	done := make(chan string, 1)
	go func() {
		text, _, _, _ := grepTool(context.Background(), spec, grepArgs{Pattern: "never"}, 1, time.Now().UTC(), "grep")
		done <- text
	}()
	select {
	case text := <-done:
		if !strings.Contains(text, "matches=0") {
			t.Fatalf("grepTool() text = %q, want no matches", text)
		}
	case <-time.After(time.Second):
		t.Fatal("grepTool() blocked opening a FIFO")
	}
}
