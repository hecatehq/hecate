package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestWorkspaceManagerCanonicalizesConfiguredRootSymlink(t *testing.T) {
	actualRoot := t.TempDir()
	parent := t.TempDir()
	rootLink := filepath.Join(parent, "workspace-root")
	if err := os.Symlink(actualRoot, rootLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	mgr := NewWorkspaceManager(rootLink)
	got, err := mgr.workspacePath("task-1", "run-1")
	if err != nil {
		t.Fatalf("workspacePath: %v", err)
	}
	want := filepath.Join(canonicalTestPath(t, actualRoot), "task-1", "run-1")
	if got != want {
		t.Errorf("workspace path = %q, want resolved root path %q", got, want)
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
