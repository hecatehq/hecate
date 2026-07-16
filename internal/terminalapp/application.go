package terminalapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/sandbox"
	"github.com/hecatehq/hecate/internal/workspace"
	"github.com/hecatehq/hecate/internal/workspacecoord"
)

var (
	ErrDisabled      = errors.New("operator terminals are disabled")
	ErrNotFound      = errors.New("terminal not found")
	ErrValidation    = errors.New("terminal validation failed")
	ErrWorkspaceBusy = errors.New("terminal workspace is temporarily unavailable")
	ErrShuttingDown  = errors.New("operator terminals are shutting down")
)

type Options struct {
	Enabled              bool
	OutputMaxBytes       int
	NewWorkspace         func() workspace.Workspace
	WorkspaceCoordinator *workspacecoord.Registry
	Now                  func() time.Time
}

type Application struct {
	enabled              bool
	outputMaxBytes       int
	newWorkspace         func() workspace.Workspace
	workspaceCoordinator *workspacecoord.Registry
	now                  func() time.Time

	lifecycleMu         sync.Mutex
	shuttingDown        bool
	startsInFlight      int
	startsDrained       chan struct{}
	shutdownGate        chan struct{}
	shutdownCleanupOnce sync.Once

	mu       sync.Mutex
	sessions map[string]*session
}

type StartCommand struct {
	Workspace        string
	WorkingDirectory string
	Command          string
	Args             []string
	Env              map[string]string
	OutputByteLimit  int
}

