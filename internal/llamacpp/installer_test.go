package llamacpp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// stubStore is a minimal InstallerStore used by the installer tests.
// Captures Upsert / Delete calls so tests can assert what the
// installer asked the store to persist.
type stubStore struct {
	mu       sync.Mutex
	upserts  []InstalledModel
	deletes  []string
	upsertFn func(InstalledModel) (InstalledModel, error)
}

func (s *stubStore) UpsertInstalledModel(_ context.Context, model InstalledModel) (InstalledModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserts = append(s.upserts, model)
	if s.upsertFn != nil {
		return s.upsertFn(model)
	}
	if model.InstalledAt.IsZero() {
		model.InstalledAt = time.Unix(0, 0).UTC()
	}
	return model, nil
}

func (s *stubStore) DeleteInstalledModel(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes = append(s.deletes, id)
	return nil
}

// fixedClock returns the same instant on every call. The installer's
// progress sampler uses wall-clock spacing, so tests that want to
// force or suppress sampling pin time deliberately.
func fixedClock(t time.Time) Clock { return func() time.Time { return t } }

// makeInstallerWithCatalog wires an installer pointed at a temp dir,
// a stub store, and the supplied catalog. Used by every test below.
func makeInstallerWithCatalog(t *testing.T, catalog *Catalog, opts InstallerOptions) (*Installer, *stubStore, string) {
	t.Helper()
	store := &stubStore{}
	dir := t.TempDir()
	inst, err := NewInstaller(dir, store, catalog, opts)
	if err != nil {
		t.Fatalf("NewInstaller: %v", err)
	}
	return inst, store, dir
}

// drainEvents consumes all events from a handle into a slice. Returns
// nil when the channel closes; bounded by the test timeout.
func drainEvents(t *testing.T, h *InstallHandle) []ProgressEvent {
	t.Helper()
	out := make([]ProgressEvent, 0, 8)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-h.Events:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline:
			t.Fatalf("timed out waiting for events; collected so far: %+v", out)
			return out
		}
	}
}

func lastEvent(events []ProgressEvent) ProgressEvent {
	if len(events) == 0 {
		return ProgressEvent{}
	}
	return events[len(events)-1]
}

func TestInstaller_PasteURLHappyPath(t *testing.T) {
	t.Parallel()

	payload := []byte("not a real gguf, but a deterministic body")
	expectedSHA := sha256.Sum256(payload)
	expectedHex := hex.EncodeToString(expectedSHA[:])

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	inst, store, dir := makeInstallerWithCatalog(t, NewCatalog(), InstallerOptions{
		HTTP:                  srv.Client(),
		Clock:                 fixedClock(time.Unix(1700000000, 0).UTC()),
		ProgressIntervalBytes: 1, // emit every chunk so tests see progress
		ProgressIntervalMS:    1,
	})

	// Build a paste-URL by appending a /resolve/main/<file>.gguf path
	// onto the test server's URL — the parser only cares about the
	// shape, not the host.
	pasteURL := srv.URL + "/test/repo/resolve/main/test-model.gguf"
	handle, err := inst.Install(context.Background(), InstallSpec{URL: pasteURL, SHA256: expectedHex})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	events := drainEvents(t, handle)

	final := lastEvent(events)
	if final.Kind != ProgressCompleted {
		t.Fatalf("final event = %+v; want completed", final)
	}
	if final.SHA256 != expectedHex {
		t.Fatalf("final sha = %q; want %q", final.SHA256, expectedHex)
	}
	if final.BytesDownloaded != int64(len(payload)) {
		t.Fatalf("BytesDownloaded = %d; want %d", final.BytesDownloaded, len(payload))
	}

	// File must exist at the expected path, .part must be gone.
	finalPath := filepath.Join(dir, "models", "test-model.gguf")
	if _, err := os.Stat(finalPath); err != nil {
		t.Fatalf("final file missing: %v", err)
	}
	if _, err := os.Stat(finalPath + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial file should be removed; stat = %v", err)
	}

	// Registry upsert recorded once with the expected payload.
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.upserts) != 1 {
		t.Fatalf("upserts = %d; want 1", len(store.upserts))
	}
	got := store.upserts[0]
	if got.ID != "test-model" {
		t.Fatalf("model id = %q; want test-model", got.ID)
	}
	if got.SHA256 != expectedHex {
		t.Fatalf("model sha = %q; want %q", got.SHA256, expectedHex)
	}
	if got.FilePath != "models/test-model.gguf" {
		t.Fatalf("file_path = %q; want models/test-model.gguf", got.FilePath)
	}
	if got.SizeBytes != int64(len(payload)) {
		t.Fatalf("size = %d; want %d", got.SizeBytes, len(payload))
	}
}

