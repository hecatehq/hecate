package llamacpp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// HTTPDoer is the slice of net/http.Client the installer needs. Pulled
// out so tests can hand in a fake without spinning up a server.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Clock lets tests pin EmittedAt timestamps. Production wires this to
// time.Now; tests inject a deterministic source.
type Clock func() time.Time

// InstallerOptions captures the wiring an Installer needs. All fields
// have safe defaults — zero-value means "production": net/http default
// client, real clock, 32 KiB buffer, 256 KiB progress emit threshold.
type InstallerOptions struct {
	// HTTP is the client used for both HEAD probes and the streamed
	// GET. Defaults to http.DefaultClient when nil.
	HTTP HTTPDoer
	// Clock backs ProgressEvent.EmittedAt. Defaults to time.Now.
	Clock Clock
	// CopyBufferBytes is the per-iteration read size. Larger buffers
	// reduce syscall overhead but make cancellation slower to land
	// — 32 KiB is the standard io.Copy default.
	CopyBufferBytes int
	// ProgressIntervalBytes is the minimum bytes between progress
	// events on a given install stream. Without this the SSE
	// channel emits an event per buffer fill and can drown the UI
	// for fast LAN downloads. Default 256 KiB.
	ProgressIntervalBytes int64
	// ProgressIntervalMS is the minimum wall-clock spacing between
	// progress events. Default 250 ms.
	ProgressIntervalMS int
}

// Installer owns the download path. It serializes installs (one at a
// time in v1) so an over-eager UI can't kick off two parallel pulls
// of the same model, and it owns the on-disk shape — partial files
// land at "<file>.part" and are atomically renamed only after the
// sha256 check passes.
type Installer struct {
	dataDir string
	store   InstallerStore
	catalog *Catalog
	opts    resolvedInstallerOptions

	mu      sync.Mutex
	active  *runningInstall // nil when idle
	counter uint64          // monotonic install_id source

	// fanouts gives one ProgressEvent stream per active install_id.
	// Populated by AttachInstall; readers drain via Subscribe. The
	// fanout buffers up to fanoutBufferSize events so a slow
	// subscriber can still catch up to the most recent terminal
	// event after a tab reload.
	fanoutMu sync.Mutex
	fanouts  map[string]*installFanout
}

type resolvedInstallerOptions struct {
	http              HTTPDoer
	clock             Clock
	copyBufferBytes   int
	progressBytesStep int64
	progressTimeStep  time.Duration
}

// InstallerStore is the slim view of controlplane.Store the installer
// needs. Defined here so tests can stub it without pulling the full
// controlplane surface.
type InstallerStore interface {
	UpsertInstalledModel(ctx context.Context, model InstalledModel) (InstalledModel, error)
	DeleteInstalledModel(ctx context.Context, id string) error
}

// runningInstall is the live state of an in-flight install. The
// installer holds exactly one of these while busy; concurrent install
// attempts return ErrInstallInProgress.
type runningInstall struct {
	installID string
	modelID   string
	cancel    context.CancelFunc
	done      chan struct{}
	// events is the buffered output channel given to subscribers.
	// Closed when the install finishes (success or failure). Capacity
	// is small (16) because consumers are expected to drain promptly;
	// a slow consumer drops events on the floor rather than blocking
	// the download.
	events chan ProgressEvent
}

// NewInstaller wires an installer. dataDir must already exist and be
// writable; the installer creates the models/ subdirectory on first
// use. Both store and catalog must be non-nil.
func NewInstaller(dataDir string, store InstallerStore, catalog *Catalog, opts InstallerOptions) (*Installer, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("installer: dataDir is required")
	}
	if store == nil {
		return nil, errors.New("installer: store is required")
	}
	if catalog == nil {
		return nil, errors.New("installer: catalog is required")
	}
	resolved := resolvedInstallerOptions{
		http:              opts.HTTP,
		clock:             opts.Clock,
		copyBufferBytes:   opts.CopyBufferBytes,
		progressBytesStep: opts.ProgressIntervalBytes,
		progressTimeStep:  time.Duration(opts.ProgressIntervalMS) * time.Millisecond,
	}
	if resolved.http == nil {
		resolved.http = http.DefaultClient
	}
	if resolved.clock == nil {
		resolved.clock = time.Now
	}
	if resolved.copyBufferBytes <= 0 {
		resolved.copyBufferBytes = 32 * 1024
	}
	if resolved.progressBytesStep <= 0 {
		resolved.progressBytesStep = 256 * 1024
	}
	if resolved.progressTimeStep <= 0 {
		resolved.progressTimeStep = 250 * time.Millisecond
	}
	return &Installer{
		dataDir: dataDir,
		store:   store,
		catalog: catalog,
		opts:    resolved,
		fanouts: make(map[string]*installFanout),
	}, nil
}

