package api

import (
	"testing"

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
