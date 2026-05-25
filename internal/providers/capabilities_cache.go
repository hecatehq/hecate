package providers

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/telemetry"
)

func resolveCapabilities(
	ctx context.Context,
	logger *slog.Logger,
	providerName string,
	kind Kind,
	apiKey string,
	refresh bool,
	mu *sync.Mutex,
	cachedCaps *Capabilities,
	capsExpiry *time.Time,
	inFlight **capabilityDiscoveryCall,
	discover func(context.Context) (Capabilities, error),
	staticCaps func(source string) Capabilities,
) (Capabilities, error) {
	mu.Lock()
	if discoveryUnconfigured(kind, apiKey) {
		cached := staticCaps("config_unconfigured")
		*cachedCaps = cached
		*capsExpiry = time.Now().Add(capabilitiesUnconfiguredTTL)
		mu.Unlock()
		return cached, nil
	}
	if !refresh && !capsExpiry.IsZero() && time.Now().Before(*capsExpiry) {
		cached := *cachedCaps
		mu.Unlock()
		return cached, nil
	}
	if *inFlight != nil {
		call := *inFlight
		mu.Unlock()
		// Refresh callers join any in-flight discovery for this provider. That
		// still bypasses completed cache entries, but avoids starting a duplicate
		// upstream probe when a normal cache miss is already discovering state.
		return waitForCapabilityDiscovery(ctx, call)
	}
	call := &capabilityDiscoveryCall{done: make(chan struct{})}
	*inFlight = call
	mu.Unlock()

	discovered, err := discover(ctx)
	if err != nil {
		retryAfter := discoveryFailureTTL(kind, err)
		telemetry.Info(logger, ctx, "gateway.providers.capabilities.discovery_degraded",
			slog.String("event.name", "gateway.providers.capabilities.discovery_degraded"),
			slog.String("gen_ai.provider.name", providerName),
			slog.Duration("hecate.providers.capabilities.retry_after", retryAfter),
			slog.Any("error", err),
		)
		cached := staticCaps("config_fallback")
		cached.LastError = err.Error()
		if cached.RefreshedAt.IsZero() {
			cached.RefreshedAt = time.Now().UTC()
		}
		mu.Lock()
		*cachedCaps = cached
		*capsExpiry = time.Now().Add(retryAfter)
		call.caps = cached
		*inFlight = nil
		close(call.done)
		mu.Unlock()
		return cached, nil
	}

	mu.Lock()
	*cachedCaps = discovered
	*capsExpiry = time.Now().Add(capabilitiesSuccessTTL)
	call.caps = discovered
	*inFlight = nil
	close(call.done)
	mu.Unlock()
	return discovered, nil
}

type capabilityDiscoveryCall struct {
	done chan struct{}
	caps Capabilities
}

func waitForCapabilityDiscovery(ctx context.Context, call *capabilityDiscoveryCall) (Capabilities, error) {
	select {
	case <-ctx.Done():
		return Capabilities{}, ctx.Err()
	case <-call.done:
		return call.caps, nil
	}
}
