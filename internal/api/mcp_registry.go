package api

import (
	"encoding/json"
	"fmt"
	"strings"

	mcpregistry "github.com/hecatehq/hecate/internal/mcp/registry"
)

type MCPRegistryServersResponse struct {
	Object string                         `json:"object"`
	Data   MCPRegistryServersResponseItem `json:"data"`
}

type MCPRegistryServersResponseItem struct {
	RegistryURL string                        `json:"registry_url"`
	Servers     []MCPRegistryServerDescriptor `json:"servers"`
	NextCursor  string                        `json:"next_cursor,omitempty"`
	Count       int                           `json:"count,omitempty"`
}

type MCPRegistryServerDescriptor struct {
	Server       mcpregistry.ServerDetail `json:"server"`
	Meta         json.RawMessage          `json:"_meta,omitempty"`
	InstallHints []MCPRegistryInstallHint `json:"install_hints,omitempty"`
}

type MCPRegistryInstallHint struct {
	Source            string                   `json:"source"`
	Transport         string                   `json:"transport,omitempty"`
	Supported         bool                     `json:"supported"`
	URL               string                   `json:"url,omitempty"`
	RegistryType      string                   `json:"registry_type,omitempty"`
	Identifier        string                   `json:"identifier,omitempty"`
	RuntimeHint       string                   `json:"runtime_hint,omitempty"`
	RequiredSecrets   []string                 `json:"required_secrets,omitempty"`
	HecateConfig      *MCPRegistryHecateConfig `json:"hecate_config,omitempty"`
	UnsupportedReason string                   `json:"unsupported_reason,omitempty"`
}

type MCPRegistryHecateConfig struct {
	Name    string            `json:"name"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

func renderMCPRegistryServers(registryURL string, list mcpregistry.ServerList) MCPRegistryServersResponseItem {
	servers := make([]MCPRegistryServerDescriptor, 0, len(list.Servers))
	for _, item := range list.Servers {
		servers = append(servers, MCPRegistryServerDescriptor{
			Server:       item.Server,
			Meta:         item.Meta,
			InstallHints: buildMCPRegistryInstallHints(item.Server),
		})
	}
	return MCPRegistryServersResponseItem{
		RegistryURL: registryURL,
		Servers:     servers,
		NextCursor:  list.Metadata.NextCursor,
		Count:       list.Metadata.Count,
	}
}

func buildMCPRegistryInstallHints(server mcpregistry.ServerDetail) []MCPRegistryInstallHint {
	hints := make([]MCPRegistryInstallHint, 0, len(server.Remotes)+len(server.Packages))
	for _, remote := range server.Remotes {
		transport := strings.TrimSpace(remote.Type)
		hint := MCPRegistryInstallHint{
			Source:          "remote",
			Transport:       transport,
			URL:             strings.TrimSpace(remote.URL),
			RequiredSecrets: requiredRegistrySecrets(remote.Headers),
		}
		switch {
		case transport == "streamable-http" && hint.URL != "":
			hint.Supported = true
			hint.HecateConfig = &MCPRegistryHecateConfig{
				Name:    registryServerAlias(server),
				URL:     hint.URL,
				Headers: registryHeaderPlaceholders(remote.Headers),
			}
		case transport == "streamable-http":
			hint.UnsupportedReason = "streamable-http registry remote is missing a URL"
		case transport == "sse":
			hint.UnsupportedReason = "SSE registry remotes are listed for discovery; Hecate's MCP client supports streamable HTTP and stdio today"
		case transport == "":
			hint.UnsupportedReason = "registry remote does not declare a transport"
		default:
			hint.UnsupportedReason = fmt.Sprintf("MCP transport %q is not supported by Hecate's MCP client", transport)
		}
		hints = append(hints, hint)
	}
	for _, pkg := range server.Packages {
		transport := strings.TrimSpace(pkg.Transport.Type)
		if transport == "" {
			transport = "stdio"
		}
		hints = append(hints, MCPRegistryInstallHint{
			Source:            "package",
			Transport:         transport,
			RegistryType:      strings.TrimSpace(pkg.RegistryType),
			Identifier:        strings.TrimSpace(pkg.Identifier),
			RuntimeHint:       strings.TrimSpace(pkg.RuntimeHint),
			RequiredSecrets:   requiredRegistrySecrets(pkg.EnvironmentVariables),
			UnsupportedReason: "package entries require an operator-chosen local runtime command before Hecate can probe them",
		})
	}
	return hints
}

func registryHeaderPlaceholders(headers []mcpregistry.InputSpec) map[string]string {
	out := make(map[string]string, len(headers))
	for _, header := range headers {
		name := strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		if value := strings.TrimSpace(header.Value); value != "" && !header.IsSecret {
			out[name] = value
			continue
		}
		out[name] = "$" + registryInputEnvName(name)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func requiredRegistrySecrets(inputs []mcpregistry.InputSpec) []string {
	out := make([]string, 0, len(inputs))
	for _, input := range inputs {
		name := strings.TrimSpace(input.Name)
		if name == "" || (!input.IsRequired && !input.IsSecret) {
			continue
		}
		out = append(out, registryInputEnvName(name))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func registryInputEnvName(name string) string {
	name = strings.ToUpper(strings.TrimSpace(name))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		valid := (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "SECRET"
	}
	if !strings.HasPrefix(out, "MCP_") {
		out = "MCP_" + out
	}
	return out
}

func registryServerAlias(server mcpregistry.ServerDetail) string {
	raw := strings.TrimSpace(server.Name)
	if raw == "" {
		raw = strings.TrimSpace(server.Title)
	}
	if i := strings.LastIndex(raw, "/"); i >= 0 {
		raw = raw[i+1:]
	}
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(raw) {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "registry-mcp"
	}
	return out
}
