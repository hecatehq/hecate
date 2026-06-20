package agentadapters

import (
	"fmt"
	"os"
	"sort"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/hecatehq/hecate/internal/secrets"
	"github.com/hecatehq/hecate/pkg/types"
)

func acpMCPServers(configs []types.MCPServerConfig) []acp.McpServer {
	if len(configs) == 0 {
		return []acp.McpServer{}
	}
	servers := make([]acp.McpServer, 0, len(configs))
	for _, cfg := range configs {
		name := strings.TrimSpace(cfg.Name)
		if name == "" {
			continue
		}
		if url := strings.TrimSpace(cfg.URL); url != "" {
			servers = append(servers, acp.McpServer{
				Http: &acp.McpServerHttpInline{
					Type:    "http",
					Name:    name,
					Url:     url,
					Headers: acpHTTPHeaders(cfg.Headers),
				},
			})
			continue
		}
		if command := strings.TrimSpace(cfg.Command); command != "" {
			servers = append(servers, acp.McpServer{
				Stdio: &acp.McpServerStdio{
					Name:    name,
					Command: command,
					Args:    append([]string(nil), cfg.Args...),
					Env:     acpEnvVariables(cfg.Env),
				},
			})
		}
	}
	return servers
}

func acpEnvVariables(values map[string]string) []acp.EnvVariable {
	if len(values) == 0 {
		return []acp.EnvVariable{}
	}
	keys := sortedStringMapKeys(values)
	out := make([]acp.EnvVariable, 0, len(keys))
	for _, key := range keys {
		out = append(out, acp.EnvVariable{Name: key, Value: values[key]})
	}
	return out
}

func acpHTTPHeaders(values map[string]string) []acp.HttpHeader {
	if len(values) == 0 {
		return []acp.HttpHeader{}
	}
	keys := sortedStringMapKeys(values)
	out := make([]acp.HttpHeader, 0, len(keys))
	for _, key := range keys {
		out = append(out, acp.HttpHeader{Name: key, Value: values[key]})
	}
	return out
}

func sortedStringMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func cloneMCPServerConfigs(values []types.MCPServerConfig) []types.MCPServerConfig {
	if values == nil {
		return nil
	}
	out := make([]types.MCPServerConfig, len(values))
	for i, value := range values {
		out[i] = value
		out[i].Args = append([]string(nil), value.Args...)
		out[i].Env = cloneStringMap(value.Env)
		out[i].Headers = cloneStringMap(value.Headers)
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func resolveMCPServerConfigs(configs []types.MCPServerConfig, cipher secrets.Cipher) ([]types.MCPServerConfig, error) {
	if len(configs) == 0 {
		return configs, nil
	}
	out := make([]types.MCPServerConfig, len(configs))
	for i, cfg := range configs {
		resolved := cfg
		resolved.Args = append([]string(nil), cfg.Args...)
		env, err := resolveMCPValueMap(cfg.Name, "env", cfg.Env, cipher)
		if err != nil {
			return nil, err
		}
		headers, err := resolveMCPValueMap(cfg.Name, "headers", cfg.Headers, cipher)
		if err != nil {
			return nil, err
		}
		resolved.Env = env
		resolved.Headers = headers
		out[i] = resolved
	}
	return out, nil
}

func resolveMCPValueMap(serverName, kind string, values map[string]string, cipher secrets.Cipher) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		resolved, err := resolveMCPValue(serverName, kind, key, value, cipher)
		if err != nil {
			return nil, err
		}
		out[key] = resolved
	}
	return out, nil
}

func resolveMCPValue(serverName, kind, key, value string, cipher secrets.Cipher) (string, error) {
	switch {
	case strings.HasPrefix(value, types.MCPEnvEncPrefix):
		if cipher == nil {
			return "", fmt.Errorf("mcp server %q: %s %q: value is encrypted (enc:) but no control-plane secret key is configured", serverName, kind, key)
		}
		plaintext, err := cipher.Decrypt(value[len(types.MCPEnvEncPrefix):])
		if err != nil {
			return "", fmt.Errorf("mcp server %q: %s %q: decrypt: %w", serverName, kind, key, err)
		}
		return plaintext, nil
	case len(value) > 0 && value[0] == '$':
		if !isMCPEnvRef(value) {
			return "", fmt.Errorf("mcp server %q: %s %q: %q looks like a variable reference but is not a valid env-var name (expected $NAME)", serverName, kind, key, value)
		}
		name := value[1:]
		resolved, exists := os.LookupEnv(name)
		if !exists {
			return "", fmt.Errorf("mcp server %q: %s %q: $%s is not set in the runtime environment", serverName, kind, key, name)
		}
		if resolved == "" {
			return "", fmt.Errorf("mcp server %q: %s %q: $%s is set but empty", serverName, kind, key, name)
		}
		return resolved, nil
	default:
		return value, nil
	}
}

func isMCPEnvRef(v string) bool {
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

func sameMCPServerConfigs(a, b []types.MCPServerConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i].Name) != strings.TrimSpace(b[i].Name) ||
			strings.TrimSpace(a[i].Command) != strings.TrimSpace(b[i].Command) ||
			strings.TrimSpace(a[i].URL) != strings.TrimSpace(b[i].URL) ||
			a[i].ApprovalPolicy != b[i].ApprovalPolicy ||
			!sameStringSlices(a[i].Args, b[i].Args) ||
			!sameStringMaps(a[i].Env, b[i].Env) ||
			!sameStringMaps(a[i].Headers, b[i].Headers) {
			return false
		}
	}
	return true
}

func sameStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, aValue := range a {
		if b[key] != aValue {
			return false
		}
	}
	return true
}
