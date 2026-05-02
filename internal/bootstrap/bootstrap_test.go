package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveGeneratesAndPersistsOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hecate.bootstrap.json")

	b, err := Resolve(path, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// ControlPlaneSecretKey is 32 random bytes base64-encoded → 44 chars
	// (with std-base64 padding) so secrets.NewAESGCMCipher can decode it
	// to exactly 32 bytes.
	if len(b.ControlPlaneSecretKey) != 44 {
		t.Errorf("ControlPlaneSecretKey length = %d, want 44 (base64 of 32 bytes)", len(b.ControlPlaneSecretKey))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 0600", info.Mode().Perm())
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var disk Bootstrap
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if disk != b {
		t.Errorf("disk content does not match returned: disk=%+v in-mem=%+v", disk, b)
	}
}

func TestResolveReusesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hecate.bootstrap.json")

	first, err := Resolve(path, "")
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	second, err := Resolve(path, "")
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if second != first {
		t.Errorf("values changed across runs: first=%+v second=%+v", first, second)
	}
}

func TestResolveEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hecate.bootstrap.json")

	original, err := Resolve(path, "")
	if err != nil {
		t.Fatalf("seed Resolve: %v", err)
	}

	const overrideSecret = "abcdef0123456789abcdef0123456789abcdef0123456789ab=="
	b, err := Resolve(path, overrideSecret)
	if err != nil {
		t.Fatalf("Resolve with env override: %v", err)
	}
	if b.ControlPlaneSecretKey != overrideSecret {
		t.Errorf("env secret didn't override; got %q", b.ControlPlaneSecretKey)
	}
	if b == original {
		t.Error("override should have changed values from initial seed")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var disk Bootstrap
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if disk != b {
		t.Errorf("file not updated to env values: %+v", disk)
	}
}

func TestResolveCreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deeper", "hecate.bootstrap.json")

	if _, err := Resolve(path, ""); err != nil {
		t.Fatalf("Resolve with missing parent dir: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file at %s, got %v", path, err)
	}
}

func TestResolveRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hecate.bootstrap.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := Resolve(path, ""); err == nil {
		t.Error("expected error on corrupt JSON, got nil")
	}
}
