package codeintel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hecatehq/hecate/internal/processrunner"
	"github.com/hecatehq/hecate/internal/sandbox"
	"github.com/hecatehq/hecate/internal/workspacefs"
)

const (
	structuralStdoutBytes = 2 * 1024 * 1024
	structuralStderrBytes = 32 * 1024
	structuralConfigName  = "sgconfig.yml"
	structuralConfig      = "ruleDirs: []\n"
)

var structuralLanguages = map[string]string{
	"go":         "go",
	"golang":     "go",
	"js":         "js",
	"javascript": "js",
	"jsx":        "jsx",
	"ts":         "ts",
	"typescript": "ts",
	"tsx":        "tsx",
	"python":     "python",
	"py":         "python",
	"rust":       "rust",
	"rs":         "rust",
	"java":       "java",
	"c":          "c",
	"cpp":        "cpp",
	"c++":        "cpp",
	"csharp":     "csharp",
	"c#":         "csharp",
	"html":       "html",
	"css":        "css",
	"json":       "json",
	"yaml":       "yaml",
	"yml":        "yaml",
	"bash":       "bash",
	"sh":         "bash",
}

var structuralExtensionLanguages = map[string][]string{
	".go": {"go"}, ".js": {"js"}, ".mjs": {"js"}, ".cjs": {"js"}, ".jsx": {"jsx"},
	".ts": {"ts"}, ".mts": {"ts"}, ".cts": {"ts"}, ".tsx": {"tsx"}, ".py": {"python"},
	".pyi": {"python"}, ".rs": {"rust"}, ".java": {"java"}, ".c": {"c"}, ".h": {"c", "cpp"},
	".cc": {"cpp"}, ".cpp": {"cpp"}, ".cxx": {"cpp"}, ".hpp": {"cpp"}, ".cs": {"csharp"},
	".html": {"html"}, ".htm": {"html"}, ".css": {"css"}, ".json": {"json"},
	".yaml": {"yaml"}, ".yml": {"yaml"}, ".sh": {"bash"}, ".bash": {"bash"},
}

type astGrepMatch struct {
	File  string       `json:"file"`
	Path  string       `json:"path"`
	Text  string       `json:"text"`
	Range astGrepRange `json:"range"`
}

type astGrepRange struct {
	Start astGrepPosition `json:"start"`
	End   astGrepPosition `json:"end"`
}

type astGrepPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

func normalizeStructuralLanguage(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := structuralLanguages[value]; ok {
		return normalized
	}
	return value
}

func (s *Service) queryStructural(ctx context.Context, fsys *workspacefs.FS, request Request) (Result, error) {
	if request.Query == "" {
		return Result{}, fmt.Errorf("query is required for structural_search")
	}
	target := request.Path
	if target == "" {
		target = "."
	}
	info, absoluteTarget, err := fsys.Stat(target)
	if err != nil {
		return Result{}, fmt.Errorf("resolve structural search path %q: %w", target, err)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return Result{}, fmt.Errorf("structural search path %q must be a regular file or directory", target)
	}
	language, err := selectStructuralLanguage(request.Language, request.Path, info.IsDir())
	if err != nil {
		return Result{}, err
	}
	binary, err := s.resolveTrustedBinary(fsys, "ast-grep")
	if err != nil {
		return Result{}, fmt.Errorf("structural_search is unavailable: install ast-grep on a trusted global PATH or set HECATE_CODEINTEL_AST_GREP_PATH")
	}
	neutralDir, configPath, err := createStructuralRuntimeDirectory(fsys.Root(), structuralNeutralBase())
	if err != nil {
		return Result{}, fmt.Errorf("create trusted structural-search directory")
	}
	defer os.RemoveAll(neutralDir)
	argv := []string{binary, "run", "--config", configPath, "--pattern", request.Query}
	if request.Selector != "" {
		argv = append(argv, "--selector", request.Selector)
	}
	argv = append(argv, "--lang", language,
		"--json=stream", "--color", "never", "--threads", "1", absoluteTarget)
	argv = sandbox.WrapReadOnlyArgv(argv, fsys.Root(), false, neutralDir)
	if len(argv) == 0 {
		return Result{}, fmt.Errorf("structural search command is empty")
	}
	runResult, runErr := s.runner.Run(ctx, processrunner.Request{
		Command:        argv[0],
		Args:           argv[1:],
		Dir:            neutralDir,
		Env:            providerProcessEnv(ctx, "ast-grep"),
		Timeout:        s.timeout,
		MaxStdoutBytes: structuralStdoutBytes,
		MaxStderrBytes: structuralStderrBytes,
	})
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return Result{}, runErr
		}
		var exitErr *exec.ExitError
		if runResult.ExitCode == 1 && errors.As(runErr, &exitErr) {
			// ast-grep documents exit 1 as a successful search with no
			// matches. Do not turn an ordinary negative query into a tool
			// failure or expose the subprocess error text.
			return Result{Operation: OpStructuralSearch, Provider: "ast-grep"}, nil
		}
		if errors.As(runErr, &exitErr) || runResult.ExitCode != 0 {
			return Result{}, fmt.Errorf("structural_search failed with exit code %d", runResult.ExitCode)
		}
		return Result{}, fmt.Errorf("structural_search process failed")
	}
	if runResult.ExitCode == 1 {
		return Result{Operation: OpStructuralSearch, Provider: "ast-grep"}, nil
	}
	result := Result{Operation: OpStructuralSearch, Provider: "ast-grep", Truncated: runResult.StdoutTruncated}
	cache := newSourceCache(fsys)
	lines := bytes.Split([]byte(runResult.Stdout), []byte{'\n'})
	attempted := 0
	for index, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if runResult.StdoutTruncated && index == len(lines)-1 {
			result.Truncated = true
			break
		}
		// Every provider record consumes the result budget, even if it is
		// later omitted. Otherwise an adversarial stream of invalid ranges or
		// unsafe paths could make Hecate open files without bound while
		// producing fewer than MaxResults items.
		if attempted >= request.MaxResults {
			result.Truncated = true
			break
		}
		attempted++
		var match astGrepMatch
		if err := json.Unmarshal(line, &match); err != nil {
			return Result{}, fmt.Errorf("ast-grep returned a malformed JSON stream record at index %d", index+1)
		}
		path := match.File
		if strings.TrimSpace(path) == "" {
			path = match.Path
		}
		if strings.TrimSpace(path) == "" {
			result.OmittedExternal++
			continue
		}
		if !filepath.IsAbs(path) {
			base := absoluteTarget
			if !info.IsDir() {
				base = filepath.Dir(absoluteTarget)
			}
			path = filepath.Join(base, path)
		}
		file, err := cache.openURI(pathToFileURI(path))
		if err != nil {
			result.OmittedExternal++
			continue
		}
		item, err := normalizedRange(file, lspRange{
			Start: lspPosition{Line: match.Range.Start.Line, Character: match.Range.Start.Column},
			End:   lspPosition{Line: match.Range.End.Line, Character: match.Range.End.Column},
		}, positionUTF32)
		if err != nil {
			result.OmittedExternal++
			continue
		}
		item.Detail = concise(match.Text, 512)
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func isStructuralSelector(value string) bool {
	for index := 0; index < len(value); index++ {
		char := value[index]
		if index == 0 {
			if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || char == '_' {
				continue
			}
			return false
		}
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' {
			continue
		}
		return false
	}
	return value != ""
}

