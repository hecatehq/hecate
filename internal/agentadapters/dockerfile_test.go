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
