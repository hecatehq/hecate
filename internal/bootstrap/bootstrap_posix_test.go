//go:build !windows

package bootstrap

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveGeneratesWithPrivateFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hecate.bootstrap.json")

	if _, err := Resolve(path, ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	assertFileMode(t, path, 0o600)
}

func TestResolveRepairsLooseExistingFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hecate.bootstrap.json")
	writeBootstrapFixture(t, path, testBootstrapKey(0xef), 0o644)

	if _, err := Resolve(path, ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	assertFileMode(t, path, 0o600)
}

func TestResolveAcceptsStricterExistingFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hecate.bootstrap.json")
	writeBootstrapFixture(t, path, testBootstrapKey(0xef), 0o400)

	if _, err := Resolve(path, ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	assertFileMode(t, path, 0o400)
}

func TestResolveRepairsLoosePermissionsWhenEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hecate.bootstrap.json")
	writeBootstrapFixture(t, path, testBootstrapKey(0xef), 0o644)

	if _, err := Resolve(path, testBootstrapKey(0xab)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	assertFileMode(t, path, 0o600)
}

func TestResolveEnvOverrideReplacesLooseFileInsteadOfRewritingInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hecate.bootstrap.json")
	oldSecret := testBootstrapKey(0xef)
	writeBootstrapFixture(t, path, oldSecret, 0o644)

	oldHandle, err := os.Open(path)
	if err != nil {
		t.Fatalf("open old file: %v", err)
	}
	defer oldHandle.Close()

	newSecret := testBootstrapKey(0xab)
	if _, err := Resolve(path, newSecret); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	oldHandleRaw, err := io.ReadAll(oldHandle)
	if err != nil {
		t.Fatalf("read old handle: %v", err)
	}
	var oldHandleDisk Bootstrap
	if err := json.Unmarshal(oldHandleRaw, &oldHandleDisk); err != nil {
		t.Fatalf("decode old handle: %v", err)
	}
	if oldHandleDisk.ControlPlaneSecretKey != oldSecret {
		t.Fatalf("old handle secret = %q, want original secret", oldHandleDisk.ControlPlaneSecretKey)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read replacement: %v", err)
	}
	var replacement Bootstrap
	if err := json.Unmarshal(raw, &replacement); err != nil {
		t.Fatalf("decode replacement: %v", err)
	}
	if replacement.ControlPlaneSecretKey != newSecret {
		t.Fatalf("replacement secret = %q, want env override", replacement.ControlPlaneSecretKey)
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("file mode = %o, want %o", got, want)
	}
}
