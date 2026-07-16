//go:build darwin || linux

package agentadapters

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

type acpPromptStageIdentity struct {
	dir               *os.File
	dirInfo           os.FileInfo
	parent            *os.File
	parentInfo        os.FileInfo
	canonicalParent   string
	originalName      string
	currentName       string
	pendingQuarantine string
	onQuarantine      func(string)
	ancestors         []acpPromptStageAncestor
	children          map[string]acpPromptStageChild
}

type acpPromptStageAncestor struct {
	file *os.File
	info os.FileInfo
	path string
}

type acpPromptStageChild struct {
	file *os.File
	info os.FileInfo
}

func createPrivateACPPromptStageDir() (string, *acpPromptStageIdentity, error) {
	parentPath, err := filepath.Abs(os.TempDir())
	if err != nil {
		return "", nil, err
	}
	return createPrivateACPPromptStageDirAt(parentPath, nil)
}

func createPrivateACPPromptStageDirAt(parentPath string, afterParentOpen func() error) (string, *acpPromptStageIdentity, error) {
	var err error
	parentPath, err = filepath.EvalSymlinks(parentPath)
	if err != nil || !filepath.IsAbs(parentPath) {
		return "", nil, errors.New("resolve ACP prompt staging parent")
	}
	parent, err := os.Open(parentPath)
	if err != nil {
		return "", nil, err
	}
	parentInfo, err := parent.Stat()
	if err != nil || !parentInfo.IsDir() {
		_ = parent.Close()
		return "", nil, errors.New("inspect ACP prompt staging parent")
	}
	if err := verifyPrivateACPPromptTempParent(parent, parentInfo); err != nil {
		_ = parent.Close()
		return "", nil, err
	}
	if err := verifyPathMatchesOpenFile(parentPath, parent, parentInfo); err != nil {
		_ = parent.Close()
		return "", nil, errors.New("ACP prompt staging parent identity changed")
	}
	ancestors, err := openPrivateACPPromptStageAncestors(filepath.Dir(parentPath))
	if err != nil {
		_ = parent.Close()
		return "", nil, err
	}
	if afterParentOpen != nil {
		if err := afterParentOpen(); err != nil {
			closePrivateACPPromptStageAncestors(ancestors)
			_ = parent.Close()
			return "", nil, err
		}
	}
	name, err := randomACPPromptStageName("hecate-acp-input-")
	if err != nil {
		closePrivateACPPromptStageAncestors(ancestors)
		_ = parent.Close()
		return "", nil, err
	}
	if err := unix.Mkdirat(int(parent.Fd()), name, 0o700); err != nil {
		closePrivateACPPromptStageAncestors(ancestors)
		_ = parent.Close()
		return "", nil, err
	}
	dir := filepath.Join(parentPath, name)
	fail := func(cause error, stageDir *os.File) (string, *acpPromptStageIdentity, error) {
		if stageDir != nil {
			_ = stageDir.Close()
		}
		if cleanupErr := unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR); cleanupErr != nil && !errors.Is(cleanupErr, unix.ENOENT) {
			cause = errors.Join(cause, errors.New("remove private staged prompt input after create failure"))
		}
		closePrivateACPPromptStageAncestors(ancestors)
		_ = parent.Close()
		return "", nil, cause
	}
	stageFD, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail(err, nil)
	}
	stageDir := os.NewFile(uintptr(stageFD), name)
	if err := stripPrivateACPPromptStageACL(stageDir, true); err != nil {
		return fail(err, stageDir)
	}
	if err := stageDir.Chmod(0o700); err != nil {
		return fail(err, stageDir)
	}
	dirInfo, err := stageDir.Stat()
	if err != nil {
		return fail(err, stageDir)
	}
	if err := verifyPrivateACPPromptStageInfo(stageDir, dirInfo, true, 0o700); err != nil {
		return fail(err, stageDir)
	}
	identity := &acpPromptStageIdentity{
		dir:             stageDir,
		dirInfo:         dirInfo,
		parent:          parent,
		parentInfo:      parentInfo,
		canonicalParent: parentPath,
		originalName:    name,
		currentName:     name,
		ancestors:       ancestors,
	}
	if err := verifyPrivateACPPromptStageIdentity(dir, identity); err != nil {
		return fail(err, stageDir)
	}
	return dir, identity, nil
}

