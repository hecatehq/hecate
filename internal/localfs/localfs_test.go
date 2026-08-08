package localfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureBoundedLocalPathAndOpenedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata")
	if err := os.WriteFile(path, []byte("bounded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureBoundedPath(path); err != nil {
		t.Fatalf("EnsureBoundedPath: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := EnsureBoundedFile(file); err != nil {
		t.Fatalf("EnsureBoundedFile: %v", err)
	}
}
