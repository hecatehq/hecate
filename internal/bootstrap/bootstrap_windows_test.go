//go:build windows

package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWindowsLoadsReadOnlyBootstrapWithoutModeRepair(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hecate.bootstrap.json")
	secret := testBootstrapKey(0xef)
	writeBootstrapFixture(t, path, secret, 0o444)
	defer func() {
		_ = os.Chmod(path, 0o600)
	}()

	b, err := Resolve(path, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if b.ControlPlaneSecretKey != secret {
		t.Fatalf("ControlPlaneSecretKey = %q, want existing secret", b.ControlPlaneSecretKey)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o200 != 0 {
		t.Fatalf("read-only bootstrap file became writable: mode=%o", info.Mode().Perm())
	}
}

func TestResolveWindowsEnvOverrideReplacesBootstrapFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hecate.bootstrap.json")
	writeBootstrapFixture(t, path, testBootstrapKey(0xef), 0o600)

	override := testBootstrapKey(0xab)
	b, err := Resolve(path, override)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if b.ControlPlaneSecretKey != override {
		t.Fatalf("ControlPlaneSecretKey = %q, want override", b.ControlPlaneSecretKey)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read replacement: %v", err)
	}
	var disk Bootstrap
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("decode replacement: %v", err)
	}
	if disk.ControlPlaneSecretKey != override {
		t.Fatalf("disk ControlPlaneSecretKey = %q, want override", disk.ControlPlaneSecretKey)
	}
}