func structuralNeutralBase() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	// Ignore a gateway TMPDIR that could point into the active checkout. A
	// neutral cwd keeps the private fixed config outside project control while
	// ast-grep receives the workspace target as an explicit argument.
	return "/tmp"
}

func createStructuralNeutralDirectory(workspace, base string) (string, error) {
	directory, err := os.MkdirTemp(base, "hecate-codeintel-")
	if err != nil {
		return "", err
	}
	canonical, directoryErr := filepath.EvalSymlinks(directory)
	canonicalWorkspace, workspaceErr := filepath.EvalSymlinks(workspace)
	if directoryErr != nil || workspaceErr != nil || pathWithinRoot(projectTrustRoot(canonicalWorkspace), canonical) {
		_ = os.RemoveAll(directory)
		return "", fmt.Errorf("neutral directory is inside the project boundary")
	}
	return directory, nil
}

func createStructuralRuntimeDirectory(workspace, base string) (string, string, error) {
	directory, err := createStructuralNeutralDirectory(workspace, base)
	if err != nil {
		return "", "", err
	}
	configPath := filepath.Join(directory, structuralConfigName)
	if !filepath.IsAbs(configPath) {
		_ = os.RemoveAll(directory)
		return "", "", fmt.Errorf("structural configuration path is not absolute")
	}
	if err := os.WriteFile(configPath, []byte(structuralConfig), 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return "", "", err
	}
	return directory, configPath, nil
}

func selectStructuralLanguage(requested, path string, directory bool) (string, error) {
	extension := ""
	var compatible []string
	if path != "" && !directory {
		extension = strings.ToLower(filepath.Ext(path))
		compatible = structuralExtensionLanguages[extension]
	}
	if requested == "" {
		if len(compatible) == 0 {
			if path != "" && !directory {
				return "", fmt.Errorf("no allowlisted structural-search language supports %q", filepath.Ext(path))
			}
			return "", fmt.Errorf("language is required when structural_search targets a directory")
		}
		return compatible[0], nil
	}
	if _, ok := structuralLanguages[requested]; !ok {
		found := false
		for _, language := range structuralLanguages {
			if requested == language {
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("structural-search language %q is not allowlisted", requested)
		}
	}
	if extension != "" && len(compatible) == 0 {
		return "", fmt.Errorf("no allowlisted structural-search language supports %q", filepath.Ext(path))
	}
	if len(compatible) > 0 && !containsStructuralLanguage(compatible, requested) {
		return "", fmt.Errorf("structural-search language %q does not match path %q", requested, path)
	}
	return requested, nil
}

func containsStructuralLanguage(languages []string, requested string) bool {
	for _, language := range languages {
		if language == requested {
			return true
		}
	}
	return false
}
