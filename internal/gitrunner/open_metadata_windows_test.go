//go:build windows

package gitrunner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenReadOnlyMetadataRejectsNetworkAndDeviceNamespaces(t *testing.T) {
	for _, path := range []string{`\\server\share\exclude`, `\\.\pipe\hecate-test`, `\\?\UNC\server\share\exclude`} {
		if _, err := openReadOnlyMetadata(path); err == nil || !strings.Contains(err.Error(), "local drive") {
			t.Errorf("openReadOnlyMetadata(%q) error = %v, want local-drive refusal", path, err)
		}
	}
}

func TestOpenReadOnlyMetadataRejectsIntermediateReparsePoint(t *testing.T) {
	dir := t.TempDir()
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "exclude"), []byte("ignored/**\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}
	if _, err := openReadOnlyMetadata(filepath.Join(link, "exclude")); err == nil {
		t.Fatalf("intermediate reparse open error = %v, want refusal", err)
	}
}

func TestOpenReadOnlyMetadataRejectsIntermediateReplacement(t *testing.T) {
	dir := t.TempDir()
	target := t.TempDir()
	metadataDir := filepath.Join(dir, "metadata")
	if err := os.Mkdir(metadataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(metadataDir, "exclude")
	if err := os.WriteFile(path, []byte("local/**\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "exclude"), []byte("remote/**\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := openReadOnlyMetadataWithHook(path, func() {
		if renameErr := os.Rename(metadataDir, metadataDir+"-old"); renameErr != nil {
			t.Fatalf("rename metadata directory: %v", renameErr)
		}
		if linkErr := os.Symlink(target, metadataDir); linkErr != nil {
			t.Skipf("directory symlinks are unavailable: %v", linkErr)
		}
	})
	if err == nil {
		t.Fatal("open after intermediate replacement succeeded, want no-follow refusal")
	}
}

func TestOpenReadOnlyMetadataRejectsReservedDeviceBasenames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"NUL", "CON", "COM1", "LPT9", "nul.txt"} {
		if file, err := openReadOnlyMetadata(filepath.Join(dir, name)); err == nil {
			_ = file.Close()
			t.Errorf("openReadOnlyMetadata(%q) succeeded, want reserved-device refusal", name)
		}
	}
}

func TestOpenReadOnlyMetadataAllowsLocalRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exclude")
	if err := os.WriteFile(path, []byte("ignored/**\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openReadOnlyMetadata(path)
	if err != nil {
		t.Fatalf("openReadOnlyMetadata: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
