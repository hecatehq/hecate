//go:build windows

package agentadapters

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type acpPromptStageACLGrant struct {
	sid  *windows.SID
	mask windows.ACCESS_MASK
}

type acpPromptStageIdentity struct {
	dir               windows.Handle
	dirMutable        bool
	cleanupACLReady   bool
	dirInfo           windows.ByHandleFileInformation
	parent            windows.Handle
	parentInfo        windows.ByHandleFileInformation
	canonicalParent   string
	originalName      string
	currentName       string
	pendingQuarantine string
	onQuarantine      func(string)
	deletePending     bool
	ancestors         []acpPromptStageWindowsAncestor
	children          map[string]acpPromptStageWindowsChild
}

type acpPromptStageWindowsAncestor struct {
	handle windows.Handle
	info   windows.ByHandleFileInformation
	path   string
}

type acpPromptStageWindowsChild struct {
	file *os.File
	info windows.ByHandleFileInformation
}

type acpPromptStageACLShape uint8

const (
	acpPromptStageACLExplicitFile acpPromptStageACLShape = iota
	acpPromptStageACLInheritableDirectory
	acpPromptStageACLExplicitDirectory
)

const (
	acpPromptStageFullControl = windows.GENERIC_ALL
	acpPromptStageReadFile    = windows.GENERIC_READ
	acpPromptStageReadDir     = windows.GENERIC_READ | windows.GENERIC_EXECUTE
	acpPromptFileDeleteChild  = windows.ACCESS_MASK(0x00000040)

	// TrustedInstaller is the Windows Modules Installer service SID. Current
	// Windows installations can assign it ownership and full control of system
	// ancestors such as the volume root. It is trusted only while auditing an
	// existing managed parent or ancestor, never as the owner of a Hecate-created
	// stage.
	acpPromptStageTrustedInstallerSID = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"
)

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
	if err != nil || !isLocalDriveACPPromptStagePath(parentPath) {
		return "", nil, errors.New("ACP prompt staging requires a resolved local Windows drive")
	}
	parent, parentInfo, finalParent, err := openACPPromptStageDirectoryHandle(parentPath, false)
	if err != nil {
		return "", nil, err
	}
	if !sameWindowsPath(parentPath, finalParent) || !isLocalDriveACPPromptStagePath(finalParent) {
		_ = windows.CloseHandle(parent)
		return "", nil, errors.New("ACP prompt staging parent did not resolve to its local drive path")
	}
	if err := verifyPrivateACPPromptWindowsParent(parent); err != nil {
		_ = windows.CloseHandle(parent)
		return "", nil, err
	}
	ancestors, err := openPrivateACPPromptWindowsAncestors(filepath.Dir(finalParent))
	if err != nil {
		_ = windows.CloseHandle(parent)
		return "", nil, err
	}
	if afterParentOpen != nil {
		if err := afterParentOpen(); err != nil {
			closePrivateACPPromptWindowsAncestors(ancestors)
			_ = windows.CloseHandle(parent)
			return "", nil, err
		}
	}
	name, err := randomACPPromptStageName("hecate-acp-input-")
	if err != nil {
		closePrivateACPPromptWindowsAncestors(ancestors)
		_ = windows.CloseHandle(parent)
		return "", nil, err
	}
	grants, err := acpPromptStageACLGrants(acpPromptStageFullControl, acpPromptStageFullControl)
	if err != nil {
		closePrivateACPPromptWindowsAncestors(ancestors)
		_ = windows.CloseHandle(parent)
		return "", nil, err
	}
	securityDescriptor, err := privateACPPromptStageSecurityDescriptor(
		grants,
		windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
	)
	if err != nil {
		closePrivateACPPromptWindowsAncestors(ancestors)
		_ = windows.CloseHandle(parent)
		return "", nil, err
	}
	stageHandle, stageInfo, finalStage, err := createPrivateACPPromptStageDirectoryRelative(parent, name, securityDescriptor)
	runtime.KeepAlive(grants)
	runtime.KeepAlive(securityDescriptor)
	if err != nil {
		closePrivateACPPromptWindowsAncestors(ancestors)
		_ = windows.CloseHandle(parent)
		return "", nil, err
	}
	fail := func(cause error, stageHandle windows.Handle) (string, *acpPromptStageIdentity, error) {
		if stageHandle != 0 && stageHandle != windows.InvalidHandle {
			if cleanupErr := deletePrivateACPPromptStageDirectoryHandle(stageHandle); cleanupErr != nil {
				cause = errors.Join(cause, errors.New("remove private staged prompt input after create failure"))
			}
			_ = windows.CloseHandle(stageHandle)
		}
		closePrivateACPPromptWindowsAncestors(ancestors)
		_ = windows.CloseHandle(parent)
		return "", nil, cause
	}
	if !isLocalDriveACPPromptStagePath(finalStage) || !sameWindowsPath(filepath.Dir(finalStage), finalParent) {
		return fail(errors.New("ACP prompt stage did not resolve to its local parent"), stageHandle)
	}
	if err := verifyPrivateACPPromptStageHandleACL(stageHandle, grants, true, acpPromptStageACLInheritableDirectory); err != nil {
		return fail(err, stageHandle)
	}
	identity := &acpPromptStageIdentity{
		dir:             stageHandle,
		dirMutable:      true,
		dirInfo:         stageInfo,
		parent:          parent,
		parentInfo:      parentInfo,
		canonicalParent: finalParent,
		originalName:    name,
		currentName:     name,
		ancestors:       ancestors,
	}
	if err := verifyPrivateACPPromptStageIdentity(finalStage, identity); err != nil {
		return fail(err, stageHandle)
	}
	return finalStage, identity, nil
}

