package llamacpp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hecatehq/hecate/internal/controlplane"
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

	catalog     *Catalog
	installer   *Installer
	runtime     *Runtime
	proxy       *Proxy
	huggingface *HuggingFaceClient
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
	// HuggingFaceOptions overrides the HF client wiring. Tests
	// inject a BaseURL pointing at an httptest.Server; production
	// leaves this zero and gets the real huggingface.co endpoint.
	HuggingFaceOptions HuggingFaceOptions
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
		binaryPath:  opts.BinaryPath,
		dataDir:     opts.DataDir,
		store:       opts.Store,
		catalog:     catalog,
		installer:   installer,
		runtime:     rt,
		proxy:       NewProxy(rt),
		huggingface: NewHuggingFaceClient(opts.HuggingFaceOptions),
	}, nil
}

// HuggingFace exposes the HF browse client. UI hits this through
// GET /hecate/v1/local-models/huggingface/search and
// GET /hecate/v1/local-models/huggingface/repos/...
func (s *Service) HuggingFace() *HuggingFaceClient { return s.huggingface }

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
	// Defense in depth: reject the shell-script placeholder that
	// `just tauri-llama-sidecar` writes for non-arm64-darwin
	// targets. The placeholder is executable so the os.Stat check
	// above passes, but it's NOT a real llama-server. The Tauri
	// sidecar resolver detects this on its side too — this branch
	// handles the headless / dev gateway path where an operator
	// might point HECATE_LLAMA_SERVER_BIN directly at the stub.
	if isLlamaServerPlaceholder(s.binaryPath) {
		return FeatureAvailability{Available: false, Reason: "binary_is_placeholder"}
	}
	return FeatureAvailability{Available: true, BinaryPath: s.binaryPath}
}

// isLlamaServerPlaceholder returns true if the named file is the
// build-time stub written by `just tauri-llama-sidecar` for
// non-supported targets. Matches the sentinel string in the script
// body within the first 512 bytes.
func isLlamaServerPlaceholder(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return false
	}
	return bytes.Contains(buf[:n], []byte("hecate-llama-server-placeholder"))
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

// managedProviderID is the stable ID for the Hecate-managed llamacpp
// provider row. Matching `/v1/models`' `owned_by` field exactly so
// routing/UI lookups by provider ID resolve to the same row that the
// auto-registration touches.
const managedProviderID = "llamacpp"

// EnsureAutoRegisteredProvider creates or refreshes the single managed
// `llamacpp` provider row at gateway boot. Behavior:
//
//   - No managed row exists → create one with ID=managedProviderID.
//   - A managed row exists (matched by ID==managedProviderID) → refresh
//     its BaseURL so a desktop launch on a different port doesn't leave
//     the row pointing at a stale internal-proxy URL.
//   - A different row with PresetID="llamacpp" exists → operator
//     override. Leave alone, return ErrAutoProviderOperatorOwned so the
//     caller can log a structured warning.
//
// The ID distinction is what separates "row we own" from "row the
// operator added through Add Provider" — the operator path generates
// a slug-derived ID like `llamacpp-1`, while ours is the literal
// `llamacpp`.
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
	baseURL := strings.TrimRight(gatewayBaseURL, "/") + internalProxyPathPrefix
	for _, existing := range state.Providers {
		if existing.ID == managedProviderID {
			// Our managed row from a previous boot. Re-upsert to
			// refresh BaseURL — the desktop app picks a fresh port
			// on each launch, so the stored URL can be stale.
			_, err = s.store.UpsertProvider(ctx, controlplane.Provider{
				ID:       managedProviderID,
				Name:     "llama.cpp",
				PresetID: "llamacpp",
				Kind:     "local",
				Protocol: "openai",
				BaseURL:  baseURL,
				Enabled:  existing.Enabled,
			}, nil)
			return err
		}
		if existing.PresetID == "llamacpp" {
			// Operator-created override path — leave it alone.
			return ErrAutoProviderOperatorOwned
		}
	}
	_, err = s.store.UpsertProvider(ctx, controlplane.Provider{
		ID:       managedProviderID,
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

// ErrInstalledModelNotFound is returned by Uninstall when the model
// id doesn't match any registry row.
var ErrInstalledModelNotFound = errors.New("installed model not found")

// Uninstall removes a model: stops the runtime if it's running this
// model, deletes the .gguf file from disk, removes the registry row.
// Idempotent on the file (best-effort os.Remove); the registry
// removal is the gate that turns "not installed" into a 404 for
// subsequent calls.
func (s *Service) Uninstall(ctx context.Context, modelID string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return errors.New("model id is required")
	}
	state, err := s.store.Snapshot(ctx)
	if err != nil {
		return err
	}
	var target *InstalledModel
	for i := range state.InstalledModels {
		if state.InstalledModels[i].ID == modelID {
			target = &state.InstalledModels[i]
			break
		}
	}
	if target == nil {
		return ErrInstalledModelNotFound
	}

	// If the runtime is currently loaded with this model, stop it
	// first so the file isn't held open during deletion.
	if status := s.runtime.Status(); status.ActiveModelID == modelID {
		if err := s.runtime.Stop(ctx); err != nil {
			// Best-effort — proceed with file removal even if the
			// stop returned an error. A subsequent EnsureLoaded
			// will pick a new port; the leaked child (if any) is
			// the operator's call to clean up via process tools.
			_ = err
		}
	}

	absPath := target.FilePath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(s.dataDir, absPath)
	}
	_ = os.Remove(absPath)

	return s.store.DeleteInstalledModel(ctx, modelID)
}

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
