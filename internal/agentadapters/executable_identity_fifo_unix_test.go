//go:build darwin || linux

package agentadapters

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestMeasureExecutableIdentity_RegularFileReplacementWithFIFODoesNotBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent")
	writeExecutableIdentityFixture(t, path, append(nativeExecutableFixturePrefix(), []byte("before")...))

	done := make(chan error, 1)
	go func() {
		var mutationErr error
		_, err := measureExecutableIdentityWithHooks(path, func() {
			if mutationErr = os.Remove(path); mutationErr != nil {
				return
			}
			mutationErr = unix.Mkfifo(path, 0o700)
		}, nil)
		if mutationErr != nil {
			done <- mutationErr
			return
		}
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrExecutableIdentityUnavailable) {
			t.Fatalf("error = %v, want ErrExecutableIdentityUnavailable", err)
		}
	case <-time.After(2 * time.Second):
		// Unblock a regressed blocking reader so the test process does not retain
		// a goroutine after reporting the bounded-open failure.
		fd, _ := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0)
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		t.Fatal("executable identity measurement blocked while opening a FIFO")
	}
}