func TestInstaller_ShaMismatchHardFails(t *testing.T) {
	t.Parallel()
	payload := []byte("body that does not match the asserted sha")

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	inst, store, dir := makeInstallerWithCatalog(t, NewCatalog(), InstallerOptions{
		HTTP:  srv.Client(),
		Clock: fixedClock(time.Unix(1700000000, 0).UTC()),
	})

	handle, err := inst.Install(context.Background(), InstallSpec{
		URL:    srv.URL + "/test/repo/resolve/main/bad-model.gguf",
		SHA256: "deadbeef" + "00000000000000000000000000000000000000000000000000000000",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	events := drainEvents(t, handle)
	final := lastEvent(events)
	if final.Kind != ProgressFailed {
		t.Fatalf("expected failed event, got %+v", final)
	}
	if final.ErrorKind != ErrorKindShaMismatch {
		t.Fatalf("error kind = %q; want %q", final.ErrorKind, ErrorKindShaMismatch)
	}
	if final.ExpectedSHA256 == "" || final.ActualSHA256 == "" {
		t.Fatalf("mismatch event must carry both hashes: %+v", final)
	}

	// Partial file must be cleaned up, registry must not see an
	// upsert for a bad file.
	if _, err := os.Stat(filepath.Join(dir, "models", "bad-model.gguf.part")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial file should be removed on sha mismatch; stat = %v", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.upserts) != 0 {
		t.Fatalf("expected no upserts on sha mismatch, got %+v", store.upserts)
	}
}

func TestInstaller_GatedRepoMappedToGated(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gated", http.StatusForbidden)
	}))
	defer srv.Close()

	inst, _, _ := makeInstallerWithCatalog(t, NewCatalog(), InstallerOptions{
		HTTP:  srv.Client(),
		Clock: fixedClock(time.Unix(1700000000, 0).UTC()),
	})

	handle, err := inst.Install(context.Background(), InstallSpec{
		URL: srv.URL + "/foo/resolve/main/gated.gguf",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	events := drainEvents(t, handle)
	final := lastEvent(events)
	if final.Kind != ProgressFailed {
		t.Fatalf("expected failed, got %+v", final)
	}
	if final.ErrorKind != ErrorKindGated {
		t.Fatalf("error kind = %q; want %q", final.ErrorKind, ErrorKindGated)
	}
}

func TestInstaller_CancelDuringDownloadCleansUp(t *testing.T) {
	t.Parallel()

	// Server streams forever (until the client hangs up) so the
	// installer is guaranteed to be inside the download loop when we
	// call Cancel.
	ready := make(chan struct{}, 1)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		flusher, _ := w.(http.Flusher)
		chunk := make([]byte, 1024)
		select {
		case ready <- struct{}{}:
		default:
		}
		for {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}))
	defer srv.Close()

	inst, _, dir := makeInstallerWithCatalog(t, NewCatalog(), InstallerOptions{
		HTTP:                  srv.Client(),
		Clock:                 fixedClock(time.Unix(1700000000, 0).UTC()),
		ProgressIntervalBytes: 1,
		ProgressIntervalMS:    1,
	})

	handle, err := inst.Install(context.Background(), InstallSpec{
		URL: srv.URL + "/test/resolve/main/cancellable.gguf",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Wait until the server has handed out at least one chunk before
	// cancelling, otherwise the loop hasn't really started.
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("server never received the request")
	}
	if err := inst.Cancel(handle.InstallID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	events := drainEvents(t, handle)
	final := lastEvent(events)
	if final.Kind != ProgressCancelled {
		t.Fatalf("expected cancelled, got %+v", final)
	}
	if _, err := os.Stat(filepath.Join(dir, "models", "cancellable.gguf.part")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("part file should be cleaned up on cancel; stat = %v", err)
	}
}

func TestInstaller_RejectsParallelInstall(t *testing.T) {
	t.Parallel()

	hold := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hold
		_, _ = io.WriteString(w, "x")
	}))
	defer srv.Close()
	defer close(hold)

	inst, _, _ := makeInstallerWithCatalog(t, NewCatalog(), InstallerOptions{
		HTTP:  srv.Client(),
		Clock: fixedClock(time.Unix(1700000000, 0).UTC()),
	})

	first, err := inst.Install(context.Background(), InstallSpec{
		URL: srv.URL + "/foo/resolve/main/first.gguf",
	})
	if err != nil {
		t.Fatalf("first Install: %v", err)
	}

	if _, err := inst.Install(context.Background(), InstallSpec{
		URL: srv.URL + "/foo/resolve/main/second.gguf",
	}); !errors.Is(err, ErrInstallInProgress) {
		t.Fatalf("expected ErrInstallInProgress, got %v", err)
	}

	if err := inst.Cancel(first.InstallID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	drainEvents(t, first)
}

func TestInstaller_SpecValidation(t *testing.T) {
	t.Parallel()
	inst, _, _ := makeInstallerWithCatalog(t, NewCatalog(), InstallerOptions{})

	if _, err := inst.Install(context.Background(), InstallSpec{}); !errors.Is(err, ErrInstallSpecEmpty) {
		t.Fatalf("empty spec: got %v", err)
	}
	if _, err := inst.Install(context.Background(), InstallSpec{
		CatalogID: "qwen2.5-0_5b-instruct-q4_k_m",
		URL:       "https://example.com/x.gguf",
	}); !errors.Is(err, ErrInstallSpecAmbiguous) {
		t.Fatalf("ambiguous spec: got %v", err)
	}
	if _, err := inst.Install(context.Background(), InstallSpec{
		CatalogID: "not-a-real-entry",
	}); !errors.Is(err, ErrCatalogEntryNotFound) {
		t.Fatalf("unknown catalog id: got %v", err)
	}
}

func TestInstaller_CancelNoActiveInstall(t *testing.T) {
	t.Parallel()
	inst, _, _ := makeInstallerWithCatalog(t, NewCatalog(), InstallerOptions{})
	if err := inst.Cancel(""); !errors.Is(err, ErrInstallNotFound) {
		t.Fatalf("Cancel with no active: got %v", err)
	}
}
