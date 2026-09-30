package browserapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Candidate is local-only setup information. Its opaque ID binds a passive
// installation snapshot for selection; it does not authenticate a publisher.
type Candidate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type installation struct {
	name string
	path string
}

type discoveredCandidate struct {
	Candidate
	canonicalPath string
}

func discover(ctx context.Context, installations []installation) ([]discoveredCandidate, error) {
	result := make([]discoveredCandidate, 0)
	seen := make(map[string]bool)
	for _, installation := range installations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate, err := inspectInstallation(ctx, installation)
		if err != nil {
			continue
		}
		key := candidate.canonicalPath
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, candidate)
	}
	return result, ctx.Err()
}

// This is host configuration inspection, not workspace IO. Never invoke a
// candidate, even with --version: an installation path is not a safety verdict.
func inspectInstallation(ctx context.Context, installation installation) (discoveredCandidate, error) {
	if err := ctx.Err(); err != nil {
		return discoveredCandidate{}, err
	}
	path := strings.TrimSpace(installation.path)
	if !filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
		return discoveredCandidate{}, ErrCandidateChanged
	}
	path = filepath.Clean(path)
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(canonical) {
		return discoveredCandidate{}, ErrCandidateChanged
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		return discoveredCandidate{}, ErrCandidateChanged
	}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(canonical), ".exe") {
		return discoveredCandidate{}, ErrCandidateChanged
	}
	fileID, err := installationFileID(canonical, info)
	if err != nil {
		return discoveredCandidate{}, ErrCandidateChanged
	}
	// A second resolution/stat rejects replacement during passive discovery.
	// This is not handle-bound exec and does not close same-user path swaps.
	again, err := filepath.EvalSymlinks(path)
	if err != nil || again != canonical {
		return discoveredCandidate{}, ErrCandidateChanged
	}
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(info, current) || current.Mode() != info.Mode() || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
		return discoveredCandidate{}, ErrCandidateChanged
	}
	if err := ctx.Err(); err != nil {
		return discoveredCandidate{}, err
	}
	metadata := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%d", path, canonical, fileID, uint32(info.Mode()), info.Size(), info.ModTime().UnixNano())
	digest := sha256.Sum256([]byte(metadata))
	return discoveredCandidate{Candidate: Candidate{
		ID: "browser_" + hex.EncodeToString(digest[:]), Name: installation.name, Path: path,
	}, canonicalPath: canonical}, nil
}

func defaultInstallations() []installation {
	home, _ := os.UserHomeDir()
	return platformInstallations(runtime.GOOS, home, os.Getenv)
}

// Enumerate bounded known installation locations, never cwd, arbitrary PATH
// entries, recursive scans, shell discovery, or package-manager subprocesses.
func platformInstallations(goos, home string, getenv func(string) string) []installation {
	var result []installation
	add := func(name, root, relative string) {
		if root != "" {
			result = append(result, installation{name, filepath.Join(root, relative)})
		}
	}
	switch goos {
	case "darwin":
		for _, root := range []string{"/Applications", filepath.Join(home, "Applications")} {
			if root == "Applications" {
				continue
			}
			for _, app := range []struct{ name, binary string }{
				{"Google Chrome", "Google Chrome"}, {"Chromium", "Chromium"},
				{"Microsoft Edge", "Microsoft Edge"}, {"Brave Browser", "Brave Browser"},
			} {
				add(app.name, root, app.name+".app/Contents/MacOS/"+app.binary)
			}
		}
	case "linux":
		for _, entry := range []installation{
			{"Google Chrome", "/usr/bin/google-chrome"}, {"Google Chrome", "/usr/bin/google-chrome-stable"},
			{"Google Chrome", "/opt/google/chrome/chrome"},
			{"Chromium", "/usr/bin/chromium"}, {"Chromium", "/usr/bin/chromium-browser"},
			{"Chromium", "/usr/lib/chromium/chromium"}, {"Chromium", "/snap/bin/chromium"},
			{"Microsoft Edge", "/usr/bin/microsoft-edge"}, {"Microsoft Edge", "/usr/bin/microsoft-edge-stable"},
			{"Microsoft Edge", "/opt/microsoft/msedge/msedge"},
			{"Brave Browser", "/usr/bin/brave-browser"}, {"Brave Browser", "/opt/brave.com/brave/brave"},
		} {
			result = append(result, entry)
		}
	case "windows":
		for _, root := range []string{getenv("ProgramFiles"), getenv("ProgramFiles(x86)"), getenv("LOCALAPPDATA")} {
			add("Google Chrome", root, "Google/Chrome/Application/chrome.exe")
			add("Chromium", root, "Chromium/Application/chrome.exe")
			add("Microsoft Edge", root, "Microsoft/Edge/Application/msedge.exe")
			add("Brave Browser", root, "BraveSoftware/Brave-Browser/Application/brave.exe")
		}
	}
	return result
}
