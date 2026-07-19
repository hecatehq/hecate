package codeintel

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/workspacefs"
)

func TestPositions_ConvertUTF8ByteColumnsAndUTF16(t *testing.T) {
	file := &sourceFile{data: []byte("a😀éz\n")}
	position, err := file.requestPosition(1, 8, positionUTF16)
	if err != nil {
		t.Fatalf("request position: %v", err)
	}
	if position.Character != 4 {
		t.Fatalf("UTF-16 character = %d, want 4", position.Character)
	}
	byteOffset, err := lspCharacterToBytes(file.data, lspPosition{Line: 0, Character: 4}, positionUTF16)
	if err != nil {
		t.Fatalf("response position: %v", err)
	}
	if byteOffset != 7 {
		t.Fatalf("UTF-8 byte offset = %d, want 7", byteOffset)
	}
	if _, err := file.requestPosition(1, 3, positionUTF8); err == nil || !strings.Contains(err.Error(), "splits") {
		t.Fatalf("split UTF-8 error = %v, want split rejection", err)
	}
	if _, err := lspCharacterToBytes(file.data, lspPosition{Line: 0, Character: 2}, positionUTF16); err == nil {
		t.Fatal("expected split surrogate pair to be rejected")
	}
}

func TestPositions_WorkspaceURIRejectsExternalAndSymlinkPaths(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "safe.go"), []byte("package safe\n"), 0o644); err != nil {
		t.Fatalf("write safe file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "outside.go"), []byte("package outside\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	fsys, err := workspacefs.New(workspace)
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	if got, err := workspaceRelativeURI(fsys, pathToFileURI(filepath.Join(workspace, "safe.go"))); err != nil || filepath.ToSlash(got) != "safe.go" {
		t.Fatalf("safe URI = %q, %v", got, err)
	}
	if _, err := workspaceRelativeURI(fsys, pathToFileURI(filepath.Join(outside, "outside.go"))); err == nil {
		t.Fatal("expected outside URI to be rejected")
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Symlink(filepath.Join(outside, "outside.go"), filepath.Join(workspace, "linked.go")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := workspaceRelativeURI(fsys, pathToFileURI(filepath.Join(workspace, "linked.go"))); err == nil {
		t.Fatal("expected symlink URI to be rejected")
	}
}

func TestSourceCacheBoundsRetainedFilesWithoutDroppingNormalization(t *testing.T) {
	workspace := t.TempDir()
	firstData := []byte("package first\n")
	secondData := []byte("package second\n")
	if err := os.WriteFile(filepath.Join(workspace, "first.go"), firstData, 0o644); err != nil {
		t.Fatalf("write first source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "second.go"), secondData, 0o644); err != nil {
		t.Fatalf("write second source: %v", err)
	}
	fsys, err := workspacefs.New(workspace)
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	cache := newSourceCache(fsys)
	cache.maxBytes = int64(len(firstData))
	if _, err := cache.openRelative("first.go"); err != nil {
		t.Fatalf("open first source: %v", err)
	}
	second, err := cache.openRelative("second.go")
	if err != nil {
		t.Fatalf("open uncached second source: %v", err)
	}
	if string(second.data) != string(secondData) {
		t.Fatalf("second source = %q, want %q", second.data, secondData)
	}
	if len(cache.files) != 1 || cache.files["first.go"] == nil || cache.files["second.go"] != nil {
		t.Fatalf("cached files = %+v, want only first.go", cache.files)
	}
	if cache.bytes != int64(len(firstData)) {
		t.Fatalf("retained bytes = %d, want %d", cache.bytes, len(firstData))
	}
}
