//go:build linux

package agentadapters

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestACPPromptStageLinuxFilesystemAllowlist(t *testing.T) {
	t.Parallel()

	accepted := map[string]uint32{
		"ext":     uint32(unix.EXT4_SUPER_MAGIC),
		"xfs":     uint32(unix.XFS_SUPER_MAGIC),
		"btrfs":   uint32(unix.BTRFS_SUPER_MAGIC),
		"tmpfs":   uint32(unix.TMPFS_MAGIC),
		"overlay": uint32(unix.OVERLAYFS_SUPER_MAGIC),
		"ramfs":   uint32(unix.RAMFS_MAGIC),
		"f2fs":    uint32(unix.F2FS_SUPER_MAGIC),
	}
	for name, magic := range accepted {
		magic := magic
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if !supportedACPPromptLinuxFilesystem(magic) {
				t.Fatalf("filesystem magic %#x rejected", magic)
			}
		})
	}

	rejected := map[string]uint32{
		"nfs":     uint32(unix.NFS_SUPER_MAGIC),
		"cifs":    uint32(unix.CIFS_SUPER_MAGIC),
		"fuse":    uint32(unix.FUSE_SUPER_MAGIC),
		"unknown": 0x13579bdf,
	}
	for name, magic := range rejected {
		magic := magic
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if supportedACPPromptLinuxFilesystem(magic) {
				t.Fatalf("filesystem magic %#x accepted", magic)
			}
		})
	}
}

func TestACPPromptStageLinuxAcceptsTempFilesystem(t *testing.T) {
	t.Parallel()

	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open temporary directory: %v", err)
	}
	defer dir.Close()
	if err := verifyPrivateACPPromptLinuxFilesystem(dir); err != nil {
		t.Fatalf("temporary filesystem rejected: %v", err)
	}
}
