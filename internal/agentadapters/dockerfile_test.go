package agentadapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerfilesInstallGoACPAdapterReleaseBinaries(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"Dockerfile", "Dockerfile.release"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw, err := os.ReadFile(filepath.Join("..", "..", name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			text := string(raw)
			for _, required := range []string{
				"FROM alpine:${ALPINE_VERSION} AS adapter-downloader",
				"ARG CODEX_ACP_ADAPTER_VERSION=",
				"ARG CLAUDE_CODE_ACP_ADAPTER_VERSION=",
				"download_adapter codex-acp-adapter codex-acp-adapter \"$CODEX_ACP_ADAPTER_VERSION\"",
				"download_adapter claude-code-acp-adapter claude-code-acp-adapter \"$CLAUDE_CODE_ACP_ADAPTER_VERSION\"",
				"COPY --from=adapter-downloader /adapter-bin/codex-acp-adapter /usr/local/bin/codex-acp-adapter",
				"COPY --from=adapter-downloader /adapter-bin/claude-code-acp-adapter /usr/local/bin/claude-code-acp-adapter",
			} {
				if !strings.Contains(text, required) {
					t.Fatalf("%s is missing adapter release-binary contract %q", name, required)
				}
			}
			for _, forbidden := range []string{
				"@hecatehq/codex-acp-adapter",
				"@hecatehq/claude-code-acp-adapter",
				"@zed-industries/codex-acp",
				"@agentclientprotocol/claude-agent-acp",
			} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("%s contains legacy npm ACP adapter package %q", name, forbidden)
				}
			}
		})
	}
}

func TestDockerfilesPinSameGoACPAdapterVersions(t *testing.T) {
	t.Parallel()

	dev := readDockerfile(t, "Dockerfile")
	release := readDockerfile(t, "Dockerfile.release")
	goMod := readRepoFile(t, "go.mod")
	adapters := map[string]Adapter{}
	for _, adapter := range BuiltIns() {
		adapters[adapter.ID] = adapter
	}

	tests := []struct {
		name   string
		id     string
		arg    string
		module string
	}{
		{
			name:   "codex",
			id:     "codex",
			arg:    "CODEX_ACP_ADAPTER_VERSION",
			module: "github.com/hecatehq/codex-acp-adapter",
		},
		{
			name:   "claude_code",
			id:     "claude_code",
			arg:    "CLAUDE_CODE_ACP_ADAPTER_VERSION",
			module: "github.com/hecatehq/claude-code-acp-adapter",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			devVersion := dockerfileArgValue(dev, tt.arg)
			releaseVersion := dockerfileArgValue(release, tt.arg)
			moduleVersion := goModRequireVersion(t, goMod, tt.module)
			adapter, ok := adapters[tt.id]
			if !ok {
				t.Fatalf("missing built-in adapter %q", tt.id)
			}

			if devVersion == "" || releaseVersion == "" {
				t.Fatalf("%s versions = dev:%q release:%q, want both Dockerfiles pinned", tt.arg, devVersion, releaseVersion)
			}
			if devVersion != releaseVersion {
				t.Fatalf("%s versions drifted: Dockerfile=%s Dockerfile.release=%s", tt.arg, devVersion, releaseVersion)
			}
			if devVersion != moduleVersion {
				t.Fatalf("%s version = %s, want go.mod %s", tt.arg, devVersion, moduleVersion)
			}
			if !satisfiesRange(strings.TrimPrefix(devVersion, "v"), adapter.SupportedRange) {
				t.Fatalf("%s version %s does not satisfy registry range %q", tt.arg, devVersion, adapter.SupportedRange)
			}
			wantRange := ">=" + strings.TrimPrefix(moduleVersion, "v")
			if adapter.SupportedRange != wantRange {
				t.Fatalf("%s registry range = %q, want %q from go.mod", tt.id, adapter.SupportedRange, wantRange)
			}
		})
	}
}

func TestDockerfilesPinSameCursorInstallerChecksum(t *testing.T) {
	t.Parallel()

	dev := readDockerfile(t, "Dockerfile")
	release := readDockerfile(t, "Dockerfile.release")
	const want = "05d42095f24db4289feff7a08934a23ce68d5a6cf9e9d85e4c538939671b33ea"

	devChecksum := dockerfileArgValue(dev, "CURSOR_INSTALL_SHA256")
	releaseChecksum := dockerfileArgValue(release, "CURSOR_INSTALL_SHA256")
	if devChecksum == "" || releaseChecksum == "" {
		t.Fatalf("CURSOR_INSTALL_SHA256 = dev:%q release:%q, want both Dockerfiles pinned", devChecksum, releaseChecksum)
	}
	if devChecksum != releaseChecksum {
		t.Fatalf("CURSOR_INSTALL_SHA256 drifted: Dockerfile=%s Dockerfile.release=%s", devChecksum, releaseChecksum)
	}
	if devChecksum != want {
		t.Fatalf("CURSOR_INSTALL_SHA256 = %s, want %s", devChecksum, want)
	}
}

func readDockerfile(t testing.TB, name string) string {
	t.Helper()
	return readRepoFile(t, name)
}

func readRepoFile(t testing.TB, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(raw)
}

func dockerfileArgValue(text string, name string) string {
	prefix := "ARG " + name + "="
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func goModRequireVersion(t testing.TB, text, module string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == module {
			return fields[1]
		}
	}
	t.Fatalf("go.mod missing require for %s", module)
	return ""
}
