// Package workspacecoord coordinates process-local access to canonical
// workspaces. It lets ordinary runtime writers proceed concurrently while a
// destructive mutation can acquire a short, exclusive closure.
package workspacecoord

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var (
	// ErrBusy means a destructive mutation could not acquire the workspace
	// because one or more admitted writers are still active.
	ErrBusy = errors.New("workspace has active writers")
	// ErrClosed means an exclusive destructive mutation currently owns the
	// workspace, so another writer or closure cannot be admitted.
	ErrClosed = errors.New("workspace is closed for destructive mutation")
)

// BusyError identifies the canonical workspace and active writer count that
// prevented an exclusive closure. Its public message stays generic so callers
// can safely map it to a conflict without exposing a local path.
type BusyError struct {
	Workspace     string
	ActiveWriters int
}

func (err *BusyError) Error() string { return ErrBusy.Error() }

func (err *BusyError) Unwrap() error { return ErrBusy }

// ClosedError identifies the canonical workspace already owned by an
// exclusive destructive mutation. Its public message deliberately omits the
// local path.
type ClosedError struct {
	Workspace string
}

func (err *ClosedError) Error() string { return ErrClosed.Error() }

func (err *ClosedError) Unwrap() error { return ErrClosed }

// CanonicalWorkspace returns the stable process-local coordination key for a
// workspace path. Resolving symlinks prevents aliases of the same directory
// from entering independent coordination domains.
func CanonicalWorkspace(workspacePath string) (string, error) {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return "", errors.New("workspace path is required")
	}
	absPath, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", fmt.Errorf("resolve absolute workspace path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("resolve workspace symlinks: %w", err)
	}
	return filepath.Clean(canonical), nil
}

// CanonicalWorkspaceForCreation returns the stable coordination key for a
// workspace that may not exist yet, without creating any filesystem entries.
// It resolves the nearest existing ancestor (including symlink aliases), then
// appends the missing path suffix. This lets provisioning acquire admission
// before mkdir, clone, or copy mutates the destination.
func CanonicalWorkspaceForCreation(workspacePath string) (string, error) {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return "", errors.New("workspace path is required")
	}
	absPath, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", fmt.Errorf("resolve absolute workspace path: %w", err)
	}

	candidate := filepath.Clean(absPath)
	missing := make([]string, 0, 4)
	for {
		_, statErr := os.Lstat(candidate)
		if statErr == nil {
			canonical, resolveErr := filepath.EvalSymlinks(candidate)
			if resolveErr != nil {
				return "", fmt.Errorf("resolve workspace ancestor symlinks: %w", resolveErr)
			}
			resolvedInfo, resolvedErr := os.Stat(canonical)
			if resolvedErr != nil {
				return "", fmt.Errorf("inspect workspace ancestor: %w", resolvedErr)
			}
			if !resolvedInfo.IsDir() {
				return "", fmt.Errorf("workspace ancestor %q is not a directory", canonical)
			}
			for index := len(missing) - 1; index >= 0; index-- {
				canonical = filepath.Join(canonical, missing[index])
			}
			return filepath.Clean(canonical), nil
		}
		if !os.IsNotExist(statErr) {
			return "", fmt.Errorf("inspect workspace ancestor: %w", statErr)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", fmt.Errorf("resolve existing workspace ancestor for %q", absPath)
		}
		missing = append(missing, filepath.Base(candidate))
		candidate = parent
	}
}

// WorkspacePathsOverlap reports whether either canonical workspace contains
// the other. A writer rooted at a parent can mutate every descendant, so an
// exclusive closure must coordinate both directions.
func WorkspacePathsOverlap(first, second string) (bool, error) {
	firstKey, err := CanonicalWorkspace(first)
	if err != nil {
		return false, err
	}
	secondKey, err := CanonicalWorkspace(second)
	if err != nil {
		return false, err
	}
	return CanonicalKeysOverlap(firstKey, secondKey), nil
}

