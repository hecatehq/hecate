package taskapp

import (
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/secrets"
	"github.com/hecatehq/hecate/pkg/types"
)

// NormalizeMCPServerConfigs converts the command shape into the internal
// types.MCPServerConfig slice used by the orchestrator. Trims whitespace
// on string fields, enforces non-empty Name, rejects duplicate names,
// validates that exactly one of command or url is set per entry, and
// caps the number of entries.
//
// maxEntries is the per-task cap. Zero or negative disables the cap
// (used by tests that don't care about the limit). When exceeded the
// caller surfaces this as a 400 — a malformed task configuring 1000
// servers would otherwise burn N initialize handshakes and N file
// descriptors before the run even started.
//
// Env and Header values are stored in three forms depending on cipher
// availability:
//   - "$VAR_NAME" references are stored verbatim — resolved from the
//     Hecate process environment at subprocess spawn time.
//   - Literal values are encrypted with cipher when available →
//     stored as "enc:<base64>". When cipher is nil they are stored
//     as-is; operators should prefer $VAR_NAME references in that case.
//
// Returns nil for an empty input (the agent loop skips MCP-host
// startup when MCPServers is nil/empty).
func NormalizeMCPServerConfigs(items []MCPServerCommand, cipher secrets.Cipher, maxEntries int) ([]types.MCPServerConfig, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if maxEntries > 0 && len(items) > maxEntries {
		return nil, fmt.Errorf("mcp_servers: %d entries exceeds per-task cap of %d (raise HECATE_TASK_MAX_MCP_SERVERS_PER_TASK if you genuinely need more)", len(items), maxEntries)
	}
	out := make([]types.MCPServerConfig, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for i, item := range items {
		name := strings.TrimSpace(item.Name)
		command := strings.TrimSpace(item.Command)
		rawURL := strings.TrimSpace(item.URL)
		if name == "" {
			return nil, fmt.Errorf("mcp_servers[%d]: name is required", i)
		}
		if command != "" && rawURL != "" {
			return nil, fmt.Errorf("mcp_servers[%d] (%s): command and url are mutually exclusive", i, name)
		}
		if command == "" && rawURL == "" {
			return nil, fmt.Errorf("mcp_servers[%d] (%s): either command or url is required", i, name)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("mcp_servers[%d]: duplicate name %q", i, name)
		}
		seen[name] = struct{}{}
		policy := strings.TrimSpace(item.ApprovalPolicy)
		if !types.IsValidMCPApprovalPolicy(policy) {
			return nil, fmt.Errorf("mcp_servers[%d] (%s): approval_policy %q is invalid; must be one of \"auto\", \"require_approval\", \"block\"", i, name, policy)
		}
		args := append([]string(nil), item.Args...)
		env, err := StoreSecretMap(item.Env, cipher, fmt.Sprintf("mcp_servers[%d] (%s): env", i, name))
		if err != nil {
			return nil, err
		}
		headers, err := StoreSecretMap(item.Headers, cipher, fmt.Sprintf("mcp_servers[%d] (%s): headers", i, name))
		if err != nil {
			return nil, err
		}
		out = append(out, types.MCPServerConfig{
			Name:           name,
			Command:        command,
			Args:           args,
			Env:            env,
			URL:            rawURL,
			Headers:        headers,
			ApprovalPolicy: policy,
		})
	}
	return out, nil
}

// StoreSecretMap applies the MCP secret storage rules to every value in m,
// returning a defensive copy. Returns nil for a nil or empty input.
func StoreSecretMap(m map[string]string, cipher secrets.Cipher, errPrefix string) (map[string]string, error) {
	if len(m) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		stored, err := storeMCPEnvValue(v, cipher)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", errPrefix, k, err)
		}
		out[k] = stored
	}
	return out, nil
}

func storeMCPEnvValue(v string, cipher secrets.Cipher) (string, error) {
	if IsMCPEnvRef(v) || strings.HasPrefix(v, types.MCPEnvEncPrefix) {
		return v, nil
	}
	if cipher == nil {
		return v, nil
	}
	ct, err := cipher.Encrypt(v)
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}
	return types.MCPEnvEncPrefix + ct, nil
}

// IsMCPEnvRef reports whether v is a $VAR_NAME reference. Mirrors the
// orchestrator's resolveEnvValue rules so storage and spawn agree on what
// counts as a reference vs. a value that should be redacted or encrypted.
func IsMCPEnvRef(v string) bool {
	if len(v) < 2 || v[0] != '$' {
		return false
	}
	for i, c := range v[1:] {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
