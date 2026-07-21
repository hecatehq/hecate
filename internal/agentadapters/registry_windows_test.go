//go:build windows

package agentadapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveInstalledCommandSkipsPATHScriptForNativeCandidate(t *testing.T) {
	pathDir := t.TempDir()
	shim := filepath.Join(pathDir, "provider.cmd")
	if err := os.WriteFile(shim, []byte("@exit /b 0\r\n"), 0o600); err != nil {
		t.Fatalf("write command shim: %v", err)
	}
	nativeDir := t.TempDir()
	native := filepath.Join(nativeDir, "provider.exe")
	if err := os.WriteFile(native, []byte("native fixture"), 0o600); err != nil {
		t.Fatalf("write native fixture: %v", err)
	}
	t.Setenv("PATH", pathDir)
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")

	got, err := resolveInstalledCommand("provider", []string{native}, nil)
	if err != nil {
		t.Fatalf("resolveInstalledCommand: %v", err)
	}
	if got != filepath.Clean(native) {
		t.Fatalf("resolved path = %q, want native candidate %q", got, native)
	}
}

func TestResolveInstalledCommandRejectsPATHScriptWithoutNativeCandidate(t *testing.T) {
	pathDir := t.TempDir()
	shim := filepath.Join(pathDir, "provider.cmd")
	if err := os.WriteFile(shim, []byte("@exit /b 0\r\n"), 0o600); err != nil {
		t.Fatalf("write command shim: %v", err)
	}
	t.Setenv("PATH", pathDir)
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")

	_, err := resolveInstalledCommand("provider", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "native .exe") {
		t.Fatalf("resolveInstalledCommand error = %v, want native-executable guidance", err)
	}
}
