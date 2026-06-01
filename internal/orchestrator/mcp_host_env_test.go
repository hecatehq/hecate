package orchestrator

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/secrets"
	"github.com/hecatehq/hecate/pkg/types"
)

// newTestCipher builds a real AES-GCM cipher from a deterministic 32-byte
// key. The key is base64-encoded at construction so the input is always
// exactly 32 decoded bytes, which is what AESGCMCipher requires.
func newTestCipher(t *testing.T) secrets.Cipher {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("k"), 32))
	c, err := secrets.NewAESGCMCipher(key)
	if err != nil {
		t.Fatalf("newTestCipher: %v", err)
	}
	return c
}

// TestIsEnvRef pins the $VAR_NAME recognition logic: valid identifiers
// return true, everything else (bare $, digit-start, hyphen, empty)
// returns false.
func TestIsEnvRef(t *testing.T) {
	trueCases := []string{
		"$GITHUB_TOKEN",
		"$A",
		"$_LEADING_UNDER",
		"$token123",
		"$MY_LONG_VAR_NAME_99",
	}
	for _, v := range trueCases {
		if !isEnvRef(v) {
			t.Errorf("isEnvRef(%q) = false, want true", v)
		}
	}

	falseCases := []string{
		"",
		"$",
		"$1STARTS_DIGIT",
		"$foo-bar",
		"$has space",
		"NONDOLLAR",
		"enc:something",
	}
	for _, v := range falseCases {
		if isEnvRef(v) {
			t.Errorf("isEnvRef(%q) = true, want false", v)
		}
	}
}

// TestResolveEnvConfigs_RefResolvedFromEnv: $VAR → value from os env.
func TestResolveEnvConfigs_RefResolvedFromEnv(t *testing.T) {
	t.Setenv("HECATE_TEST_MCP_TOKEN", "s3cr3t-resolved")
	configs := []types.MCPServerConfig{
		{Name: "github", Env: map[string]string{"GITHUB_TOKEN": "$HECATE_TEST_MCP_TOKEN"}},
	}
	resolved, err := resolveEnvConfigs(configs, nil)
	if err != nil {
		t.Fatalf("resolveEnvConfigs: %v", err)
	}
	if got := resolved[0].Env["GITHUB_TOKEN"]; got != "s3cr3t-resolved" {
		t.Errorf("GITHUB_TOKEN = %q, want %q", got, "s3cr3t-resolved")
	}
}

// TestResolveEnvConfigs_LiteralPassesThrough: bare literal → unchanged.
func TestResolveEnvConfigs_LiteralPassesThrough(t *testing.T) {
	configs := []types.MCPServerConfig{
		{Name: "fs", Env: map[string]string{"HOME": "/workspace"}},
	}
	resolved, err := resolveEnvConfigs(configs, nil)
	if err != nil {
		t.Fatalf("resolveEnvConfigs: %v", err)
	}
	if got := resolved[0].Env["HOME"]; got != "/workspace" {
		t.Errorf("HOME = %q, want /workspace", got)
	}
}

// TestResolveEnvConfigs_EncryptedValueDecrypted: enc: → decrypted with cipher.
func TestResolveEnvConfigs_EncryptedValueDecrypted(t *testing.T) {
	cipher := newTestCipher(t)
	plaintext := "super-secret-token"
	ct, err := cipher.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	stored := types.MCPEnvEncPrefix + ct

	configs := []types.MCPServerConfig{
		{Name: "github", Env: map[string]string{"TOKEN": stored}},
	}
	resolved, err := resolveEnvConfigs(configs, cipher)
	if err != nil {
		t.Fatalf("resolveEnvConfigs: %v", err)
	}
	if got := resolved[0].Env["TOKEN"]; got != plaintext {
		t.Errorf("TOKEN = %q, want %q", got, plaintext)
	}
}