func verifyPrivateACPPromptTempParent(parent *os.File, info os.FileInfo) error {
	if parent == nil || info == nil || !info.IsDir() {
		return errors.New("ACP prompt staging parent is not a directory")
	}
	return verifyPrivateACPPromptTrustedDirectory(parent, info)
}

func verifyPrivateACPPromptTrustedDirectory(file *os.File, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) {
		return errors.New("ACP prompt staging directory has an untrusted owner")
	}
	mode := info.Mode()
	if mode.Perm()&0o022 != 0 && mode&os.ModeSticky == 0 {
		return errors.New("ACP prompt staging directory is writable without sticky protection")
	}
	if err := verifyPrivateACPPromptStageACL(file, true); err != nil {
		return errors.New("ACP prompt staging directory has an unsafe extended ACL")
	}
	return nil
}

func openPrivateACPPromptStageAncestors(path string) ([]acpPromptStageAncestor, error) {
	var ancestors []acpPromptStageAncestor
	for {
		file, err := os.Open(path)
		if err != nil {
			closePrivateACPPromptStageAncestors(ancestors)
			return nil, err
		}
		info, err := file.Stat()
		if err != nil || !info.IsDir() || verifyPrivateACPPromptTrustedDirectory(file, info) != nil || verifyPathMatchesOpenFile(path, file, info) != nil {
			_ = file.Close()
			closePrivateACPPromptStageAncestors(ancestors)
			return nil, errors.New("ACP prompt staging ancestor is not trusted")
		}
		ancestors = append(ancestors, acpPromptStageAncestor{file: file, info: info, path: path})
		next := filepath.Dir(path)
		if next == path {
			return ancestors, nil
		}
		path = next
	}
}

func closePrivateACPPromptStageAncestors(ancestors []acpPromptStageAncestor) {
	for index := range ancestors {
		if ancestors[index].file != nil {
			_ = ancestors[index].file.Close()
		}
	}
}

