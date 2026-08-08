//go:build darwin

package workspacecoord

import (
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

func pathPartEqualFold(first, second string) bool {
	return darwinComparablePathPart(first) == darwinComparablePathPart(second)
}

func darwinComparablePathPart(part string) string {
	// APFS and HFS commonly treat canonically equivalent Unicode spellings as
	// the same path. Normalize before and after Unicode case folding so an NFC
	// alias cannot enter a separate coordination domain from its NFD spelling.
	decomposed := norm.NFD.String(part)
	return norm.NFD.String(cases.Fold().String(decomposed))
}
