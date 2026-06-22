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
)

var (
	ErrDisabled   = errors.New("operator terminals are disabled")
	ErrNotFound   = errors.New("terminal not found")
	ErrValidation = errors.New("terminal validation failed")
)

type Options struct {
	Enabled        bool
	OutputMaxBytes int
	NewWorkspace   func() workspace.Workspace
	Now            func() time.Time
}

type Application struct {
	enabled        bool
	outputMaxBytes int
	newWorkspace   func() workspace.Workspace
	now            func() time.Time

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

	mu        sync.Mutex
	output    outputBuffer
	exitCode  *int
	waitErr   error
	done      bool
	doneCh    chan struct{}
	updatedAt time.Time
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
		enabled:        opts.Enabled,
		outputMaxBytes: outputMaxBytes,
		newWorkspace:   newWorkspace,
		now:            now,
		sessions:       make(map[string]*session),
	}
}

func (a *Application) Start(ctx context.Context, cmd StartCommand) (Snapshot, error) {
	if a == nil || !a.enabled {
		return Snapshot{}, ErrDisabled
	}
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
	term, err := a.newWorkspace().OpenTerminal(ctx, workspace.TerminalOptions{
		Command:          command,
		Args:             append([]string(nil), cmd.Args...),
		WorkingDirectory: workingDirectory,
		Policy:           workspace.Policy{AllowedRoot: workspaceRoot},
		Env:              cloneEnv(cmd.Env),
	})
	if err != nil {
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
		updatedAt:        now,
		output:           outputBuffer{limit: limit},
		doneCh:           make(chan struct{}),
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
	s, err := a.remove(id)
	if err != nil {
		return err
	}
	return s.terminal.Close(ctx)
}

func (a *Application) Shutdown(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	sessions := make([]*session, 0, len(a.sessions))
	for id, s := range a.sessions {
		sessions = append(sessions, s)
		delete(a.sessions, id)
	}
	a.mu.Unlock()
	var errs []error
	for _, s := range sessions {
		if err := s.terminal.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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

func (a *Application) remove(id string) (*session, error) {
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
	delete(a.sessions, id)
	return s, nil
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
	close(s.doneCh)
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
