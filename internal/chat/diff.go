package chat

import (
	"sort"
	"strconv"
	"strings"
)

type ChangedFile struct {
	Path      string
	Additions int
	Deletions int
	Status    string
	Diff      string
}

func ParseChangedFiles(diff, diffStat string) []ChangedFile {
	files := parseUnifiedDiffFiles(diff)
	if len(files) == 0 {
		files = parseDiffStatFiles(diffStat)
	}
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files
}

func ExtractFileDiff(diff, path string) (ChangedFile, bool) {
	if path == "" {
		return ChangedFile{}, false
	}
	for _, file := range parseUnifiedDiffFiles(diff) {
		if file.Path == path {
			return file, true
		}
	}
	return ChangedFile{}, false
}

func parseUnifiedDiffFiles(diff string) []ChangedFile {
	lines := strings.Split(diff, "\n")
	var out []ChangedFile
	var current *ChangedFile
	var block []string
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			if current != nil {
				current.Diff = strings.TrimRight(strings.Join(block, "\n"), "\n")
				out = append(out, *current)
			}
			current = &ChangedFile{Path: parseDiffGitPath(line), Status: "modified"}
			block = []string{line}
			continue
		}
		if current == nil {
			continue
		}
		block = append(block, line)
		switch {
		case strings.HasPrefix(line, "new file mode "):
			current.Status = "added"
		case strings.HasPrefix(line, "deleted file mode "):
			current.Status = "deleted"
		case strings.HasPrefix(line, "rename from "):
			current.Status = "renamed"
		case strings.HasPrefix(line, "rename to "):
			if to, ok := parseGitPathField(strings.TrimPrefix(line, "rename to "), ""); ok {
				current.Path = to
			}
		case strings.HasPrefix(line, "copy from "):
			current.Status = "copied"
		case strings.HasPrefix(line, "copy to "):
			if to, ok := parseGitPathField(strings.TrimPrefix(line, "copy to "), ""); ok {
				current.Path = to
			}
		case strings.HasPrefix(line, "Binary files "):
			current.Status = "binary"
		case strings.HasPrefix(line, "--- "):
			if from, ok := parseGitPathField(strings.TrimPrefix(line, "--- "), "a/"); ok {
				current.Path = from
			}
		case strings.HasPrefix(line, "+++ "):
			if to, ok := parseGitPathField(strings.TrimPrefix(line, "+++ "), "b/"); ok {
				current.Path = to
			}
			continue
		case strings.HasPrefix(line, "+"):
			current.Additions++
		case strings.HasPrefix(line, "-"):
			current.Deletions++
		}
	}
	if current != nil {
		current.Diff = strings.TrimRight(strings.Join(block, "\n"), "\n")
		out = append(out, *current)
	}
	for i := range out {
		if out[i].Path == "" {
			out[i].Path = "unknown"
		}
	}
	return out
}

func parseDiffGitPath(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	if strings.HasPrefix(rest, "\"") {
		_, remaining, ok := consumeQuotedGitPath(rest)
		if ok {
			if path, ok := parseGitPathField(strings.TrimLeft(remaining, " "), "b/"); ok {
				return path
			}
		}
	}
	// Unquoted Git paths may contain spaces, including the literal delimiter
	// text " b/". Prefer the split whose a/ and b/ payloads are identical;
	// rename/copy blocks later replace this fallback from their explicit
	// extended headers.
	for offset := 0; offset < len(rest); {
		relative := strings.Index(rest[offset:], " b/")
		if relative < 0 {
			break
		}
		split := offset + relative
		left := rest[:split]
		right := rest[split+1:]
		if strings.HasPrefix(left, "a/") && strings.HasPrefix(right, "b/") && left[2:] == right[2:] {
			return right[2:]
		}
		offset = split + 1
	}
	if quotedRight := strings.LastIndex(rest, " \"b/"); quotedRight >= 0 {
		if path, ok := parseGitPathField(rest[quotedRight+1:], "b/"); ok {
			return path
		}
	}
	if split := strings.Index(rest, " b/"); split >= 0 {
		return strings.TrimPrefix(rest[split+1:], "b/")
	}
	if strings.HasPrefix(rest, "a/") {
		return rest[2:]
	}
	return ""
}

// parseGitPathField decodes one Git path field and removes its synthetic a/
// or b/ prefix. Git uses C-style quoting for control characters and non-ASCII
// bytes; strconv.Unquote matches that representation, including octal escapes.
func parseGitPathField(field, prefix string) (string, bool) {
	if field == "" || field == "/dev/null" {
		return "", false
	}
	path := field
	if strings.HasPrefix(field, "\"") {
		decoded, remaining, ok := consumeQuotedGitPath(field)
		if !ok || strings.TrimSpace(remaining) != "" {
			return "", false
		}
		path = decoded
	} else if value, _, found := strings.Cut(field, "\t"); found {
		// Git terminates an unquoted ---/+++ path containing whitespace
		// with a tab (the generic unified-diff format may follow it with a
		// timestamp). Literal tabs in names are C-quoted, so this delimiter
		// is not part of the path.
		path = value
	}
	if prefix != "" {
		if !strings.HasPrefix(path, prefix) {
			return "", false
		}
		path = strings.TrimPrefix(path, prefix)
	}
	if path == "" {
		return "", false
	}
	return path, true
}

func consumeQuotedGitPath(value string) (string, string, bool) {
	if !strings.HasPrefix(value, "\"") {
		return "", value, false
	}
	escaped := false
	for i := 1; i < len(value); i++ {
		switch {
		case escaped:
			escaped = false
		case value[i] == '\\':
			escaped = true
		case value[i] == '"':
			decoded, err := strconv.Unquote(value[:i+1])
			if err != nil {
				return "", value, false
			}
			return decoded, value[i+1:], true
		}
	}
	return "", value, false
}

func parseDiffStatFiles(diffStat string) []ChangedFile {
	var out []ChangedFile
	for _, line := range strings.Split(diffStat, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, " files changed") || strings.Contains(line, " file changed") {
			continue
		}
		path, change, ok := strings.Cut(line, "|")
		if !ok {
			continue
		}
		item := ChangedFile{Path: strings.TrimSpace(path), Status: "modified"}
		for _, r := range change {
			switch r {
			case '+':
				item.Additions++
			case '-':
				item.Deletions++
			}
		}
		if item.Path != "" {
			out = append(out, item)
		}
	}
	return out
}