// installFanout buffers ProgressEvents emitted by a single install
// so the SSE handler can subscribe after the install has already
// started. The buffer retains every event the install produced
// (downloads are short relative to ProgressIntervalBytes) so a late
// subscriber sees the full history.
type installFanout struct {
	mu         sync.Mutex
	events     []ProgressEvent
	closed     bool
	subscriber chan ProgressEvent
}

const fanoutBufferSize = 256

// AttachInstall hooks an InstallHandle into the fanout map and
// starts a goroutine that copies events into the fanout buffer.
// Safe to call exactly once per handle; subsequent calls for the
// same install_id replace the existing fanout (the previous one
// closes its subscriber).
//
// Why a buffer-plus-subscriber design rather than just forwarding
// the install's channel directly: the SSE handler attaches AFTER
// the operator's POST /install round-trip completes, which means
// the install has already emitted "started" by the time SSE
// connects. The buffer lets the late subscriber replay the
// preamble, then receive new events live.
func (i *Installer) AttachInstall(handle *InstallHandle) {
	if handle == nil {
		return
	}
	fan := &installFanout{}
	i.fanoutMu.Lock()
	i.fanouts[handle.InstallID] = fan
	i.fanoutMu.Unlock()
	go func() {
		for ev := range handle.Events {
			fan.push(ev)
		}
		fan.close()
		// Keep the fanout around for a short grace period so a UI
		// reload mid-install can still read the terminal events.
		// 60 s strikes a balance: long enough to outlive any
		// realistic tab-switch lag, short enough that the map
		// doesn't grow unboundedly under hammering. The
		// background reaper drops the entry on schedule.
		time.AfterFunc(60*time.Second, func() {
			i.fanoutMu.Lock()
			defer i.fanoutMu.Unlock()
			if existing, ok := i.fanouts[handle.InstallID]; ok && existing == fan {
				delete(i.fanouts, handle.InstallID)
			}
		})
	}()
}

// Subscribe returns a channel that receives all buffered events plus
// any future events emitted while the install is still active. The
// second return value is false when no install with that id is
// known. v1 supports a single subscriber per install_id (the UI);
// a second subscriber displaces the first.
func (i *Installer) Subscribe(installID string) (<-chan ProgressEvent, bool) {
	i.fanoutMu.Lock()
	fan, ok := i.fanouts[installID]
	i.fanoutMu.Unlock()
	if !ok {
		return nil, false
	}
	ch := fan.attach()
	return ch, true
}

func (f *installFanout) push(ev ProgressEvent) {
	f.mu.Lock()
	f.events = append(f.events, ev)
	if len(f.events) > fanoutBufferSize {
		// Drop the oldest. With fanoutBufferSize=256 and a 4 GB
		// download at 256 KiB per progress event = 16k events, so
		// retention is partial; the UI cares about the latest
		// state anyway.
		drop := len(f.events) - fanoutBufferSize
		f.events = f.events[drop:]
	}
	sub := f.subscriber
	f.mu.Unlock()
	if sub != nil {
		select {
		case sub <- ev:
		default:
			// Slow subscriber — drop. The buffered history is
			// the operator's recovery path (reconnect to replay).
		}
	}
}

func (f *installFanout) close() {
	f.mu.Lock()
	f.closed = true
	sub := f.subscriber
	f.subscriber = nil
	f.mu.Unlock()
	if sub != nil {
		close(sub)
	}
}

func (f *installFanout) attach() chan ProgressEvent {
	ch := make(chan ProgressEvent, fanoutBufferSize+1)
	f.mu.Lock()
	// Displace any prior subscriber.
	if f.subscriber != nil {
		close(f.subscriber)
	}
	// Replay buffered history before going live.
	for _, ev := range f.events {
		select {
		case ch <- ev:
		default:
		}
	}
	if f.closed {
		close(ch)
		f.mu.Unlock()
		return ch
	}
	f.subscriber = ch
	f.mu.Unlock()
	return ch
}

