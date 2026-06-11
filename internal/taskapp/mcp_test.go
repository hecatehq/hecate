package taskapp

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/secrets"
	"github.com/hecatehq/hecate/pkg/types"
)

func newTestCipher(t *testing.T) secrets.Cipher {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("k"), 32))
	c, err := secrets.NewAESGCMCipher(key)
	if err != nil {
		t.Fatalf("newTestCipher: %v", err)
	}
	return c
}

func TestIsMCPEnvRef(t *testing.T) {
	valid := []string{"$GITHUB_TOKEN", "$A", "$_UNDER", "$tok123"}
	for _, v := range valid {
		if !IsMCPEnvRef(v) {
			t.Errorf("IsMCPEnvRef(%q) = false, want true", v)
		}
	}
	invalid := []string{"", "$", "$1BAD", "$foo-bar", "LITERAL", "enc:x"}
	for _, v := range invalid {
		if IsMCPEnvRef(v) {
			t.Errorf("IsMCPEnvRef(%q) = true, want false", v)
		}
	}
}

func TestNormalizeMCPServerConfigs_Validation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		desc    string
		items   []MCPServerCommand
		wantErr string
	}{
		{
			desc:    "empty name",
			items:   []MCPServerCommand{{Name: "", Command: "npx"}},
			wantErr: "name is required",
		},
		{
			desc:    "empty command and url",
			items:   []MCPServerCommand{{Name: "fs", Command: ""}},
			wantErr: "either command or url is required",
		},
		{
			desc: "duplicate name",
			items: []MCPServerCommand{
				{Name: "fs", Command: "npx"},
				{Name: "fs", Command: "uvx"},
			},
			wantErr: "duplicate name",
		},
		{
			desc: "invalid approval_policy value",
			items: []MCPServerCommand{
				{Name: "gh", Command: "npx", ApprovalPolicy: "yolo"},
			},
			wantErr: "approval_policy",
		},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := normalizeMCPServerConfigs(tc.items, nil, 0)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %q, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestNormalizeMCPServerConfigs_RefPassesThrough(t *testing.T) {
	t.Parallel()
	for _, cipher := range []secrets.Cipher{nil, newTestCipher(t)} {
		items := []MCPServerCommand{
			{Name: "gh", Command: "npx", Env: map[string]string{"TOKEN": "$GITHUB_TOKEN"}},
		}
		out, err := normalizeMCPServerConfigs(items, cipher, 0)
		if err != nil {
			t.Fatalf("normalize: %v", err)
		}
		if got := out[0].Env["TOKEN"]; got != "$GITHUB_TOKEN" {
			t.Errorf("cipher=%v: TOKEN = %q, want $GITHUB_TOKEN (ref must not be altered)", cipher, got)
		}
	}
}

func TestNormalizeMCPServerConfigs_NoCipherLiteralStoredAsIs(t *testing.T) {
	t.Parallel()
	items := []MCPServerCommand{
		{Name: "gh", Command: "npx", Env: map[string]string{"TOKEN": "plaintext-secret"}},
	}
	out, err := normalizeMCPServerConfigs(items, nil, 0)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got := out[0].Env["TOKEN"]; got != "plaintext-secret" {
		t.Errorf("TOKEN = %q, want literal passthrough when cipher nil", got)
	}
}

func TestNormalizeMCPServerConfigs_CipherEncryptsLiteral(t *testing.T) {
	t.Parallel()
	cipher := newTestCipher(t)
	items := []MCPServerCommand{
		{Name: "gh", Command: "npx", Env: map[string]string{"TOKEN": "my-plaintext-token"}},
	}
	out, err := normalizeMCPServerConfigs(items, cipher, 0)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	stored := out[0].Env["TOKEN"]
	if !strings.HasPrefix(stored, types.MCPEnvEncPrefix) {
		t.Fatalf("TOKEN = %q, want enc: prefix", stored)
	}
	plaintext, err := cipher.Decrypt(stored[len(types.MCPEnvEncPrefix):])
	if err != nil {
		t.Fatalf("decrypt stored value: %v", err)
	}
	if plaintext != "my-plaintext-token" {
		t.Errorf("round-trip plaintext = %q, want %q", plaintext, "my-plaintext-token")
	}
}

func TestNormalizeMCPServerConfigs_AlreadyEncryptedPassesThrough(t *testing.T) {
	t.Parallel()
	cipher := newTestCipher(t)
	ct, _ := cipher.Encrypt("secret")
	already := types.MCPEnvEncPrefix + ct

	items := []MCPServerCommand{
		{Name: "gh", Command: "npx", Env: map[string]string{"TOKEN": already}},
	}
	out, err := normalizeMCPServerConfigs(items, cipher, 0)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got := out[0].Env["TOKEN"]; got != already {
		t.Errorf("TOKEN = %q, want already-encrypted value unchanged", got)
	}
}

func TestNormalizeMCPServerConfigs_ApprovalPolicyAccepted(t *testing.T) {
	t.Parallel()
	cases := []string{"", types.MCPApprovalAuto, types.MCPApprovalRequireApproval, types.MCPApprovalBlock}
	for _, policy := range cases {
		t.Run("policy="+policy, func(t *testing.T) {
			items := []MCPServerCommand{
				{Name: "gh", Command: "npx", ApprovalPolicy: policy},
			}
			out, err := normalizeMCPServerConfigs(items, nil, 0)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			if got := out[0].ApprovalPolicy; got != policy {
				t.Errorf("ApprovalPolicy = %q, want %q", got, policy)
			}
		})
	}
}

func makeMCPItems(n int) []MCPServerCommand {
	out := make([]MCPServerCommand, n)
	for i := range out {
		out[i] = MCPServerCommand{
			Name:    fmt.Sprintf("srv-%d", i),
			Command: "npx",
		}
	}
	return out
}

func TestNormalizeMCPServerConfigs_CapAcceptsAtBoundary(t *testing.T) {
	t.Parallel()
	const max = 4
	out, err := normalizeMCPServerConfigs(makeMCPItems(max), nil, max)
	if err != nil {
		t.Fatalf("at-boundary normalize: %v", err)
	}
	if len(out) != max {
		t.Errorf("len(out) = %d, want %d", len(out), max)
	}
}

func TestNormalizeMCPServerConfigs_CapRejectsOverLimit(t *testing.T) {
	t.Parallel()
	const max = 4
	_, err := normalizeMCPServerConfigs(makeMCPItems(max+1), nil, max)
	if err == nil {
		t.Fatal("expected error for over-cap entries, got nil")
	}
	if !strings.Contains(err.Error(), "5") || !strings.Contains(err.Error(), "4") {
		t.Errorf("err = %q, want it to contain both counts (5 and 4)", err)
	}
	if !strings.Contains(err.Error(), "HECATE_TASK_MAX_MCP_SERVERS_PER_TASK") {
		t.Errorf("err = %q, want it to mention the env var", err)
	}
}

func TestNormalizeMCPServerConfigs_CapDisabledByZero(t *testing.T) {
	t.Parallel()
	out, err := normalizeMCPServerConfigs(makeMCPItems(50), nil, 0)
	if err != nil {
		t.Fatalf("normalize with cap=0 should not enforce: %v", err)
	}
	if len(out) != 50 {
		t.Errorf("len(out) = %d, want 50", len(out))
	}
}
