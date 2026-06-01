package agentadapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvExampleOmitsAgentAdapterFixtureOverrides(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join("..", "..", ".env.example"))
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	text := string(content)
	for _, name := range []string{adapterDiscoveryOverrideEnv, adapterDevOverrideEnv} {
		if strings.Contains(text, name) {
			t.Fatalf(".env.example exposes %s; fixture-only adapter overrides must stay in development docs and just recipes", name)
		}
	}
}
