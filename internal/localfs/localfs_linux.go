//go:build linux

package localfs

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const mountInfoLimit = 4 * 1024 * 1024

var boundedLinuxFilesystems = map[string]struct{}{
	"btrfs": {}, "erofs": {}, "ext2": {}, "ext3": {}, "ext4": {},
	"f2fs": {}, "jfs": {}, "nilfs2": {}, "overlay": {}, "ramfs": {},
	"reiserfs": {}, "squashfs": {}, "tmpfs": {}, "ubifs": {}, "xfs": {}, "zfs": {},
}

var boundedLinuxFilesystemMagic = map[int64]struct{}{
	unix.EXT4_SUPER_MAGIC:      {},
	unix.XFS_SUPER_MAGIC:       {},
	unix.BTRFS_SUPER_MAGIC:     {},
	unix.TMPFS_MAGIC:           {},
	unix.RAMFS_MAGIC:           {},
	unix.OVERLAYFS_SUPER_MAGIC: {},
	unix.SQUASHFS_MAGIC:        {},
	unix.F2FS_SUPER_MAGIC:      {},
}

type linuxMount struct {
	point      string
	filesystem string
}

type Inspector struct {
	mounts []linuxMount
}

func NewInspector() (*Inspector, error) {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, fmt.Errorf("inspect mounted filesystems: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, mountInfoLimit+1))
	if err != nil {
		return nil, fmt.Errorf("inspect mounted filesystems: %w", err)
	}
	return newLinuxInspector(data)
}

func newLinuxInspector(data []byte) (*Inspector, error) {
	if len(data) > mountInfoLimit {
		return nil, fmt.Errorf("inspect mounted filesystems: inventory exceeds %d bytes", mountInfoLimit)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), mountInfoLimit+1)
	mounts := make([]linuxMount, 0, 64)
	for scanner.Scan() {
		line := scanner.Text()
		before, after, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		fields := strings.Fields(before)
		afterFields := strings.Fields(after)
		if len(fields) < 5 || len(afterFields) < 1 {
			continue
		}
		mountPoint, decodeErr := decodeMountInfoPath(fields[4])
		if decodeErr != nil {
			return nil, decodeErr
		}
		mounts = append(mounts, linuxMount{point: mountPoint, filesystem: afterFields[0]})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("inspect mounted filesystems: %w", err)
	}
	return &Inspector{mounts: mounts}, nil
}

func EnsureBoundedPath(path string) error {
	inspector, err := NewInspector()
	if err != nil {
		return err
	}
	return inspector.EnsurePath(path)
}

func EnsureBoundedTree(path string) error {
	inspector, err := NewInspector()
	if err != nil {
		return err
	}
	return inspector.EnsureTree(path)
}

func (inspector *Inspector) EnsurePath(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	bestLength := -1
	bestType := ""
	ambiguous := false
	for _, mount := range inspector.mounts {
		if !containsPath(mount.point, absolute) || len(mount.point) < bestLength {
			continue
		}
		if len(mount.point) == bestLength {
			// Multiple mounts may be stacked at the same path. A static mountinfo
			// snapshot does not prove which layer is visible, so never let an
			// older local entry authorize reads through a later remote overmount.
			ambiguous = true
			continue
		}
		bestLength = len(mount.point)
		bestType = mount.filesystem
		ambiguous = false
	}
	if bestLength < 0 || ambiguous {
		return ErrUnboundedFilesystem
	}
	if _, ok := boundedLinuxFilesystems[bestType]; !ok {
		return fmt.Errorf("%w: %s", ErrUnboundedFilesystem, bestType)
	}
	return nil
}

func (inspector *Inspector) EnsureTree(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := inspector.EnsurePath(absolute); err != nil {
		return err
	}
	seen := make(map[string]struct{})
	for _, mount := range inspector.mounts {
		point := filepath.Clean(mount.point)
		if !containsPath(absolute, point) {
			continue
		}
		if _, ok := seen[point]; ok {
			return fmt.Errorf("%w: stacked mount beneath inspected tree", ErrUnboundedFilesystem)
		}
		seen[point] = struct{}{}
		if _, ok := boundedLinuxFilesystems[mount.filesystem]; !ok {
			return fmt.Errorf("%w: %s beneath inspected tree", ErrUnboundedFilesystem, mount.filesystem)
		}
	}
	return nil
}

func EnsureBoundedFile(file *os.File) error {
	if file == nil {
		return ErrUnboundedFilesystem
	}
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		return fmt.Errorf("inspect opened filesystem: %w", err)
	}
	if _, ok := boundedLinuxFilesystemMagic[stat.Type]; !ok {
		return fmt.Errorf("%w: filesystem type %#x", ErrUnboundedFilesystem, stat.Type)
	}
	return nil
}

func decodeMountInfoPath(value string) (string, error) {
	var result strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '\\' {
			result.WriteByte(value[index])
			continue
		}
		if index+3 >= len(value) {
			return "", errorsNewMalformedMountPath()
		}
		decoded, err := strconv.ParseUint(value[index+1:index+4], 8, 8)
		if err != nil {
			return "", errorsNewMalformedMountPath()
		}
		result.WriteByte(byte(decoded))
		index += 3
	}
	return result.String(), nil
}

func errorsNewMalformedMountPath() error {
	return fmt.Errorf("malformed mountinfo path")
}

func containsPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
