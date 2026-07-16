package workspacecoord

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistry_CanonicalAliasesShareOneCoordinationDomain(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realWorkspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(realWorkspace, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	alias := filepath.Join(root, "workspace-link")
	if err := os.Symlink(realWorkspace, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	registry := NewRegistry()
	writer, err := registry.AcquireWriter(t.Context(), realWorkspace)
	if err != nil {
		t.Fatalf("AcquireWriter(real) error = %v", err)
	}
	if _, err := registry.TryClose(t.Context(), alias); !errors.Is(err, ErrBusy) {
		t.Fatalf("TryClose(alias) error = %v, want ErrBusy", err)
	} else {
		var busy *BusyError
		if !errors.As(err, &busy) || busy.ActiveWriters != 1 || busy.Workspace != writer.Workspace() {
			t.Fatalf("TryClose(alias) error = %#v, want canonical BusyError with one writer", err)
		}
	}

	writer.Release()
	closure, err := registry.TryClose(t.Context(), alias)
	if err != nil {
		t.Fatalf("TryClose(alias) after release error = %v", err)
	}
	if closure.Workspace() != writer.Workspace() {
		t.Fatalf("canonical keys differ: closure=%q writer=%q", closure.Workspace(), writer.Workspace())
	}
	if _, err := registry.AcquireWriter(t.Context(), realWorkspace); !errors.Is(err, ErrClosed) {
		t.Fatalf("AcquireWriter(real) while alias closed error = %v, want ErrClosed", err)
	} else {
		var closed *ClosedError
		if !errors.As(err, &closed) || closed.Workspace != closure.Workspace() {
			t.Fatalf("AcquireWriter(real) error = %#v, want canonical ClosedError", err)
		}
	}
	closure.Release()

	registry.mu.Lock()
	stateCount := len(registry.states)
	registry.mu.Unlock()
	if stateCount != 0 {
		t.Fatalf("registry state count after releases = %d, want 0", stateCount)
	}
}

func TestRegistry_CaseInsensitiveAliasesShareExistingAndFutureCoordinationDomains(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("case-insensitive coordination aliases are platform-specific")
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "MixedCaseWorkspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatalf("Mkdir(workspace): %v", err)
	}
	alias := filepath.Join(root, "mixedcaseworkspace")
	workspaceInfo, err := os.Stat(workspace)
	if err != nil {
		t.Fatalf("Stat(workspace): %v", err)
	}
	aliasInfo, err := os.Stat(alias)
	if err != nil || !os.SameFile(workspaceInfo, aliasInfo) {
		t.Skip("test volume is case-sensitive")
	}

	workspaceKey, err := CanonicalWorkspace(workspace)
	if err != nil {
		t.Fatalf("CanonicalWorkspace(workspace): %v", err)
	}
	aliasKey, err := CanonicalWorkspace(alias)
	if err != nil {
		t.Fatalf("CanonicalWorkspace(alias): %v", err)
	}
	if !CanonicalKeysOverlap(workspaceKey, aliasKey) {
		t.Fatalf("case aliases do not overlap: %q and %q", workspaceKey, aliasKey)
	}

	registry := NewRegistry()
	writer, err := registry.AcquireWriter(t.Context(), workspace)
	if err != nil {
		t.Fatalf("AcquireWriter(workspace): %v", err)
	}
	if _, err := registry.TryClose(t.Context(), alias); !errors.Is(err, ErrBusy) {
		t.Fatalf("TryClose(case alias) error = %v, want ErrBusy", err)
	}
	writer.Release()

	firstFuture := filepath.Join(workspace, "FuturePath", "Run")
	secondFuture := filepath.Join(alias, "futurepath", "run")
	firstFutureKey, err := CanonicalWorkspaceForCreation(firstFuture)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceForCreation(first): %v", err)
	}
	secondFutureKey, err := CanonicalWorkspaceForCreation(secondFuture)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceForCreation(second): %v", err)
	}
	if !CanonicalKeysOverlap(firstFutureKey, secondFutureKey) {
		t.Fatalf("case-aliased future paths do not overlap: %q and %q", firstFutureKey, secondFutureKey)
	}

	closure, err := registry.TryClose(t.Context(), alias)
	if err != nil {
		t.Fatalf("TryClose(case alias): %v", err)
	}
	defer closure.Release()
	waitCtx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := registry.WaitWriterForCreation(waitCtx, firstFuture); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitWriterForCreation(case-aliased future) error = %v, want deadline", err)
	}
}

