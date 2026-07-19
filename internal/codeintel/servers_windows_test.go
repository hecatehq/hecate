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
