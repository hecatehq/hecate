//go:build darwin

package localfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type Inspector struct {
	mounts []unix.Statfs_t
}

const darwinMountLimit = 4096

func NewInspector() (*Inspector, error) {
	count, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, fmt.Errorf("inspect mounted filesystems: %w", err)
	}
	if count <= 0 || count > darwinMountLimit {
		return nil, ErrUnboundedFilesystem
	}
	mounts := make([]unix.Statfs_t, count)
	count, err = unix.Getfsstat(mounts, unix.MNT_NOWAIT)
	if err != nil {
		return nil, fmt.Errorf("inspect mounted filesystems: %w", err)
	}
	if count < 0 || count > len(mounts) {
		return nil, ErrUnboundedFilesystem
	}
	return &Inspector{mounts: mounts[:count]}, nil
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
	bounded := false
	ambiguous := false
	for _, mount := range inspector.mounts {
		mountPoint := cString(mount.Mntonname[:])
		mountKey := darwinComparablePath(mountPoint)
		if !PathWithinDirectory(mountPoint, absolute) || len(mountKey) < bestLength {
			continue
		}
		if len(mountKey) == bestLength {
			// Stacked mounts share a mount point. Fail closed instead of trusting
			// a possibly hidden local entry from this point-in-time inventory.
			ambiguous = true
			continue
		}
		bestLength = len(mountKey)
		bounded = boundedDarwinFilesystem(mount)
		ambiguous = false
	}
	if bestLength < 0 || ambiguous || !bounded {
		return ErrUnboundedFilesystem
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
		point := filepath.Clean(cString(mount.Mntonname[:]))
		if !PathWithinDirectory(absolute, point) {
			continue
		}
		key := darwinComparablePath(point)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("%w: stacked mount beneath inspected tree", ErrUnboundedFilesystem)
		}
		seen[key] = struct{}{}
		if !boundedDarwinFilesystem(mount) {
			return fmt.Errorf("%w: unsupported mount beneath inspected tree", ErrUnboundedFilesystem)
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
	if !boundedDarwinFilesystem(stat) {
		return ErrUnboundedFilesystem
	}
	return nil
}

func boundedDarwinFilesystem(stat unix.Statfs_t) bool {
	if stat.Flags&unix.MNT_LOCAL == 0 {
		return false
	}
	switch strings.ToLower(cString(stat.Fstypename[:])) {
	case "apfs", "hfs", "msdos", "exfat":
		return true
	default:
		return false
	}
}

func cString(value []byte) string {
	if index := strings.IndexByte(string(value), 0); index >= 0 {
		value = value[:index]
	}
	return string(value)
}

// PathWithinDirectory reports whether candidate is root or one of its
// descendants under Darwin's conservative path-comparison posture.
func PathWithinDirectory(root, candidate string) bool {
	// Most Darwin installations use case-insensitive APFS or HFS paths whose
	// lookups also treat canonically equivalent Unicode spellings as identical.
	// Conservatively normalize and fold both sides so alternate case or NFC/NFD
	// spelling cannot hide a nested remote/FUSE mount. False rejection on a
	// stricter volume is safer than pre-opening an unclassified target.
	relative, err := filepath.Rel(darwinComparablePath(root), darwinComparablePath(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func darwinComparablePath(path string) string {
	decomposed := norm.NFD.String(filepath.Clean(path))
	return norm.NFD.String(cases.Fold().String(decomposed))
}
