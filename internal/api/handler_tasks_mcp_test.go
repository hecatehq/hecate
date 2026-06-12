package api

import (
	"encoding/json"
	"reflect"
	"testing"

	mcpclient "github.com/hecatehq/hecate/internal/mcp/client"
	mcpregistry "github.com/hecatehq/hecate/internal/mcp/registry"
	"github.com/hecatehq/hecate/pkg/types"
)

// TestRenderMCPServerConfigs_RefsShownVerbatim: $VAR_NAME references are
// safe to return in API responses — they name a variable, not a secret.
func TestRenderMCPServerConfigs_RefsShownVerbatim(t *testing.T) {
	t.Parallel()
	configs := []types.MCPServerConfig{
		{Name: "gh", Command: "npx", Env: map[string]string{"TOKEN": "$GITHUB_TOKEN"}},
	}
	out := renderMCPServerConfigs(configs)
	if got := out[0].Env["TOKEN"]; got != "$GITHUB_TOKEN" {
		t.Errorf("TOKEN = %q, want $GITHUB_TOKEN (ref must be shown)", got)
	}
}

// TestRenderMCPServerConfigs_EncryptedValueRedacted: enc: ciphertext must
// not leak through the task API — it becomes "[redacted]".
func TestRenderMCPServerConfigs_EncryptedValueRedacted(t *testing.T) {
	t.Parallel()
	configs := []types.MCPServerConfig{
		{Name: "gh", Command: "npx", Env: map[string]string{"TOKEN": types.MCPEnvEncPrefix + "someciphertext="}},
	}
	out := renderMCPServerConfigs(configs)
	if got := out[0].Env["TOKEN"]; got != "[redacted]" {
		t.Errorf("TOKEN = %q, want [redacted]", got)
	}
}

// TestRenderMCPServerConfigs_LiteralRedacted: bare literal values (stored
// when no cipher was configured) are also hidden, not returned to callers.
func TestRenderMCPServerConfigs_LiteralRedacted(t *testing.T) {
	t.Parallel()
	configs := []types.MCPServerConfig{
		{Name: "gh", Command: "npx", Env: map[string]string{"TOKEN": "plaintext-leaked"}},
	}
	out := renderMCPServerConfigs(configs)
	if got := out[0].Env["TOKEN"]; got != "[redacted]" {
		t.Errorf("TOKEN = %q, want [redacted] for literal values", got)
	}
}

// TestRenderMCPServerConfigs_EmptyEnvOmitted: no Env map on the item
// when the config has no env — avoids a non-nil empty map in the JSON.
func TestRenderMCPServerConfigs_EmptyEnvOmitted(t *testing.T) {
	t.Parallel()
	configs := []types.MCPServerConfig{{Name: "fs", Command: "uvx"}}
	out := renderMCPServerConfigs(configs)
	if out[0].Env != nil {
		t.Errorf("Env = %v, want nil for config with no env", out[0].Env)
	}
}

// TestRenderMCPServerConfigs_ApprovalPolicyShownVerbatim: the policy is
// not a secret — it appears in API responses unchanged so the UI can
// render the operator's chosen gate accurately.
func TestRenderMCPServerConfigs_ApprovalPolicyShownVerbatim(t *testing.T) {
	t.Parallel()
	configs := []types.MCPServerConfig{
		{Name: "gh", Command: "npx", ApprovalPolicy: types.MCPApprovalRequireApproval},
	}
	out := renderMCPServerConfigs(configs)
	if got := out[0].ApprovalPolicy; got != types.MCPApprovalRequireApproval {
		t.Errorf("ApprovalPolicy = %q, want %q", got, types.MCPApprovalRequireApproval)
	}
}

func TestRenderMCPProbeTools_MCPAppsMetadata(t *testing.T) {
	t.Parallel()
	tools := []mcpclient.NamespacedTool{{
		Name:          "weather",
		Description:   "Weather with UI",
		Schema:        json.RawMessage(`{"type":"object"}`),
		Meta:          json.RawMessage(`{"ui":{"resourceUri":"ui://weather/dashboard","visibility":["app"]}}`),
		UIResourceURI: "ui://weather/dashboard",
		UIVisibility:  []string{"app"},
		ModelVisible:  false,
	}}

	out := renderMCPProbeTools(tools)
	if len(out) != 1 {
		t.Fatalf("tools len = %d, want 1", len(out))
	}
	if got := string(out[0].Meta); got != `{"ui":{"resourceUri":"ui://weather/dashboard","visibility":["app"]}}` {
		t.Fatalf("_meta = %s", got)
	}
	if out[0].UIResourceURI != "ui://weather/dashboard" {
		t.Fatalf("ui_resource_uri = %q", out[0].UIResourceURI)
	}
	if !reflect.DeepEqual(out[0].UIVisibility, []string{"app"}) {
		t.Fatalf("ui_visibility = %#v", out[0].UIVisibility)
	}
	if out[0].ModelVisible {
		t.Fatal("model_visible = true, want false")
	}
}

func TestBuildMCPRegistryInstallHints_StreamableHTTPRemote(t *testing.T) {
	t.Parallel()

	hints := buildMCPRegistryInstallHints(mcpregistry.ServerDetail{
		Name: "io.github/example/weather",
		Remotes: []mcpregistry.Remote{{
			Type: "streamable-http",
			URL:  "https://weather.example/mcp",
			Headers: []mcpregistry.InputSpec{
				{Name: "Authorization", IsRequired: true, IsSecret: true},
				{Name: "X-Client", Value: "hecate"},
			},
		}},
		Packages: []mcpregistry.Package{{
			RegistryType:         "npm",
			Identifier:           "@example/weather",
			RuntimeHint:          "npx",
			Transport:            mcpregistry.Transport{Type: "stdio"},
			EnvironmentVariables: []mcpregistry.InputSpec{{Name: "API_KEY", IsSecret: true}},
		}},
	})

	if len(hints) != 2 {
		t.Fatalf("hints len = %d, want 2", len(hints))
	}
	remote := hints[0]
	if !remote.Supported {
		t.Fatalf("remote supported = false, reason=%q", remote.UnsupportedReason)
	}
	if remote.HecateConfig == nil {
		t.Fatal("remote hecate_config = nil")
	}
	if remote.HecateConfig.Name != "weather" {
		t.Fatalf("hecate_config.name = %q, want weather", remote.HecateConfig.Name)
	}
	if remote.HecateConfig.Headers["Authorization"] != "$MCP_AUTHORIZATION" {
		t.Fatalf("Authorization header = %q", remote.HecateConfig.Headers["Authorization"])
	}
	if remote.HecateConfig.Headers["X-Client"] != "hecate" {
		t.Fatalf("X-Client header = %q", remote.HecateConfig.Headers["X-Client"])
	}
	if !reflect.DeepEqual(remote.RequiredSecrets, []string{"MCP_AUTHORIZATION"}) {
		t.Fatalf("remote required_secrets = %#v", remote.RequiredSecrets)
	}

	pkg := hints[1]
	if pkg.Supported {
		t.Fatal("package supported = true, want conservative unsupported hint")
	}
	if pkg.Source != "package" || pkg.RegistryType != "npm" || pkg.Identifier != "@example/weather" {
		t.Fatalf("package hint = %#v", pkg)
	}
	if !reflect.DeepEqual(pkg.RequiredSecrets, []string{"MCP_API_KEY"}) {
		t.Fatalf("package required_secrets = %#v", pkg.RequiredSecrets)
	}
}
