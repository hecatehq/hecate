package llamacpp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hecate/agent-runtime/internal/controlplane"
)

// Service is the top-level facade for the local-models subsystem.
// API handlers, the chat-composer's /v1/models aggregator, and the
// boot-time provider auto-registration all reach for this single
// object — internally it composes Catalog + Installer + Runtime +
// Proxy and owns the small store-adapter that bridges
// controlplane.Store onto the Runtime's ModelLookup interface.
//
// Construct one per gateway process. Safe for concurrent use; all
// inner state machines own their own mutexes.
type Service struct {
	binaryPath string
	dataDir    string
	store      controlplane.Store

	catalog   *Catalog
	installer *Installer
	runtime   *Runtime
	proxy     *Proxy
}

// ServiceOptions configures Service. Required: DataDir + Store. The
// rest of the wiring is optional — sensible production defaults are
// applied when fields are zero.
type ServiceOptions struct {
	// BinaryPath is the resolved llama-server path. When empty the
	// service runs in dormant mode: API handlers return 503, the
	// proxy returns 503, no provider is auto-registered.
	BinaryPath string
	// DataDir is the GATEWAY_DATA_DIR. Models land under DataDir/models/.
	DataDir string
	// Store is the controlplane store the installer writes through
	// and the runtime reads through. Required.
	Store controlplane.Store
	// Catalog overrides the compiled-in catalog. Tests pass a fake;
	// production leaves this nil and gets NewCatalog().
	Catalog *Catalog
	// Starter overrides the production exec-based starter. Tests
	// pass a fakeStarter; production wires NewExecProcessStarter.
	Starter ProcessStarter
	// InstallerOptions are forwarded verbatim to NewInstaller.
	InstallerOptions InstallerOptions
	// RuntimeOptions overrides are forwarded verbatim to NewRuntime.
	// BinaryPath and DataDir are set from the outer ServiceOptions;
	// anything else falls through. Storefield on RuntimeOptions is
	// ignored — the service wires the adapter for you.
	RuntimeOptions RuntimeOptions
	// Stderr is the writer the production starter pipes child
	// stderr to. Ignored when Starter is non-nil.
	Stderr *os.File
}

// NewService builds the service. Returns an error when DataDir or
// Store is missing; an empty BinaryPath is fine and produces a
// dormant service (Available() reports false). Production wires this
// from cmd/server with the resolved HECATE_LLAMA_SERVER_BIN value.
func NewService(opts ServiceOptions) (*Service, error) {
	if opts.DataDir == "" {
		return nil, errors.New("service: DataDir is required")
	}
	if opts.Store == nil {
		return nil, errors.New("service: Store is required")
	}
	catalog := opts.Catalog
	if catalog == nil {
		catalog = NewCatalog()
	}

	installer, err := NewInstaller(opts.DataDir, opts.Store, catalog, opts.InstallerOptions)
	if err != nil {
		return nil, fmt.Errorf("service: installer: %w", err)
	}

	lookup := &controlplaneModelLookup{store: opts.Store}
	starter := opts.Starter
	if starter == nil {
		starter = NewExecProcessStarter(opts.Stderr)
	}
	runtimeOpts := opts.RuntimeOptions
	runtimeOpts.BinaryPath = opts.BinaryPath
	runtimeOpts.DataDir = opts.DataDir
	runtimeOpts.ModelStore = lookup
	runtimeOpts.Starter = starter
	rt, err := NewRuntime(runtimeOpts)
	if err != nil {
		return nil, fmt.Errorf("service: runtime: %w", err)
	}

	return &Service{
		binaryPath: opts.BinaryPath,
		dataDir:    opts.DataDir,
		store:      opts.Store,
		catalog:    catalog,
		installer:  installer,
		runtime:    rt,
		proxy:      NewProxy(rt),
	}, nil
}

// Catalog exposes the curated entries. Used by the
// GET /hecate/v1/local-models/catalog handler.
func (s *Service) Catalog() *Catalog { return s.catalog }

// Installer exposes the installer. Used by install / cancel handlers
// and the SSE event stream.
func (s *Service) Installer() *Installer { return s.installer }

// Runtime exposes the runtime. Used by start / stop / status handlers
// and (via ActiveBaseURL) by /v1/models when reporting which model is
// currently loaded.
func (s *Service) Runtime() *Runtime { return s.runtime }

// Proxy returns the http.Handler for the gateway-internal proxy. The
// api package mounts this at /hecate/internal/llamacpp/v1/.
func (s *Service) Proxy() http.Handler { return s.proxy }