func TestPathContainsFold(t *testing.T) {
	for _, tc := range []struct {
		name      string
		root      string
		candidate string
		want      bool
	}{
		{name: "equal", root: "/Users/Operator/Repo", candidate: "/users/operator/repo", want: true},
		{name: "child", root: "/Users/Operator/Repo", candidate: "/users/operator/repo/Child", want: true},
		{name: "lookalike sibling", root: "/Users/Operator/Repo", candidate: "/users/operator/repository", want: false},
		{name: "sibling", root: "/Users/Operator/Repo", candidate: "/users/operator/Other", want: false},
		{name: "unicode low slash byte", root: "/Users/Operator/įtem/Repo", candidate: "/users/operator/įtem/repo/Child", want: true},
		{name: "unicode low backslash byte", root: "/Users/Operator/Ŝtem/Repo", candidate: "/users/operator/Ŝtem/repo/Child", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathContainsFold(filepath.FromSlash(tc.root), filepath.FromSlash(tc.candidate)); got != tc.want {
				t.Fatalf("pathContainsFold(%q, %q) = %v, want %v", tc.root, tc.candidate, got, tc.want)
			}
		})
	}
	if runtime.GOOS != "windows" {
		root := filepath.FromSlash(`/Users/Operator/Repo\Part`)
		candidate := filepath.FromSlash(`/users/operator/repo/part/Child`)
		if pathContainsFold(root, candidate) {
			t.Fatalf("pathContainsFold treated a legal backslash filename byte as a separator")
		}
	}
}

func TestRegistry_AllowsSharedWritersAndCleansReleasedState(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewRegistry()
	first, err := registry.AcquireWriter(t.Context(), workspace)
	if err != nil {
		t.Fatalf("AcquireWriter(first) error = %v", err)
	}
	second, err := registry.AcquireWriter(t.Context(), workspace)
	if err != nil {
		t.Fatalf("AcquireWriter(second) error = %v", err)
	}
	if _, err := registry.TryClose(t.Context(), workspace); !errors.Is(err, ErrBusy) {
		t.Fatalf("TryClose() with two writers error = %v, want ErrBusy", err)
	} else {
		var busy *BusyError
		if !errors.As(err, &busy) || busy.ActiveWriters != 2 {
			t.Fatalf("TryClose() error = %#v, want two active writers", err)
		}
	}

	first.Release()
	first.Release()
	if _, err := registry.TryClose(t.Context(), workspace); !errors.Is(err, ErrBusy) {
		t.Fatalf("TryClose() after one writer release error = %v, want ErrBusy", err)
	} else {
		var busy *BusyError
		if !errors.As(err, &busy) || busy.ActiveWriters != 1 {
			t.Fatalf("TryClose() error = %#v, want one active writer", err)
		}
	}
	second.Release()
	second.Release()

	closure, err := registry.TryClose(t.Context(), workspace)
	if err != nil {
		t.Fatalf("TryClose() after writers release error = %v", err)
	}
	closure.Release()
	closure.Release()

	registry.mu.Lock()
	stateCount := len(registry.states)
	registry.mu.Unlock()
	if stateCount != 0 {
		t.Fatalf("registry state count after idempotent releases = %d, want 0", stateCount)
	}
}

