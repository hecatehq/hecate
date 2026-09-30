// Package browserapp owns host-local browser setup, separate from Work-policy
// grants and per-call approval. Discovery and configuration never launch apps.
package browserapp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/browserrunner"
)

var (
	ErrManaged          = errors.New("browser setup is managed by HECATE_TASK_BROWSER_EXECUTABLE")
	ErrRemote           = errors.New("browser setup is available only on a local Hecate runtime")
	ErrCandidateChanged = errors.New("the browser installation changed or is unavailable; refresh and select it again")
	ErrStoreUnavailable = errors.New("browser settings are unavailable")
)

type Readiness struct {
	Available      bool   `json:"available"`
	Status         string `json:"status"`
	Message        string `json:"message"`
	OperatorAction string `json:"operator_action,omitempty"`
}

type Settings struct {
	Readiness  Readiness   `json:"readiness"`
	Source     string      `json:"source"`
	Selected   *Candidate  `json:"selected,omitempty"`
	Candidates []Candidate `json:"candidates"`
	Backend    string      `json:"backend"`
}

type Options struct {
	RuntimeHostID      string
	Remote             bool
	ExecutableOverride string
	Timeout            time.Duration
	AllowPrivateIPs    bool
	Store              Store
}

type runner interface {
	browserrunner.Inspector
	browserrunner.FlowRunner
}

type Service struct {
	mu            sync.Mutex
	options       Options
	store         Store
	generation    uint64
	workingID     string
	installations func() []installation
	newRunner     func(browserrunner.Config) (runner, error)
}

func New(options Options) *Service {
	options.RuntimeHostID = strings.TrimSpace(options.RuntimeHostID)
	options.ExecutableOverride = strings.TrimSpace(options.ExecutableOverride)
	if options.Store == nil {
		options.Store = NewMemoryStore()
	}
	return &Service{
		options: options, store: options.Store, installations: defaultInstallations,
		newRunner: func(cfg browserrunner.Config) (runner, error) { return browserrunner.New(cfg) },
	}
}

// SetStore is startup composition; it discards process-local working evidence.
func (s *Service) SetStore(store Store) {
	if s == nil || store == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = store
	s.generation++
	s.workingID = ""
}

// Available describes configuration, not a successful browser launch. The
// orchestrator must freeze this hint consistently across catalog, approval and
// dispatch, so a false-to-true transition cannot enable an ungated call.
func (s *Service) Available() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.Readiness(ctx).Available
}