// FeatureAvailability summarizes whether the feature can do work. UI
// uses this on first card render so it doesn't need to probe each
// endpoint individually to decide what to show.
func (s *Service) FeatureAvailability() FeatureAvailability {
	if strings.TrimSpace(s.binaryPath) == "" {
		return FeatureAvailability{Available: false, Reason: "binary_not_found"}
	}
	info, err := os.Stat(s.binaryPath)
	if err != nil {
		return FeatureAvailability{Available: false, Reason: "binary_not_found"}
	}
	// On unix the executable bit is the meaningful check; on
	// Windows os.Stat doesn't reflect executability and the field
	// would always be zero, but Tauri's Windows path uses .exe
	// suffix and the OS handles dispatch — so the mode check is
	// best-effort.
	if mode := info.Mode(); mode.IsRegular() && mode&0o111 == 0 {
		return FeatureAvailability{Available: false, Reason: "binary_not_executable"}
	}
	return FeatureAvailability{Available: true, BinaryPath: s.binaryPath}
}

// ListInstalled returns the installed-model rows from the
// controlplane snapshot, sorted by display name (then id). Used by
// both the GET /installed handler and the /v1/models integration.
//
// Boot reconciliation hooks in here: rows whose file is missing on
// disk are filtered out and the corresponding store row is deleted.
// This keeps the chat composer's model picker from offering
// nonexistent files without forcing the operator to manually clean
// up after a `rm -rf models/`.
func (s *Service) ListInstalled(ctx context.Context) ([]InstalledModel, error) {
	state, err := s.store.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]InstalledModel, 0, len(state.InstalledModels))
	var staleIDs []string
	for _, m := range state.InstalledModels {
		absPath := m.FilePath
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(s.dataDir, absPath)
		}
		if _, statErr := os.Stat(absPath); statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				staleIDs = append(staleIDs, m.ID)
				continue
			}
		}
		out = append(out, m)
	}
	// Drop stale rows out-of-band — the boot reconciliation
	// promise from the RFC. We do this synchronously inside the
	// list to avoid an "appear, then disappear" race the next
	// time the operator refreshes.
	for _, id := range staleIDs {
		_ = s.store.DeleteInstalledModel(ctx, id)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].DisplayName == out[j].DisplayName {
			return out[i].ID < out[j].ID
		}
		return out[i].DisplayName < out[j].DisplayName
	})
	return out, nil
}

// EnsureAutoRegisteredProvider creates the single managed `llamacpp`
// provider row if it doesn't exist yet. Called once at gateway boot.
// No-op when an operator-created provider with that id already
// exists — the RFC commits to leaving operator overrides alone and
// logging a warning instead. The caller threads a logger if it
// wants the warning surfaced; this method returns it as a
// well-known sentinel.
func (s *Service) EnsureAutoRegisteredProvider(ctx context.Context, gatewayBaseURL string) error {
	if strings.TrimSpace(s.binaryPath) == "" {
		// Dormant feature: don't auto-register so the providers
		// table doesn't sprout a stale row that points at a port
		// we'll never bind.
		return nil
	}
	state, err := s.store.Snapshot(ctx)
	if err != nil {
		return err
	}
	for _, existing := range state.Providers {
		// Match by PresetID: the operator path through Add Provider
		// stamps PresetID="llamacpp" on the row, while the
		// generated provider ID is a slug derived from Name +
		// CustomName. PresetID is the stable signal.
		if existing.PresetID != "llamacpp" {
			continue
		}
		// Operator override path — leave it alone.
		return ErrAutoProviderOperatorOwned
	}
	baseURL := strings.TrimRight(gatewayBaseURL, "/") + internalProxyPathPrefix
	_, err = s.store.UpsertProvider(ctx, controlplane.Provider{
		Name:     "llama.cpp",
		PresetID: "llamacpp",
		Kind:     "local",
		Protocol: "openai",
		BaseURL:  baseURL,
		Enabled:  true,
	}, nil)
	return err
}

// ErrAutoProviderOperatorOwned signals EnsureAutoRegisteredProvider
// found an existing row and skipped registration. Not a hard error —
// the caller logs it as a structured warning.
var ErrAutoProviderOperatorOwned = errors.New("llamacpp provider already exists; operator override takes precedence")

// controlplaneModelLookup adapts controlplane.Store onto the slim
// Runtime ModelLookup interface. Linear scan of the snapshot is fine
// for v1 — the installed-models list is bounded by disk capacity, so
// "many" is still <100 entries on any realistic install.
type controlplaneModelLookup struct {
	store controlplane.Store
}

func (l *controlplaneModelLookup) LookupInstalled(ctx context.Context, id string) (InstalledModel, error) {
	state, err := l.store.Snapshot(ctx)
	if err != nil {
		return InstalledModel{}, err
	}
	for _, m := range state.InstalledModels {
		if m.ID == id {
			return m, nil
		}
	}
	return InstalledModel{}, fmt.Errorf("installed model %q not found", id)
}
