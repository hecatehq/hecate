//go:build darwin

package gitrunner

import (
	"path/filepath"
	"testing"
)

func TestPathWithinDirectoryDarwinCaseAndCanonicalAliases(t *testing.T) {
	t.Parallel()

	nfcRoot := filepath.Join(string(filepath.Separator), "Users", "Operator", "Caf\u00e9", "Repo")
	nfdRoot := filepath.Join(string(filepath.Separator), "users", "operator", "Cafe\u0301", "repo")
	for _, test := range []struct {
		name      string
		root      string
		candidate string
		want      bool
	}{
		{name: "case alias equality", root: nfcRoot, candidate: filepath.Join(string(filepath.Separator), "users", "operator", "CAF\u00c9", "repo"), want: true},
		{name: "NFC root NFD descendant", root: nfcRoot, candidate: filepath.Join(nfdRoot, "metadata"), want: true},
		{name: "NFD root NFC descendant", root: nfdRoot, candidate: filepath.Join(nfcRoot, "metadata"), want: true},
		{name: "lookalike sibling", root: nfcRoot, candidate: filepath.Join(string(filepath.Separator), "users", "operator", "Cafe\u0301", "repository"), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := pathWithinDirectory(test.root, test.candidate); got != test.want {
				t.Fatalf("pathWithinDirectory(%q, %q) = %v, want %v", test.root, test.candidate, got, test.want)
			}
		})
	}
}
