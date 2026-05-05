package agentchat

import (
	"sort"
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
	path = strings.TrimSpace(path)
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
			if to := strings.TrimSpace(strings.TrimPrefix(line, "rename to ")); to != "" {
				current.Path = to
			}
		case strings.HasPrefix(line, "Binary files "):
			current.Status = "binary"
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
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
	rest := strings.TrimSpace(strings.TrimPrefix(line, "diff --git "))
	left, right, ok := strings.Cut(rest, " b/")
	if ok {
		_ = left
		return strings.TrimSpace(right)
	}
	parts := strings.Fields(rest)
	if len(parts) >= 2 {
		return strings.TrimPrefix(parts[1], "b/")
	}
	if len(parts) == 1 {
		return strings.TrimPrefix(parts[0], "a/")
	}
	return ""
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
