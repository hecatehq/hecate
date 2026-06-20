package agentadapters

import (
	"reflect"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/hecatehq/hecate/internal/secrets"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestACPMCPServersConvertsHTTPAndStdioConfigs(t *testing.T) {
	t.Parallel()

	got := acpMCPServers([]types.MCPServerConfig{
		{
			Name: " weather ",
			URL:  " https://example.com/mcp ",
			Headers: map[string]string{
				"X-B": "two",
				"X-A": "one",
				" ":   "ignored",
			},
		},
		{
			Name:    "fs",
			Command: " node ",
			Args:    []string{"server.js"},
			Env: map[string]string{
				"ZED":   "zed",
				"ALPHA": "alpha",
				"":      "ignored",
			},
		},
		{Name: " ", Command: "ignored"},
	})

	want := []acp.McpServer{
		{
			Http: &acp.McpServerHttpInline{
				Type: "http",
				Name: "weather",
				Url:  "https://example.com/mcp",
				Headers: []acp.HttpHeader{
					{Name: "X-A", Value: "one"},
					{Name: "X-B", Value: "two"},
				},
			},
		},
		{
			Stdio: &acp.McpServerStdio{
				Name:    "fs",
				Command: "node",
				Args:    []string{"server.js"},
				Env: []acp.EnvVariable{
					{Name: "ALPHA", Value: "alpha"},
					{Name: "ZED", Value: "zed"},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("acpMCPServers() = %#v, want %#v", got, want)
	}
}

func TestSameMCPServerConfigsDetectsConfigChanges(t *testing.T) {
	t.Parallel()

	base := []types.MCPServerConfig{{
		Name:    "fs",
		Command: "node",
		Args:    []string{"server.js"},
		Env:     map[string]string{"TOKEN": "$TOKEN"},
	}}
	clone := cloneMCPServerConfigs(base)
	if !sameMCPServerConfigs(base, clone) {
		t.Fatalf("sameMCPServerConfigs(base, clone) = false, want true")
	}
	clone[0].Env["TOKEN"] = "$OTHER"
	if sameMCPServerConfigs(base, clone) {
		t.Fatalf("sameMCPServerConfigs detected no change after env mutation")
	}
	if base[0].Env["TOKEN"] != "$TOKEN" {
		t.Fatalf("clone mutation changed original env: %#v", base[0].Env)
	}
}

func TestResolveMCPServerConfigsResolvesEncryptedAndEnvValues(t *testing.T) {
	cipher := newAgentAdapterTestCipher(t)
	token, err := cipher.Encrypt("secret-token")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	t.Setenv("MCP_HEADER", "header-token")
	configs := []types.MCPServerConfig{{
		Name:    "secure",
		Command: "node",
		Env:     map[string]string{"TOKEN": types.MCPEnvEncPrefix + token},
		Headers: map[string]string{"Authorization": "$MCP_HEADER"},
	}}

	got, err := resolveMCPServerConfigs(configs, cipher)
	if err != nil {
		t.Fatalf("resolveMCPServerConfigs: %v", err)
	}
	if got[0].Env["TOKEN"] != "secret-token" || got[0].Headers["Authorization"] != "header-token" {
		t.Fatalf("resolved config = %#v, want decrypted/env values", got[0])
	}
	if strings.HasPrefix(configs[0].Env["TOKEN"], types.MCPEnvEncPrefix) == false || configs[0].Headers["Authorization"] != "$MCP_HEADER" {
		t.Fatalf("original config mutated: %#v", configs[0])
	}
}

func TestResolveMCPServerConfigsEncryptedWithoutCipherErrors(t *testing.T) {
	t.Parallel()

	_, err := resolveMCPServerConfigs([]types.MCPServerConfig{{
		Name:    "secure",
		Command: "node",
		Env:     map[string]string{"TOKEN": types.MCPEnvEncPrefix + "ciphertext"},
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), "no control-plane secret key") {
		t.Fatalf("resolveMCPServerConfigs error = %v, want missing cipher diagnostic", err)
	}
}

func newAgentAdapterTestCipher(t *testing.T) secrets.Cipher {
	t.Helper()
	cipher, err := secrets.NewAESGCMCipher("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatalf("NewAESGCMCipher: %v", err)
	}
	return cipher
}
