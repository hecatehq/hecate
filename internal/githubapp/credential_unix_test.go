//go:build darwin || linux

package githubapp

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestClient_RejectsFIFOWithoutBlocking(t *testing.T) {
	c, _ := testClient(t)
	path := filepath.Join(t.TempDir(), "credential")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	c.config.PrivateKeyFile = path
	done := make(chan error, 1)
	go func() {
		_, err := c.Inspect(t.Context(), "https://github.com/owner/repo/pull/7")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrCredential) || len(c.slots) != 0 {
			t.Fatalf("error=%v occupied slots=%d", err, len(c.slots))
		}
	case <-time.After(2 * time.Second):
		fd, _ := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0)
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		t.Fatal("credential open blocked on FIFO")
	}
	// The lower-level opener also must not block if a regular path is replaced
	// with a FIFO after the caller's pre-open check.
	f, err := openCredentialFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Mode().IsRegular() {
		t.Fatalf("FIFO descriptor type: %v, %v", info, err)
	}
}

func TestClient_RejectsSymlinkCredential(t *testing.T) {
	c, _ := testClient(t)
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.Symlink(c.config.PrivateKeyFile, path); err != nil {
		t.Fatal(err)
	}
	c.config.PrivateKeyFile = path
	if _, err := c.jwt(); !errors.Is(err, ErrCredential) {
		t.Fatalf("accepted symlink: %v", err)
	}
	if f, err := openCredentialFile(path); err == nil {
		f.Close()
		t.Fatal("opener followed symlink")
	}
}
