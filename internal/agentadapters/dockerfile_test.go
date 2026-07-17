package agentadapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerfilesUseEmbeddedACPAdapters(t *testing.T) {
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
				"@openai/codex@${OPENAI_CODEX_VERSION}",
				"@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}",
			} {
				if !strings.Contains(text, required) {
					t.Fatalf("%s is missing provider CLI contract %q", name, required)
				}
			}
			for _, forbidden := range []string{
				"adapter-downloader",
				"ACP_ADAPTER_VERSION",
				"/usr/local/bin/codex-acp-adapter",
				"/usr/local/bin/claude-code-acp-adapter",
				"@hecatehq/codex-acp-adapter",
				"@hecatehq/claude-code-acp-adapter",
				"@zed-industries/codex-acp",
				"@agentclientprotocol/claude-agent-acp",
			} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("%s contains standalone ACP adapter packaging %q", name, forbidden)
				}
			}
		})
	}
}

func TestDockerfilesPinSameCursorInstallerChecksum(t *testing.T) {
	t.Parallel()

	dev := readDockerfile(t, "Dockerfile")
	release := readDockerfile(t, "Dockerfile.release")
	const want = "d15e655a16bf4cd5551003d41e428c330bdca3a6904c1cd4e0a279e3d50f73e5"

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
