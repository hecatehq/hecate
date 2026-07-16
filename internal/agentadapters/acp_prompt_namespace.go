package agentadapters

import (
	"path/filepath"
	"strings"
	"sync"
)

// acpPromptStageNamespace is the body-free filesystem fence for one private
// prompt stage. Unlike the active turn, it remains owned by the client until
// handle-authoritative cleanup has proved the stage is gone.
type acpPromptStageNamespace struct {
	mu        sync.RWMutex
	dirs      []string
	register  func(string)
	release   func()
	releaseMu sync.Once
}

func (n *acpPromptStageNamespace) registerDirectory(dir string) {
	if n == nil {
		return
	}
	if n.register != nil {
		n.register(dir)
		return
	}
	n.addDirectory(dir)
}

func newACPPromptStageNamespace(dir string) *acpPromptStageNamespace {
	namespace := &acpPromptStageNamespace{}
	namespace.addDirectory(dir)
	return namespace
}

func (n *acpPromptStageNamespace) addDirectory(dir string) {
	if n == nil {
		return
	}
	dir = filepath.Clean(dir)
	if dir == "." || !filepath.IsAbs(dir) {
		return
	}
	dirs := []string{dir}
	if canonical, err := filepath.EvalSymlinks(dir); err == nil && filepath.IsAbs(canonical) {
		dirs = append(dirs, filepath.Clean(canonical))
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, candidate := range dirs {
		seen := false
		for _, existing := range n.dirs {
			if acpPromptAliasEqual(candidate, existing) {
				seen = true
				break
			}
		}
		if !seen {
			n.dirs = append(n.dirs, candidate)
		}
	}
}

func (n *acpPromptStageNamespace) contains(path string) bool {
	if n == nil {
		return false
	}
	path = cleanACPReadPath(path)
	if path == "" {
		return false
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	for _, dir := range n.dirs {
		if acpPromptPathWithin(path, dir) {
			return true
		}
	}
	return false
}

func (n *acpPromptStageNamespace) markRemoved() {
	if n == nil {
		return
	}
	n.releaseMu.Do(func() {
		if n.release != nil {
			n.release()
		}
	})
}

// acpPromptStageCallbackPaths derives every absolute spelling under which a
// callback could reach a private stage through WorkspaceFS. The workspace root
// is normally canonicalized at admission, but retaining both lexical and
// canonical spellings keeps the deny boundary correct for probes and tests that
// construct a client directly with a symlinked root.
func acpPromptStageCallbackPaths(value, workspace string) []string {
	var paths []string
	add := func(path string) {
		path = filepath.Clean(path)
		if path == "." || !filepath.IsAbs(path) {
			return
		}
		for _, existing := range paths {
			if acpPromptAliasEqual(existing, path) {
				return
			}
		}
		paths = append(paths, path)
	}

	lexicalRoot, rootErr := filepath.Abs(filepath.Clean(workspace))
	canonicalRoot := ""
	if rootErr == nil && filepath.IsAbs(lexicalRoot) {
		if canonical, err := filepath.EvalSymlinks(lexicalRoot); err == nil && filepath.IsAbs(canonical) {
			canonicalRoot = filepath.Clean(canonical)
		}
	} else {
		lexicalRoot = ""
	}

	if direct := cleanACPReadPath(value); direct != "" {
		add(direct)
		// EvalSymlinks cannot canonicalize a quarantined original pathname
		// because its leaf no longer exists. Map paths between the lexical and
		// canonical workspace roots explicitly so those old aliases remain
		// denied until handle-bound removal proof.
		if relative, ok := acpPromptPathRelativeToRoot(direct, lexicalRoot); ok && canonicalRoot != "" {
			add(filepath.Join(canonicalRoot, relative))
		}
		if relative, ok := acpPromptPathRelativeToRoot(direct, canonicalRoot); ok && lexicalRoot != "" {
			add(filepath.Join(lexicalRoot, relative))
		}
	} else {
		relative := filepath.FromSlash(strings.TrimSpace(value))
		if relative == "" || !filepath.IsLocal(relative) || lexicalRoot == "" {
			return nil
		}
		add(filepath.Join(lexicalRoot, relative))
		if canonicalRoot != "" {
			add(filepath.Join(canonicalRoot, relative))
		}
	}

	// Resolve complete candidates when they currently exist. This adds a
	// defense-only comparison spelling and does not relax WorkspaceFS's own
	// rejection of symlink components.
	initial := append([]string(nil), paths...)
	for _, path := range initial {
		if canonical, err := filepath.EvalSymlinks(path); err == nil {
			add(canonical)
		}
	}
	return paths
}

func acpPromptPathRelativeToRoot(path, root string) (string, bool) {
	if path == "" || root == "" {
		return "", false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return "", false
	}
	return relative, true
}