// InstallHandle is what callers hold while a download runs. The
// channel produces ProgressEvents until the install ends (final event
// kind is one of completed / failed / cancelled), then closes.
type InstallHandle struct {
	InstallID string
	ModelID   string
	Events    <-chan ProgressEvent
}

// Install resolves the spec to a concrete plan (catalog lookup or
// paste-URL parse), then kicks off the download on a background
// goroutine and returns the handle. Returns ErrInstallInProgress if
// another install is currently in flight; v1 serializes installs.
func (i *Installer) Install(ctx context.Context, spec InstallSpec) (*InstallHandle, error) {
	plan, err := i.resolveSpec(spec)
	if err != nil {
		return nil, err
	}

	i.mu.Lock()
	if i.active != nil {
		i.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrInstallInProgress, i.active.modelID)
	}
	id := i.nextInstallID()
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	events := make(chan ProgressEvent, 16)
	run := &runningInstall{
		installID: id,
		modelID:   plan.modelID,
		cancel:    cancel,
		done:      make(chan struct{}),
		events:    events,
	}
	i.active = run
	i.mu.Unlock()

	go i.run(runCtx, run, plan)

	return &InstallHandle{
		InstallID: id,
		ModelID:   plan.modelID,
		Events:    events,
	}, nil
}

// Cancel terminates the active install if any. Returns ErrInstallNotFound
// when no install is in flight or when the in-flight install has a
// different id. The partial file is removed before the cancelled event
// fires.
func (i *Installer) Cancel(installID string) error {
	i.mu.Lock()
	active := i.active
	i.mu.Unlock()
	if active == nil {
		return ErrInstallNotFound
	}
	if installID != "" && active.installID != installID {
		return ErrInstallNotFound
	}
	active.cancel()
	<-active.done
	return nil
}

// ActiveInstallID returns the id of the in-flight install or "".
// Useful for the install events SSE handler to know whether to keep
// the stream open or 404 immediately.
func (i *Installer) ActiveInstallID() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.active == nil {
		return ""
	}
	return i.active.installID
}

// installPlan is the resolved per-install state — everything we need
// to download a single file.
type installPlan struct {
	modelID            string
	displayName        string
	url                string
	expectedSHA256     string // lowercase hex; empty when not pinned
	expectedSizeBytes  int64
	recommendedContext int
	capabilities       Capabilities
	finalPath          string // absolute path to the .gguf
	partPath           string // absolute path to the .part file
}

func (i *Installer) resolveSpec(spec InstallSpec) (installPlan, error) {
	if strings.TrimSpace(spec.CatalogID) != "" && strings.TrimSpace(spec.URL) != "" {
		return installPlan{}, ErrInstallSpecAmbiguous
	}

	if id := strings.TrimSpace(spec.CatalogID); id != "" {
		entry, err := i.catalog.Lookup(id)
		if err != nil {
			return installPlan{}, err
		}
		filename := pathFilename(entry.HuggingFaceURL)
		if filename == "" {
			filename = entry.ID + ".gguf"
		}
		finalPath := filepath.Join(i.dataDir, "models", entry.ID+".gguf")
		return installPlan{
			modelID:            entry.ID,
			displayName:        entry.DisplayName,
			url:                entry.HuggingFaceURL,
			expectedSHA256:     strings.ToLower(strings.TrimSpace(entry.SHA256)),
			expectedSizeBytes:  entry.SizeBytes,
			recommendedContext: entry.RecommendedContext,
			capabilities:       entry.Capabilities,
			finalPath:          finalPath,
			partPath:           finalPath + ".part",
		}, nil
	}

	if u := strings.TrimSpace(spec.URL); u != "" {
		canonical, _, slug, err := ParsePasteURL(u)
		if err != nil {
			return installPlan{}, err
		}
		finalPath := filepath.Join(i.dataDir, "models", slug+".gguf")
		return installPlan{
			modelID:        slug,
			displayName:    slug,
			url:            canonical,
			expectedSHA256: strings.ToLower(strings.TrimSpace(spec.SHA256)),
			// Paste-URL installs default to streaming-only,
			// tool-calling: none — the safest assumption for an
			// arbitrary community GGUF. Operators can override
			// in modelcaps after install if they know better.
			capabilities: Capabilities{Streaming: true, ToolCalling: "none"},
			finalPath:    finalPath,
			partPath:     finalPath + ".part",
		}, nil
	}

	return installPlan{}, ErrInstallSpecEmpty
}

