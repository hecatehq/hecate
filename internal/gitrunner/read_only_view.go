package gitrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const readOnlyViewMetadataLimit = 1024 * 1024

// ReadOnlyView is an immutable Git control-plane snapshot for passive
// inspection. The worktree, index, and object database remain the source
// repository's read-only data, but Git reads configuration, HEAD, refs, and
// info attributes from a private temporary gitdir. Repository config changes
// therefore cannot introduce executable helpers between validation and use.
type ReadOnlyView struct {
	runner          *LocalRunner
	workspace       string
	workTree        string
	workspacePrefix string
	tempDir         string
}

// NewReadOnlyView snapshots the non-executable repository metadata needed by
// status/diff and returns a runner that never loads the source repository's
// mutable config. The caller must Close the view.
func (r *LocalRunner) NewReadOnlyView(ctx context.Context, workspace string) (*ReadOnlyView, error) {
	workspace, err := cleanWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	workTree, err := r.probeValue(ctx, workspace, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	workTree = normalizeProbedGitPath(workspace, workTree)
	canonicalWorkspace := workspace
	if resolved, resolveErr := filepath.EvalSymlinks(workspace); resolveErr == nil {
		canonicalWorkspace = resolved
	}
	if resolved, resolveErr := filepath.EvalSymlinks(workTree); resolveErr == nil {
		workTree = resolved
	}
	workspacePrefix, err := filepath.Rel(workTree, canonicalWorkspace)
	if err != nil || !filepath.IsLocal(workspacePrefix) {
		return nil, fmt.Errorf("workspace %q is outside Git worktree %q", workspace, workTree)
	}
	workspacePrefix = filepath.Clean(workspacePrefix)
	gitDir, err := r.probeValue(ctx, workspace, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	commonDir, err := r.probeValue(ctx, workspace, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	indexPath, err := r.probeValue(ctx, workspace, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return nil, err
	}
	infoAttributesPath, err := r.probeValue(ctx, workspace, "rev-parse", "--path-format=absolute", "--git-path", "info/attributes")
	if err != nil {
		return nil, err
	}
	infoExcludePath, err := r.probeValue(ctx, workspace, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return nil, err
	}
	objectFormat, err := r.probeValue(ctx, workspace, "rev-parse", "--show-object-format")
	if err != nil {
		return nil, err
	}
	objectFormat = strings.ToLower(strings.TrimSpace(objectFormat))
	if objectFormat != "sha1" && objectFormat != "sha256" {
		return nil, fmt.Errorf("unsupported Git object format %q", objectFormat)
	}

	branchRef, err := r.probeOptionalValue(ctx, workspace, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return nil, err
	}
	headOID, err := r.probeOptionalValue(ctx, workspace, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return nil, err
	}
	if err := validateReadOnlyViewHead(branchRef, headOID, objectFormat); err != nil {
		return nil, err
	}

	coreConfig, err := r.safeCoreConfig(ctx, workspace)
	if err != nil {
		return nil, err
	}
	infoAttributes, err := readBoundedOptionalFile(infoAttributesPath, readOnlyViewMetadataLimit)
	if err != nil {
		return nil, fmt.Errorf("read Git info attributes: %w", err)
	}
	infoExclude, err := readBoundedOptionalFile(infoExcludePath, readOnlyViewMetadataLimit)
	if err != nil {
		return nil, fmt.Errorf("read Git info excludes: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "hecate-git-read-")
	if err != nil {
		return nil, fmt.Errorf("create passive Git metadata view: %w", err)
	}
	cleanup := func(err error) (*ReadOnlyView, error) {
		_ = os.RemoveAll(tempDir)
		return nil, err
	}
	for _, rel := range []string{"objects", "refs", "info"} {
		if err := os.MkdirAll(filepath.Join(tempDir, rel), 0o700); err != nil {
			return cleanup(fmt.Errorf("create passive Git metadata view: %w", err))
		}
	}
	if err := writeReadOnlyViewConfig(filepath.Join(tempDir, "config"), objectFormat, coreConfig); err != nil {
		return cleanup(err)
	}
	if err := writeReadOnlyViewHead(tempDir, branchRef, headOID); err != nil {
		return cleanup(err)
	}
	if len(infoAttributes) > 0 {
		if err := os.WriteFile(filepath.Join(tempDir, "info", "attributes"), infoAttributes, 0o600); err != nil {
			return cleanup(fmt.Errorf("snapshot Git info attributes: %w", err))
		}
	}
	if len(infoExclude) > 0 {
		if err := os.WriteFile(filepath.Join(tempDir, "info", "exclude"), infoExclude, 0o600); err != nil {
			return cleanup(fmt.Errorf("snapshot Git info excludes: %w", err))
		}
	}

	viewRunner := *r
	viewRunner.Env = replaceEnvironment(r.env(), map[string]string{
		"GIT_DIR":              tempDir,
		"GIT_COMMON_DIR":       tempDir,
		"GIT_WORK_TREE":        workTree,
		"GIT_INDEX_FILE":       normalizeProbedGitPath(workspace, indexPath),
		"GIT_OBJECT_DIRECTORY": filepath.Join(normalizeProbedGitPath(workspace, commonDir), "objects"),
		"GIT_CONFIG_NOSYSTEM":  "1",
		"GIT_CONFIG_GLOBAL":    os.DevNull,
		"GIT_CONFIG_SYSTEM":    os.DevNull,
		"GIT_ATTR_NOSYSTEM":    "1",
	})
	viewRunner.ReadOnlyPaths = appendUniquePaths(viewRunner.ReadOnlyPaths,
		tempDir,
		workTree,
		normalizeProbedGitPath(workspace, gitDir),
		normalizeProbedGitPath(workspace, commonDir),
	)
	return &ReadOnlyView{
		runner:          &viewRunner,
		workspace:       workspace,
		workTree:        workTree,
		workspacePrefix: workspacePrefix,
		tempDir:         tempDir,
	}, nil
}

func (v *ReadOnlyView) Close() error {
	if v == nil || strings.TrimSpace(v.tempDir) == "" {
		return nil
	}
	err := os.RemoveAll(v.tempDir)
	v.tempDir = ""
	return err
}

func (v *ReadOnlyView) RunLimited(ctx context.Context, maxBytes int64, args ...string) (Result, error) {
	if v == nil || v.runner == nil {
		return Result{ExitCode: -1}, errors.New("passive Git metadata view is not configured")
	}
	return v.runner.RunLimitedReadOnly(ctx, v.workspace, maxBytes, args...)
}

// RunLimitedInput runs a fixed passive Git command with caller-provided input.
func (v *ReadOnlyView) RunLimitedInput(ctx context.Context, maxBytes int64, stdin string, args ...string) (Result, error) {
	if v == nil || v.runner == nil {
		return Result{ExitCode: -1}, errors.New("passive Git metadata view is not configured")
	}
	return v.runner.RunLimitedReadOnlyInput(ctx, v.workspace, maxBytes, stdin, args...)
}

// WorkspacePrefix returns Workspace relative to WorkTree. Git paths emitted
// with --full-name are relative to this same root.
func (v *ReadOnlyView) WorkspacePrefix() string {
	if v == nil {
		return ""
	}
	return v.workspacePrefix
}

func (r *LocalRunner) probeValue(ctx context.Context, workspace string, args ...string) (string, error) {
	result, err := r.RunLimitedReadOnly(ctx, workspace, readOnlyViewMetadataLimit, append([]string{"--no-pager"}, args...)...)
	if err != nil {
		return "", gitProbeError(args, result, err)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return "", fmt.Errorf("Git metadata probe output exceeded %d bytes", readOnlyViewMetadataLimit)
	}
	value := strings.TrimSpace(result.Stdout)
	if value == "" {
		return "", fmt.Errorf("Git metadata probe %q returned no value", strings.Join(args, " "))
	}
	return value, nil
}

func (r *LocalRunner) probeOptionalValue(ctx context.Context, workspace string, args ...string) (string, error) {
	result, err := r.RunLimitedReadOnly(ctx, workspace, readOnlyViewMetadataLimit, append([]string{"--no-pager"}, args...)...)
	if err != nil {
		if result.ExitCode == 1 && strings.TrimSpace(result.Stdout) == "" && strings.TrimSpace(result.Stderr) == "" {
			return "", nil
		}
		return "", gitProbeError(args, result, err)
	}
	return strings.TrimSpace(result.Stdout), nil
}

func gitProbeError(args []string, result Result, err error) error {
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Stdout)
	}
	if detail == "" {
		return fmt.Errorf("Git metadata probe %q: %w", strings.Join(args, " "), err)
	}
	return fmt.Errorf("Git metadata probe %q: %w: %s", strings.Join(args, " "), err, detail)
}

func (r *LocalRunner) safeCoreConfig(ctx context.Context, workspace string) (map[string]string, error) {
	const pattern = `^core\.(filemode|ignorecase|ignorestat|symlinks|autocrlf|eol|safecrlf|checkstat|trustctime|quotepath|precomposeunicode|longpaths)$`
	result, err := r.RunLimitedReadOnly(ctx, workspace, readOnlyViewMetadataLimit,
		"--no-pager", "config", "-z", "--includes", "--get-regexp", pattern,
	)
	if err != nil {
		if result.ExitCode == 1 && strings.TrimSpace(result.Stdout) == "" && strings.TrimSpace(result.Stderr) == "" {
			return map[string]string{}, nil
		}
		return nil, gitProbeError([]string{"config", "--get-regexp", pattern}, result, err)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return nil, fmt.Errorf("safe Git configuration exceeded %d bytes", readOnlyViewMetadataLimit)
	}
	out := make(map[string]string)
	for _, entry := range strings.Split(result.Stdout, "\x00") {
		if entry == "" {
			continue
		}
		key, value, ok := strings.Cut(entry, "\n")
		if !ok {
			return nil, fmt.Errorf("malformed safe Git configuration entry %q", entry)
		}
		value, ok = normalizeSafeCoreConfig(strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value))
		if !ok {
			return nil, fmt.Errorf("unsupported value for passive Git configuration %s", strings.TrimSpace(key))
		}
		out[strings.ToLower(strings.TrimSpace(key))] = value
	}
	return out, nil
}

func normalizeSafeCoreConfig(key, value string) (string, bool) {
	lower := strings.ToLower(value)
	switch key {
	case "core.filemode", "core.ignorecase", "core.ignorestat", "core.symlinks", "core.trustctime", "core.quotepath", "core.precomposeunicode", "core.longpaths":
		return normalizeGitBool(lower)
	case "core.autocrlf":
		if lower == "input" {
			return lower, true
		}
		return normalizeGitBool(lower)
	case "core.eol":
		if lower == "lf" || lower == "crlf" || lower == "native" {
			return lower, true
		}
	case "core.safecrlf":
		if lower == "warn" {
			return lower, true
		}
		return normalizeGitBool(lower)
	case "core.checkstat":
		if lower == "default" || lower == "minimal" {
			return lower, true
		}
	}
	return "", false
}

func normalizeGitBool(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "on", "1":
		return "true", true
	case "false", "no", "off", "0":
		return "false", true
	default:
		return "", false
	}
}

func writeReadOnlyViewConfig(path, objectFormat string, coreConfig map[string]string) error {
	if _, ok := coreConfig["core.filemode"]; !ok {
		coreConfig["core.filemode"] = "true"
	}
	keys := make([]string, 0, len(coreConfig))
	for key := range coreConfig {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("[core]\n")
	if objectFormat == "sha256" {
		b.WriteString("\trepositoryformatversion = 1\n")
	} else {
		b.WriteString("\trepositoryformatversion = 0\n")
	}
	b.WriteString("\tbare = false\n")
	for _, key := range keys {
		fmt.Fprintf(&b, "\t%s = %q\n", strings.TrimPrefix(key, "core."), coreConfig[key])
	}
	if objectFormat == "sha256" {
		b.WriteString("[extensions]\n\tobjectFormat = sha256\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write passive Git config: %w", err)
	}
	return nil
}

func writeReadOnlyViewHead(gitDir, branchRef, headOID string) error {
	head := strings.TrimSpace(headOID)
	if branchRef != "" {
		head = "ref: " + branchRef
		branchPath := filepath.FromSlash(strings.TrimPrefix(branchRef, "refs/heads/"))
		refPath := filepath.Join(gitDir, "refs", "heads", branchPath)
		if headOID != "" {
			if err := os.MkdirAll(filepath.Dir(refPath), 0o700); err != nil {
				return fmt.Errorf("create passive Git branch ref: %w", err)
			}
			if err := os.WriteFile(refPath, []byte(headOID+"\n"), 0o600); err != nil {
				return fmt.Errorf("write passive Git branch ref: %w", err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(head+"\n"), 0o600); err != nil {
		return fmt.Errorf("write passive Git HEAD: %w", err)
	}
	return nil
}

func validateReadOnlyViewHead(branchRef, headOID, objectFormat string) error {
	if branchRef == "" && headOID == "" {
		return errors.New("Git repository has neither a branch nor HEAD commit")
	}
	if branchRef != "" {
		if !strings.HasPrefix(branchRef, "refs/heads/") {
			return fmt.Errorf("unsupported Git symbolic HEAD %q", branchRef)
		}
		branchPath := filepath.FromSlash(strings.TrimPrefix(branchRef, "refs/heads/"))
		if !filepath.IsLocal(branchPath) {
			return fmt.Errorf("unsafe Git branch ref %q", branchRef)
		}
	}
	if headOID != "" {
		wantLen := 40
		if objectFormat == "sha256" {
			wantLen = 64
		}
		if len(headOID) != wantLen {
			return fmt.Errorf("invalid %s HEAD object id", objectFormat)
		}
		for _, r := range headOID {
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return fmt.Errorf("invalid %s HEAD object id", objectFormat)
			}
		}
	}
	return nil
}

func readBoundedOptionalFile(path string, maxBytes int64) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	file, err := openReadOnlyMetadata(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	return data, nil
}

func normalizeProbedGitPath(workspace, path string) string {
	path = strings.TrimSpace(path)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(workspace, path))
}

func replaceEnvironment(env []string, values map[string]string) []string {
	out := make([]string, 0, len(env)+len(values))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, replaced := values[key]; replaced {
				continue
			}
		}
		out = append(out, entry)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

func appendUniquePaths(paths []string, values ...string) []string {
	seen := make(map[string]struct{}, len(paths)+len(values))
	out := make([]string, 0, len(paths)+len(values))
	for _, path := range append(append([]string(nil), paths...), values...) {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}
