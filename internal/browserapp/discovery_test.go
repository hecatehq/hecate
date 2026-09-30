package browserapp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiscoveryRejectsInvalidPathsAndDeduplicatesAliases(t *testing.T) {
	path := browserFixture(t)
	installations := []installation{{"Chromium", path}, {"Duplicate", path}, {"Relative", "browser"}, {"Directory", t.TempDir()}, {"Missing", filepath.Join(t.TempDir(), "missing")}}
	if runtime.GOOS != "windows" {
		notExecutable := filepath.Join(t.TempDir(), "not-executable")
		if err := os.WriteFile(notExecutable, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
		installations = append(installations, installation{"Non-executable", notExecutable})
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(path, alias); err != nil {
			t.Fatal(err)
		}
		installations = append(installations, installation{"Alias", alias})
	}
	result, err := discover(context.Background(), installations)
	if err != nil || len(result) != 1 || result[0].Name != "Chromium" {
		t.Fatalf("discovery = %#v, %v", result, err)
	}
	if !strings.HasPrefix(result[0].ID, "browser_") || strings.Contains(result[0].ID, path) {
		t.Fatalf("candidate ID is not opaque: %q", result[0].ID)
	}
}

func TestPlatformInstallationsAreBoundedAndDoNotReadPATH(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows", "unsupported"} {
		t.Run(goos, func(t *testing.T) {
			getenv := func(key string) string {
				switch key {
				case "ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA":
					return filepath.Join(t.TempDir(), key)
				default:
					t.Fatalf("unexpected environment discovery %q", key)
					return ""
				}
			}
			entries := platformInstallations(goos, t.TempDir(), getenv)
			if len(entries) > 16 || (goos != "unsupported" && len(entries) == 0) {
				t.Fatalf("unbounded/empty discovery entries: %d", len(entries))
			}
			for _, entry := range entries {
				absolute := filepath.IsAbs(entry.path)
				if goos == "darwin" || goos == "linux" {
					// Cross-platform table cases contain POSIX system roots even
					// when this test runs on Windows, whose IsAbs requires a drive.
					absolute = absolute || strings.HasPrefix(filepath.ToSlash(entry.path), "/")
				}
				if !absolute || entry.name == "" {
					t.Fatalf("invalid known installation: %#v", entry)
				}
			}
		})
	}
}

func TestDiscoveryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := discover(ctx, []installation{{"Chromium", browserFixture(t)}}); err == nil {
		t.Fatal("cancelled discovery succeeded")
	}
}

func TestWindowsEmptyInstallRootsProduceNoCandidates(t *testing.T) {
	if got := platformInstallations("windows", "", func(string) string { return "" }); len(got) != 0 {
		t.Fatalf("empty roots generated candidates: %#v", got)
	}
}
