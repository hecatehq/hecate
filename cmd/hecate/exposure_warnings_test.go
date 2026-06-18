package main

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestNetworkExposureProtectionWarnings(t *testing.T) {
	t.Parallel()

	configuredProvider := exposureTestProvider{
		name:            "openai",
		credentialState: providers.CredentialStateConfigured,
		enabled:         true,
	}
	missingCredentialProvider := exposureTestProvider{
		name:            "openai",
		credentialState: providers.CredentialStateMissing,
		enabled:         true,
	}
	unknownCredentialProvider := exposureTestProvider{
		name:            "openai",
		credentialState: providers.CredentialStateUnknown,
		enabled:         true,
	}
	disabledConfiguredProvider := exposureTestProvider{
		name:            "openai",
		credentialState: providers.CredentialStateConfigured,
		enabled:         false,
	}
	nonReportingCloudProvider := exposureBareProvider{
		name:    "future_cloud",
		kind:    providers.KindCloud,
		enabled: true,
	}
	nonReportingLocalProvider := exposureBareProvider{
		name:    "future_local",
		kind:    providers.KindLocal,
		enabled: true,
	}

	cases := []struct {
		name      string
		cfg       config.Config
		registry  providers.Registry
		wantCount int
		wantEnv   []string
	}{
		{
			name: "loopback stays quiet",
			cfg: config.Config{Server: config.ServerConfig{
				Address: "127.0.0.1:8765",
			}},
			registry: providers.NewRegistry(configuredProvider),
		},
		{
			name: "remote runtime identity mode stays quiet",
			cfg: config.Config{Server: config.ServerConfig{
				Address:           "0.0.0.0:8765",
				RemoteRuntimeMode: true,
			}},
			registry: providers.NewRegistry(configuredProvider),
		},
		{
			name: "no configured provider credentials stays quiet",
			cfg: config.Config{Server: config.ServerConfig{
				Address: "0.0.0.0:8765",
			}},
			registry: providers.NewRegistry(missingCredentialProvider),
		},
		{
			name: "disabled configured provider stays quiet",
			cfg: config.Config{Server: config.ServerConfig{
				Address: "0.0.0.0:8765",
			}},
			registry: providers.NewRegistry(disabledConfiguredProvider),
		},
		{
			name: "unknown credential state warns conservatively",
			cfg: config.Config{Server: config.ServerConfig{
				Address: "0.0.0.0:8765",
			}},
			registry:  providers.NewRegistry(unknownCredentialProvider),
			wantCount: 1,
			wantEnv:   []string{"HECATE_INFERENCE_TOKEN", "HECATE_RUNTIME_TOKEN"},
		},
		{
			name: "cloud provider without credential reporter warns conservatively",
			cfg: config.Config{Server: config.ServerConfig{
				Address: "0.0.0.0:8765",
			}},
			registry:  providers.NewRegistry(nonReportingCloudProvider),
			wantCount: 1,
			wantEnv:   []string{"HECATE_INFERENCE_TOKEN", "HECATE_RUNTIME_TOKEN"},
		},
		{
			name: "local provider without credential reporter stays quiet",
			cfg: config.Config{Server: config.ServerConfig{
				Address: "0.0.0.0:8765",
			}},
			registry: providers.NewRegistry(nonReportingLocalProvider),
		},
		{
			name: "non-loopback with configured credentials warns for both missing tokens",
			cfg: config.Config{Server: config.ServerConfig{
				Address: "0.0.0.0:8765",
			}},
			registry:  providers.NewRegistry(configuredProvider),
			wantCount: 1,
			wantEnv:   []string{"HECATE_INFERENCE_TOKEN", "HECATE_RUNTIME_TOKEN"},
		},
		{
			name: "inference token leaves runtime token warning",
			cfg: config.Config{Server: config.ServerConfig{
				Address:        "0.0.0.0:8765",
				InferenceToken: "inference-token-present",
			}},
			registry:  providers.NewRegistry(configuredProvider),
			wantCount: 1,
			wantEnv:   []string{"HECATE_RUNTIME_TOKEN"},
		},
		{
			name: "runtime token leaves inference token warning",
			cfg: config.Config{Server: config.ServerConfig{
				Address:      "0.0.0.0:8765",
				RuntimeToken: "runtime-token-present",
			}},
			registry:  providers.NewRegistry(configuredProvider),
			wantCount: 1,
			wantEnv:   []string{"HECATE_INFERENCE_TOKEN"},
		},
		{
			name: "both tokens configured stays quiet",
			cfg: config.Config{Server: config.ServerConfig{
				Address:        "0.0.0.0:8765",
				InferenceToken: "inference-token-present",
				RuntimeToken:   "runtime-token-present",
			}},
			registry:  providers.NewRegistry(configuredProvider),
			wantCount: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotCount, gotWarnings := networkExposureProtectionWarnings(tc.cfg, tc.registry)
			if gotCount != tc.wantCount {
				t.Fatalf("credential risk provider count = %d, want %d", gotCount, tc.wantCount)
			}
			gotEnv := make([]string, 0, len(gotWarnings))
			for _, warning := range gotWarnings {
				gotEnv = append(gotEnv, warning.envName)
			}
			if !slices.Equal(gotEnv, tc.wantEnv) {
				t.Fatalf("warning envs = %v, want %v", gotEnv, tc.wantEnv)
			}
		})
	}
}

type exposureTestProvider struct {
	name            string
	credentialState providers.CredentialState
	enabled         bool
}

func (p exposureTestProvider) Name() string {
	return p.name
}

func (p exposureTestProvider) Kind() providers.Kind {
	return providers.KindCloud
}

func (p exposureTestProvider) DefaultModel() string {
	return ""
}

func (p exposureTestProvider) Capabilities(context.Context) (providers.Capabilities, error) {
	return providers.Capabilities{
		Name: p.name,
		Kind: providers.KindCloud,
	}, nil
}

func (p exposureTestProvider) Chat(context.Context, types.ChatRequest) (*types.ChatResponse, error) {
	return nil, errors.New("not used")
}

func (p exposureTestProvider) Supports(string) bool {
	return true
}

func (p exposureTestProvider) CredentialState() providers.CredentialState {
	return p.credentialState
}

func (p exposureTestProvider) Enabled() bool {
	return p.enabled
}

type exposureBareProvider struct {
	name    string
	kind    providers.Kind
	enabled bool
}

func (p exposureBareProvider) Name() string {
	return p.name
}

func (p exposureBareProvider) Kind() providers.Kind {
	return p.kind
}

func (p exposureBareProvider) DefaultModel() string {
	return ""
}

func (p exposureBareProvider) Capabilities(context.Context) (providers.Capabilities, error) {
	return providers.Capabilities{
		Name: p.name,
		Kind: p.kind,
	}, nil
}

func (p exposureBareProvider) Chat(context.Context, types.ChatRequest) (*types.ChatResponse, error) {
	return nil, errors.New("not used")
}

func (p exposureBareProvider) Supports(string) bool {
	return true
}

func (p exposureBareProvider) Enabled() bool {
	return p.enabled
}