func TestRegistry_ParentAndChildWorkspacesConflictWhileSiblingsRemainIndependent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parent := filepath.Join(root, "repo")
	child := filepath.Join(parent, "packages", "app")
	sibling := filepath.Join(root, "other-repo")
	for _, path := range []string{child, sibling} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
	}
	for _, tc := range []struct {
		name        string
		first       string
		second      string
		wantOverlap bool
	}{
		{name: "parent_child", first: parent, second: child, wantOverlap: true},
		{name: "child_parent", first: child, second: parent, wantOverlap: true},
		{name: "equal", first: child, second: child, wantOverlap: true},
		{name: "siblings", first: child, second: sibling, wantOverlap: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			overlap, err := WorkspacePathsOverlap(tc.first, tc.second)
			if err != nil || overlap != tc.wantOverlap {
				t.Fatalf("WorkspacePathsOverlap() = %v, %v; want %v, nil", overlap, err, tc.wantOverlap)
			}
			firstKey, err := CanonicalWorkspace(tc.first)
			if err != nil {
				t.Fatalf("CanonicalWorkspace(first) error = %v", err)
			}
			secondKey, err := CanonicalWorkspace(tc.second)
			if err != nil {
				t.Fatalf("CanonicalWorkspace(second) error = %v", err)
			}
			if got := CanonicalKeysOverlap(firstKey, secondKey); got != tc.wantOverlap {
				t.Fatalf("CanonicalKeysOverlap() = %v, want %v", got, tc.wantOverlap)
			}
		})
	}

	registry := NewRegistry()
	parentWriter, err := registry.AcquireWriter(t.Context(), parent)
	if err != nil {
		t.Fatalf("AcquireWriter(parent) error = %v", err)
	}
	childWriter, err := registry.AcquireWriter(t.Context(), child)
	if err != nil {
		t.Fatalf("AcquireWriter(child) error = %v", err)
	}
	if _, err := registry.TryClose(t.Context(), child); !errors.Is(err, ErrBusy) {
		t.Fatalf("TryClose(child) with parent and child writers error = %v, want ErrBusy", err)
	} else {
		var busy *BusyError
		if !errors.As(err, &busy) || busy.ActiveWriters != 2 {
			t.Fatalf("TryClose(child) error = %#v, want two overlapping writers", err)
		}
	}
	parentWriter.Release()
	childWriter.Release()

	childClosure, err := registry.TryClose(t.Context(), child)
	if err != nil {
		t.Fatalf("TryClose(child) error = %v", err)
	}
	if _, err := registry.AcquireWriter(t.Context(), parent); !errors.Is(err, ErrClosed) {
		t.Fatalf("AcquireWriter(parent) while child closed error = %v, want ErrClosed", err)
	}
	if _, err := registry.TryClose(t.Context(), parent); !errors.Is(err, ErrClosed) {
		t.Fatalf("TryClose(parent) while child closed error = %v, want ErrClosed", err)
	}
	siblingWriter, err := registry.AcquireWriter(t.Context(), sibling)
	if err != nil {
		t.Fatalf("AcquireWriter(sibling) while child closed error = %v", err)
	}
	childClosure.Release()
	parentClosure, err := registry.TryClose(t.Context(), parent)
	if err != nil {
		t.Fatalf("TryClose(parent) with sibling writer error = %v", err)
	}
	parentClosure.Release()
	siblingWriter.Release()
}

func TestRegistry_ConcurrentWritersAndExclusiveClosuresNeverOverlap(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewRegistry()
	var activeWriters atomic.Int64
	var activeExclusive atomic.Int64
	var wg sync.WaitGroup
	errorsCh := make(chan error, 1)
	recordError := func(err error) {
		select {
		case errorsCh <- err:
		default:
		}
	}

	const goroutines = 24
	const iterations = 400
	start := make(chan struct{})
	for worker := 0; worker < goroutines; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for iteration := 0; iteration < iterations; iteration++ {
				if (worker+iteration)%5 == 0 {
					closure, err := registry.TryClose(t.Context(), workspace)
					if errors.Is(err, ErrBusy) || errors.Is(err, ErrClosed) {
						runtime.Gosched()
						continue
					}
					if err != nil {
						recordError(err)
						return
					}
					if activeExclusive.Add(1) != 1 {
						recordError(errors.New("overlapping exclusive leases"))
					}
					if activeWriters.Load() != 0 {
						recordError(errors.New("exclusive lease overlapped writer lease"))
					}
					runtime.Gosched()
					activeExclusive.Add(-1)
					closure.Release()
					continue
				}

				writerLease, err := registry.AcquireWriter(t.Context(), workspace)
				if errors.Is(err, ErrClosed) {
					runtime.Gosched()
					continue
				}
				if err != nil {
					recordError(err)
					return
				}
				activeWriters.Add(1)
				if activeExclusive.Load() != 0 {
					recordError(errors.New("writer lease overlapped exclusive lease"))
				}
				runtime.Gosched()
				activeWriters.Add(-1)
				writerLease.Release()
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}

	registry.mu.Lock()
	stateCount := len(registry.states)
	registry.mu.Unlock()
	if stateCount != 0 {
		t.Fatalf("registry state count after concurrent releases = %d, want 0", stateCount)
	}
}

