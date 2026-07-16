//go:build linux

package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/sandbox"
)

func TestLocalTerminal_RejectsRealSetsidBeforeEscapedWriterStarts(t *testing.T) {
	setsid, err := exec.LookPath("setsid")
	if err != nil {
		t.Skipf("setsid unavailable: %v", err)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "escaped-writer")
	script := fmt.Sprintf(`%q sh -c 'sleep 0.05; printf escaped > "$1"' sh %q &`, setsid, marker)
	_, err = NewLocalWorkspace().OpenTerminal(context.Background(), TerminalOptions{
		Command:          "sh",
		Args:             []string{"-c", script},
		WorkingDirectory: dir,
		Policy:           Policy{AllowedRoot: dir},
	})
	if !sandbox.IsPolicyDenied(err) {
		t.Fatalf("OpenTerminal error = %T %v, want pre-spawn PolicyError", err, err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("escaped writer marker exists or is unreadable: %v", err)
	}
}
