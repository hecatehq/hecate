package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestGrepToolHonorsCancelledContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	text, step, artifacts, err := grepTool(ctx, spec, grepArgs{Pattern: "main"}, 1, time.Now().UTC(), "grep")
	if err != nil {
		t.Fatalf("grepTool() error = %v", err)
	}
	if step != nil || len(artifacts) != 0 {
		t.Fatalf("cancelled grep produced step/artifacts: step=%+v artifacts=%+v", step, artifacts)
	}
	if !strings.Contains(text, context.Canceled.Error()) {
		t.Fatalf("grepTool() text = %q, want cancellation", text)
	}
}

func TestGrepToolBoundsLongMatchingLinesAndAggregateOutput(t *testing.T) {
	dir := t.TempDir()
	line := "Target-🙂-" + strings.Repeat("x", grepLineHardCapBytes*2)
	for fileIndex := 0; fileIndex < 140; fileIndex++ {
		var content strings.Builder
		for lineIndex := 0; lineIndex < 4; lineIndex++ {
			content.WriteString(line)
			content.WriteByte('\n')
		}
		name := filepath.Join(dir, fmt.Sprintf("match-%03d.go", fileIndex))
		if err := os.WriteFile(name, []byte(content.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir

	text, step, _, err := grepTool(context.Background(), spec, grepArgs{Pattern: "Target", MaxMatches: grepHardCapMatches}, 1, time.Now().UTC(), "grep")
	if err != nil {
		t.Fatalf("grepTool() error = %v", err)
	}
	if step == nil {
		t.Fatal("grepTool() step = nil")
	}
	header := strings.SplitN(text, "\n", 2)[0]
	if !strings.Contains(header, "truncated=true") || !strings.Contains(header, "scan_limit=match_limit") || !strings.Contains(header, "output_truncated=true") {
		t.Fatalf("grepTool() header = %q, want independent match and output limits", header)
	}
	if len(text) > searchOutputHardCap {
		t.Fatalf("grepTool() output bytes = %d, want at most %d", len(text), searchOutputHardCap)
	}
	if !utf8.ValidString(text) || !strings.Contains(text, "…") {
		t.Fatalf("grepTool() output must be valid UTF-8 with truncated line")
	}
	if got := step.Input["scan_limit"]; got != "match_limit" {
		t.Fatalf("step scan_limit = %v, want match_limit", got)
	}
	if got := step.Input["output_truncated"]; got != true {
		t.Fatalf("step output_truncated = %v, want true", got)
	}
}

func TestGrepToolReportsMatchLimitInsideSingleFile(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("Target\n", grepDefaultMaxMatches+1)
	if err := os.WriteFile(filepath.Join(dir, "many.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir

	text, step, _, err := grepTool(context.Background(), spec, grepArgs{Pattern: "Target"}, 1, time.Now().UTC(), "grep")
	if err != nil {
		t.Fatalf("grepTool() error = %v", err)
	}
	if step == nil || !strings.Contains(text, "matches=100") || !strings.Contains(text, "truncated=true scan_limit=match_limit output_truncated=false") {
		t.Fatalf("grepTool() = step %+v text %q, want explicit match truncation", step, strings.SplitN(text, "\n", 2)[0])
	}
}

func TestGlobToolBoundsAggregateOutput(t *testing.T) {
	dir := t.TempDir()
	for index := 0; index < 320; index++ {
		name := fmt.Sprintf("%03d-%s.go", index, strings.Repeat("x", 220))
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir

	text, step, _, err := globTool(context.Background(), spec, globArgs{Pattern: "*.go", MaxMatches: globHardCapMatches}, 1, time.Now().UTC(), "glob")
	if err != nil {
		t.Fatalf("globTool() error = %v", err)
	}
	if step == nil {
		t.Fatal("globTool() step = nil")
	}
	if !strings.Contains(text, "truncated=true output_truncated=true") || strings.Contains(text, "scan_limit=") {
		t.Fatalf("globTool() header = %q, want output limit", strings.SplitN(text, "\n", 2)[0])
	}
	if len(text) > searchOutputHardCap {
		t.Fatalf("globTool() output bytes = %d, want at most %d", len(text), searchOutputHardCap)
	}
	if got := step.Input["scan_limit"]; got != "" {
		t.Fatalf("step scan_limit = %v, want empty", got)
	}
	if got := step.Input["output_truncated"]; got != true {
		t.Fatalf("step output_truncated = %v, want true", got)
	}
}

func TestSearchToolsRejectOversizedPatternsBeforeTraversal(t *testing.T) {
	dir := t.TempDir()
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	// Fewer than maxLength code points, but larger than the native encoded-byte
	// budget: server-side validation must remain authoritative for Unicode.
	oversized := strings.Repeat("é", searchPatternHardCap/2+1)

	tests := []struct {
		name string
		run  func() (string, *types.TaskStep)
	}{
		{
			name: "grep pattern",
			run: func() (string, *types.TaskStep) {
				text, step, _, _ := grepTool(context.Background(), spec, grepArgs{Pattern: oversized}, 1, time.Now().UTC(), "grep")
				return text, step
			},
		},
		{
			name: "grep include",
			run: func() (string, *types.TaskStep) {
				text, step, _, _ := grepTool(context.Background(), spec, grepArgs{Pattern: "x", Include: oversized}, 1, time.Now().UTC(), "grep")
				return text, step
			},
		},
		{
			name: "glob pattern",
			run: func() (string, *types.TaskStep) {
				text, step, _, _ := globTool(context.Background(), spec, globArgs{Pattern: oversized}, 1, time.Now().UTC(), "glob")
				return text, step
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text, step := test.run()
			if step != nil {
				t.Fatalf("step = %+v, want nil", step)
			}
			if !strings.Contains(text, "exceeds the 4096-byte limit") {
				t.Fatalf("text = %q, want bounded pattern error", text)
			}
			if len(text) > searchOutputHardCap {
				t.Fatalf("output bytes = %d, want at most %d", len(text), searchOutputHardCap)
			}
		})
	}
}

func TestBoundedSearchOutputCapsCompleteResponse(t *testing.T) {
	text := boundedSearchOutput(strings.Repeat("metadata", searchOutputHardCap))
	if len(text) > searchOutputHardCap {
		t.Fatalf("boundedSearchOutput() bytes = %d, want at most %d", len(text), searchOutputHardCap)
	}
	if !utf8.ValidString(text) || !strings.HasSuffix(text, "…(truncated)") {
		t.Fatalf("boundedSearchOutput() must be valid UTF-8 with a truncation marker")
	}
}

func TestGrepFileDoesNotSearchIncompleteTrailingDataAtAggregateByteBudget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("safe\nTarget-and-more"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	fsys, errMsg := workspaceFileSystem(spec)
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	re := regexp.MustCompile(`^(safe|Target)$`)

	budget := int64(len("safe\nTarget"))
	result := grepFile(fsys, "main.txt", "main.txt", re, 10, budget)
	if result.err != nil {
		t.Fatalf("grepFile() error = %v", result.err)
	}
	if result.bytesRead != budget {
		t.Fatalf("bytesRead = %d, want %d", result.bytesRead, budget)
	}
	if len(result.matches) != 1 || result.matches[0].Text != "safe" || result.matchLimit || !result.byteLimit {
		t.Fatalf("grepFile() matches=%+v matchLimit=%v byteLimit=%v, want only complete line", result.matches, result.matchLimit, result.byteLimit)
	}
}

func TestGrepFileExactAggregateBudgetAtEOFIsComplete(t *testing.T) {
	dir := t.TempDir()
	content := []byte("Target")
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	fsys, errMsg := workspaceFileSystem(spec)
	if errMsg != "" {
		t.Fatal(errMsg)
	}

	result := grepFile(fsys, "main.txt", "main.txt", regexp.MustCompile(`Target$`), 10, int64(len(content)))
	if result.err != nil {
		t.Fatalf("grepFile() error = %v", result.err)
	}
	if len(result.matches) != 1 || result.byteLimit {
		t.Fatalf("grepFile() matches=%+v byteLimit=%v, want complete exact-budget match", result.matches, result.byteLimit)
	}
}

func TestCompiledGlobPatternSupportsZeroOrMoreRecursiveDirectories(t *testing.T) {
	matcher, err := compileGlobPattern("**/*.go")
	if err != nil {
		t.Fatalf("compileGlobPattern() error = %v", err)
	}
	matches := func(matcher *compiledGlobPattern, path string) bool {
		t.Helper()
		matched, exhausted := matcher.Matches(path, newGlobMatchWorkBudget(1_000_000))
		if exhausted {
			t.Fatalf("matcher exhausted a generous budget for %q", path)
		}
		return matched
	}
	if !matches(&matcher, "main.go") || !matches(&matcher, "internal/orchestrator/executor.go") || matches(&matcher, "internal/orchestrator/executor.ts") {
		t.Fatal("compiled recursive glob did not preserve expected matching")
	}

	internal, err := compileGlobPattern("internal/**/*.go")
	if err != nil {
		t.Fatalf("compile internal glob: %v", err)
	}
	if !matches(&internal, "internal/root.go") || !matches(&internal, "internal/nested/worker.go") || matches(&internal, "root.go") {
		t.Fatal("internal recursive glob must include direct and nested children only")
	}
}

func TestCompiledGlobPatternAdversarialRecursionExhaustsBoundedReusableWork(t *testing.T) {
	parts := make([]string, 0, 401)
	pathParts := make([]string, 0, 200)
	for range 200 {
		parts = append(parts, "**", "x")
		pathParts = append(pathParts, "x")
	}
	parts = append(parts, "never")
	matcher, err := compileGlobPattern(strings.Join(parts, "/"))
	if err != nil {
		t.Fatalf("compile adversarial glob: %v", err)
	}
	budget := newGlobMatchWorkBudget(1_000)
	matched, exhausted := matcher.Matches(strings.Join(pathParts, "/"), budget)
	if matched || !exhausted {
		t.Fatalf("matched=%v exhausted=%v, want bounded exhaustion", matched, exhausted)
	}
	if budget.Used() != 1_000 {
		t.Fatalf("pattern work = %d, want exact budget", budget.Used())
	}
	if cap(matcher.statesA) != len(matcher.segments)+1 || cap(matcher.statesB) != len(matcher.segments)+1 {
		t.Fatalf("matcher scratch is not linear in pattern: segments=%d capA=%d capB=%d", len(matcher.segments), cap(matcher.statesA), cap(matcher.statesB))
	}
}

func TestGlobToolReportsSharedPathAndBasenamePatternWorkLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plain.ts"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir

	// The full-path check consumes this exact budget and does not match. The
	// basename fallback must share it, observe exhaustion, and stop honestly.
	const workLimit = int64(1 + len("*.go")*len("plain.ts"))
	text, step, _, err := globToolWithPatternWorkLimit(context.Background(), spec, globArgs{
		Pattern:    "*.go",
		MaxMatches: globHardCapMatches,
	}, 1, time.Now().UTC(), "glob", workLimit)
	if err != nil {
		t.Fatalf("globToolWithPatternWorkLimit() error = %v", err)
	}
	if step == nil {
		t.Fatal("glob tool omitted step at pattern work limit")
	}
	header := strings.SplitN(text, "\n", 2)[0]
	if !strings.Contains(header, "truncated=true scan_limit=pattern_work_limit") || !strings.Contains(header, fmt.Sprintf("pattern_work=%d", workLimit)) {
		t.Fatalf("glob header = %q, want explicit shared pattern-work cutoff", header)
	}
	if got := step.Input["scan_limit"]; got != "pattern_work_limit" {
		t.Fatalf("step scan_limit = %v, want pattern_work_limit", got)
	}
	if got := step.Input["pattern_work"]; got != workLimit {
		t.Fatalf("step pattern_work = %v, want %d", got, workLimit)
	}
}

func TestGrepIncludeReportsSharedPathAndBasenamePatternWorkLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plain.ts"), []byte("Target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir

	const workLimit = int64(1 + len("*.go")*len("plain.ts"))
	text, step, _, err := grepToolWithPatternWorkLimit(context.Background(), spec, grepArgs{
		Pattern: "Target",
		Include: "*.go",
	}, 1, time.Now().UTC(), "grep", workLimit)
	if err != nil {
		t.Fatalf("grepToolWithPatternWorkLimit() error = %v", err)
	}
	if step == nil {
		t.Fatal("grep tool omitted step at pattern work limit")
	}
	header := strings.SplitN(text, "\n", 2)[0]
	if !strings.Contains(header, "truncated=true scan_limit=pattern_work_limit") || !strings.Contains(header, fmt.Sprintf("pattern_work=%d", workLimit)) {
		t.Fatalf("grep header = %q, want explicit shared pattern-work cutoff", header)
	}
	if got := step.Input["scan_limit"]; got != "pattern_work_limit" {
		t.Fatalf("step scan_limit = %v, want pattern_work_limit", got)
	}
	if got := step.Input["pattern_work"]; got != workLimit {
		t.Fatalf("step pattern_work = %v, want %d", got, workLimit)
	}
}

func TestConsumeSearchEntryEnforcesExactHardCap(t *testing.T) {
	scanned := 0
	for index := 0; index < searchEntryHardCap; index++ {
		if !consumeSearchEntry(&scanned, searchEntryHardCap) {
			t.Fatalf("entry %d was rejected before the hard cap", index+1)
		}
	}
	if consumeSearchEntry(&scanned, searchEntryHardCap) {
		t.Fatal("entry beyond the hard cap was accepted")
	}
	if scanned != searchEntryHardCap {
		t.Fatalf("scanned entries = %d, want exact cap %d", scanned, searchEntryHardCap)
	}
}

func TestGrepIncludeDoubleStarIncludesRootAndNestedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"main.go":            "package main // Target\n",
		"internal/worker.go": "package internal // Target\n",
		"ignored.ts":         "const Target = true\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir

	text, step, _, err := grepTool(context.Background(), spec, grepArgs{
		Pattern: "Target",
		Include: "**/*.go",
	}, 1, time.Now().UTC(), "grep")
	if err != nil {
		t.Fatalf("grepTool() error = %v", err)
	}
	if step == nil || !strings.Contains(text, "matches=2") || !strings.Contains(text, "main.go:1:") || !strings.Contains(text, "internal/worker.go:1:") || strings.Contains(text, "ignored.ts:1:") {
		t.Fatalf("grepTool() text = %q step=%+v, want root and nested Go matches", text, step)
	}
}

func TestGrepToolReportsBoundedSkippedFileMetadata(t *testing.T) {
	dir := t.TempDir()
	large := filepath.Join(dir, "generated.txt")
	if err := os.WriteFile(large, []byte("Target"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(large, grepFileHardCapBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "small.txt"), []byte("ordinary text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir

	text, step, _, err := grepTool(context.Background(), spec, grepArgs{Pattern: "Target"}, 1, time.Now().UTC(), "grep")
	if err != nil {
		t.Fatalf("grepTool() error = %v", err)
	}
	if step == nil || !strings.Contains(text, "matches=0") || !strings.Contains(text, "incomplete=true skipped_files=1 skip_reasons=large_file:1") {
		t.Fatalf("grepTool() text = %q step=%+v, want explicit incomplete result", text, step)
	}
	if len(text) > searchOutputHardCap {
		t.Fatalf("grepTool() output bytes = %d, want at most %d", len(text), searchOutputHardCap)
	}
	if got := step.Input["incomplete"]; got != true {
		t.Fatalf("step incomplete = %v, want true", got)
	}
	if got := step.Input["skipped_files"]; got != 1 {
		t.Fatalf("step skipped_files = %v, want 1", got)
	}
	reasons, ok := step.Input["skip_reasons"].(map[string]int)
	if !ok || reasons[grepSkipLargeFile] != 1 {
		t.Fatalf("step skip_reasons = %#v, want large_file:1", step.Input["skip_reasons"])
	}
}

func TestGrepFileSnapshotChangedDetectsSizeModTimeAndShortRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(path, []byte("Target"), 0o644); err != nil {
		t.Fatal(err)
	}
	initial, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if grepFileSnapshotChanged(initial, initial, initial.Size(), false) {
		t.Fatal("unchanged complete snapshot reported as changed")
	}
	if !grepFileSnapshotChanged(initial, initial, initial.Size()-1, false) {
		t.Fatal("short non-budgeted read was not reported as changed")
	}
	if err := os.WriteFile(path, []byte("Target-more"), 0o644); err != nil {
		t.Fatal(err)
	}
	larger, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !grepFileSnapshotChanged(initial, larger, larger.Size(), false) {
		t.Fatal("size change was not detected")
	}
	if err := os.WriteFile(path, []byte("Other!"), 0o644); err != nil {
		t.Fatal(err)
	}
	changedAt := initial.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(path, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}
	sameSizeChanged, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !grepFileSnapshotChanged(initial, sameSizeChanged, sameSizeChanged.Size(), false) {
		t.Fatal("same-size modification was not detected")
	}
}