// CanonicalKeysOverlap compares already-canonical absolute workspace keys.
// Sibling paths remain independent; equality and ancestor/descendant paths
// overlap.
func CanonicalKeysOverlap(firstKey, secondKey string) bool {
	firstKey = filepath.Clean(strings.TrimSpace(firstKey))
	secondKey = filepath.Clean(strings.TrimSpace(secondKey))
	if firstKey == "." || secondKey == "." || !filepath.IsAbs(firstKey) || !filepath.IsAbs(secondKey) {
		return false
	}
	if keysNeedCaseFold(firstKey, secondKey) {
		return pathContainsFold(firstKey, secondKey) || pathContainsFold(secondKey, firstKey)
	}
	return pathContains(firstKey, secondKey) || pathContains(secondKey, firstKey)
}

func keysNeedCaseFold(_, _ string) bool {
	// Darwin supports both case-sensitive and case-insensitive volumes, but a
	// false conflict is safer than allowing a destructive mutation to overlap
	// an active writer. Keeping this decision filesystem-independent also makes
	// future, unreadable, and volume-root paths fail closed.
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
}

func pathContainsFold(root, candidate string) bool {
	rootVolume, rootParts := foldedPathParts(root)
	candidateVolume, candidateParts := foldedPathParts(candidate)
	if !strings.EqualFold(rootVolume, candidateVolume) || len(candidateParts) < len(rootParts) {
		return false
	}
	for index := range rootParts {
		if !strings.EqualFold(rootParts[index], candidateParts[index]) {
			return false
		}
	}
	return true
}

func foldedPathParts(path string) (string, []string) {
	volume := filepath.VolumeName(path)
	remainder := strings.TrimPrefix(path, volume)
	parts := strings.FieldsFunc(remainder, func(char rune) bool {
		return char == '/' || (runtime.GOOS == "windows" && char == '\\')
	})
	return volume, parts
}