type Snapshot struct {
	ID               string    `json:"id"`
	Workspace        string    `json:"workspace"`
	WorkingDirectory string    `json:"working_directory"`
	Command          string    `json:"command,omitempty"`
	Args             []string  `json:"args,omitempty"`
	Output           string    `json:"output"`
	Truncated        bool      `json:"truncated"`
	Running          bool      `json:"running"`
	ExitCode         *int      `json:"exit_code,omitempty"`
	Error            string    `json:"error,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type session struct {
	id               string
	terminal         workspace.Terminal
	workspace        string
	workingDirectory string
	command          string
	args             []string
	createdAt        time.Time
	workspaceLease   *workspacecoord.WriterLease

	mu                   sync.Mutex
	output               outputBuffer
	exitCode             *int
	waitErr              error
	done                 bool
	doneCh               chan struct{}
	updatedAt            time.Time
	closeGate            chan struct{}
	workspaceReleaseOnce sync.Once
}

type outputBuffer struct {
	limit     int
	buf       []byte
	truncated bool
}

const defaultOutputMaxBytes = 1024 * 1024

func New(opts Options) *Application {
	outputMaxBytes := opts.OutputMaxBytes
	if outputMaxBytes <= 0 {
		outputMaxBytes = defaultOutputMaxBytes
	}
	newWorkspace := opts.NewWorkspace
	if newWorkspace == nil {
		newWorkspace = func() workspace.Workspace { return workspace.NewLocalWorkspace() }
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Application{
		enabled:              opts.Enabled,
		outputMaxBytes:       outputMaxBytes,
		newWorkspace:         newWorkspace,
		workspaceCoordinator: opts.WorkspaceCoordinator,
		now:                  now,
		shutdownGate:         newLifecycleGate(),
		sessions:             make(map[string]*session),
	}
}

func (a *Application) Start(ctx context.Context, cmd StartCommand) (Snapshot, error) {
	if a == nil || !a.enabled {
		return Snapshot{}, ErrDisabled
	}
	if !a.beginStart() {
		return Snapshot{}, ErrShuttingDown
	}
	defer a.finishStart()
	workspaceRoot, err := canonicalWorkspace(cmd.Workspace)
	if err != nil {
		return Snapshot{}, validation(err.Error())
	}
	workingDirectory, err := terminalWorkingDirectory(workspaceRoot, cmd.WorkingDirectory)
	if err != nil {
		return Snapshot{}, validation(err.Error())
	}
	command := strings.TrimSpace(cmd.Command)
	limit := cmd.OutputByteLimit
	if limit <= 0 {
		limit = a.outputMaxBytes
	}
	id, err := newTerminalID()
	if err != nil {
		return Snapshot{}, err
	}
	now := a.now().UTC()
	if a.workspaceCoordinator == nil {
		return Snapshot{}, errors.New("workspace coordination is unavailable")
	}
	workspaceLease, err := a.workspaceCoordinator.AcquireWriter(ctx, workspaceRoot)
	if err != nil {
		if errors.Is(err, workspacecoord.ErrClosed) {
			return Snapshot{}, ErrWorkspaceBusy
		}
		return Snapshot{}, err
	}
	term, err := a.newWorkspace().OpenTerminal(ctx, workspace.TerminalOptions{
		Command:          command,
		Args:             append([]string(nil), cmd.Args...),
		WorkingDirectory: workingDirectory,
		Policy:           workspace.Policy{AllowedRoot: workspaceRoot},
		Env:              cloneEnv(cmd.Env),
	})
	if err != nil {
		workspaceLease.Release()
		if sandbox.IsPolicyDenied(err) {
			return Snapshot{}, validation(err.Error())
		}
		return Snapshot{}, err
	}
	s := &session{
		id:               id,
		terminal:         term,
		workspace:        workspaceRoot,
		workingDirectory: workingDirectory,
		command:          command,
		args:             append([]string(nil), cmd.Args...),
		createdAt:        now,
		workspaceLease:   workspaceLease,
		updatedAt:        now,
		output:           outputBuffer{limit: limit},
		doneCh:           make(chan struct{}),
		closeGate:        newLifecycleGate(),
	}
	a.mu.Lock()
	a.sessions[id] = s
	a.mu.Unlock()
	go s.watch()
	return s.snapshot(), nil
}

func (a *Application) Output(id string) (Snapshot, error) {
	s, err := a.get(id)
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(), nil
}

func (a *Application) Write(ctx context.Context, id, input string) (Snapshot, error) {
	s, err := a.get(id)
	if err != nil {
		return Snapshot{}, err
	}
	if err := s.terminal.Write(ctx, input); err != nil {
		if sandbox.IsPolicyDenied(err) {
			return Snapshot{}, validation(err.Error())
		}
		return Snapshot{}, err
	}
	return s.snapshot(), nil
}

func (a *Application) Kill(ctx context.Context, id string) (Snapshot, error) {
	s, err := a.get(id)
	if err != nil {
		return Snapshot{}, err
	}
	if err := s.terminal.Kill(ctx); err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(), nil
}

func (a *Application) Wait(ctx context.Context, id string) (Snapshot, error) {
	s, err := a.get(id)
	if err != nil {
		return Snapshot{}, err
	}
	select {
	case <-s.doneCh:
	case <-ctx.Done():
		return s.snapshot(), ctx.Err()
	}
	return s.snapshot(), s.waitError()
}

func (a *Application) Release(ctx context.Context, id string) error {
	s, err := a.get(id)
	if err != nil {
		return err
	}
	if err := s.close(ctx); err != nil {
		return err
	}
	a.deleteSession(id, s)
	return nil
}

func (a *Application) Shutdown(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	startsDrained := a.fenceStarts()
	if err := ctx.Err(); err != nil {
		a.forceCloseSessions()
		a.continueShutdown()
		return err
	}
	select {
	case <-ctx.Done():
		a.forceCloseSessions()
		a.continueShutdown()
		return ctx.Err()
	case <-a.shutdownGate:
	}
	defer func() { a.shutdownGate <- struct{}{} }()

	if startsDrained != nil {
		select {
		case <-startsDrained:
		case <-ctx.Done():
			a.forceCloseSessions()
			a.continueShutdown()
			return ctx.Err()
		}
	}
	sessions := a.snapshotSessions()
	var errs []error
	for _, s := range sessions {
		if err := s.close(ctx); err != nil {
			s.forceClose()
			errs = append(errs, err)
			continue
		}
		a.deleteSession(s.id, s)
	}
	shutdownErr := errors.Join(errs...)
	if shutdownErr != nil {
		a.continueShutdown()
	}
	return shutdownErr
}

func (a *Application) fenceStarts() <-chan struct{} {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	a.shuttingDown = true
	return a.startsDrained
}

func (a *Application) snapshotSessions() []*session {
	a.mu.Lock()
	defer a.mu.Unlock()
	sessions := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}

func (a *Application) forceCloseSessions() {
	for _, s := range a.snapshotSessions() {
		s.forceClose()
	}
}

// continueShutdown retains a cleanup owner after a caller's budget expires.
// Session watchers remain the sole workspace-lease owners and release only
// after process exit and output drain have both been observed.
func (a *Application) continueShutdown() {
	a.shutdownCleanupOnce.Do(func() {
		go func() {
			_ = a.Shutdown(context.Background())
		}()
	})
}

func (a *Application) beginStart() bool {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.shuttingDown {
		return false
	}
	if a.startsInFlight == 0 {
		a.startsDrained = make(chan struct{})
	}
	a.startsInFlight++
	return true
}

func (a *Application) finishStart() {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	a.startsInFlight--
	if a.startsInFlight == 0 {
		close(a.startsDrained)
		a.startsDrained = nil
	}
}

func (a *Application) get(id string) (*session, error) {
	if a == nil || !a.enabled {
		return nil, ErrDisabled
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, validation("terminal id is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	return s, nil
}

func (a *Application) deleteSession(id string, expected *session) {
	if a == nil || expected == nil {
		return
	}
	id = strings.TrimSpace(id)
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[id]
	if ok && s == expected {
		delete(a.sessions, id)
	}
}

func (s *session) watch() {
	for chunk := range s.terminal.Output() {
		s.mu.Lock()
		s.output.append(chunk.Text)
		s.updatedAt = time.Now().UTC()
		s.mu.Unlock()
	}
	result, err := s.terminal.WaitForExit(context.Background())
	s.mu.Lock()
	exitCode := result.ExitCode
	s.exitCode = &exitCode
	s.waitErr = err
	s.done = true
	s.updatedAt = time.Now().UTC()
	s.mu.Unlock()
	s.releaseWorkspace()
	close(s.doneCh)
}

func (s *session) close(ctx context.Context) error {
	select {
	case <-s.doneCh:
		return nil
	default:
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		s.forceClose()
		return err
	}
	select {
	case <-ctx.Done():
		s.forceClose()
		return ctx.Err()
	case <-s.closeGate:
	}
	defer func() { s.closeGate <- struct{}{} }()
	if err := s.terminal.Close(ctx); err != nil {
		return err
	}
	// Close success means the terminal implementation stopped and reclaimed
	// the process, but the watcher remains the sole lease authority. Waiting
	// for it prevents a buggy or delayed Close path from opening an exclusive
	// mutation window before process exit has actually been observed.
	select {
	case <-s.doneCh:
		return nil
	default:
	}
	select {
	case <-s.doneCh:
		return nil
	case <-ctx.Done():
		s.forceClose()
		return ctx.Err()
	}
}

func (s *session) forceClose() {
	if s == nil || s.terminal == nil {
		return
	}
	forceCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = s.terminal.Kill(forceCtx)
	_ = s.terminal.Close(forceCtx)
}

func newLifecycleGate() chan struct{} {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return gate
}

func (s *session) releaseWorkspace() {
	if s == nil {
		return
	}
	s.workspaceReleaseOnce.Do(func() {
		s.workspaceLease.Release()
	})
}

func (s *session) snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	output, truncated := s.output.snapshot()
	var exitCode *int
	if s.exitCode != nil {
		value := *s.exitCode
		exitCode = &value
	}
	errMsg := ""
	if s.waitErr != nil {
		errMsg = s.waitErr.Error()
	}
	return Snapshot{
		ID:               s.id,
		Workspace:        s.workspace,
		WorkingDirectory: s.workingDirectory,
		Command:          s.command,
		Args:             append([]string(nil), s.args...),
		Output:           output,
		Truncated:        truncated,
		Running:          !s.done,
		ExitCode:         exitCode,
		Error:            errMsg,
		CreatedAt:        s.createdAt,
		UpdatedAt:        s.updatedAt,
	}
}

func (s *session) waitError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waitErr
}

func (b *outputBuffer) append(text string) {
	if text == "" {
		return
	}
	b.buf = append(b.buf, text...)
	if b.limit <= 0 || len(b.buf) <= b.limit {
		return
	}
	b.truncated = true
	trimmed := b.buf[len(b.buf)-b.limit:]
	for len(trimmed) > 0 && !utf8.Valid(trimmed) {
		trimmed = trimmed[1:]
	}
	b.buf = append(b.buf[:0], trimmed...)
}

func (b *outputBuffer) snapshot() (string, bool) {
	return string(b.buf), b.truncated
}

func canonicalWorkspace(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("workspace is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace is not a directory")
	}
	return resolved, nil
}

func terminalWorkingDirectory(workspaceRoot, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = workspaceRoot
	} else if !filepath.IsAbs(requested) {
		requested = filepath.Join(workspaceRoot, requested)
	}
	return sandbox.ResolveWorkingDirectory(requested, sandbox.Policy{AllowedRoot: workspaceRoot})
}

func cloneEnv(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		dst[key] = v
	}
	return dst
}

func newTerminalID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "term_" + hex.EncodeToString(raw[:]), nil
}

func validation(message string) error {
	return fmt.Errorf("%w: %s", ErrValidation, message)
}