func createPrivateACPPromptStageDirectoryRelative(parent windows.Handle, name string, securityDescriptor *windows.SECURITY_DESCRIPTOR) (windows.Handle, windows.ByHandleFileInformation, string, error) {
	var empty windows.ByHandleFileInformation
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, empty, "", err
	}
	oa := &windows.OBJECT_ATTRIBUTES{
		Length:             uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory:      parent,
		ObjectName:         objectName,
		Attributes:         windows.OBJ_CASE_INSENSITIVE,
		SecurityDescriptor: securityDescriptor,
	}
	var handle windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	var allocationSize int64
	err = windows.NtCreateFile(
		&handle,
		windows.GENERIC_ALL|windows.SYNCHRONIZE,
		oa,
		&iosb,
		&allocationSize,
		windows.FILE_ATTRIBUTE_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_CREATE,
		windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
		0,
		0,
	)
	runtime.KeepAlive(securityDescriptor)
	if err != nil {
		return 0, empty, "", err
	}
	fail := func(cause error) (windows.Handle, windows.ByHandleFileInformation, string, error) {
		_ = deletePrivateACPPromptStageDirectoryHandle(handle)
		_ = windows.CloseHandle(handle)
		return 0, empty, "", cause
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fail(err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fail(errors.New("ACP prompt stage handle is not a non-reparse directory"))
	}
	finalPath, err := acpPromptFinalPath(handle)
	if err != nil || !isLocalDriveACPPromptStagePath(finalPath) {
		return fail(errors.New("ACP prompt stage handle did not resolve to a local drive"))
	}
	return handle, info, finalPath, nil
}

func privateACPPromptStageSecurityDescriptor(grants []acpPromptStageACLGrant, inheritance uint32) (*windows.SECURITY_DESCRIPTOR, error) {
	if len(grants) == 0 || grants[0].sid == nil {
		return nil, errors.New("private staged prompt input security principals are unavailable")
	}
	flags := ""
	switch inheritance {
	case windows.NO_INHERITANCE:
	case windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT:
		flags = "OICI"
	default:
		return nil, errors.New("private staged prompt input ACL inheritance is unsupported")
	}
	var sddl strings.Builder
	sddl.WriteString("O:")
	sddl.WriteString(grants[0].sid.String())
	sddl.WriteString("D:P")
	for _, grant := range grants {
		if grant.sid == nil {
			return nil, errors.New("private staged prompt input security principal is unavailable")
		}
		sddl.WriteString("(A;")
		sddl.WriteString(flags)
		sddl.WriteString(";0x")
		sddl.WriteString(strconv.FormatUint(uint64(uint32(grant.mask)), 16))
		sddl.WriteString(";;;")
		sddl.WriteString(grant.sid.String())
		sddl.WriteByte(')')
	}
	descriptor, err := windows.SecurityDescriptorFromString(sddl.String())
	if err != nil {
		return nil, err
	}
	runtime.KeepAlive(grants)
	return descriptor, nil
}

func deletePrivateACPPromptStageDirectoryHandle(handle windows.Handle) error {
	disposition := struct{ DeleteFile byte }{DeleteFile: 1}
	return windows.SetFileInformationByHandle(
		handle,
		windows.FileDispositionInfo,
		(*byte)(unsafe.Pointer(&disposition)),
		uint32(unsafe.Sizeof(disposition)),
	)
}

func isLocalDriveACPPromptStagePath(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	volume := filepath.VolumeName(filepath.Clean(path))
	if len(volume) != 2 || volume[1] != ':' {
		return false
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return false
	}
	driveType := windows.GetDriveType(root)
	return driveType != windows.DRIVE_REMOTE && driveType != windows.DRIVE_NO_ROOT_DIR && driveType != windows.DRIVE_UNKNOWN
}

func openACPPromptStageDirectoryHandle(path string, mutable bool) (windows.Handle, windows.ByHandleFileInformation, string, error) {
	var empty windows.ByHandleFileInformation
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, empty, "", err
	}
	access := uint32(windows.READ_CONTROL | windows.FILE_READ_ATTRIBUTES | windows.FILE_LIST_DIRECTORY | windows.SYNCHRONIZE)
	if mutable {
		access |= windows.WRITE_DAC | windows.WRITE_OWNER | windows.DELETE
	}
	handle, err := windows.CreateFile(
		pathPtr,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return 0, empty, "", err
	}
	fail := func(cause error) (windows.Handle, windows.ByHandleFileInformation, string, error) {
		_ = windows.CloseHandle(handle)
		return 0, empty, "", cause
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fail(err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fail(errors.New("ACP prompt stage handle is not a non-reparse directory"))
	}
	finalPath, err := acpPromptFinalPath(handle)
	if err != nil || !isLocalDriveACPPromptStagePath(finalPath) {
		return fail(errors.New("ACP prompt stage handle did not resolve to a local drive"))
	}
	return handle, info, finalPath, nil
}

