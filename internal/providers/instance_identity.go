package providers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/pkg/types"
)

var (
	runtimeProviderInstanceNamespace = newRuntimeProviderInstanceNamespace()
	runtimeProviderInstanceSequence  atomic.Uint64
)

func newProviderInstance(provider Provider) ProviderInstance {
	if reporter, ok := provider.(ProviderInstanceIdentityReporter); ok {
		if identity := reporter.ProviderInstanceIdentity(); identity.Valid() {
			return ProviderInstance{Provider: provider, Identity: identity}
		}
	}
	return ProviderInstance{
		Provider: provider,
		Identity: types.ProviderInstanceIdentity{
			ID:   fmt.Sprintf("runtime-v1:%s:%016x", runtimeProviderInstanceNamespace, runtimeProviderInstanceSequence.Add(1)),
			Kind: types.ProviderInstanceIdentityRuntime,
		},
	}
}

func newRuntimeProviderInstanceNamespace() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	// Runtime identities are deliberately non-durable. This fallback only needs
	// to avoid reuse within and across ordinary process starts when the platform
	// entropy source is unavailable; the per-process sequence supplies the
	// within-process uniqueness.
	fallback := sha256.Sum256([]byte(fmt.Sprintf("%d:%d", os.Getpid(), time.Now().UTC().UnixNano())))
	return hex.EncodeToString(fallback[:16])
}

func configurationProviderInstanceIdentity(cfg config.OpenAICompatibleProviderConfig) types.ProviderInstanceIdentity {
	if cfg.InstanceGeneration == "" {
		// Env-only and ad-hoc providers have no persisted, non-secret generation
		// Hecate can trust across a restart. The registry will assign a runtime
		// fence instead of persisting a credential-derived fingerprint.
		return types.ProviderInstanceIdentity{}
	}
	// Keep this payload explicit. It prevents future JSON tags on the runtime
	// config from silently removing a dispatch-affecting field from the fence.
	payload := struct {
		Name                   string
		Aliases                []string
		InstanceGeneration     string
		ProviderFamily         string
		Kind                   string
		Protocol               string
		BaseURL                string
		APIVersion             string
		ChatPath               string
		ModelsPath             string
		TimeoutNanoseconds     int64
		StubMode               bool
		StubResponse           string
		DefaultModel           string
		Enabled                bool
		KnownModels            []string
		AnthropicCacheDisabled bool
	}{
		Name:                   cfg.Name,
		Aliases:                append([]string(nil), cfg.Aliases...),
		InstanceGeneration:     cfg.InstanceGeneration,
		ProviderFamily:         cfg.ProviderFamily,
		Kind:                   cfg.Kind,
		Protocol:               cfg.Protocol,
		BaseURL:                cfg.BaseURL,
		APIVersion:             cfg.APIVersion,
		ChatPath:               cfg.ChatPath,
		ModelsPath:             cfg.ModelsPath,
		TimeoutNanoseconds:     int64(cfg.Timeout),
		StubMode:               cfg.StubMode,
		StubResponse:           cfg.StubResponse,
		DefaultModel:           cfg.DefaultModel,
		Enabled:                cfg.Enabled,
		KnownModels:            append([]string(nil), cfg.KnownModels...),
		AnthropicCacheDisabled: cfg.AnthropicCacheDisabled,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		// The payload contains only JSON-safe scalar and slice fields, so this is a
		// defensive fallback rather than an expected path.
		encoded = []byte(fmt.Sprintf("%#v", payload))
	}
	digest := sha256.Sum256(append([]byte("hecate-provider-configuration-v1\x00"), encoded...))
	return types.ProviderInstanceIdentity{
		ID:   "configuration-v1:" + hex.EncodeToString(digest[:]),
		Kind: types.ProviderInstanceIdentityConfiguration,
	}
}
