//go:build darwin

package localfs

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinInspectorRejectsNestedNonNativeMount(t *testing.T) {
	local := darwinMount("/", "apfs", unix.MNT_LOCAL)
	fuse := darwinMount("/workspace/remote", "macfuse", unix.MNT_LOCAL)
	inspector := &Inspector{mounts: []unix.Statfs_t{local, fuse}}
	if err := inspector.EnsurePath("/workspace/local.txt"); err != nil {
		t.Fatalf("local APFS path: %v", err)
	}
	if err := inspector.EnsurePath("/workspace/remote/file.txt"); !errors.Is(err, ErrUnboundedFilesystem) {
		t.Fatalf("nested FUSE path error = %v", err)
	}
}

func TestDarwinInspectorRejectsUnboundedDescendantMount(t *testing.T) {
	local := darwinMount("/", "apfs", unix.MNT_LOCAL)
	fuse := darwinMount("/workspace/remote", "macfuse", unix.MNT_LOCAL)
	inspector := &Inspector{mounts: []unix.Statfs_t{local, fuse}}
	if err := inspector.EnsureTree("/workspace"); !errors.Is(err, ErrUnboundedFilesystem) {
		t.Fatalf("workspace tree error = %v, want nested FUSE refusal", err)
	}
	if err := inspector.EnsureTree("/workspace/local"); err != nil {
		t.Fatalf("unrelated local subtree error = %v", err)
	}
}

func TestDarwinInspectorRejectsStackedMountPoint(t *testing.T) {
	local := darwinMount("/", "apfs", unix.MNT_LOCAL)
	data := darwinMount("/workspace", "apfs", unix.MNT_LOCAL)
	fuse := darwinMount("/workspace", "macfuse", unix.MNT_LOCAL)
	inspector := &Inspector{mounts: []unix.Statfs_t{local, data, fuse}}
	if err := inspector.EnsurePath("/workspace/file.txt"); !errors.Is(err, ErrUnboundedFilesystem) {
		t.Fatalf("stacked mount error = %v", err)
	}
}

func TestDarwinInspectorMatchesNestedMountCaseInsensitively(t *testing.T) {
	local := darwinMount("/", "apfs", unix.MNT_LOCAL)
	fuse := darwinMount("/Volumes/Remote", "macfuse", unix.MNT_LOCAL)
	inspector := &Inspector{mounts: []unix.Statfs_t{local, fuse}}
	if err := inspector.EnsurePath("/volumes/remote/file.txt"); !errors.Is(err, ErrUnboundedFilesystem) {
		t.Fatalf("alternate-case nested FUSE path error = %v", err)
	}
}

func TestDarwinInspectorMatchesNestedMountCanonicalUnicode(t *testing.T) {
	local := darwinMount("/", "apfs", unix.MNT_LOCAL)
	for _, test := range []struct {
		name      string
		mountPath string
		queryPath string
	}{
		{name: "NFC mount and NFD query", mountPath: "/Volumes/Caf\u00e9", queryPath: "/Volumes/Cafe\u0301/workspace"},
		{name: "NFD mount and NFC query", mountPath: "/Volumes/Cafe\u0301", queryPath: "/Volumes/Caf\u00e9/workspace"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fuse := darwinMount(test.mountPath, "macfuse", unix.MNT_LOCAL)
			inspector := &Inspector{mounts: []unix.Statfs_t{local, fuse}}
			if err := inspector.EnsurePath(test.queryPath); !errors.Is(err, ErrUnboundedFilesystem) {
				t.Fatalf("canonically equivalent nested FUSE path error = %v", err)
			}
			if err := inspector.EnsureTree("/Volumes"); !errors.Is(err, ErrUnboundedFilesystem) {
				t.Fatalf("canonically equivalent descendant FUSE tree error = %v", err)
			}
		})
	}
}

func darwinMount(point, filesystem string, flags uint32) unix.Statfs_t {
	var stat unix.Statfs_t
	stat.Flags = flags
	copy(stat.Mntonname[:], point)
	copy(stat.Fstypename[:], filesystem)
	return stat
}