func acpPromptFinalPath(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 512)
	for {
		length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if int(length) < len(buffer) {
			value := windows.UTF16ToString(buffer[:length])
			if strings.HasPrefix(strings.ToUpper(value), `\\?\UNC\`) {
				return "", errors.New("ACP prompt stage resolved to a UNC path")
			}
			value = strings.TrimPrefix(value, `\\?\`)
			return filepath.Clean(value), nil
		}
		buffer = make([]uint16, int(length)+1)
	}
}

func sameWindowsPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func sameACPPromptWindowsFileInfo(left, right windows.ByHandleFileInformation) bool {
	return left.VolumeSerialNumber == right.VolumeSerialNumber &&
		left.FileIndexHigh == right.FileIndexHigh &&
		left.FileIndexLow == right.FileIndexLow
}

func verifyPrivateACPPromptWindowsParent(handle windows.Handle) error {
	// Hosted and managed Windows environments commonly assign the per-user
	// temporary directory or its ancestors to LocalSystem, Administrators, or
	// TrustedInstaller. Those owners are trusted only when the DACL below also
	// proves that no other principal has delete-child or security-descriptor
	// control.
	return verifyPrivateACPPromptWindowsDirectoryControl(handle, false)
}

func verifyPrivateACPPromptWindowsDirectoryControl(handle windows.Handle, requireUserOwner bool) error {
	descriptor, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	return verifyPrivateACPPromptWindowsDirectoryDescriptor(descriptor, requireUserOwner)
}

func verifyPrivateACPPromptWindowsDirectoryDescriptor(descriptor *windows.SECURITY_DESCRIPTOR, requireUserOwner bool) error {
	if descriptor == nil {
		return errors.New("ACP prompt staging directory has no security descriptor")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	userSID, err := user.User.Sid.Copy()
	if err != nil {
		return err
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	trustedInstallerSID, err := windows.StringToSid(acpPromptStageTrustedInstallerSID)
	if err != nil {
		return err
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil {
		return errors.New("ACP prompt staging directory has no owner")
	}
	trustedOwner := owner.Equals(userSID)
	if !requireUserOwner {
		trustedOwner = trustedOwner || owner.Equals(systemSID) || owner.Equals(adminSID) || owner.Equals(trustedInstallerSID)
	}
	if !trustedOwner {
		return errors.New("ACP prompt staging directory has an untrusted owner")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.New("ACP prompt staging parent has no DACL")
	}
	dangerous := windows.ACCESS_MASK(acpPromptFileDeleteChild | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_ALL)
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return err
		}
		if ace == nil {
			return errors.New("ACP prompt staging parent DACL contains an invalid entry")
		}
		// An inherit-only ACE grants no rights on this directory. Every existing
		// descendant is verified through its own retained handle, while the new
		// stage receives an explicit protected DACL and cannot inherit this ACE.
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("ACP prompt staging parent DACL contains an unsupported allow entry")
		}
		if ace.Mask&dangerous == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		trustedController := sid.IsValid() && (sid.Equals(userSID) || sid.Equals(systemSID) || sid.Equals(adminSID))
		if !requireUserOwner {
			trustedController = trustedController || (sid.IsValid() && sid.Equals(trustedInstallerSID))
		}
		if !trustedController {
			return errors.New("ACP prompt staging parent grants delete-child control to an untrusted principal")
		}
	}
	runtime.KeepAlive(userSID)
	runtime.KeepAlive(systemSID)
	runtime.KeepAlive(adminSID)
	runtime.KeepAlive(trustedInstallerSID)
	return nil
}

func openPrivateACPPromptWindowsAncestors(path string) ([]acpPromptStageWindowsAncestor, error) {
	var ancestors []acpPromptStageWindowsAncestor
	for {
		handle, info, finalPath, err := openACPPromptStageDirectoryHandle(path, false)
		if err != nil {
			closePrivateACPPromptWindowsAncestors(ancestors)
			return nil, fmt.Errorf("open ACP prompt staging ancestor: %w", err)
		}
		if !sameWindowsPath(path, finalPath) {
			_ = windows.CloseHandle(handle)
			closePrivateACPPromptWindowsAncestors(ancestors)
			return nil, errors.New("ACP prompt staging ancestor resolved to a different path")
		}
		if err := verifyPrivateACPPromptWindowsDirectoryControl(handle, false); err != nil {
			if handle != 0 && handle != windows.InvalidHandle {
				_ = windows.CloseHandle(handle)
			}
			closePrivateACPPromptWindowsAncestors(ancestors)
			return nil, fmt.Errorf("ACP prompt staging ancestor security: %w", err)
		}
		ancestors = append(ancestors, acpPromptStageWindowsAncestor{handle: handle, info: info, path: finalPath})
		next := filepath.Dir(finalPath)
		if sameWindowsPath(next, finalPath) {
			return ancestors, nil
		}
		path = next
	}
}

func closePrivateACPPromptWindowsAncestors(ancestors []acpPromptStageWindowsAncestor) {
	for index := range ancestors {
		if ancestors[index].handle != 0 && ancestors[index].handle != windows.InvalidHandle {
			_ = windows.CloseHandle(ancestors[index].handle)
		}
	}
}

func openPrivateACPPromptStageFile(identity *acpPromptStageIdentity, name string) (*os.File, error) {
	if identity == nil || identity.dir == 0 || filepath.Base(name) != name || strings.TrimSpace(name) == "" {
		return nil, errors.New("private staged prompt input identity is unavailable")
	}
	grants, err := acpPromptStageACLGrants(acpPromptStageFullControl, acpPromptStageFullControl)
	if err != nil {
		return nil, err
	}
	securityDescriptor, err := privateACPPromptStageSecurityDescriptor(grants, windows.NO_INHERITANCE)
	if err != nil {
		return nil, err
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	oa := &windows.OBJECT_ATTRIBUTES{
		Length:             uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory:      identity.dir,
		ObjectName:         objectName,
		Attributes:         windows.OBJ_CASE_INSENSITIVE,
		SecurityDescriptor: securityDescriptor,
	}
	var handle windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	var allocationSize int64
	err = windows.NtCreateFile(
		&handle,
		windows.GENERIC_WRITE|windows.READ_CONTROL|windows.WRITE_DAC|windows.WRITE_OWNER|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		oa,
		&iosb,
		&allocationSize,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_CREATE,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
		0,
		0,
	)
	runtime.KeepAlive(securityDescriptor)
	runtime.KeepAlive(grants)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), name)
	// Elevated Windows tokens can default new-object ownership to the
	// Administrators group. Read back the atomically supplied process-user owner
	// and protected private DACL before any prompt byte is written. Owner/DACL
	// changes after this point are handle-based so a pathname substitution cannot
	// redirect them.
	verifyErr := verifyPrivateACPPromptStageHandleACL(
		handle,
		grants,
		true,
		acpPromptStageACLExplicitFile,
	)
	if verifyErr != nil {
		_ = file.Close()
		_ = deletePrivateACPPromptStageChild(identity, name)
		return nil, verifyErr
	}
	return file, nil
}

func sealPrivateACPPromptStageFile(file *os.File) error {
	grants, err := acpPromptStageACLGrants(acpPromptStageReadFile, acpPromptStageFullControl)
	if err != nil {
		return err
	}
	handle := windows.Handle(file.Fd())
	if err := applyPrivateACPPromptStageACLToHandle(windows.Handle(file.Fd()), grants, windows.NO_INHERITANCE); err != nil {
		return err
	}
	return verifyPrivateACPPromptStageHandleACL(handle, grants, true, acpPromptStageACLExplicitFile)
}

func retainPrivateACPPromptStageFile(identity *acpPromptStageIdentity, name string, file *os.File) error {
	if identity == nil || file == nil || filepath.Base(name) != name || name == "" {
		return errors.New("private staged prompt input identity is unavailable")
	}
	handle := windows.Handle(file.Fd())
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("private staged prompt input child is not a non-reparse file")
	}
	grants, err := acpPromptStageACLGrants(acpPromptStageReadFile, acpPromptStageFullControl)
	if err != nil {
		return err
	}
	if err := verifyPrivateACPPromptStageHandleACL(handle, grants, true, acpPromptStageACLExplicitFile); err != nil {
		return err
	}
	if identity.children == nil {
		identity.children = make(map[string]acpPromptStageWindowsChild)
	}
	if _, exists := identity.children[name]; exists {
		return errors.New("private staged prompt input identity already retained")
	}
	retainedHandle, err := openACPPromptStageChildRelative(
		identity,
		name,
		windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.SYNCHRONIZE,
		0,
	)
	if err != nil {
		return err
	}
	retainedFile := os.NewFile(uintptr(retainedHandle), name)
	fail := func(cause error) error {
		_ = retainedFile.Close()
		return cause
	}
	var retainedInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(retainedHandle, &retainedInfo); err != nil {
		return fail(err)
	}
	if !sameACPPromptWindowsFileInfo(retainedInfo, info) ||
		retainedInfo.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 ||
		retainedInfo.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fail(errors.New("private staged prompt input child identity changed while narrowing its retained handle"))
	}
	if err := verifyPrivateACPPromptStageHandleACL(retainedHandle, grants, true, acpPromptStageACLExplicitFile); err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		return fail(err)
	}
	identity.children[name] = acpPromptStageWindowsChild{file: retainedFile, info: retainedInfo}
	return nil
}

func sealPrivateACPPromptStageDir(_ string, identity *acpPromptStageIdentity) error {
	if identity == nil || identity.dir == 0 {
		return errors.New("private staged prompt input identity is unavailable")
	}
	grants, err := acpPromptStageACLGrants(acpPromptStageReadDir, acpPromptStageFullControl)
	if err != nil {
		return err
	}
	// Every child already has its own protected DACL. Keep the sealed directory
	// ACEs explicit to the directory so SetSecurityInfo cannot propagate or
	// normalize generic inheritable entries into a different ACL shape.
	if err := applyPrivateACPPromptStageACLToHandle(identity.dir, grants, windows.NO_INHERITANCE); err != nil {
		return fmt.Errorf("restrict ACP prompt staging directory ACL: %w", err)
	}
	// SetSecurityInfo has already replaced the full-control DACL even if a
	// later verification or retained-handle transition fails. Preserve that
	// state so failure cleanup restores mutation access instead of assuming the
	// directory still grants delete-child control.
	identity.dirMutable = false
	identity.cleanupACLReady = false
	if err := verifyPrivateACPPromptStageHandleACL(identity.dir, grants, true, acpPromptStageACLExplicitDirectory); err != nil {
		return fmt.Errorf("verify restricted ACP prompt staging directory ACL: %w", err)
	}
	if err := retainSealedPrivateACPPromptStageDirectory(identity, grants); err != nil {
		return fmt.Errorf("retain restricted ACP prompt staging directory: %w", err)
	}
	return nil
}

func retainSealedPrivateACPPromptStageDirectory(identity *acpPromptStageIdentity, grants []acpPromptStageACLGrant) error {
	retained, retainedInfo, finalPath, err := openACPPromptStageDirectoryRelative(
		identity.parent,
		identity.currentName,
		windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.SYNCHRONIZE,
	)
	if err != nil {
		return err
	}
	fail := func(cause error) error {
		_ = windows.CloseHandle(retained)
		return cause
	}
	if !sameACPPromptWindowsFileInfo(retainedInfo, identity.dirInfo) ||
		!sameWindowsPath(filepath.Dir(finalPath), identity.canonicalParent) {
		return fail(errors.New("private staged prompt input directory identity changed while narrowing its retained handle"))
	}
	if err := verifyPrivateACPPromptStageHandleACL(retained, grants, true, acpPromptStageACLExplicitDirectory); err != nil {
		return fail(err)
	}
	if err := windows.CloseHandle(identity.dir); err != nil {
		return fail(err)
	}
	identity.dir = retained
	identity.dirMutable = false
	identity.cleanupACLReady = false
	return nil
}

func verifySealedPrivateACPPromptStageFile(path string) error {
	grants, err := acpPromptStageACLGrants(acpPromptStageReadFile, acpPromptStageFullControl)
	if err != nil {
		return err
	}
	return verifyPrivateACPPromptStagePathACL(path, grants, true, acpPromptStageACLExplicitFile)
}

func verifySealedPrivateACPPromptStageDir(path string) error {
	grants, err := acpPromptStageACLGrants(acpPromptStageReadDir, acpPromptStageFullControl)
	if err != nil {
		return err
	}
	return verifyPrivateACPPromptStagePathACL(path, grants, true, acpPromptStageACLExplicitDirectory)
}

func preparePrivateACPPromptStageCleanup(identity *acpPromptStageIdentity) error {
	if identity == nil || identity.dir == 0 {
		return errors.New("private staged prompt input identity is unavailable")
	}
	grants, err := acpPromptStageACLGrants(acpPromptStageFullControl, acpPromptStageFullControl)
	if err != nil {
		return err
	}
	// Cleanup needs mutation rights only on the retained directory. Children are
	// deleted through their exact retained names and remain protected separately.
	if err := applyPrivateACPPromptStageACLToHandle(identity.dir, grants, windows.NO_INHERITANCE); err != nil {
		return err
	}
	return verifyPrivateACPPromptStageHandleACL(identity.dir, grants, true, acpPromptStageACLExplicitDirectory)
}

func verifyPrivateACPPromptStageIdentity(_ string, identity *acpPromptStageIdentity) error {
	if identity == nil || identity.dir == 0 || identity.parent == 0 || identity.currentName == "" {
		return errors.New("private staged prompt input identity is unavailable")
	}
	parent, parentInfo, finalParent, err := openACPPromptStageDirectoryHandle(identity.canonicalParent, false)
	if err != nil {
		return err
	}
	_ = windows.CloseHandle(parent)
	if !sameACPPromptWindowsFileInfo(parentInfo, identity.parentInfo) || !sameWindowsPath(finalParent, identity.canonicalParent) {
		return errors.New("private staged prompt input parent identity changed")
	}
	var retainedParent windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(identity.parent, &retainedParent); err != nil ||
		!sameACPPromptWindowsFileInfo(retainedParent, identity.parentInfo) ||
		verifyPrivateACPPromptWindowsParent(identity.parent) != nil {
		return errors.New("private staged prompt input parent is no longer trusted")
	}
	for _, ancestor := range identity.ancestors {
		current, info, finalPath, err := openACPPromptStageDirectoryHandle(ancestor.path, false)
		if err != nil {
			return errors.New("private staged prompt input ancestor identity changed")
		}
		_ = windows.CloseHandle(current)
		var retained windows.ByHandleFileInformation
		if !sameWindowsPath(finalPath, ancestor.path) ||
			!sameACPPromptWindowsFileInfo(info, ancestor.info) ||
			windows.GetFileInformationByHandle(ancestor.handle, &retained) != nil ||
			!sameACPPromptWindowsFileInfo(retained, ancestor.info) ||
			verifyPrivateACPPromptWindowsDirectoryControl(ancestor.handle, false) != nil {
			return errors.New("private staged prompt input ancestor identity changed")
		}
	}
	path := filepath.Join(identity.canonicalParent, identity.currentName)
	current, currentInfo, _, err := openACPPromptStageDirectoryHandle(path, false)
	if err != nil {
		return err
	}
	_ = windows.CloseHandle(current)
	if !sameACPPromptWindowsFileInfo(currentInfo, identity.dirInfo) {
		return errors.New("private staged prompt input identity changed")
	}
	fileGrants, err := acpPromptStageACLGrants(acpPromptStageReadFile, acpPromptStageFullControl)
	if err != nil {
		return err
	}
	for name, child := range identity.children {
		if child.file == nil {
			return errors.New("private staged prompt input child identity is unavailable")
		}
		retainedHandle := windows.Handle(child.file.Fd())
		var retained windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(retainedHandle, &retained); err != nil ||
			!sameACPPromptWindowsFileInfo(retained, child.info) ||
			verifyPrivateACPPromptStageHandleACL(retainedHandle, fileGrants, true, acpPromptStageACLExplicitFile) != nil {
			return errors.New("private staged prompt input child identity changed")
		}
		opened, err := openACPPromptStageChildRelative(identity, name, windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.SYNCHRONIZE, 0)
		if err != nil {
			return errors.New("private staged prompt input child path identity changed")
		}
		var openedInfo windows.ByHandleFileInformation
		infoErr := windows.GetFileInformationByHandle(opened, &openedInfo)
		_ = windows.CloseHandle(opened)
		if infoErr != nil || !sameACPPromptWindowsFileInfo(openedInfo, child.info) ||
			openedInfo.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("private staged prompt input child path identity changed")
		}
	}
	return nil
}

type acpPromptFileRenameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

func quarantinePrivateACPPromptStage(_ string, identity *acpPromptStageIdentity) error {
	if err := verifyPrivateACPPromptStageIdentity("", identity); err != nil {
		return err
	}
	if err := acquireMutablePrivateACPPromptStageDirectory(identity); err != nil {
		return err
	}
	if identity.currentName != identity.originalName {
		return releasePrivateACPPromptStageWindowsChildren(identity)
	}
	quarantine, err := pendingACPPromptStageQuarantineName(&identity.pendingQuarantine)
	if err != nil {
		return err
	}
	if identity.onQuarantine != nil {
		identity.onQuarantine(filepath.Join(identity.canonicalParent, quarantine))
	}
	name, err := windows.UTF16FromString(quarantine)
	if err != nil {
		return err
	}
	nameBytes := (len(name) - 1) * 2
	var shape acpPromptFileRenameInformation
	bufferSize := int(unsafe.Offsetof(shape.FileName)) + nameBytes
	buffer := make([]byte, bufferSize)
	info := (*acpPromptFileRenameInformation)(unsafe.Pointer(&buffer[0]))
	// A basename with a null RootDirectory is Windows' same-parent rename
	// form. This keeps the already-verified source handle authoritative for its
	// existing parent and avoids an unnecessary destination-root traversal
	// through the retained read-only parent handle.
	info.FileNameLength = uint32(nameBytes)
	copy((*[windows.MAX_LONG_PATH]uint16)(unsafe.Pointer(&info.FileName[0]))[:len(name)-1:len(name)-1], name[:len(name)-1])
	var iosb windows.IO_STATUS_BLOCK
	if err := verifyPrivateACPPromptStageIdentity("", identity); err != nil {
		return err
	}
	// Windows rejects a directory rename while it contains any open descendant
	// handles. Child identity was re-verified above, so release only Hecate's
	// own retained child handles immediately before the handle-authoritative
	// rename. The retained directory identity remains live throughout cleanup.
	if err := releasePrivateACPPromptStageWindowsChildren(identity); err != nil {
		return err
	}
	if err := windows.NtSetInformationFile(
		identity.dir,
		&iosb,
		&buffer[0],
		uint32(len(buffer)),
		windows.FileRenameInformation,
	); err != nil {
		return err
	}
	identity.currentName = quarantine
	identity.pendingQuarantine = ""
	if err := verifyPrivateACPPromptStageIdentity("", identity); err != nil {
		return err
	}
	return nil
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

func acquireMutablePrivateACPPromptStageDirectory(identity *acpPromptStageIdentity) error {
	if identity == nil || identity.dir == 0 || identity.parent == 0 {
		return errors.New("private staged prompt input identity is unavailable")
	}
	if identity.dirMutable {
		return nil
	}
	sealedGrants, err := acpPromptStageACLGrants(acpPromptStageReadDir, acpPromptStageFullControl)
	if err != nil {
		return err
	}
	cleanupGrants, err := acpPromptStageACLGrants(acpPromptStageFullControl, acpPromptStageFullControl)
	if err != nil {
		return err
	}
	if !identity.cleanupACLReady {
		aclHandle, aclInfo, aclPath, err := openACPPromptStageDirectoryRelative(
			identity.parent,
			identity.currentName,
			windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.WRITE_DAC|windows.SYNCHRONIZE,
		)
		if err != nil {
			return err
		}
		fail := func(cause error) error {
			_ = windows.CloseHandle(aclHandle)
			return cause
		}
		if !sameACPPromptWindowsFileInfo(aclInfo, identity.dirInfo) ||
			!sameWindowsPath(filepath.Dir(aclPath), identity.canonicalParent) {
			return fail(errors.New("private staged prompt input directory identity changed while restoring cleanup access"))
		}
		if err := verifyPrivateACPPromptStageHandleACL(aclHandle, sealedGrants, true, acpPromptStageACLExplicitDirectory); err != nil {
			return fail(err)
		}
		if err := applyPrivateACPPromptStageACLToHandle(aclHandle, cleanupGrants, windows.NO_INHERITANCE); err != nil {
			return fail(err)
		}
		// SetSecurityInfo has already changed the directory even if the
		// subsequent read-back fails transiently. Preserve that intermediate
		// state so the next cleanup attempt verifies the full-control shape
		// instead of incorrectly demanding the sealed read-only shape.
		identity.cleanupACLReady = true
		if err := verifyPrivateACPPromptStageHandleACL(aclHandle, cleanupGrants, true, acpPromptStageACLExplicitDirectory); err != nil {
			return fail(err)
		}
		if err := windows.CloseHandle(aclHandle); err != nil {
			return err
		}
	} else if err := verifyPrivateACPPromptStageHandleACL(identity.dir, cleanupGrants, true, acpPromptStageACLExplicitDirectory); err != nil {
		return err
	}
	mutable, mutableInfo, finalPath, err := openACPPromptStageDirectoryRelative(
		identity.parent,
		identity.currentName,
		windows.GENERIC_ALL|windows.SYNCHRONIZE,
	)
	if err != nil {
		return err
	}
	fail := func(cause error) error {
		_ = windows.CloseHandle(mutable)
		return cause
	}
	if !sameACPPromptWindowsFileInfo(mutableInfo, identity.dirInfo) ||
		!sameWindowsPath(filepath.Dir(finalPath), identity.canonicalParent) {
		return fail(errors.New("private staged prompt input directory identity changed while preparing cleanup"))
	}
	if err := verifyPrivateACPPromptStageHandleACL(mutable, cleanupGrants, true, acpPromptStageACLExplicitDirectory); err != nil {
		return fail(err)
	}
	if err := windows.CloseHandle(identity.dir); err != nil {
		return fail(err)
	}
	identity.dir = mutable
	identity.dirMutable = true
	return nil
}

func removePrivateACPPromptStage(identity *acpPromptStageIdentity, filenames []string) error {
	if err := verifyPrivateACPPromptStageIdentity("", identity); err != nil {
		return err
	}
	for _, name := range filenames {
		if filepath.Base(name) != name || name == "" {
			return errors.New("invalid private staged prompt input name")
		}
		if err := deletePrivateACPPromptStageChild(identity, name); err != nil &&
			!privateACPPromptStageChildGone(err) {
			return err
		}
	}
	if err := verifyPrivateACPPromptStageIdentity("", identity); err != nil {
		return err
	}
	disposition := struct{ DeleteFile byte }{DeleteFile: 1}
	if err := windows.SetFileInformationByHandle(
		identity.dir,
		windows.FileDispositionInfo,
		(*byte)(unsafe.Pointer(&disposition)),
		uint32(unsafe.Sizeof(disposition)),
	); err != nil {
		return err
	}
	identity.deletePending = true
	return nil
}

func privateACPPromptStageChildGone(err error) bool {
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_DELETE_PENDING) {
		return true
	}
	// Child removal uses NtCreateFile, which returns NTStatus directly rather
	// than a Win32 errno. Recognize its idempotent terminal states explicitly so
	// repeated manifest entries and cleanup retries do not strand a private
	// stage after the first handle-relative deletion succeeds.
	var status windows.NTStatus
	return errors.As(err, &status) &&
		(status == windows.STATUS_NO_SUCH_FILE ||
			status == windows.STATUS_OBJECT_NAME_NOT_FOUND ||
			status == windows.STATUS_OBJECT_PATH_NOT_FOUND ||
			status == windows.STATUS_DELETE_PENDING)
}

func deletePrivateACPPromptStageChild(identity *acpPromptStageIdentity, name string) error {
	handle, err := openACPPromptStageChildRelative(
		identity,
		name,
		windows.DELETE|windows.SYNCHRONIZE,
		windows.FILE_DELETE_ON_CLOSE,
	)
	if err != nil {
		return err
	}
	return windows.CloseHandle(handle)
}

func openACPPromptStageChildRelative(identity *acpPromptStageIdentity, name string, access uint32, extraOptions uint32) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	oa := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: identity.dir,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	var handle windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	var allocationSize int64
	if err := windows.NtCreateFile(
		&handle,
		access,
		oa,
		&iosb,
		&allocationSize,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT|extraOptions,
		0,
		0,
	); err != nil {
		return 0, err
	}
	return handle, nil
}

func openACPPromptStageDirectoryRelative(parent windows.Handle, name string, access uint32) (windows.Handle, windows.ByHandleFileInformation, string, error) {
	var empty windows.ByHandleFileInformation
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, empty, "", err
	}
	oa := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	var handle windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	var allocationSize int64
	if err := windows.NtCreateFile(
		&handle,
		access,
		oa,
		&iosb,
		&allocationSize,
		windows.FILE_ATTRIBUTE_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
		0,
		0,
	); err != nil {
		return 0, empty, "", err
	}
	fail := func(cause error) (windows.Handle, windows.ByHandleFileInformation, string, error) {
		_ = windows.CloseHandle(handle)
		return 0, empty, "", cause
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fail(err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fail(errors.New("ACP prompt stage handle is not a non-reparse directory"))
	}
	finalPath, err := acpPromptFinalPath(handle)
	if err != nil || !isLocalDriveACPPromptStagePath(finalPath) {
		return fail(errors.New("ACP prompt stage handle did not resolve to a local drive"))
	}
	return handle, info, finalPath, nil
}

func privateACPPromptStageIdentityRemoved(identity *acpPromptStageIdentity) bool {
	if identity == nil || identity.currentName == "" {
		return false
	}
	if identity.deletePending {
		return true
	}
	path := filepath.Join(identity.canonicalParent, identity.currentName)
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) || errors.Is(err, windows.ERROR_DELETE_PENDING)
	}
	_ = windows.CloseHandle(handle)
	return false
}

func closePrivateACPPromptStageIdentity(identity *acpPromptStageIdentity) {
	if identity == nil {
		return
	}
	identity.onQuarantine = nil
	identity.pendingQuarantine = ""
	closePrivateACPPromptStageWindowsChildren(identity)
	if identity.dir != 0 && identity.dir != windows.InvalidHandle {
		_ = windows.CloseHandle(identity.dir)
		identity.dir = 0
		identity.dirMutable = false
		identity.cleanupACLReady = false
	}
	if identity.parent != 0 && identity.parent != windows.InvalidHandle {
		_ = windows.CloseHandle(identity.parent)
		identity.parent = 0
	}
	closePrivateACPPromptWindowsAncestors(identity.ancestors)
	identity.ancestors = nil
}

func closePrivateACPPromptStageWindowsChildren(identity *acpPromptStageIdentity) {
	if identity == nil {
		return
	}
	_ = releasePrivateACPPromptStageWindowsChildren(identity)
	// Teardown is best-effort. Retry any handles whose first close reported an
	// error, then discard the identity because its owner is closing as well.
	for name, child := range identity.children {
		if child.file != nil {
			_ = child.file.Close()
		}
		delete(identity.children, name)
	}
	identity.children = nil
}

func releasePrivateACPPromptStageWindowsChildren(identity *acpPromptStageIdentity) error {
	if identity == nil {
		return nil
	}
	var closeErr error
	for name, child := range identity.children {
		if child.file != nil {
			if err := child.file.Close(); err != nil {
				closeErr = errors.Join(closeErr, err)
				continue
			}
		}
		delete(identity.children, name)
	}
	if len(identity.children) == 0 {
		identity.children = nil
	}
	if closeErr != nil {
		return fmt.Errorf("close retained staged prompt input handles: %w", closeErr)
	}
	return nil
}

func acpPromptStageACLGrants(userMask, systemMask windows.ACCESS_MASK) ([]acpPromptStageACLGrant, error) {
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	userSID, err := tokenUser.User.Sid.Copy()
	if err != nil {
		return nil, err
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, err
	}
	if userSID.Equals(systemSID) {
		return []acpPromptStageACLGrant{{sid: userSID, mask: userMask | systemMask}}, nil
	}
	return []acpPromptStageACLGrant{
		{sid: userSID, mask: userMask},
		{sid: systemSID, mask: systemMask},
	}, nil
}

func applyPrivateACPPromptStageACLToHandle(handle windows.Handle, grants []acpPromptStageACLGrant, inheritance uint32) error {
	descriptor, err := privateACPPromptStageSecurityDescriptor(grants, inheritance)
	if err != nil {
		return err
	}
	acl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	if acl == nil {
		return errors.New("private staged prompt input security descriptor has no DACL")
	}
	err = windows.SetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	)
	// acl points into descriptor-owned memory.
	runtime.KeepAlive(descriptor)
	return err
}

func verifyPrivateACPPromptStagePathACL(
	path string,
	grants []acpPromptStageACLGrant,
	requireProtected bool,
	shape acpPromptStageACLShape,
) error {
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	return verifyPrivateACPPromptStageDescriptor(descriptor, grants, requireProtected, shape)
}

func verifyPrivateACPPromptStageHandleACL(
	handle windows.Handle,
	grants []acpPromptStageACLGrant,
	requireProtected bool,
	shape acpPromptStageACLShape,
) error {
	descriptor, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	return verifyPrivateACPPromptStageDescriptor(descriptor, grants, requireProtected, shape)
}

func verifyPrivateACPPromptStageDescriptor(
	descriptor *windows.SECURITY_DESCRIPTOR,
	grants []acpPromptStageACLGrant,
	requireProtected bool,
	shape acpPromptStageACLShape,
) error {
	if descriptor == nil {
		return errors.New("staged prompt input has no security descriptor")
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return err
	}
	if owner == nil || !owner.Equals(grants[0].sid) {
		return errors.New("staged prompt input owner is not the process user")
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return err
	}
	if requireProtected && control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("staged prompt input DACL is not protected")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	// Windows can mark a DACL as defaulted after SetSecurityInfo even when the
	// caller supplied it explicitly. Provenance does not affect access; the
	// complete effective owner, protection, ACE count, inheritance, principals,
	// and masks are verified below.
	if dacl == nil {
		return errors.New("staged prompt input has no DACL")
	}
	if int(dacl.AceCount) != len(grants) {
		return fmt.Errorf(
			"staged prompt input DACL entry count is %d; expected %d",
			dacl.AceCount,
			len(grants),
		)
	}

	seen := make([]bool, len(grants))
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return err
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("staged prompt input DACL contains a non-allow entry")
		}
		if !validACPPromptStageACEFlags(ace.Header.AceFlags, shape) {
			return errors.New("staged prompt input DACL has unexpected inheritance")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() {
			return errors.New("staged prompt input DACL contains an invalid SID")
		}
		match := -1
		for grantIndex, grant := range grants {
			if !seen[grantIndex] && sid.Equals(grant.sid) {
				match = grantIndex
				break
			}
		}
		if match < 0 {
			return errors.New("staged prompt input DACL grants an unexpected principal")
		}
		if !acpPromptStageMaskMatches(ace.Mask, grants[match].mask) {
			return errors.New("staged prompt input DACL has unexpected permissions")
		}
		seen[match] = true
	}
	for _, matched := range seen {
		if !matched {
			return errors.New("staged prompt input DACL omits an expected principal")
		}
	}
	runtime.KeepAlive(grants)
	return nil
}

func validACPPromptStageACEFlags(flags uint8, shape acpPromptStageACLShape) bool {
	switch shape {
	case acpPromptStageACLExplicitFile:
		return flags == 0
	case acpPromptStageACLInheritableDirectory:
		return flags == windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE
	case acpPromptStageACLExplicitDirectory:
		return flags == 0
	default:
		return false
	}
}

func acpPromptStageMaskMatches(actual, expected windows.ACCESS_MASK) bool {
	if expected&windows.GENERIC_ALL != 0 {
		if actual&windows.GENERIC_ALL != 0 {
			return true
		}
		required := windows.ACCESS_MASK(
			windows.FILE_GENERIC_READ |
				windows.FILE_GENERIC_WRITE |
				windows.FILE_GENERIC_EXECUTE |
				windows.DELETE |
				windows.WRITE_DAC |
				windows.WRITE_OWNER,
		)
		return actual&required == required
	}

	if expected&windows.GENERIC_READ != 0 &&
		actual&windows.GENERIC_READ == 0 &&
		actual&windows.FILE_GENERIC_READ != windows.FILE_GENERIC_READ {
		return false
	}
	if expected&windows.GENERIC_EXECUTE != 0 &&
		actual&windows.GENERIC_EXECUTE == 0 &&
		actual&windows.FILE_GENERIC_EXECUTE != windows.FILE_GENERIC_EXECUTE {
		return false
	}
	forbiddenWrite := windows.ACCESS_MASK(
		windows.GENERIC_ALL |
			windows.GENERIC_WRITE |
			windows.FILE_WRITE_DATA |
			windows.FILE_APPEND_DATA |
			windows.FILE_WRITE_EA |
			windows.FILE_WRITE_ATTRIBUTES |
			acpPromptFileDeleteChild |
			windows.DELETE |
			windows.WRITE_DAC |
			windows.WRITE_OWNER,
	)
	return actual&forbiddenWrite == 0
}
