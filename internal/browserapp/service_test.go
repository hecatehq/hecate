package browserapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hecatehq/hecate/internal/browserrunner"
)

type fakeRunner struct {
	inspect func(context.Context, browserrunner.InspectRequest) (browserrunner.InspectResult, error)
	flow    func(context.Context, browserrunner.FlowRequest) (browserrunner.FlowResult, error)
}

func (r fakeRunner) Inspect(ctx context.Context, req browserrunner.InspectRequest) (browserrunner.InspectResult, error) {
	if r.inspect != nil {
		return r.inspect(ctx, req)
	}
	return browserrunner.InspectResult{FinalURL: req.URL}, nil
}

func (r fakeRunner) RunFlow(ctx context.Context, req browserrunner.FlowRequest) (browserrunner.FlowResult, error) {
	if r.flow != nil {
		return r.flow(ctx, req)
	}
	return browserrunner.FlowResult{}, nil
}

func browserFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "browser.exe")
	// A marker-producing script makes accidental --version or other discovery
	// execution observable on Unix. The Windows test uses a non-executable PE
	// placeholder; passive discovery may inspect it but must never run it.
	if err := os.WriteFile(path, []byte("#!/bin/sh\ntouch \"$0.started\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixtureService(t *testing.T, store Store) (*Service, string) {
	t.Helper()
	path := browserFixture(t)
	s := New(Options{RuntimeHostID: "runtime-a", Store: store})
	s.installations = func() []installation { return []installation{{"Chromium", path}} }
	s.newRunner = func(browserrunner.Config) (runner, error) { return fakeRunner{}, nil }
	return s, path
}

func enableFixture(t *testing.T, service *Service) Settings {
	t.Helper()
	settings, err := service.Settings(context.Background())
	if err != nil || len(settings.Candidates) != 1 {
		t.Fatalf("Settings = %#v, %v", settings, err)
	}
	settings, err = service.Enable(context.Background(), settings.Candidates[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

func TestServiceDiscoveryAndEnableArePassive(t *testing.T) {
	s, path := fixtureService(t, NewMemoryStore())
	var constructions atomic.Int32
	s.newRunner = func(browserrunner.Config) (runner, error) {
		constructions.Add(1)
		return fakeRunner{}, nil
	}
	if initial := s.Readiness(context.Background()); initial.Available || initial.Status != "not_configured" {
		t.Fatalf("initial readiness = %#v", initial)
	}
	settings := enableFixture(t, s)
	if settings.Source != "settings" || settings.Readiness.Status != "configured" || !settings.Readiness.Available {
		t.Fatalf("enabled settings = %#v", settings)
	}
	if !s.Available() || constructions.Load() != 0 {
		t.Fatalf("passive setup constructed %d runners", constructions.Load())
	}
	if _, err := os.Stat(path + ".started"); !os.IsNotExist(err) {
		t.Fatalf("discovery/enable executed the browser: %v", err)
	}
	if _, err := s.Inspect(context.Background(), browserrunner.InspectRequest{URL: "https://example.test/"}); err != nil {
		t.Fatal(err)
	}
	if ready := s.Readiness(context.Background()); ready.Status != "working" || !ready.Available {
		t.Fatalf("successful use readiness = %#v", ready)
	}
	if constructions.Load() != 1 {
		t.Fatalf("runner constructions = %d", constructions.Load())
	}
}

func TestServiceFlowMarksWorkingAndFailedNavigationDoesNotDisable(t *testing.T) {
	s, _ := fixtureService(t, NewMemoryStore())
	enableFixture(t, s)
	s.newRunner = func(browserrunner.Config) (runner, error) {
		return fakeRunner{flow: func(context.Context, browserrunner.FlowRequest) (browserrunner.FlowResult, error) {
			return browserrunner.FlowResult{}, browserrunner.ErrOriginNotAllowed
		}}, nil
	}
	if _, err := s.RunFlow(context.Background(), browserrunner.FlowRequest{}); !errors.Is(err, browserrunner.ErrOriginNotAllowed) {
		t.Fatalf("RunFlow error = %v", err)
	}
	if ready := s.Readiness(context.Background()); ready.Status != "configured" || !ready.Available {
		t.Fatalf("navigation failure changed configuration: %#v", ready)
	}
	s.newRunner = func(browserrunner.Config) (runner, error) { return fakeRunner{}, nil }
	if _, err := s.RunFlow(context.Background(), browserrunner.FlowRequest{}); err != nil {
		t.Fatal(err)
	}
	if s.Readiness(context.Background()).Status != "working" {
		t.Fatal("flow success did not mark working")
	}
	s.newRunner = func(browserrunner.Config) (runner, error) {
		return fakeRunner{inspect: func(context.Context, browserrunner.InspectRequest) (browserrunner.InspectResult, error) {
			return browserrunner.InspectResult{}, browserrunner.ErrPrivateNetwork
		}}, nil
	}
	_, _ = s.Inspect(context.Background(), browserrunner.InspectRequest{})
	if s.Readiness(context.Background()).Status != "working" {
		t.Fatal("a policy denial erased observed working evidence")
	}
}

func TestServiceSelectionPersistsButWorkingIsProcessScoped(t *testing.T) {
	store := NewMemoryStore()
	s, path := fixtureService(t, store)
	enableFixture(t, s)
	_, _ = s.Inspect(context.Background(), browserrunner.InspectRequest{})
	restarted := New(Options{RuntimeHostID: "runtime-a", Store: store})
	restarted.installations = s.installations
	settings, err := restarted.Settings(context.Background())
	if err != nil || settings.Readiness.Status != "configured" || settings.Selected.Path != path {
		t.Fatalf("restart = %#v, %v", settings, err)
	}
	other := New(Options{RuntimeHostID: "runtime-b", Store: store})
	if other.Available() || other.Readiness(context.Background()).Status != "not_configured" {
		t.Fatal("browser selection leaked across runtime hosts")
	}
}

func TestServiceEnableRejectsStaleCandidate(t *testing.T) {
	s, path := fixtureService(t, NewMemoryStore())
	settings, err := s.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n# installation changed\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enable(context.Background(), settings.Candidates[0].ID); !errors.Is(err, ErrCandidateChanged) {
		t.Fatalf("stale selection error = %v", err)
	}
	if s.Available() {
		t.Fatal("stale candidate was enabled")
	}
}

func TestServiceEnableRejectsEqualMetadataReplacement(t *testing.T) {
	s, path := fixtureService(t, NewMemoryStore())
	settings, err := s.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Retain the old file while replacing its installation pathname so inode /
	// file-index reuse cannot make the test accidentally cover the same object.
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enable(context.Background(), settings.Candidates[0].ID); !errors.Is(err, ErrCandidateChanged) {
		t.Fatalf("equal-metadata replacement selection error = %v", err)
	}
}

func TestServiceSelectedInstallationUpdateResetsWorkingWithoutExecuting(t *testing.T) {
	s, path := fixtureService(t, NewMemoryStore())
	enableFixture(t, s)
	_, _ = s.Inspect(context.Background(), browserrunner.InspectRequest{})
	if err := os.WriteFile(path, []byte("#!/bin/sh\n# installed browser update\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ready := s.Readiness(context.Background()); ready.Status != "configured" || !ready.Available {
		t.Fatalf("updated installation = %#v", ready)
	}
}

func TestServiceMissingSelectionNeverFallsBack(t *testing.T) {
	s, path := fixtureService(t, NewMemoryStore())
	enableFixture(t, s)
	other := browserFixture(t)
	s.installations = func() []installation { return []installation{{"Other browser", other}} }
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	settings, err := s.Settings(context.Background())
	if err != nil || settings.Readiness.Status != "unavailable" || settings.Readiness.Available || settings.Selected.Path != path {
		t.Fatalf("missing selection = %#v, %v", settings, err)
	}
	if _, err := s.Inspect(context.Background(), browserrunner.InspectRequest{}); !errors.Is(err, browserrunner.ErrUnavailable) {
		t.Fatalf("missing selected browser error = %v", err)
	}
}

func TestServiceRechecksCanonicalSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires platform privileges")
	}
	s, original := fixtureService(t, NewMemoryStore())
	alias := filepath.Join(t.TempDir(), "browser")
	if err := os.Symlink(original, alias); err != nil {
		t.Fatal(err)
	}
	s.installations = func() []installation { return []installation{{"Chromium", alias}} }
	enableFixture(t, s)
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(browserFixture(t), alias); err != nil {
		t.Fatal(err)
	}
	if s.Available() {
		t.Fatal("symlink retarget silently changed selected browser")
	}
	if _, err := s.RunFlow(context.Background(), browserrunner.FlowRequest{}); !errors.Is(err, browserrunner.ErrUnavailable) {
		t.Fatalf("retargeted selection error = %v", err)
	}
}

func TestServiceEnvironmentOverrideIsAuthoritative(t *testing.T) {
	store := NewMemoryStore()
	s, path := fixtureService(t, store)
	enableFixture(t, s)
	for _, override := range []string{path, filepath.Join(t.TempDir(), "missing"), "relative-browser"} {
		t.Run(filepath.Base(override), func(t *testing.T) {
			managed := New(Options{RuntimeHostID: "runtime-a", Store: store, ExecutableOverride: override})
			managed.installations = s.installations
			settings, err := managed.Settings(context.Background())
			if err != nil || settings.Source != "environment" || settings.Selected.Path != override {
				t.Fatalf("managed settings = %#v, %v", settings, err)
			}
			if settings.Readiness.Available != (override == path) {
				t.Fatal("invalid environment override fell back to persisted selection")
			}
			if _, err := managed.Enable(context.Background(), settings.Candidates[0].ID); !errors.Is(err, ErrManaged) {
				t.Fatalf("managed enable error = %v", err)
			}
			if _, err := managed.Disable(context.Background()); !errors.Is(err, ErrManaged) {
				t.Fatalf("managed disable error = %v", err)
			}
		})
	}
}

func TestServiceRemoteDoesNotDiscoverReadOrExecute(t *testing.T) {
	s := New(Options{RuntimeHostID: "runtime-a", Remote: true, Store: errorStore{}})
	s.installations = func() []installation { t.Fatal("remote discovery"); return nil }
	s.newRunner = func(browserrunner.Config) (runner, error) { t.Fatal("remote runner"); return nil, nil }
	if ready := s.Readiness(context.Background()); ready.Available || ready.Status != "local_only" {
		t.Fatalf("remote readiness = %#v", ready)
	}
	if _, err := s.Settings(context.Background()); !errors.Is(err, ErrRemote) {
		t.Fatal(err)
	}
	if _, err := s.Enable(context.Background(), "anything"); !errors.Is(err, ErrRemote) {
		t.Fatal(err)
	}
	if _, err := s.Disable(context.Background()); !errors.Is(err, ErrRemote) {
		t.Fatal(err)
	}
	if _, err := s.Inspect(context.Background(), browserrunner.InspectRequest{}); !errors.Is(err, browserrunner.ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestServiceDisableFencesFutureCallsAndStaleSuccess(t *testing.T) {
	s, _ := fixtureService(t, NewMemoryStore())
	enableFixture(t, s)
	started, finish := make(chan struct{}), make(chan struct{})
	s.newRunner = func(browserrunner.Config) (runner, error) {
		return fakeRunner{inspect: func(context.Context, browserrunner.InspectRequest) (browserrunner.InspectResult, error) {
			close(started)
			<-finish
			return browserrunner.InspectResult{}, nil
		}}, nil
	}
	done := make(chan error, 1)
	go func() { _, err := s.Inspect(context.Background(), browserrunner.InspectRequest{}); done <- err }()
	<-started
	settings, err := s.Disable(context.Background())
	if err != nil || settings.Readiness.Available || settings.Source != "none" {
		t.Fatalf("disable = %#v, %v", settings, err)
	}
	if _, err := s.RunFlow(context.Background(), browserrunner.FlowRequest{}); !errors.Is(err, browserrunner.ErrUnavailable) {
		t.Fatal("disabled runtime admitted new flow")
	}
	// Re-enabling the same path is a new generation. An older call's success
	// must not mark this new configuration as verified.
	enableFixture(t, s)
	close(finish)
	if err := <-done; err != nil {
		t.Fatalf("already-admitted call should finish: %v", err)
	}
	if ready := s.Readiness(context.Background()); ready.Status != "configured" {
		t.Fatalf("stale success promoted new selection: %#v", ready)
	}
}

type errorStore struct{ Store }

func (errorStore) Backend() string { return "memory" }
func (errorStore) Get(context.Context, string) (Selection, error) {
	return Selection{}, errors.New("sensitive /host/path diagnostics")
}

func TestServiceStoreFailureIsUnavailableAndPathFree(t *testing.T) {
	s := New(Options{RuntimeHostID: "runtime-a", Store: errorStore{}})
	readiness := s.Readiness(context.Background())
	if readiness.Available || readiness.Status != "unavailable" || strings.Contains(readiness.Message, "/host/path") {
		t.Fatalf("failed store readiness = %#v", readiness)
	}
	if _, err := s.Settings(context.Background()); !errors.Is(err, ErrStoreUnavailable) || strings.Contains(err.Error(), "/host/path") {
		t.Fatalf("failed store error = %v", err)
	}
}

func TestServiceCancelledAdmissionDoesNotConstructRunner(t *testing.T) {
	s, _ := fixtureService(t, NewMemoryStore())
	enableFixture(t, s)
	s.newRunner = func(browserrunner.Config) (runner, error) {
		t.Fatal("cancelled call constructed a runner")
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Inspect(ctx, browserrunner.InspectRequest{}); !errors.Is(err, browserrunner.ErrUnavailable) {
		t.Fatalf("cancelled admission error = %v", err)
	}
}
