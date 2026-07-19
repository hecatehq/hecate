package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/taskworkflow"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestIsGitRepositoryDetectsDotGitDir(t *testing.T) {
	dir := t.TempDir()

	if isGitRepository(dir) {
		t.Fatal("plain temp dir reported as git repo")
	}

	gitDir := filepath.Join(dir, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if !isGitRepository(dir) {
		t.Error("temp dir with .git/ not detected as git repo")
	}

	// .git as a file (e.g. submodule pointer) is intentionally not treated as
	// a repo by isGitRepository — it requires .git to be a directory so the
	// orchestrator can `git clone --no-hardlinks` from it.
	plainDir := t.TempDir()
	gitFile := filepath.Join(plainDir, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: ../foo"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}
	if isGitRepository(plainDir) {
		t.Error(".git regular file should not be treated as a repository")
	}
}

func TestWorkspaceSourcePrefersWorkingDirectoryOverRepo(t *testing.T) {
	wd := t.TempDir()
	repo := t.TempDir()
	// Mark the repo as a git directory so it would otherwise be picked.
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir repo/.git: %v", err)
	}

	got := workspaceSource(types.Task{WorkingDirectory: wd, Repo: repo})
	wantPath := canonicalTestPath(t, wd)
	if got.path != wantPath {
		t.Errorf("path = %q, want %q (working directory should win)", got.path, wantPath)
	}
	if got.kind != "directory" {
		t.Errorf("kind = %q, want %q", got.kind, "directory")
	}
}

func TestWorkspaceSourceClassifiesGitRepo(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	got := workspaceSource(types.Task{Repo: repo})
	if got.kind != "git" {
		t.Errorf("kind = %q, want %q", got.kind, "git")
	}
	wantPath := canonicalTestPath(t, repo)
	if got.path != wantPath {
		t.Errorf("path = %q, want %q", got.path, wantPath)
	}
}

