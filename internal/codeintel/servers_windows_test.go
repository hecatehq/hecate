//go:build windows

package codeintel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestService_TrustedBinaryResolutionRejectsCommandShimOnWindows(t *testing.T) {
	workspace := t.TempDir()
	shim := filepath.Join(t.TempDir(), "tsc.cmd")
	if err := os.WriteFile(shim, []byte("@exit /b 0\r\n"), 0o644); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	fsys, err := openWorkspace(workspace)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	service := NewService()
	setProviderPath(service, "tsc", shim)
	if _, err := service.resolveTrustedBinary(fsys, "tsc"); err == nil || !strings.Contains(err.Error(), "native .exe") {
		t.Fatalf("error = %v, want command-shim rejection", err)
	}
}

func TestTrustedBinaryInvocationPathUsesCanonicalExecutableOnWindows(t *testing.T) {
	const discovered = `C:\tools\tsc.cmd`
	const canonical = `C:\tools\tsc.exe`
	if got := trustedBinaryInvocationPath(discovered, canonical); got != canonical {
		t.Fatalf("invocation path = %q, want canonical executable %q", got, canonical)
	}
}

func TestService_TrustedBinaryResolutionInvokesCanonicalExecutableOnWindows(t *testing.T) {
	workspace := t.TempDir()
	directory := t.TempDir()
	target := executableFixture(t, directory, "tsc")
	shim := filepath.Join(directory, "tsc.cmd")
	if err := os.Symlink(target, shim); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	fsys, err := openWorkspace(workspace)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	service := NewService()
	setProviderPath(service, "tsc", shim)

	resolved, err := service.resolveTrustedBinary(fsys, "tsc")
	if err != nil {
		t.Fatalf("resolve canonical executable: %v", err)
	}
	canonical, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("canonicalize target: %v", err)
	}
	if resolved != canonical {
		t.Fatalf("resolved path = %q, want canonical executable %q", resolved, canonical)
	}
}