// TestResolveEnvConfigs_EncryptedNoCipherErrors: enc: value without a
// cipher returns a clear diagnostic — the operator will see exactly
// what misconfiguration caused the failure.
func TestResolveEnvConfigs_EncryptedNoCipherErrors(t *testing.T) {
	configs := []types.MCPServerConfig{
		{Name: "github", Env: map[string]string{"TOKEN": types.MCPEnvEncPrefix + "someciphertext"}},
	}
	_, err := resolveEnvConfigs(configs, nil)
	if err == nil {
		t.Fatal("expected error for enc: value with nil cipher")
	}
	if !strings.Contains(err.Error(), "no control-plane secret key") {
		t.Errorf("err = %v, want 'no control-plane secret key' diagnostic", err)
	}
}

// TestResolveEnvConfigs_UnsetRefErrors: $VAR where the var is absent.
func TestResolveEnvConfigs_UnsetRefErrors(t *testing.T) {
	const absent = "HECATE_TEST_MCP_DEFINITELY_ABSENT_XYZ9"
	configs := []types.MCPServerConfig{
		{Name: "github", Env: map[string]string{"TOKEN": "$" + absent}},
	}
	_, err := resolveEnvConfigs(configs, nil)
	if err == nil {
		t.Fatal("expected error for unset env var reference")
	}
	if !strings.Contains(err.Error(), absent) {
		t.Errorf("err = %v, want mention of %q", err, absent)
	}
}

// TestResolveEnvConfigs_EmptyRefErrors: $VAR where the var is set to "".
func TestResolveEnvConfigs_EmptyRefErrors(t *testing.T) {
	t.Setenv("HECATE_TEST_MCP_EMPTY_VAR", "")
	configs := []types.MCPServerConfig{
		{Name: "github", Env: map[string]string{"TOKEN": "$HECATE_TEST_MCP_EMPTY_VAR"}},
	}
	_, err := resolveEnvConfigs(configs, nil)
	if err == nil {
		t.Fatal("expected error for empty env var")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("err = %v, want 'empty' in message", err)
	}
}

// TestResolveEnvConfigs_MalformedRefErrors: values that start with $
// but aren't valid identifier names → clear error, not silent failure.
func TestResolveEnvConfigs_MalformedRefErrors(t *testing.T) {
	malformed := []string{"$", "$1DIGIT", "$foo-bar", "$ space"}
	for _, v := range malformed {
		configs := []types.MCPServerConfig{
			{Name: "x", Env: map[string]string{"K": v}},
		}
		_, err := resolveEnvConfigs(configs, nil)
		if err == nil {
			t.Errorf("resolveEnvConfigs(%q): expected error, got nil", v)
		}
	}
}

// TestResolveEnvConfigs_OriginalNotMutated: the input slice is not
// modified in place — callers that re-use their config slice won't see
// resolved values where references were.
func TestResolveEnvConfigs_OriginalNotMutated(t *testing.T) {
	t.Setenv("HECATE_TEST_MCP_IMMUTABLE", "resolved")
	original := types.MCPServerConfig{
		Name: "x",
		Env:  map[string]string{"K": "$HECATE_TEST_MCP_IMMUTABLE"},
	}
	origValue := original.Env["K"]
	_, _ = resolveEnvConfigs([]types.MCPServerConfig{original}, nil)
	if original.Env["K"] != origValue {
		t.Errorf("original Env was mutated: got %q, want %q", original.Env["K"], origValue)
	}
}

// TestResolveEnvConfigs_EmptyEnvAndNilInput: empty/nil inputs should
// never cause panics or spurious errors.
func TestResolveEnvConfigs_EmptyEnvAndNilInput(t *testing.T) {
	t.Parallel()
	resolved, err := resolveEnvConfigs(nil, nil)
	if err != nil || resolved != nil {
		t.Errorf("nil input: got (%v, %v), want (nil, nil)", resolved, err)
	}

	configs := []types.MCPServerConfig{{Name: "fs"}}
	resolved, err = resolveEnvConfigs(configs, nil)
	if err != nil {
		t.Fatalf("empty-env config: unexpected error: %v", err)
	}
	if len(resolved[0].Env) != 0 {
		t.Errorf("empty env should stay empty, got %v", resolved[0].Env)
	}
}
