//go:build windows

package codeintel

func processExists(int) bool { return false }