func TestCanonicalWorkspace_RejectsMissingAndBlankPaths(t *testing.T) {
	t.Parallel()

	if _, err := CanonicalWorkspace("   "); err == nil {
		t.Fatal("CanonicalWorkspace(blank) error = nil, want error")
	}
	if _, err := CanonicalWorkspace(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("CanonicalWorkspace(missing) error = nil, want error")
	}
}

func TestCanonicalWorkspaceForCreation_ResolvesNearestExistingAliasWithoutCreating(t *testing.T) {
	t.Parallel()

	realRoot := t.TempDir()
	aliasParent := t.TempDir()
	alias := filepath.Join(aliasParent, "workspace-root")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	target := filepath.Join(alias, "task", "run")
	key, err := CanonicalWorkspaceForCreation(target)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceForCreation() error = %v", err)
	}
	canonicalRoot, err := CanonicalWorkspace(realRoot)
	if err != nil {
		t.Fatalf("CanonicalWorkspace(real root) error = %v", err)
	}
	want := filepath.Join(canonicalRoot, "task", "run")
	if key != want {
		t.Fatalf("CanonicalWorkspaceForCreation() = %q, want %q", key, want)
	}
	if _, err := os.Stat(filepath.Join(realRoot, "task")); !os.IsNotExist(err) {
		t.Fatalf("planning created filesystem entries: %v", err)
	}
}

func TestCanonicalWorkspaceForCreation_RejectsUnsafeExistingAncestor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fileAncestor := filepath.Join(root, "file")
	if err := os.WriteFile(fileAncestor, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile(file ancestor) error = %v", err)
	}
	if _, err := CanonicalWorkspaceForCreation(filepath.Join(fileAncestor, "task", "run")); err == nil {
		t.Fatal("CanonicalWorkspaceForCreation(file ancestor) error = nil, want error")
	}
	if _, err := CanonicalWorkspaceForCreation(fileAncestor); err == nil {
		t.Fatal("CanonicalWorkspaceForCreation(existing file) error = nil, want error")
	}

	danglingAlias := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing-target"), danglingAlias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := CanonicalWorkspaceForCreation(filepath.Join(danglingAlias, "task", "run")); err == nil {
		t.Fatal("CanonicalWorkspaceForCreation(dangling alias) error = nil, want error")
	}
}

