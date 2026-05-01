package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeExecutable creates a zero-byte file with executable permissions in dir
// and returns its full path. Good enough for os.Stat / exec.LookPath probes;
// we never actually execute it in these tests.
func fakeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("fakeExecutable: %v", err)
	}
	return p
}

// TestResolveSandboxd_EnvVarValid: SANDBOXD_BIN pointing to a real file is
// returned immediately without consulting any other mechanism.
func TestResolveSandboxd_EnvVarValid(t *testing.T) {
	dir := t.TempDir()
	want := fakeExecutable(t, dir, "my-sandboxd")

	t.Setenv("SANDBOXD_BIN", want)

	got, err := resolveSandboxdPathFrom("")
	if err != nil {
		t.Fatalf("resolveSandboxdPathFrom() error = %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestResolveSandboxd_EnvVarInvalid: SANDBOXD_BIN set to a non-existent path
// must return an error that names the env var and the bad path, not fall
// through to the next resolution step.
func TestResolveSandboxd_EnvVarInvalid(t *testing.T) {
	t.Setenv("SANDBOXD_BIN", "/does/not/exist/sandboxd")

	_, err := resolveSandboxdPathFrom("")
	if err == nil {
		t.Fatal("expected error for non-existent SANDBOXD_BIN, got nil")
	}
	if !strings.Contains(err.Error(), "SANDBOXD_BIN") {
		t.Errorf("error %q should mention SANDBOXD_BIN", err.Error())
	}
	if !strings.Contains(err.Error(), "/does/not/exist/sandboxd") {
		t.Errorf("error %q should contain the bad path", err.Error())
	}
}

// TestResolveSandboxd_EnvVarEmpty: an empty (or whitespace-only) SANDBOXD_BIN
// is treated as unset and resolution continues to the next step.
func TestResolveSandboxd_EnvVarEmpty(t *testing.T) {
	// Point PATH at a dir that has sandboxd so the test doesn't fall all
	// the way through to the go-build step (which requires the toolchain).
	dir := t.TempDir()
	fakeExecutable(t, dir, "sandboxd")

	t.Setenv("SANDBOXD_BIN", "   ")
	t.Setenv("PATH", dir)

	got, err := resolveSandboxdPathFrom("")
	if err != nil {
		t.Fatalf("empty SANDBOXD_BIN should fall through; error = %v", err)
	}
	if !strings.Contains(got, "sandboxd") {
		t.Errorf("got %q, expected a path containing sandboxd", got)
	}
}

// TestResolveSandboxd_NextToExe_PlainName: when a plain "sandboxd" binary sits
// in the supplied exe directory it must be preferred over PATH lookup.
func TestResolveSandboxd_NextToExe_PlainName(t *testing.T) {
	dir := t.TempDir()
	want := fakeExecutable(t, dir, "sandboxd")

	// Clear SANDBOXD_BIN and ensure PATH doesn't have a sandboxd so we
	// know the hit came from the exe-dir probe.
	t.Setenv("SANDBOXD_BIN", "")
	t.Setenv("PATH", "")

	got, err := resolveSandboxdPathFrom(dir)
	if err != nil {
		t.Fatalf("resolveSandboxdPathFrom(dir) error = %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestResolveSandboxd_NextToExe_TripleSuffixPreferredOverPlain: when both the
// triple-suffixed and plain binary exist in the exe dir, the triple-suffixed
// one must be returned first (matching Tauri's externalBin layout).
func TestResolveSandboxd_NextToExe_TripleSuffixPreferredOverPlain(t *testing.T) {
	dir := t.TempDir()
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	plain := fakeExecutable(t, dir, "sandboxd"+ext)
	tripled := fakeExecutable(t, dir, "sandboxd-"+rustTriple()+ext)
	_ = plain // created for the test; we assert tripled wins

	t.Setenv("SANDBOXD_BIN", "")
	t.Setenv("PATH", "")

	got, err := resolveSandboxdPathFrom(dir)
	if err != nil {
		t.Fatalf("resolveSandboxdPathFrom(dir) error = %v", err)
	}
	if got != tripled {
		t.Errorf("got %q, want triple-suffixed %q", got, tripled)
	}
}

// TestResolveSandboxd_PathFallback: when the exe dir has no sandboxd but PATH
// does, the PATH entry must be returned.
func TestResolveSandboxd_PathFallback(t *testing.T) {
	exeDir := t.TempDir()  // no sandboxd in here
	pathDir := t.TempDir() // sandboxd lives here
	fakeExecutable(t, pathDir, "sandboxd")

	t.Setenv("SANDBOXD_BIN", "")
	t.Setenv("PATH", pathDir)

	got, err := resolveSandboxdPathFrom(exeDir)
	if err != nil {
		t.Fatalf("resolveSandboxdPathFrom() error = %v", err)
	}
	if !strings.Contains(got, pathDir) {
		t.Errorf("got %q, expected path inside %q", got, pathDir)
	}
}

// TestResolveSandboxd_NoGoToolchain: when no binary is found anywhere and go
// is not on PATH, the error must mention all three remediation options so an
// operator or CI log tells them exactly what to do.
func TestResolveSandboxd_NoGoToolchain(t *testing.T) {
	t.Setenv("SANDBOXD_BIN", "")
	t.Setenv("PATH", "") // wipes both sandboxd and go from PATH

	_, err := resolveSandboxdPathFrom("") // empty exeDir — no binary next to exe either
	if err == nil {
		t.Fatal("expected error when nothing is available, got nil")
	}
	for _, hint := range []string{"SANDBOXD_BIN", "PATH", "Go toolchain"} {
		if !strings.Contains(err.Error(), hint) {
			t.Errorf("error %q should mention %q", err.Error(), hint)
		}
	}
}

// TestRustTriple: the returned string must be non-empty and follow the
// arch-vendor-os pattern (at least two hyphens) so probe candidates are
// sensibly named.
func TestRustTriple(t *testing.T) {
	triple := rustTriple()
	if triple == "" {
		t.Fatal("rustTriple() returned empty string")
	}
	parts := strings.Split(triple, "-")
	if len(parts) < 3 {
		t.Errorf("rustTriple() = %q, want at least 3 hyphen-separated parts", triple)
	}
}

// TestRustTriple_KnownPlatforms: spot-check the mappings for the three
// platforms the Tauri app ships on. We can only check the current platform
// at runtime, but verifying the arch and OS components are correct catches
// the most common copy-paste mistakes.
func TestRustTriple_KnownPlatforms(t *testing.T) {
	triple := rustTriple()
	switch runtime.GOARCH {
	case "amd64":
		if !strings.HasPrefix(triple, "x86_64") {
			t.Errorf("amd64 → rustTriple() = %q, want prefix x86_64", triple)
		}
	case "arm64":
		if !strings.HasPrefix(triple, "aarch64") {
			t.Errorf("arm64 → rustTriple() = %q, want prefix aarch64", triple)
		}
	}
	switch runtime.GOOS {
	case "darwin":
		if !strings.Contains(triple, "apple-darwin") {
			t.Errorf("darwin → rustTriple() = %q, want apple-darwin segment", triple)
		}
	case "linux":
		if !strings.Contains(triple, "linux") {
			t.Errorf("linux → rustTriple() = %q, want linux segment", triple)
		}
	case "windows":
		if !strings.Contains(triple, "windows") {
			t.Errorf("windows → rustTriple() = %q, want windows segment", triple)
		}
	}
}