func (i *Installer) nextInstallID() string {
	n := atomic.AddUint64(&i.counter, 1)
	return fmt.Sprintf("install-%d-%d", i.opts.clock().UnixNano(), n)
}

func (i *Installer) run(ctx context.Context, run *runningInstall, plan installPlan) {
	defer func() {
		close(run.events)
		i.mu.Lock()
		if i.active == run {
			i.active = nil
		}
		i.mu.Unlock()
		close(run.done)
	}()

	i.emit(run, ProgressEvent{
		Kind:       ProgressStarted,
		ModelID:    plan.modelID,
		BytesTotal: plan.expectedSizeBytes,
	})

	if err := os.MkdirAll(filepath.Dir(plan.finalPath), 0o755); err != nil {
		i.emit(run, ProgressEvent{
			Kind:      ProgressFailed,
			ModelID:   plan.modelID,
			ErrorKind: ErrorKindDisk,
			Message:   fmt.Sprintf("create models dir: %v", err),
		})
		return
	}

	digest, written, err := i.download(ctx, run, plan)
	if err != nil {
		// Always remove any partial file when the download did not
		// complete. Best-effort — surface the original error first.
		_ = os.Remove(plan.partPath)
		if errors.Is(err, context.Canceled) {
			i.emit(run, ProgressEvent{
				Kind:      ProgressCancelled,
				ModelID:   plan.modelID,
				ErrorKind: ErrorKindCancelled,
				Message:   "install cancelled",
			})
			return
		}
		event := ProgressEvent{
			Kind:    ProgressFailed,
			ModelID: plan.modelID,
			Message: err.Error(),
		}
		var ghttp *httpStatusError
		if errors.As(err, &ghttp) {
			switch {
			case ghttp.code == http.StatusUnauthorized, ghttp.code == http.StatusForbidden:
				event.ErrorKind = ErrorKindGated
				event.Message = fmt.Sprintf("model is gated (HTTP %d); v1 does not support gated repos", ghttp.code)
			default:
				event.ErrorKind = ErrorKindNetwork
				event.Message = fmt.Sprintf("download failed: HTTP %d", ghttp.code)
			}
		} else {
			event.ErrorKind = ErrorKindNetwork
		}
		i.emit(run, event)
		return
	}

	// SHA verification: hard fail on mismatch, log-warn on empty.
	if plan.expectedSHA256 != "" && digest != plan.expectedSHA256 {
		_ = os.Remove(plan.partPath)
		i.emit(run, ProgressEvent{
			Kind:           ProgressFailed,
			ModelID:        plan.modelID,
			ErrorKind:      ErrorKindShaMismatch,
			Message:        "downloaded file sha256 does not match the expected digest",
			ExpectedSHA256: plan.expectedSHA256,
			ActualSHA256:   digest,
		})
		return
	}

	// Atomic rename — the registry row must never reference a path
	// that the operator might find half-downloaded.
	if err := os.Rename(plan.partPath, plan.finalPath); err != nil {
		_ = os.Remove(plan.partPath)
		i.emit(run, ProgressEvent{
			Kind:      ProgressFailed,
			ModelID:   plan.modelID,
			ErrorKind: ErrorKindDisk,
			Message:   fmt.Sprintf("finalize file: %v", err),
		})
		return
	}

	// Persist the registry row. FilePath is stored relative to the
	// data dir so a future relocation doesn't poison the row.
	rel, err := filepath.Rel(i.dataDir, plan.finalPath)
	if err != nil {
		rel = plan.finalPath
	}
	_, err = i.store.UpsertInstalledModel(context.Background(), InstalledModel{
		ID:                 plan.modelID,
		DisplayName:        plan.displayName,
		FilePath:           rel,
		SourceURL:          plan.url,
		SHA256:             digest,
		SizeBytes:          written,
		RecommendedContext: plan.recommendedContext,
		Capabilities:       plan.capabilities,
	})
	if err != nil {
		// File is on disk but registry didn't accept it — surface
		// the registry error, leave the file alone (the next install
		// or boot reconcile will pick it up).
		i.emit(run, ProgressEvent{
			Kind:      ProgressFailed,
			ModelID:   plan.modelID,
			ErrorKind: ErrorKindUnknown,
			Message:   fmt.Sprintf("registry write failed: %v", err),
		})
		return
	}

	i.emit(run, ProgressEvent{
		Kind:            ProgressCompleted,
		ModelID:         plan.modelID,
		BytesDownloaded: written,
		BytesTotal:      written,
		SHA256:          digest,
	})
}