func pathContains(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

type workspaceState struct {
	writers   int
	exclusive bool
}

// Registry coordinates all workspace writers and destructive closures in one
// process. Callers must share one Registry instance across every writer and
// destructive mutation that can touch the same workspace.
type Registry struct {
	mu      sync.Mutex
	states  map[string]*workspaceState
	changed chan struct{}
}

func NewRegistry() *Registry {
	return &Registry{
		states:  make(map[string]*workspaceState),
		changed: make(chan struct{}),
	}
}

// WriterLease holds one admitted runtime writer. Concurrent writers are
// allowed; destructive closures are rejected until every writer releases.
type WriterLease struct {
	registry *Registry
	key      string
	state    *workspaceState
	once     sync.Once
}

func (lease *WriterLease) Release() {
	if lease == nil {
		return
	}
	lease.once.Do(func() {
		if lease.registry != nil {
			lease.registry.releaseWriter(lease.key, lease.state)
		}
	})
}

// Workspace returns the canonical workspace key owned by the lease.
func (lease *WriterLease) Workspace() string {
	if lease == nil {
		return ""
	}
	return lease.key
}

// AcquireWriter admits a workspace-backed runtime writer unless an exclusive
// destructive closure currently owns the workspace.
func (registry *Registry) AcquireWriter(ctx context.Context, workspacePath string) (*WriterLease, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	key, err := CanonicalWorkspace(workspacePath)
	if err != nil {
		return nil, err
	}
	if registry == nil {
		return nil, errors.New("workspace coordination registry is not configured")
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	registry.ensureLocked()
	for existingKey, state := range registry.states {
		if state.exclusive && CanonicalKeysOverlap(key, existingKey) {
			return nil, &ClosedError{Workspace: existingKey}
		}
	}
	state := registry.stateLocked(key)
	state.writers++
	return &WriterLease{registry: registry, key: key, state: state}, nil
}

// WaitWriter admits a workspace-backed runtime writer, waiting for an active
// exclusive closure to release. Runtime queue workers use this path so a short
// operator mutation delays an already-accepted run instead of failing it.
func (registry *Registry) WaitWriter(ctx context.Context, workspacePath string) (*WriterLease, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	key, err := CanonicalWorkspace(workspacePath)
	if err != nil {
		return nil, err
	}
	if registry == nil {
		return nil, errors.New("workspace coordination registry is not configured")
	}
	return registry.waitWriterKey(ctx, key)
}

// WaitWriterForCreation admits a writer for a workspace that may not exist
// yet. The key is derived without mutating the filesystem, so callers can hold
// the lease across every provisioning write. As with WaitWriter, an active
// overlapping destructive closure delays admission until release or context
// cancellation.
func (registry *Registry) WaitWriterForCreation(ctx context.Context, workspacePath string) (*WriterLease, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	key, err := CanonicalWorkspaceForCreation(workspacePath)
	if err != nil {
		return nil, err
	}
	if registry == nil {
		return nil, errors.New("workspace coordination registry is not configured")
	}
	return registry.waitWriterKey(ctx, key)
}

func (registry *Registry) waitWriterKey(ctx context.Context, key string) (*WriterLease, error) {
	for {
		registry.mu.Lock()
		if err := contextErr(ctx); err != nil {
			registry.mu.Unlock()
			return nil, err
		}
		registry.ensureLocked()
		blocked := false
		for existingKey, state := range registry.states {
			if state.exclusive && CanonicalKeysOverlap(key, existingKey) {
				blocked = true
				break
			}
		}
		if !blocked {
			state := registry.stateLocked(key)
			state.writers++
			registry.mu.Unlock()
			return &WriterLease{registry: registry, key: key, state: state}, nil
		}
		changed := registry.changed
		registry.mu.Unlock()

		select {
		case <-contextDone(ctx):
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (registry *Registry) releaseWriter(key string, expected *workspaceState) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state := registry.states[key]
	if state == nil || state != expected || state.writers == 0 {
		return
	}
	state.writers--
	registry.pruneLocked(key, state)
}

// ExclusiveLease keeps a workspace closed to new writers while its owner
// performs one destructive mutation.
type ExclusiveLease struct {
	registry *Registry
	key      string
	state    *workspaceState
	once     sync.Once
}

func (lease *ExclusiveLease) Release() {
	if lease == nil {
		return
	}
	lease.once.Do(func() {
		if lease.registry != nil {
			lease.registry.releaseExclusive(lease.key, lease.state)
		}
	})
}

// Workspace returns the canonical workspace key owned by the lease.
func (lease *ExclusiveLease) Workspace() string {
	if lease == nil {
		return ""
	}
	return lease.key
}

// TryClose acquires an exclusive destructive closure without waiting. It
// returns BusyError when writers are active and ClosedError when another
// closure already owns the workspace. Once acquired, new writers are rejected
// until the returned lease is released.
func (registry *Registry) TryClose(ctx context.Context, workspacePath string) (*ExclusiveLease, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	key, err := CanonicalWorkspace(workspacePath)
	if err != nil {
		return nil, err
	}
	if registry == nil {
		return nil, errors.New("workspace coordination registry is not configured")
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	registry.ensureLocked()
	activeWriters := 0
	for existingKey, state := range registry.states {
		if !CanonicalKeysOverlap(key, existingKey) {
			continue
		}
		if state.exclusive {
			return nil, &ClosedError{Workspace: existingKey}
		}
		activeWriters += state.writers
	}
	if activeWriters > 0 {
		return nil, &BusyError{Workspace: key, ActiveWriters: activeWriters}
	}
	state := registry.stateLocked(key)
	state.exclusive = true
	return &ExclusiveLease{registry: registry, key: key, state: state}, nil
}

func (registry *Registry) releaseExclusive(key string, expected *workspaceState) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state := registry.states[key]
	if state == nil || state != expected || !state.exclusive {
		return
	}
	state.exclusive = false
	registry.notifyLocked()
	registry.pruneLocked(key, state)
}

func (registry *Registry) stateLocked(key string) *workspaceState {
	registry.ensureLocked()
	state := registry.states[key]
	if state == nil {
		state = &workspaceState{}
		registry.states[key] = state
	}
	return state
}

func (registry *Registry) ensureLocked() {
	if registry.states == nil {
		registry.states = make(map[string]*workspaceState)
	}
	if registry.changed == nil {
		registry.changed = make(chan struct{})
	}
}

func (registry *Registry) notifyLocked() {
	close(registry.changed)
	registry.changed = make(chan struct{})
}

func (registry *Registry) pruneLocked(key string, state *workspaceState) {
	if state.writers == 0 && !state.exclusive {
		delete(registry.states, key)
	}
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func contextDone(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}
