package pluginregistry

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/hecatehq/hecate/pkg/types"
)

const (
	MCPServerTransportStdio = "stdio"
	MCPServerTransportHTTP  = "http"
)

type MCPServerConfig struct {
	Name           string            `json:"name"`
	Transport      string            `json:"transport"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	URL            string            `json:"url,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	ApprovalPolicy string            `json:"approval_policy,omitempty"`
}

func ParseMCPServerConfig(capabilityID string, raw json.RawMessage) (MCPServerConfig, error) {
	return normalizeMCPServerConfig(capabilityID, raw)
}

func normalizeMCPServerConfigJSON(capabilityID string, raw json.RawMessage) (json.RawMessage, error) {
	cfg, err := normalizeMCPServerConfig(capabilityID, raw)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return nil, ErrInvalid
	}
	return out, nil
}

func normalizeMCPServerConfig(capabilityID string, raw json.RawMessage) (MCPServerConfig, error) {
	raw = compactJSON(raw)
	if len(raw) == 0 {
		return MCPServerConfig{}, ErrInvalid
	}
	var cfg MCPServerConfig
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return MCPServerConfig{}, ErrInvalid
	}
	cfg.Name = strings.TrimSpace(cfg.Name)
	if cfg.Name == "" {
		cfg.Name = normalizeID(capabilityID)
	}
	cfg.Transport = strings.TrimSpace(cfg.Transport)
	cfg.Command = strings.TrimSpace(cfg.Command)
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.ApprovalPolicy = strings.TrimSpace(cfg.ApprovalPolicy)
	cfg.Args = append([]string(nil), cfg.Args...)
	var err error
	if cfg.Env, err = normalizeMCPRefMap(cfg.Env); err != nil {
		return MCPServerConfig{}, err
	}
	if cfg.Headers, err = normalizeMCPRefMap(cfg.Headers); err != nil {
		return MCPServerConfig{}, err
	}
	if cfg.Name == "" {
		return MCPServerConfig{}, ErrInvalid
	}
	if cfg.Command != "" && cfg.URL != "" {
		return MCPServerConfig{}, ErrInvalid
	}
	switch {
	case cfg.Command != "":
		if cfg.Transport == "" {
			cfg.Transport = MCPServerTransportStdio
		}
		if cfg.Transport != MCPServerTransportStdio {
			return MCPServerConfig{}, ErrInvalid
		}
	case cfg.URL != "":
		if cfg.Transport == "" {
			cfg.Transport = MCPServerTransportHTTP
		}
		if cfg.Transport != MCPServerTransportHTTP {
			return MCPServerConfig{}, ErrInvalid
		}
	default:
		return MCPServerConfig{}, ErrInvalid
	}
	if !types.IsValidMCPApprovalPolicy(cfg.ApprovalPolicy) {
		return MCPServerConfig{}, ErrInvalid
	}
	return cfg, nil
}

func normalizeMCPRefMap(values map[string]string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || !isMCPEnvRef(value) {
			return nil, ErrInvalid
		}
		out[key] = value
	}
	return out, nil
}

func isMCPEnvRef(value string) bool {
	if len(value) < 2 || value[0] != '$' {
		return false
	}
	for i, c := range value[1:] {
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
