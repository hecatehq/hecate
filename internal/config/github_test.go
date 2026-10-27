package config

import (
	"path/filepath"
	"testing"
)

func TestGitHubConfiguration_ExplicitCompleteConfiguration(t *testing.T) {
	t.Setenv("HECATE_GITHUB_APP_CLIENT_ID", "Iv1.app")
	t.Setenv("HECATE_GITHUB_APP_INSTALLATION_ID", "12")
	t.Setenv("HECATE_GITHUB_APP_PRIVATE_KEY_FILE", filepath.Join(t.TempDir(), "github.pem"))
	t.Setenv("HECATE_GITHUB_REPOSITORIES", "owner/one,owner/two")
	cfg := LoadFromEnv()
	if !cfg.GitHub.Configured() || cfg.GitHub.InstallationID != 12 || len(cfg.GitHub.Repositories) != 2 || cfg.GitHub.Validate() != nil {
		t.Fatalf("GitHub config not loaded")
	}
	cfg.GitHub.PrivateKeyFile = ""
	if cfg.Validate() == nil {
		t.Fatal("partial GitHub configuration accepted")
	}
}
