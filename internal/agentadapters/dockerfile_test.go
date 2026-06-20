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
	want := map[string]string{
		"CODEX_ACP_ADAPTER_VERSION":       "v0.1.0-alpha.12",
		"CLAUDE_CODE_ACP_ADAPTER_VERSION": "v0.1.0-alpha.12",
	}
	for arg, wantVersion := range want {
		devVersion := dockerfileArgValue(dev, arg)
		releaseVersion := dockerfileArgValue(release, arg)
		if devVersion == "" || releaseVersion == "" {
			t.Fatalf("%s versions = dev:%q release:%q, want both Dockerfiles pinned", arg, devVersion, releaseVersion)
		}
		if devVersion != releaseVersion {
			t.Fatalf("%s versions drifted: Dockerfile=%s Dockerfile.release=%s", arg, devVersion, releaseVersion)
		}
		if devVersion != wantVersion {
			t.Fatalf("%s version = %s, want %s", arg, devVersion, wantVersion)
		}
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
