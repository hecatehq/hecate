package providers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type capabilitiesCacheHarness struct {
	mu         sync.Mutex
	cachedCaps Capabilities
	capsExpiry time.Time
	inFlight   *capabilityDiscoveryCall
	discover   func(context.Context) (Capabilities, error)
}

func (h *capabilitiesCacheHarness) resolve(ctx context.Context, refresh bool) (Capabilities, error) {
	return resolveCapabilities(
		ctx,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"test-provider",
		KindLocal,
		"",
		refresh,
		&h.mu,
		&h.cachedCaps,
		&h.capsExpiry,
		&h.inFlight,
		h.discover,
		func(source string) Capabilities {
			return Capabilities{
				Name:            "test-provider",
				Kind:            KindLocal,
				DefaultModel:    "fallback-model",
				Models:          []string{"fallback-model"},
				DiscoverySource: source,
				RefreshedAt:     time.Now().UTC(),
			}
		},
	)
}

func TestResolveCapabilitiesUsesCachedRead(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	harness := &capabilitiesCacheHarness{
		discover: func(context.Context) (Capabilities, error) {
			calls.Add(1)
			return discoveredTestCapabilities("model-a"), nil
		},
	}

	first, err := harness.resolve(context.Background(), false)
	if err != nil {
		t.Fatalf("first resolve error = %v", err)
	}
	second, err := harness.resolve(context.Background(), false)
	if err != nil {
		t.Fatalf("second resolve error = %v", err)
	}

	if first.Models[0] != "model-a" || second.Models[0] != "model-a" {
		t.Fatalf("models = %v then %v, want cached model-a", first.Models, second.Models)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("discovery calls = %d, want 1", got)
	}
}

func TestResolveCapabilitiesManualRefreshBypassesCache(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	harness := &capabilitiesCacheHarness{
		discover: func(context.Context) (Capabilities, error) {
			call := calls.Add(1)
			return discoveredTestCapabilities("model-" + strconv.FormatInt(call, 10)), nil
		},
	}

	first, err := harness.resolve(context.Background(), false)
	if err != nil {
		t.Fatalf("first resolve error = %v", err)
	}
	refreshed, err := harness.resolve(context.Background(), true)
	if err != nil {
		t.Fatalf("refresh resolve error = %v", err)
	}

	if first.Models[0] != "model-1" {
		t.Fatalf("first model = %q, want model-1", first.Models[0])
	}
	if refreshed.Models[0] != "model-2" {
		t.Fatalf("refreshed model = %q, want model-2", refreshed.Models[0])
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("discovery calls = %d, want 2", got)
	}
}

func TestResolveCapabilitiesProviderErrorCachesFallback(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	harness := &capabilitiesCacheHarness{
		discover: func(context.Context) (Capabilities, error) {
			calls.Add(1)
			return Capabilities{}, errors.New("provider unavailable")
		},
	}

	caps, err := harness.resolve(context.Background(), false)
	if err != nil {
		t.Fatalf("resolve error = %v, want fallback capabilities", err)
	}
	if caps.DiscoverySource != "config_fallback" {
		t.Fatalf("discovery source = %q, want config_fallback", caps.DiscoverySource)
	}
	if caps.LastError != "provider unavailable" {
		t.Fatalf("last error = %q, want provider unavailable", caps.LastError)
	}

	if _, err := harness.resolve(context.Background(), false); err != nil {
		t.Fatalf("cached fallback resolve error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("discovery calls = %d, want 1", got)
	}
}

func TestResolveCapabilitiesCacheExpiryRediscovers(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	harness := &capabilitiesCacheHarness{
		discover: func(context.Context) (Capabilities, error) {
			call := calls.Add(1)
			return discoveredTestCapabilities("model-" + strconv.FormatInt(call, 10)), nil
		},
	}

	if _, err := harness.resolve(context.Background(), false); err != nil {
		t.Fatalf("first resolve error = %v", err)
	}
	harness.mu.Lock()
	harness.capsExpiry = time.Now().Add(-time.Second)
	harness.mu.Unlock()

	caps, err := harness.resolve(context.Background(), false)
	if err != nil {
		t.Fatalf("expired resolve error = %v", err)
	}
	if caps.Models[0] != "model-2" {
		t.Fatalf("model after expiry = %q, want model-2", caps.Models[0])
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("discovery calls = %d, want 2", got)
	}
}

func TestResolveCapabilitiesConcurrentMissesShareInFlightDiscovery(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	harness := &capabilitiesCacheHarness{
		discover: func(context.Context) (Capabilities, error) {
			if calls.Add(1) == 1 {
				close(started)
			}
			<-release
			return discoveredTestCapabilities("shared-model"), nil
		},
	}

	const workers = 8
	ready := make(chan struct{})
	results := make(chan Capabilities, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			<-ready
			caps, err := harness.resolve(context.Background(), false)
			if err != nil {
				errs <- err
				return
			}
			results <- caps
		}()
	}

	close(ready)
	<-started
	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		close(release)
		t.Fatalf("discovery calls while first request is in flight = %d, want 1", got)
	}
	close(release)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent resolve error = %v", err)
	}
	for caps := range results {
		if caps.Models[0] != "shared-model" {
			t.Fatalf("model = %q, want shared-model", caps.Models[0])
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("discovery calls = %d, want 1", got)
	}
}

func discoveredTestCapabilities(model string) Capabilities {
	return Capabilities{
		Name:            "test-provider",
		Kind:            KindLocal,
		DefaultModel:    model,
		Models:          []string{model},
		Discoverable:    true,
		DiscoverySource: "upstream_v1_models",
		RefreshedAt:     time.Now().UTC(),
	}
}