func TestWorkspaceSourceCanonicalizesSymlinkedSource(t *testing.T) {
	source := t.TempDir()
	parent := t.TempDir()
	link := filepath.Join(parent, "workspace-link")
	if err := os.Symlink(source, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	got := workspaceSource(types.Task{WorkingDirectory: link})
	wantPath := canonicalTestPath(t, source)
	if got.path != wantPath {
		t.Errorf("path = %q, want resolved source %q", got.path, wantPath)
	}
	if got.kind != "directory" {
		t.Errorf("kind = %q, want %q", got.kind, "directory")
	}
}

func TestWorkspaceSourceRejectsRelativeAndMissingPaths(t *testing.T) {
	cases := []struct {
		name string
		task types.Task
	}{
		{"empty task", types.Task{}},
		{"relative working directory", types.Task{WorkingDirectory: "./relative"}},
		{"non-existent absolute path", types.Task{WorkingDirectory: "/this/path/does/not/exist/probably"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := workspaceSource(tc.task)
			if got.path != "" || got.kind != "" {
				t.Errorf("workspaceSource = %+v, want zero spec", got)
			}
		})
	}
}

func TestWorkspaceManager_InPlaceModeReturnsSourcePathWithoutCloning(t *testing.T) {
	// In-place mode skips the temp-dir clone — the workspace IS the
	// source. The sandbox AllowedRoot becomes the source path so
	// shell_exec / file / agent_loop tools can read and write the
	// operator's actual repo. Necessarily destructive, so opt-in.
	source := t.TempDir()
	// Drop a marker file so we can verify the manager didn't copy.
	marker := filepath.Join(source, "marker.txt")
	if err := os.WriteFile(marker, []byte("from-source"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	root := t.TempDir()
	mgr := NewWorkspaceManager(root)
	task := types.Task{ID: "task-1", WorkspaceMode: "in_place", WorkingDirectory: source}
	run := types.TaskRun{ID: "run-1"}

	got, err := mgr.Provision(context.Background(), task, run)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	wantSource := canonicalTestPath(t, source)
	if got != wantSource {
		t.Errorf("workspace path = %q, want source %q (in_place must NOT clone)", got, wantSource)
	}
	// And the temp root should NOT have a copy under task-1/run-1.
	cloned := filepath.Join(root, "task-1", "run-1")
	if _, err := os.Stat(cloned); !os.IsNotExist(err) {
		t.Errorf("expected no clone at %q, but it exists", cloned)
	}
}

func TestWorkspaceManager_InPlaceWithoutValidSourceFails(t *testing.T) {
	// in_place requires an absolute, existing source — silently
	// falling back to an isolated clone would be a surprising mode
	// flip. Reject up-front with a clear error.
	mgr := NewWorkspaceManager(t.TempDir())
	cases := []struct {
		name string
		task types.Task
	}{
		{"no working_directory", types.Task{ID: "t", WorkspaceMode: "in_place"}},
		{"relative path", types.Task{ID: "t", WorkspaceMode: "in_place", WorkingDirectory: "./nope"}},
		{"missing absolute path", types.Task{ID: "t", WorkspaceMode: "in_place", WorkingDirectory: "/this/does/not/exist/xyz"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mgr.Provision(context.Background(), tc.task, types.TaskRun{ID: "r"})
			if err == nil {
				t.Fatalf("expected error for in_place with %s", tc.name)
			}
			if !strings.Contains(err.Error(), "in_place") {
				t.Errorf("error = %q, want mention of in_place", err.Error())
			}
		})
	}
}

func TestWorkspaceManager_ReusePreservesManagedWorkingTree(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	trackedPath := filepath.Join(source, "tracked.txt")
	untrackedPath := filepath.Join(source, "untracked.txt")
	if err := os.WriteFile(trackedPath, []byte("unstaged edit"), 0o644); err != nil {
		t.Fatalf("write tracked marker: %v", err)
	}
	if err := os.WriteFile(untrackedPath, []byte("untracked work"), 0o644); err != nil {
		t.Fatalf("write untracked marker: %v", err)
	}

	root := t.TempDir()
	mgr := NewWorkspaceManager(root)
	got, err := mgr.Provision(context.Background(), types.Task{
		ID:               "task-next",
		WorkspaceMode:    "persistent",
		WorkspaceReuse:   true,
		WorkingDirectory: source,
	}, types.TaskRun{ID: "run-next"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if got != canonicalTestPath(t, source) {
		t.Fatalf("reused workspace = %q, want %q", got, canonicalTestPath(t, source))
	}
	for path, want := range map[string]string{trackedPath: "unstaged edit", untrackedPath: "untracked work"} {
		body, err := os.ReadFile(path)
		if err != nil || string(body) != want {
			t.Fatalf("preserved file %q = %q err=%v, want %q", path, body, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "task-next", "run-next")); !os.IsNotExist(err) {
		t.Fatalf("reuse unexpectedly provisioned a clone: %v", err)
	}
}

func TestWorkspaceManagerRejectsUnsafeTaskAndRunIDs(t *testing.T) {
	mgr := NewWorkspaceManager(t.TempDir())
	source := t.TempDir()
	cases := []struct {
		name string
		task types.Task
		run  types.TaskRun
	}{
		{"empty task id", types.Task{ID: "", WorkingDirectory: source}, types.TaskRun{ID: "run-1"}},
		{"empty run id", types.Task{ID: "task-1", WorkingDirectory: source}, types.TaskRun{ID: ""}},
		{"task path traversal", types.Task{ID: "../task-1", WorkingDirectory: source}, types.TaskRun{ID: "run-1"}},
		{"run path traversal", types.Task{ID: "task-1", WorkingDirectory: source}, types.TaskRun{ID: "../run-1"}},
		{"task nested segment", types.Task{ID: "task/nested", WorkingDirectory: source}, types.TaskRun{ID: "run-1"}},
		{"run windows separator", types.Task{ID: "task-1", WorkingDirectory: source}, types.TaskRun{ID: `run\nested`}},
		{"dot segment", types.Task{ID: ".", WorkingDirectory: source}, types.TaskRun{ID: "run-1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mgr.Provision(context.Background(), tc.task, tc.run)
			if err == nil {
				t.Fatal("expected unsafe id to be rejected")
			}
			if !strings.Contains(err.Error(), "path segment") {
				t.Errorf("error = %q, want path segment validation", err.Error())
			}
		})
	}
}

func TestWorkspaceManager_DefaultModeStillClones(t *testing.T) {
	// Default workspace mode (empty / persistent / ephemeral) must
	// keep the existing isolated-clone behavior so the safety
	// guarantee doesn't silently regress.
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "marker.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	root := t.TempDir()
	mgr := NewWorkspaceManager(root)
	task := types.Task{ID: "task-x", WorkingDirectory: source}
	run := types.TaskRun{ID: "run-x"}
	got, err := mgr.Provision(context.Background(), task, run)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	want := filepath.Join(canonicalTestPath(t, root), "task-x", "run-x")
	if got != want {
		t.Errorf("workspace path = %q, want %q (cloned under temp root)", got, want)
	}
	// And the marker copied across.
	if _, err := os.Stat(filepath.Join(want, "marker.txt")); err != nil {
		t.Errorf("marker not copied to clone: %v", err)
	}
}

func TestWorkspaceManager_QAGitSourceSkipsCloneFilterExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX Git smudge helper")
	}

	source := t.TempDir()
	runGit(t, source, "init")
	runGit(t, source, "config", "user.email", "test@example.com")
	runGit(t, source, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(source, ".gitattributes"), []byte("*.evil filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "fixture.evil"), []byte("evidence\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", ".gitattributes", "fixture.evil")
	runGit(t, source, "commit", "-m", "fixture")

	marker := filepath.Join(t.TempDir(), "smudge-called")
	helper := filepath.Join(t.TempDir(), "smudge")
	script := fmt.Sprintf("#!/bin/sh\nprintf called > %q\ncat\n", marker)
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	runGit(t, source, "config", "--global", "filter.evil.smudge", helper)

	qaTask := types.Task{
		ID:                          "task-qa-copy",
		WorkingDirectory:            source,
		WorkspaceMode:               "ephemeral",
		WorkflowMode:                types.WorkflowModeQA,
		WorkflowVersion:             taskworkflow.QAVersion,
		SandboxReadOnly:             true,
		WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
	}
	qaRun := types.TaskRun{
		ID:              "run-qa-copy",
		WorkflowMode:    types.WorkflowModeQA,
		WorkflowVersion: taskworkflow.QAVersion,
	}
	qaManager := NewWorkspaceManager(t.TempDir())
	plan, err := qaManager.planProvision(qaTask, qaRun)
	if err != nil {
		t.Fatalf("planProvision(QA): %v", err)
	}
	if plan.source.kind != "directory" {
		t.Fatalf("QA provision source kind = %q, want safe directory copy", plan.source.kind)
	}
	qaWorkspace, err := qaManager.provisionPlanned(t.Context(), plan)
	if err != nil {
		t.Fatalf("provisionPlanned(QA): %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("global Git smudge helper ran during QA provisioning; stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(qaWorkspace, ".git")); err != nil {
		t.Fatalf("QA directory snapshot did not preserve Git metadata: %v", err)
	}

	normalTask := types.Task{ID: "task-normal-clone", WorkingDirectory: source, WorkspaceMode: "ephemeral"}
	normalManager := NewWorkspaceManager(t.TempDir())
	if _, err := normalManager.Provision(t.Context(), normalTask, types.TaskRun{ID: "run-normal-clone"}); err != nil {
		t.Fatalf("Provision(normal): %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("normal Git clone did not exercise configured smudge helper: %v", err)
	}
}

func TestWorkspaceManagerCanonicalizesConfiguredRootSymlink(t *testing.T) {
	actualRoot := t.TempDir()
	parent := t.TempDir()
	rootLink := filepath.Join(parent, "workspace-root")
	if err := os.Symlink(actualRoot, rootLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	mgr := NewWorkspaceManager(rootLink)
	_, got, _, err := mgr.plannedWorkspacePath("task-1", "run-1")
	if err != nil {
		t.Fatalf("plannedWorkspacePath: %v", err)
	}
	want := filepath.Join(canonicalTestPath(t, actualRoot), "task-1", "run-1")
	if got != want {
		t.Errorf("workspace path = %q, want resolved root path %q", got, want)
	}
}

func TestWorkspaceManagerProvisionPlannedRejectsFutureRootSymlinkBeforeMutation(t *testing.T) {
	parent := t.TempDir()
	outside := t.TempDir()
	root := filepath.Join(parent, "missing", "workspaces")
	mgr := NewWorkspaceManager(root)
	plan, err := mgr.planProvision(types.Task{ID: "task-1"}, types.TaskRun{ID: "run-1"})
	if err != nil {
		t.Fatalf("planProvision: %v", err)
	}
	mgr.beforeProvisionRootOpen = func() {
		if err := os.Symlink(outside, filepath.Join(parent, "missing")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
	}

	if _, err := mgr.provisionPlanned(t.Context(), plan); err == nil {
		t.Fatal("provisionPlanned succeeded after future root became a symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "workspaces")); !os.IsNotExist(err) {
		t.Fatalf("provisioning mutated outside the admitted root: %v", err)
	}
}

func TestWorkspaceManagerProvisionPlannedDoesNotFollowRootSwapAfterOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("renaming an open directory is not portable on Windows")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "managed")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("Mkdir(root): %v", err)
	}
	outside := t.TempDir()
	movedRoot := filepath.Join(parent, "managed-original")
	mgr := NewWorkspaceManager(root)
	plan, err := mgr.planProvision(types.Task{ID: "task-1"}, types.TaskRun{ID: "run-1"})
	if err != nil {
		t.Fatalf("planProvision: %v", err)
	}
	mgr.afterProvisionRootOpen = func() {
		if err := os.Rename(root, movedRoot); err != nil {
			t.Fatalf("Rename(open root): %v", err)
		}
		if err := os.Symlink(outside, root); err != nil {
			t.Fatalf("Symlink(swapped root): %v", err)
		}
	}

	if _, err := mgr.provisionPlanned(t.Context(), plan); err == nil {
		t.Fatal("provisionPlanned succeeded after the opened root path was swapped")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatalf("ReadDir(outside): %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("provisioning wrote through swapped root symlink: %+v", entries)
	}
}

func TestWorkspaceManagerProvisionPlannedDoesNotReplaceRacingDestination(t *testing.T) {
	root := t.TempDir()
	mgr := NewWorkspaceManager(root)
	plan, err := mgr.planProvision(types.Task{ID: "task-1"}, types.TaskRun{ID: "run-1"})
	if err != nil {
		t.Fatalf("planProvision: %v", err)
	}
	var injectedInfo os.FileInfo
	mgr.beforeProvisionCommit = func() {
		if err := os.Mkdir(plan.workspacePath, 0o755); err != nil {
			t.Fatalf("Mkdir(racing destination): %v", err)
		}
		injectedInfo, err = os.Stat(plan.workspacePath)
		if err != nil {
			t.Fatalf("Stat(racing destination): %v", err)
		}
	}

	if _, err := mgr.provisionPlanned(t.Context(), plan); err == nil || !strings.Contains(err.Error(), "place generated workspace") {
		t.Fatalf("provisionPlanned error = %v, want placement conflict", err)
	}
	actualInfo, err := os.Stat(plan.workspacePath)
	if err != nil {
		t.Fatalf("Stat(preserved racing destination): %v", err)
	}
	if !os.SameFile(injectedInfo, actualInfo) {
		t.Fatal("provisioning replaced the racing destination")
	}
	assertNoProvisionStages(t, filepath.Dir(plan.workspacePath))
}

func TestWorkspaceManagerProvisionPlannedRejectsDestinationSwapBeforeReturn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit renaming the open workspace root")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "workspaces")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("Mkdir(workspace root): %v", err)
	}
	mgr := NewWorkspaceManager(root)
	plan, err := mgr.planProvision(types.Task{ID: "task-1"}, types.TaskRun{ID: "run-1"})
	if err != nil {
		t.Fatalf("planProvision: %v", err)
	}
	displacedRoot := filepath.Join(parent, "displaced-workspaces")
	mgr.beforeProvisionReturn = func() {
		if err := os.Rename(root, displacedRoot); err != nil {
			t.Fatalf("Rename(workspace root): %v", err)
		}
		if err := os.MkdirAll(plan.workspacePath, 0o755); err != nil {
			t.Fatalf("MkdirAll(replacement destination): %v", err)
		}
	}

	if _, err := mgr.provisionPlanned(t.Context(), plan); err == nil || !strings.Contains(err.Error(), "workspace destination changed during provisioning") {
		t.Fatalf("provisionPlanned error = %v, want destination identity failure", err)
	}
	if _, err := os.Stat(filepath.Join(displacedRoot, "task-1", "run-1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed provisioning left the placed workspace behind: %v", err)
	}
}

func TestWorkspaceManagerProvisionPlannedCleansRestoredOwnerReadOnlyStaging(t *testing.T) {
	source := t.TempDir()
	readOnlyDirectory := filepath.Join(source, "read-only")
	if err := os.Mkdir(readOnlyDirectory, 0o755); err != nil {
		t.Fatalf("Mkdir(read-only source): %v", err)
	}
	if err := os.WriteFile(filepath.Join(readOnlyDirectory, "copied.txt"), []byte("private staged data"), 0o644); err != nil {
		t.Fatalf("WriteFile(source): %v", err)
	}
	if err := os.Chmod(filepath.Join(readOnlyDirectory, "copied.txt"), 0o444); err != nil {
		t.Fatalf("Chmod(read-only source file): %v", err)
	}
	if err := os.Chmod(readOnlyDirectory, 0o555); err != nil {
		t.Fatalf("Chmod(read-only source): %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyDirectory, 0o755) })

	root := t.TempDir()
	mgr := NewWorkspaceManager(root)
	plan, err := mgr.planProvision(types.Task{ID: "task-1", WorkingDirectory: source}, types.TaskRun{ID: "run-1"})
	if err != nil {
		t.Fatalf("planProvision: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	mgr.beforeProvisionCommit = func() {
		entries, readErr := os.ReadDir(filepath.Dir(plan.workspacePath))
		if readErr != nil {
			t.Fatalf("ReadDir(task staging root): %v", readErr)
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), ".hecate-provision-") {
				continue
			}
			info, statErr := os.Stat(filepath.Join(filepath.Dir(plan.workspacePath), entry.Name(), "read-only"))
			if statErr != nil {
				t.Fatalf("Stat(staged read-only directory): %v", statErr)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o555 {
				t.Fatalf("staged directory mode = %o, want restored source mode 555 before commit", info.Mode().Perm())
			}
			cancel()
			return
		}
		t.Fatal("workspace staging directory not found")
	}

	if _, err := mgr.provisionPlanned(ctx, plan); !errors.Is(err, context.Canceled) {
		t.Fatalf("provisionPlanned error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(plan.workspacePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled provisioning left final workspace: %v", err)
	}
	assertNoProvisionStages(t, filepath.Dir(plan.workspacePath))
}

func TestWorkspaceManagerProvisionPlannedPublishesRestoredDirectoryModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows directory mode bits do not preserve Unix permissions")
	}
	source := t.TempDir()
	readOnlyDirectory := filepath.Join(source, "read-only")
	if err := os.Mkdir(readOnlyDirectory, 0o555); err != nil {
		t.Fatalf("Mkdir(read-only source): %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyDirectory, 0o755) })

	root := t.TempDir()
	mgr := NewWorkspaceManager(root)
	plan, err := mgr.planProvision(types.Task{ID: "task-1", WorkingDirectory: source}, types.TaskRun{ID: "run-1"})
	if err != nil {
		t.Fatalf("planProvision: %v", err)
	}
	workspacePath, err := mgr.provisionPlanned(t.Context(), plan)
	if err != nil {
		t.Fatalf("provisionPlanned: %v", err)
	}
	info, err := os.Stat(filepath.Join(workspacePath, "read-only"))
	if err != nil {
		t.Fatalf("Stat(published read-only directory): %v", err)
	}
	if info.Mode().Perm() != 0o555 {
		t.Fatalf("published directory mode = %o, want 555", info.Mode().Perm())
	}
}

func TestWorkspaceManagerProvisionPlannedReportsCleanupFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows directory mode bits do not provide this failure seam")
	}
	if currentUser, err := user.Current(); err == nil && currentUser.Uid == "0" {
		t.Skip("root bypasses the directory permissions used to force cleanup failure")
	}
	root := t.TempDir()
	mgr := NewWorkspaceManager(root)
	plan, err := mgr.planProvision(types.Task{ID: "task-1"}, types.TaskRun{ID: "run-1"})
	if err != nil {
		t.Fatalf("planProvision: %v", err)
	}
	taskPath := filepath.Dir(plan.workspacePath)
	ctx, cancel := context.WithCancel(t.Context())
	mgr.beforeProvisionCommit = func() {
		if err := os.Chmod(taskPath, 0o555); err != nil {
			t.Fatalf("Chmod(task root): %v", err)
		}
		cancel()
	}
	t.Cleanup(func() { _ = os.Chmod(taskPath, 0o755) })

	_, err = mgr.provisionPlanned(ctx, plan)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "clean incomplete workspace provisioning") {
		t.Fatalf("provisionPlanned error = %v, want cancellation plus observable cleanup failure", err)
	}
}

func TestSafeJoinWithinRootRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	cases := []string{
		"../outside",
		filepath.Join("..", "outside"),
		filepath.Clean(filepath.Join("nested", "..", "..", "outside")),
	}
	for _, relativePath := range cases {
		t.Run(relativePath, func(t *testing.T) {
			if _, err := safeJoinWithinRoot(root, relativePath); err == nil {
				t.Fatal("expected escaping path to be rejected")
			}
		})
	}
}

func TestSafeJoinWithinRootRejectsExistingSymlinkComponents(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if _, err := safeJoinWithinRoot(root, filepath.Join("linked", "file.txt")); err == nil {
		t.Fatal("expected symlink component to be rejected")
	}
}

func TestCopyDirectoryRejectsDestinationSymlinkComponents(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir source nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "file.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	destination := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(destination, "nested")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	err := copyDirectory(source, destination)
	if err == nil {
		t.Fatal("expected copy into symlinked destination component to fail")
	}
	if !strings.Contains(err.Error(), "symlink component") {
		t.Errorf("error = %q, want symlink component rejection", err.Error())
	}
}

func TestSafeJoinWithinRootAllowsNestedLocalPaths(t *testing.T) {
	root := t.TempDir()
	got, err := safeJoinWithinRoot(root, filepath.Join("nested", "file.txt"))
	if err != nil {
		t.Fatalf("safeJoinWithinRoot: %v", err)
	}
	want := filepath.Join(root, "nested", "file.txt")
	if got != want {
		t.Errorf("joined path = %q, want %q", got, want)
	}
}

func TestWorkspaceSourceRejectsRegularFiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir.txt")
	if err := os.WriteFile(file, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	got := workspaceSource(types.Task{WorkingDirectory: file})
	if got.path != "" || got.kind != "" {
		t.Errorf("workspaceSource(regular file) = %+v, want zero spec", got)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %q: %v", path, err)
	}
	return resolved
}

func assertNoProvisionStages(t *testing.T, taskPath string) {
	t.Helper()
	entries, err := os.ReadDir(taskPath)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", taskPath, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".hecate-provision-") {
			t.Fatalf("incomplete provisioning stage leaked at %q", filepath.Join(taskPath, entry.Name()))
		}
	}
}