// download streams the HTTP body into the .part file, hashing as it
// goes, and emits sampled progress events. Returns the lowercase hex
// sha256 of the downloaded bytes and the total written.
func (i *Installer) download(ctx context.Context, run *runningInstall, plan installPlan) (string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, plan.url, nil)
	if err != nil {
		return "", 0, fmt.Errorf("build request: %w", err)
	}
	resp, err := i.opts.http.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, &httpStatusError{code: resp.StatusCode}
	}

	total := plan.expectedSizeBytes
	if cl := resp.ContentLength; cl > 0 {
		total = cl
	}

	out, err := os.OpenFile(plan.partPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, fmt.Errorf("open part file: %w", err)
	}
	defer out.Close()

	hasher := sha256.New()
	buf := make([]byte, i.opts.copyBufferBytes)
	var written int64
	var lastEmit time.Time
	var lastEmitBytes int64

	for {
		// Context cancellation lands here in addition to the body
		// reader's own awareness — net/http stops the read but we
		// want a deterministic exit point in the loop.
		select {
		case <-ctx.Done():
			return "", written, ctx.Err()
		default:
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return "", written, fmt.Errorf("write part: %w", werr)
			}
			hasher.Write(buf[:n])
			written += int64(n)

			now := i.opts.clock()
			if written-lastEmitBytes >= i.opts.progressBytesStep ||
				now.Sub(lastEmit) >= i.opts.progressTimeStep {
				lastEmit = now
				lastEmitBytes = written
				i.emit(run, ProgressEvent{
					Kind:            ProgressProgress,
					ModelID:         plan.modelID,
					BytesDownloaded: written,
					BytesTotal:      total,
				})
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return "", written, readErr
		}
	}

	if err := out.Sync(); err != nil {
		return "", written, fmt.Errorf("fsync part: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), written, nil
}

func (i *Installer) emit(run *runningInstall, event ProgressEvent) {
	event.EmittedAt = i.opts.clock()
	select {
	case run.events <- event:
	default:
		// Slow consumer — drop progress events on the floor rather
		// than blocking the download. Terminal events (completed /
		// failed / cancelled) are also subject to this; the SSE
		// handler accepts the consequence in exchange for never
		// stalling the writer.
	}
}

// pathFilename returns the last path segment of a URL string, or "".
// Cheap helper to avoid threading net/url through resolveSpec.
func pathFilename(raw string) string {
	if raw == "" {
		return ""
	}
	if idx := strings.LastIndex(raw, "/"); idx >= 0 && idx < len(raw)-1 {
		return raw[idx+1:]
	}
	return raw
}

// httpStatusError carries a non-2xx HTTP response so the run loop can
// classify gated (401/403) versus generic network errors without
// inspecting strings.
type httpStatusError struct {
	code int
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("http status %d", e.code)
}

// Error kinds — matches the docs/events.md surface for the
// local_model.install.failed event.
const (
	ErrorKindNetwork     = "network"
	ErrorKindShaMismatch = "sha_mismatch"
	ErrorKindCancelled   = "cancelled"
	ErrorKindDisk        = "disk"
	ErrorKindGated       = "gated"
	ErrorKindInvalidURL  = "invalid_url"
	ErrorKindUnknown     = "unknown"
)

var (
	// ErrInstallInProgress is returned by Install when another
	// install is in flight. The handler maps this to a 409 with the
	// stable code local_model_install_already_running.
	ErrInstallInProgress = errors.New("install already in progress")

	// ErrInstallSpecEmpty surfaces when neither CatalogID nor URL
	// is set. Handler maps to 400.
	ErrInstallSpecEmpty = errors.New("install spec must set catalog_id or url")

	// ErrInstallSpecAmbiguous surfaces when both CatalogID and URL
	// are set. Handler maps to 400.
	ErrInstallSpecAmbiguous = errors.New("install spec sets both catalog_id and url; pick one")

	// ErrInstallNotFound is returned by Cancel when there is no
	// in-flight install with the requested id. Handler maps to 404.
	ErrInstallNotFound = errors.New("install not found")
)
