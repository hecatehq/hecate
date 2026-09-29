// browser-sentinel is an inert executable for approval-boundary tests. Any
// invocation is a failure signal, never a browser or personal profile launch.
package main

import "os"

func main() {
	if marker := os.Getenv("HECATE_E2E_BROWSER_MARKER"); marker != "" {
		_ = os.WriteFile(marker, []byte("browser launched"), 0o600)
	}
	os.Exit(1)
}