func openPrivateACPPromptStageFile(identity *acpPromptStageIdentity, name string) (*os.File, error) {
	if identity == nil || identity.dir == nil || filepath.Base(name) != name || strings.TrimSpace(name) == "" {
		return nil, errors.New("private staged prompt input identity is unavailable")
	}
	fd, err := unix.Openat(
		int(identity.dir.Fd()),
		name,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0o600,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	fail := func(cause error) (*os.File, error) {
		_ = file.Close()
		_ = unix.Unlinkat(int(identity.dir.Fd()), name, 0)
		return nil, cause
	}
	if err := stripPrivateACPPromptStageACL(file, false); err != nil {
		return fail(err)
	}
	if err := file.Chmod(0o600); err != nil {
		return fail(err)
	}
	info, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if err := verifyPrivateACPPromptStageInfo(file, info, false, 0o600); err != nil {
		return fail(err)
	}
	return file, nil
}

func deletePrivateACPPromptStageChild(identity *acpPromptStageIdentity, name string) error {
	if identity == nil || identity.dir == nil || filepath.Base(name) != name || name == "" {
		return errors.New("private staged prompt input identity is unavailable")
	}
	err := unix.Unlinkat(int(identity.dir.Fd()), name, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}

func sealPrivateACPPromptStageFile(file *os.File) error {
	if file == nil {
		return errors.New("private staged prompt input is nil")
	}
	if err := file.Chmod(0o400); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	return verifyPrivateACPPromptStageInfo(file, info, false, 0o400)
}

func retainPrivateACPPromptStageFile(identity *acpPromptStageIdentity, name string, file *os.File) error {
	if identity == nil || file == nil || filepath.Base(name) != name || name == "" {
		return errors.New("private staged prompt input identity is unavailable")
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if err := verifyPrivateACPPromptStageInfo(file, info, false, 0o400); err != nil {
		return err
	}
	if identity.children == nil {
		identity.children = make(map[string]acpPromptStageChild)
	}
	if _, exists := identity.children[name]; exists {
		return errors.New("private staged prompt input identity already retained")
	}
	identity.children[name] = acpPromptStageChild{file: file, info: info}
	return nil
}

func sealPrivateACPPromptStageDir(_ string, identity *acpPromptStageIdentity) error {
	if identity == nil || identity.dir == nil {
		return errors.New("private staged prompt input identity is unavailable")
	}
	if err := identity.dir.Chmod(0o500); err != nil {
		return err
	}
	info, err := identity.dir.Stat()
	if err != nil {
		return err
	}
	return verifyPrivateACPPromptStageInfo(identity.dir, info, true, 0o500)
}

func verifySealedPrivateACPPromptStageFile(path string) error {
	return verifyPrivateACPPromptStagePath(path, false, 0o400)
}

func verifySealedPrivateACPPromptStageDir(path string) error {
	return verifyPrivateACPPromptStagePath(path, true, 0o500)
}

func verifyPrivateACPPromptStagePath(path string, wantDir bool, wantMode os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("private staged prompt input is a symbolic link")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return errors.New("private staged prompt input identity changed")
	}
	return verifyPrivateACPPromptStageInfo(file, openedInfo, wantDir, wantMode)
}

func verifyPrivateACPPromptStageInfo(file *os.File, info os.FileInfo, wantDir bool, wantMode os.FileMode) error {
	if file == nil || info == nil || info.IsDir() != wantDir || (!wantDir && !info.Mode().IsRegular()) || info.Mode().Perm() != wantMode {
		return errors.New("private staged prompt input mode mismatch")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("private staged prompt input owner mismatch")
	}
	if err := verifyPrivateACPPromptStageACL(file, wantDir); err != nil {
		return errors.New("private staged prompt input has an extended ACL")
	}
	return nil
}

func verifyPrivateACPPromptStageIdentity(_ string, identity *acpPromptStageIdentity) error {
	if identity == nil || identity.dir == nil || identity.parent == nil || identity.currentName == "" {
		return errors.New("private staged prompt input identity is unavailable")
	}
	if err := verifyPathMatchesOpenFile(identity.canonicalParent, identity.parent, identity.parentInfo); err != nil {
		return err
	}
	parentInfo, err := identity.parent.Stat()
	if err != nil || verifyPrivateACPPromptTrustedDirectory(identity.parent, parentInfo) != nil {
		return errors.New("private staged prompt input parent is no longer trusted")
	}
	for _, ancestor := range identity.ancestors {
		current, err := ancestor.file.Stat()
		if err != nil || verifyPathMatchesOpenFile(ancestor.path, ancestor.file, ancestor.info) != nil || verifyPrivateACPPromptTrustedDirectory(ancestor.file, current) != nil {
			return errors.New("private staged prompt input ancestor identity changed")
		}
	}
	path := filepath.Join(identity.canonicalParent, identity.currentName)
	if err := verifyPathMatchesOpenFile(path, identity.dir, identity.dirInfo); err != nil {
		return err
	}
	for name, child := range identity.children {
		if child.file == nil {
			return errors.New("private staged prompt input child identity is unavailable")
		}
		current, err := child.file.Stat()
		if err != nil || !os.SameFile(child.info, current) || verifyPrivateACPPromptStageInfo(child.file, current, false, 0o400) != nil {
			return errors.New("private staged prompt input child identity changed")
		}
		fd, err := unix.Openat(int(identity.dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return errors.New("private staged prompt input child path identity changed")
		}
		opened := os.NewFile(uintptr(fd), name)
		openedInfo, statErr := opened.Stat()
		_ = opened.Close()
		if statErr != nil || !os.SameFile(current, openedInfo) {
			return errors.New("private staged prompt input child path identity changed")
		}
	}
	return nil
}

func verifyPathMatchesOpenFile(path string, file *os.File, retained os.FileInfo) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("private staged prompt input path identity mismatch")
	}
	current, err := file.Stat()
	if err != nil || !os.SameFile(retained, current) || !os.SameFile(current, info) {
		return errors.New("private staged prompt input path identity mismatch")
	}
	return nil
}

func quarantinePrivateACPPromptStage(_ string, identity *acpPromptStageIdentity) error {
	if err := verifyPrivateACPPromptStageIdentity("", identity); err != nil {
		return err
	}
	if identity.currentName != identity.originalName {
		return nil
	}
	quarantine, err := pendingACPPromptStageQuarantineName(&identity.pendingQuarantine)
	if err != nil {
		return err
	}
	if identity.onQuarantine != nil {
		identity.onQuarantine(filepath.Join(identity.canonicalParent, quarantine))
	}
	parentFD := int(identity.parent.Fd())
	if err := renamePrivateACPPromptStageNoReplace(parentFD, identity.currentName, quarantine); err != nil {
		return err
	}
	identity.currentName = quarantine
	identity.pendingQuarantine = ""
	return verifyPrivateACPPromptStageIdentity("", identity)
}

func currentPrivateACPPromptStageDirectory(identity *acpPromptStageIdentity) string {
	if identity == nil || identity.canonicalParent == "" || identity.currentName == "" {
		return ""
	}
	return filepath.Join(identity.canonicalParent, identity.currentName)
}

func setPrivateACPPromptStageQuarantineObserver(identity *acpPromptStageIdentity, observer func(string)) {
	if identity != nil {
		identity.onQuarantine = observer
	}
}

func preparePrivateACPPromptStageCleanup(identity *acpPromptStageIdentity) error {
	if identity == nil || identity.dir == nil {
		return errors.New("private staged prompt input identity is unavailable")
	}
	if err := identity.dir.Chmod(0o700); err != nil {
		return err
	}
	info, err := identity.dir.Stat()
	if err != nil {
		return err
	}
	return verifyPrivateACPPromptStageInfo(identity.dir, info, true, 0o700)
}

func removePrivateACPPromptStage(identity *acpPromptStageIdentity, filenames []string) error {
	if err := verifyPrivateACPPromptStageIdentity("", identity); err != nil {
		return err
	}
	closePrivateACPPromptStageChildren(identity)
	for _, name := range filenames {
		if filepath.Base(name) != name || name == "" {
			return errors.New("invalid private staged prompt input name")
		}
		if err := unix.Unlinkat(int(identity.dir.Fd()), name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	if err := verifyPrivateACPPromptStageIdentity("", identity); err != nil {
		return err
	}
	return unix.Unlinkat(int(identity.parent.Fd()), identity.currentName, unix.AT_REMOVEDIR)
}

func privateACPPromptStageIdentityRemoved(identity *acpPromptStageIdentity) bool {
	if identity == nil || identity.dir == nil || identity.parent == nil || identity.currentName == "" {
		return false
	}
	info, err := identity.dir.Stat()
	return privateACPPromptStageRemovalEvidence(info, err, func() error {
		var entry unix.Stat_t
		return unix.Fstatat(int(identity.parent.Fd()), identity.currentName, &entry, unix.AT_SYMLINK_NOFOLLOW)
	})
}

func privateACPPromptStageRemovalEvidence(info os.FileInfo, statErr error, parentEntry func() error) bool {
	// A retained-handle Stat failure is not evidence that the directory was
	// unlinked. Preserve the identity so cleanup retries and ultimately reports
	// failure instead of forgetting a possibly protected remnant.
	if statErr != nil || info == nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok && stat.Nlink == 0 {
		return true
	}
	if parentEntry == nil {
		return false
	}
	return errors.Is(parentEntry(), unix.ENOENT)
}

func closePrivateACPPromptStageIdentity(identity *acpPromptStageIdentity) {
	if identity == nil {
		return
	}
	identity.onQuarantine = nil
	identity.pendingQuarantine = ""
	closePrivateACPPromptStageChildren(identity)
	if identity.dir != nil {
		_ = identity.dir.Close()
		identity.dir = nil
	}
	if identity.parent != nil {
		_ = identity.parent.Close()
		identity.parent = nil
	}
	closePrivateACPPromptStageAncestors(identity.ancestors)
	identity.ancestors = nil
}

func closePrivateACPPromptStageChildren(identity *acpPromptStageIdentity) {
	if identity == nil {
		return
	}
	for name, child := range identity.children {
		if child.file != nil {
			_ = child.file.Close()
		}
		delete(identity.children, name)
	}
	identity.children = nil
}
