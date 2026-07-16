//go:build linux

package agentadapters

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

var acpPromptPOSIXACLAttributes = []string{
	"system.posix_acl_access",
	"system.posix_acl_default",
}

// Linux exposes multiple incompatible access-control models through the same
// filesystem APIs. Keep resource-link staging on filesystems whose effective
// access is represented by Unix mode bits and, when supported, the POSIX ACL
// xattrs checked below. Unknown, network, and FUSE filesystems fail closed
// instead of treating an unsupported POSIX ACL xattr as proof that no broader
// grant exists.
func supportedACPPromptLinuxFilesystem(magic uint32) bool {
	switch magic {
	case uint32(unix.EXT4_SUPER_MAGIC): // ext2, ext3, and ext4 share this magic.
		return true
	case uint32(unix.XFS_SUPER_MAGIC),
		uint32(unix.BTRFS_SUPER_MAGIC),
		uint32(unix.TMPFS_MAGIC),
		uint32(unix.OVERLAYFS_SUPER_MAGIC),
		uint32(unix.RAMFS_MAGIC),
		uint32(unix.F2FS_SUPER_MAGIC):
		return true
	default:
		return false
	}
}

func verifyPrivateACPPromptLinuxFilesystem(file *os.File) error {
	if file == nil {
		return errors.New("private staged prompt input is nil")
	}
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		return fmt.Errorf("inspect private staged prompt input filesystem: %w", err)
	}
	magic := uint32(stat.Type)
	if !supportedACPPromptLinuxFilesystem(magic) {
		return fmt.Errorf("private staged prompt input filesystem %#x is unsupported", magic)
	}
	return nil
}

func stripPrivateACPPromptStageACL(file *os.File, wantDir bool) error {
	if file == nil {
		return errors.New("private staged prompt input is nil")
	}
	if err := verifyPrivateACPPromptLinuxFilesystem(file); err != nil {
		return err
	}
	attributes := acpPromptPOSIXACLAttributes[:1]
	if wantDir {
		attributes = acpPromptPOSIXACLAttributes
	}
	for _, attribute := range attributes {
		if err := unix.Fremovexattr(int(file.Fd()), attribute); err != nil &&
			!errors.Is(err, unix.ENODATA) && !errors.Is(err, unix.ENOTSUP) && !errors.Is(err, unix.EOPNOTSUPP) {
			return err
		}
	}
	return verifyPrivateACPPromptStageACL(file, wantDir)
}

func verifyPrivateACPPromptStageACL(file *os.File, wantDir bool) error {
	if file == nil {
		return errors.New("private staged prompt input is nil")
	}
	if err := verifyPrivateACPPromptLinuxFilesystem(file); err != nil {
		return err
	}
	attributes := acpPromptPOSIXACLAttributes[:1]
	if wantDir {
		attributes = acpPromptPOSIXACLAttributes
	}
	for _, attribute := range attributes {
		if _, err := unix.Fgetxattr(int(file.Fd()), attribute, nil); err == nil {
			return errors.New("private staged prompt input has a POSIX ACL")
		} else if !errors.Is(err, unix.ENODATA) && !errors.Is(err, unix.ENOTSUP) && !errors.Is(err, unix.EOPNOTSUPP) {
			return err
		}
	}
	return nil
}

func renamePrivateACPPromptStageNoReplace(parentFD int, oldName, newName string) error {
	return unix.Renameat2(parentFD, oldName, parentFD, newName, unix.RENAME_NOREPLACE)
}