func (s *Service) Readiness(ctx context.Context) Readiness {
	if s == nil {
		return unavailableReadiness()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, readiness, _ := s.selectionLocked(ctx)
	return readiness
}

func (s *Service) Settings(ctx context.Context) (settings Settings, retErr error) {
	ctx, span := browserTracer.Start(ctx, "browser.setup.discover")
	defer func() { finishSpan(span, retErr, settings.Readiness.Status) }()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.options.Remote {
		return Settings{}, ErrRemote
	}
	return s.settingsLocked(ctx)
}

func (s *Service) settingsLocked(ctx context.Context) (Settings, error) {
	selected, source, readiness, err := s.selectionLocked(ctx)
	if err != nil {
		return Settings{}, err
	}
	candidates, err := discover(ctx, s.installations())
	if err != nil {
		return Settings{}, err
	}
	settings := Settings{Readiness: readiness, Source: source, Candidates: make([]Candidate, 0, len(candidates)), Backend: s.store.Backend()}
	if selected.Path != "" {
		value := selected.Candidate
		settings.Selected = &value
	}
	for _, candidate := range candidates {
		settings.Candidates = append(settings.Candidates, candidate.Candidate)
	}
	return settings, nil
}

func (s *Service) Enable(ctx context.Context, candidateID string) (settings Settings, retErr error) {
	ctx, span := browserTracer.Start(ctx, "browser.setup.enable")
	defer func() { finishSpan(span, retErr, settings.Readiness.Status) }()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.mutableLocked(); err != nil {
		return Settings{}, err
	}
	candidates, err := discover(ctx, s.installations())
	if err != nil {
		return Settings{}, err
	}
	for _, candidate := range candidates {
		if candidate.ID != candidateID {
			continue
		}
		selection := Selection{RuntimeHostID: s.options.RuntimeHostID, Name: candidate.Name, Path: candidate.Path, CanonicalPath: candidate.canonicalPath}
		if err := s.store.Put(ctx, selection); err != nil {
			return Settings{}, ErrStoreUnavailable
		}
		s.generation++
		s.workingID = ""
		return s.settingsLocked(ctx)
	}
	return Settings{}, ErrCandidateChanged
}

// Disable blocks future admissions. A browser call admitted before this write
// retains its private runner and may finish; disabling is not cancellation.
func (s *Service) Disable(ctx context.Context) (settings Settings, retErr error) {
	ctx, span := browserTracer.Start(ctx, "browser.setup.disable")
	defer func() { finishSpan(span, retErr, settings.Readiness.Status) }()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.mutableLocked(); err != nil {
		return Settings{}, err
	}
	if err := s.store.Delete(ctx, s.options.RuntimeHostID); err != nil {
		return Settings{}, ErrStoreUnavailable
	}
	s.generation++
	s.workingID = ""
	return s.settingsLocked(ctx)
}

func (s *Service) mutableLocked() error {
	if s.options.Remote {
		return ErrRemote
	}
	if s.options.ExecutableOverride != "" {
		return ErrManaged
	}
	if s.options.RuntimeHostID == "" || s.store == nil {
		return ErrStoreUnavailable
	}
	return nil
}

func (s *Service) selectionLocked(ctx context.Context) (discoveredCandidate, string, Readiness, error) {
	if s.options.Remote {
		return discoveredCandidate{}, "none", Readiness{Status: "local_only", Message: "The native browser runtime is unavailable in remote runtime.", OperatorAction: "Use a local Hecate runtime for browser tools."}, nil
	}
	if err := ctx.Err(); err != nil {
		return discoveredCandidate{}, "none", unavailableReadiness(), err
	}
	source := "settings"
	selection := Selection{}
	if s.options.ExecutableOverride != "" {
		source = "environment"
		selection = Selection{Name: "Environment-managed browser", Path: s.options.ExecutableOverride}
	} else {
		if s.options.RuntimeHostID == "" || s.store == nil {
			return discoveredCandidate{}, "none", unavailableReadiness(), ErrStoreUnavailable
		}
		var err error
		selection, err = s.store.Get(ctx, s.options.RuntimeHostID)
		if errors.Is(err, ErrNotFound) {
			return discoveredCandidate{}, "none", Readiness{Status: "not_configured", Message: "Choose and enable an installed browser to use browser tools.", OperatorAction: "Open Settings → Browser setup."}, nil
		}
		if err != nil {
			return discoveredCandidate{}, "none", unavailableReadiness(), ErrStoreUnavailable
		}
		if selection.RuntimeHostID != s.options.RuntimeHostID || selection.CanonicalPath == "" {
			return discoveredCandidate{}, source, unavailableReadiness(), ErrStoreUnavailable
		}
	}
	candidate, err := inspectInstallation(ctx, installation{selection.Name, selection.Path})
	if err != nil || (selection.CanonicalPath != "" && candidate.canonicalPath != selection.CanonicalPath) {
		return discoveredCandidate{Candidate: Candidate{Name: selection.Name, Path: selection.Path}}, source, unavailableReadiness(), nil
	}
	readiness := Readiness{Available: true, Status: "configured", Message: "Browser enabled. Its first approved use will confirm that it works."}
	if candidate.ID == s.workingID {
		readiness.Status = "working"
		readiness.Message = "Browser worked successfully during this Hecate session. Each call still requires a Work policy and approval."
	}
	return candidate, source, readiness, nil
}

func unavailableReadiness() Readiness {
	return Readiness{Status: "unavailable", Message: "The selected browser installation or its settings are unavailable.", OperatorAction: "Check that the browser is installed and has executable permissions, then select it again. If managed by the environment, check HECATE_TASK_BROWSER_EXECUTABLE and restart Hecate."}
}

func (s *Service) admit(ctx context.Context) (admitted runner, generation uint64, candidateID string, retErr error) {
	ctx, span := browserTracer.Start(ctx, "browser.runtime.admit")
	defer func() { finishSpan(span, retErr, "") }()
	if s == nil {
		return nil, 0, "", browserrunner.ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate, _, readiness, err := s.selectionLocked(ctx)
	if err != nil || !readiness.Available {
		return nil, 0, "", browserrunner.ErrUnavailable
	}
	runtime, err := s.newRunner(browserrunner.Config{ExecutablePath: candidate.Path, Timeout: s.options.Timeout, AllowPrivateIPs: s.options.AllowPrivateIPs})
	if err != nil {
		return nil, 0, "", browserrunner.ErrUnavailable
	}
	return runtime, s.generation, candidate.ID, nil
}

func (s *Service) recordSuccess(generation uint64, candidateID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation == generation {
		s.workingID = candidateID
	}
}

func (s *Service) Inspect(ctx context.Context, request browserrunner.InspectRequest) (result browserrunner.InspectResult, retErr error) {
	ctx, span := browserTracer.Start(ctx, "browser.runtime.inspect")
	defer func() { finishSpan(span, retErr, "") }()
	runtime, generation, candidateID, err := s.admit(ctx)
	if err != nil {
		return browserrunner.InspectResult{}, err
	}
	result, err = runtime.Inspect(ctx, request)
	if err == nil {
		s.recordSuccess(generation, candidateID)
	}
	return result, err
}

func (s *Service) RunFlow(ctx context.Context, request browserrunner.FlowRequest) (result browserrunner.FlowResult, retErr error) {
	ctx, span := browserTracer.Start(ctx, "browser.runtime.flow")
	defer func() { finishSpan(span, retErr, "") }()
	runtime, generation, candidateID, err := s.admit(ctx)
	if err != nil {
		return browserrunner.FlowResult{}, err
	}
	result, err = runtime.RunFlow(ctx, request)
	if err == nil {
		s.recordSuccess(generation, candidateID)
	}
	return result, err
}

var _ browserrunner.Inspector = (*Service)(nil)
var _ browserrunner.FlowRunner = (*Service)(nil)
