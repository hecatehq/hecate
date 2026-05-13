package agentadapters

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// init runs once when the test binary loads, single-threaded, so
// it's race-free relative to the t.Parallel() tests below. We
// stretch the per-probe deadline well beyond production's 5 s so
// CI subprocess-startup jitter doesn't flake the parsing-path
// tests in this file. The single test that pins timeout behavior
// (TestDetectVersionTimeoutReturnsEmpty) supplies its own short
// (50 ms) context deadline via context.WithTimeout, which beats
// this ceiling regardless of how high it's set.
//
// Production keeps the 5 s default defined in version.go
// (`var detectVersionTimeout`); registry.go is the primary
// caller. Only the test binary lifts the cap. Without this, the parsing tests
// occasionally flake on GitHub Actions runners under -race +
// parallel-suite load — fork/exec of /bin/sh has multi-second
// jitter under contention even though the script body is trivial
// (echo + exit). The function under test only cares that the
// regex sees the script's output before the context dies; a
// longer cap removes that race entirely.
func init() {
	detectVersionTimeout = 60 * time.Second
}

// writeFakeBinary writes a tiny shell script (or .cmd on Windows) that echoes
// its first arg as stdout. Returns the file path.
func writeFakeBinary(t *testing.T, dir, name, output string) string {
	t.Helper()
	var path string
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, name+".cmd")
		content := "@echo off\r\necho " + output + "\r\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write fake binary: %v", err)
		}
	} else {
		path = filepath.Join(dir, name)
		content := "#!/bin/sh\necho " + output + "\n"
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatalf("write fake binary: %v", err)
		}
	}
	return path
}

func TestDetectVersionParsesValid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeFakeBinary(t, dir, "fake-adapter", "fake-adapter version 1.23.4")
	got := DetectVersion(context.Background(), path)
	if got != "1.23.4" {
		t.Fatalf("DetectVersion = %q, want 1.23.4", got)
	}
}

func TestDetectVersionParsesPreRelease(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeFakeBinary(t, dir, "fake-adapter", "0.9.1-beta.2")
	got := DetectVersion(context.Background(), path)
	if !strings.HasPrefix(got, "0.9.1") {
		t.Fatalf("DetectVersion = %q, want prefix 0.9.1", got)
	}
}

func TestDetectVersionGarbageOutputReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeFakeBinary(t, dir, "fake-adapter", "no version info here at all")
	got := DetectVersion(context.Background(), path)
	if got != "" {
		t.Fatalf("DetectVersion = %q, want empty for garbage output", got)
	}
}

func TestDetectVersionParsesStderr(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("skip stderr helper build on Windows")
	}
	dir := t.TempDir()
	path := writeGoHelperBinary(t, dir, "stderr-adapter", `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "adapter 2.3.4")
}
`)
	got := DetectVersion(context.Background(), path)
	if got != "2.3.4" {
		t.Fatalf("DetectVersion = %q, want 2.3.4 from stderr", got)
	}
}

func writeGoHelperBinary(t *testing.T, dir, name, source string) string {
	t.Helper()
	sourcePath := filepath.Join(dir, name+".go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatalf("write helper source: %v", err)
	}
	binaryPath := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", binaryPath, sourcePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build helper binary: %v\n%s", err, out)
	}
	return binaryPath
}

func TestDetectVersionParsesOutputFromNonZeroExit(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "nonzero-adapter")
	content := "#!/bin/sh\necho adapter 3.4.5\nexit 1\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write nonzero script: %v", err)
	}
	got := DetectVersion(context.Background(), path)
	if got != "3.4.5" {
		t.Fatalf("DetectVersion = %q, want 3.4.5 from non-zero output", got)
	}
}

func TestDetectVersionMissingBinaryReturnsEmpty(t *testing.T) {
	t.Parallel()
	got := DetectVersion(context.Background(), "/does/not/exist/fake-adapter")
	if got != "" {
		t.Fatalf("DetectVersion = %q, want empty for missing binary", got)
	}
}

func TestDetectVersionTimeoutReturnsEmpty(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("skip sleep-based test on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "slow-adapter")
	// Script that sleeps longer than the test's short deadline.
	content := "#!/bin/sh\nsleep 10\necho 1.0.0\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write slow script: %v", err)
	}
	// Use a short deadline to validate timeout behavior without waiting
	// for DetectVersion's internal 5s cap.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	got := DetectVersion(ctx, path)
	if got != "" {
		t.Fatalf("DetectVersion = %q, want empty for timed-out binary", got)
	}
}

// Tests for the internal satisfiesRange / semverCmp helpers.

func TestSatisfiesRange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		v, constraint string
		want          bool
	}{
		{"1.2.3", ">=0.1.0", true},
		{"0.1.0", ">=0.1.0", true},
		{"0.0.9", ">=0.1.0", false},
		{"2.0.0", ">=1.0.0", true},
		{"0.9.9", ">=1.0.0", false},
		{"1.0.0-alpha", ">=1.0.0", false}, // pre-release is lower than release
		{"1.0.0", ">=1.0.0-alpha", true},
		{"", ">=0.1.0", true},         // unknown version → don't reject
		{"1.0.0", "", true},           // no constraint
		{"1.0.0", "unknown-op", true}, // unrecognised operator → don't reject
	}
	for _, c := range cases {
		got := satisfiesRange(c.v, c.constraint)
		if got != c.want {
			t.Errorf("satisfiesRange(%q, %q) = %v, want %v", c.v, c.constraint, got, c.want)
		}
	}
}
