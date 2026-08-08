//go:build darwin

package workspacecoord

import (
	"path/filepath"
	"testing"
)

func TestCanonicalKeysOverlapDarwinCanonicalEquivalentEquality(t *testing.T) {
	t.Parallel()

	nfc := filepath.Join(string(filepath.Separator), "Users", "Operator", "Caf\u00e9", "Repo")
	nfd := filepath.Join(string(filepath.Separator), "users", "operator", "Cafe\u0301", "repo")
	if !CanonicalKeysOverlap(nfc, nfd) {
		t.Fatalf("CanonicalKeysOverlap(%q, %q) = false, want true", nfc, nfd)
	}
	if !CanonicalKeysOverlap(nfd, nfc) {
		t.Fatalf("CanonicalKeysOverlap(%q, %q) = false, want true", nfd, nfc)
	}
}

func TestCanonicalKeysOverlapDarwinCanonicalEquivalentAncestor(t *testing.T) {
	t.Parallel()

	parent := filepath.Join(string(filepath.Separator), "Users", "Operator", "Caf\u00e9", "Repo")
	descendant := filepath.Join(string(filepath.Separator), "users", "operator", "Cafe\u0301", "repo", "packages", "app")
	if !CanonicalKeysOverlap(parent, descendant) {
		t.Fatalf("CanonicalKeysOverlap(%q, %q) = false, want true", parent, descendant)
	}
	if !CanonicalKeysOverlap(descendant, parent) {
		t.Fatalf("CanonicalKeysOverlap(%q, %q) = false, want true", descendant, parent)
	}

	sibling := filepath.Join(string(filepath.Separator), "users", "operator", "Cafe\u0301", "repository")
	if CanonicalKeysOverlap(parent, sibling) {
		t.Fatalf("CanonicalKeysOverlap(%q, %q) = true, want false", parent, sibling)
	}
}