func TestRegistry_WaitWriterForCreationCoordinatesMissingDescendantPrecisely(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	closedWorkspace := filepath.Join(root, "chat-workspace")
	if err := os.Mkdir(closedWorkspace, 0o755); err != nil {
		t.Fatalf("Mkdir(closed workspace) error = %v", err)
	}
	registry := NewRegistry()
	closure, err := registry.TryClose(t.Context(), closedWorkspace)
	if err != nil {
		t.Fatalf("TryClose() error = %v", err)
	}
	defer closure.Release()

	overlappingTarget := filepath.Join(closedWorkspace, "generated", "task", "run")
	waitCtx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := registry.WaitWriterForCreation(waitCtx, overlappingTarget); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitWriterForCreation(overlapping) error = %v, want context deadline", err)
	}
	if _, err := os.Stat(filepath.Join(closedWorkspace, "generated")); !os.IsNotExist(err) {
		t.Fatalf("writer admission created missing workspace entries: %v", err)
	}

	canonicalRoot, err := CanonicalWorkspace(root)
	if err != nil {
		t.Fatalf("CanonicalWorkspace(root) error = %v", err)
	}
	siblingTarget := filepath.Join(canonicalRoot, "task-workspaces", "task", "run")
	siblingWriter, err := registry.WaitWriterForCreation(t.Context(), siblingTarget)
	if err != nil {
		t.Fatalf("WaitWriterForCreation(sibling) error = %v", err)
	}
	defer siblingWriter.Release()
	if siblingWriter.Workspace() != siblingTarget {
		t.Fatalf("sibling writer key = %q, want precise target %q", siblingWriter.Workspace(), siblingTarget)
	}
}

func TestRegistry_CancelledContextDoesNotCreateState(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewRegistry()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := registry.AcquireWriter(ctx, workspace); !errors.Is(err, context.Canceled) {
		t.Fatalf("AcquireWriter(cancelled) error = %v, want context.Canceled", err)
	}
	if _, err := registry.TryClose(ctx, workspace); !errors.Is(err, context.Canceled) {
		t.Fatalf("TryClose(cancelled) error = %v, want context.Canceled", err)
	}

	registry.mu.Lock()
	stateCount := len(registry.states)
	registry.mu.Unlock()
	if stateCount != 0 {
		t.Fatalf("registry state count after cancelled admissions = %d, want 0", stateCount)
	}
}

func TestRegistry_WaitWriterWakesAfterExclusiveRelease(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	closedWorkspace := filepath.Join(workspace, "child")
	if err := os.Mkdir(closedWorkspace, 0o755); err != nil {
		t.Fatalf("Mkdir(child) error = %v", err)
	}
	registry := NewRegistry()
	closure, err := registry.TryClose(t.Context(), closedWorkspace)
	if err != nil {
		t.Fatalf("TryClose() error = %v", err)
	}
	defer closure.Release()
	writers := make(chan *WriterLease, 2)
	errorsCh := make(chan error, 2)
	for range 2 {
		go func() {
			lease, waitErr := registry.WaitWriter(t.Context(), workspace)
			if waitErr != nil {
				errorsCh <- waitErr
				return
			}
			writers <- lease
		}()
	}
	select {
	case lease := <-writers:
		lease.Release()
		t.Fatal("WaitWriter() returned before exclusive release")
	case err := <-errorsCh:
		t.Fatalf("WaitWriter() error before exclusive release = %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	closure.Release()
	for range 2 {
		select {
		case lease := <-writers:
			lease.Release()
		case err := <-errorsCh:
			t.Fatalf("WaitWriter() error after exclusive release = %v", err)
		}
	}
	if nextClosure, err := registry.TryClose(t.Context(), closedWorkspace); err != nil {
		t.Fatalf("TryClose() after waiting writers released error = %v", err)
	} else {
		nextClosure.Release()
	}
}

func TestRegistry_WaitWriterCancellationDoesNotLeakAdmission(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	childWorkspace := filepath.Join(workspace, "child")
	if err := os.Mkdir(childWorkspace, 0o755); err != nil {
		t.Fatalf("Mkdir(child) error = %v", err)
	}
	registry := NewRegistry()
	closure, err := registry.TryClose(t.Context(), workspace)
	if err != nil {
		t.Fatalf("TryClose() error = %v", err)
	}
	defer closure.Release()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, waitErr := registry.WaitWriter(ctx, childWorkspace)
		done <- waitErr
	}()
	select {
	case err := <-done:
		t.Fatalf("WaitWriter() returned before cancellation: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitWriter() error = %v, want context.Canceled", err)
	}
	closure.Release()
	if nextClosure, err := registry.TryClose(t.Context(), workspace); err != nil {
		t.Fatalf("TryClose() after cancelled waiter error = %v", err)
	} else {
		nextClosure.Release()
	}
}
