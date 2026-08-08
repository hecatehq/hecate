//go:build !darwin

package workspacecoord

import "strings"

func pathPartEqualFold(first, second string) bool {
	return strings.EqualFold(first, second)
}
