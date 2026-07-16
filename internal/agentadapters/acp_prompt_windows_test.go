//go:build windows

package agentadapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	acp "github.com/coder/acp-go-sdk"
	"golang.org/x/sys/windows"
)

func allowACPPromptStageSubstitutionForTest(t *testing.T, stage *acpPromptStage) {
	t.Helper()
	if stage == nil || stage.identity == nil {
		t.Fatal("staged prompt input identity is unavailable")
	}
	if err := acquireMutablePrivateACPPromptStageDirectory(stage.identity); err != nil {
		t.Fatalf("prepare staged prompt input substitution: %v", err)
	}
	if err := verifyPrivateACPPromptStageIdentity("", stage.identity); err != nil {
		t.Fatalf("verify staged prompt input before substitution: %v", err)
	}
	if err := releasePrivateACPPromptStageWindowsChildren(stage.identity); err != nil {
		t.Fatalf("release staged prompt input handles before substitution: %v", err)
	}
}

func TestStagedFileURIWindowsDriveRoundTrip(t *testing.T) {
	t.Parallel()

	path := `C:\Users\operator\AppData\Local\Temp\input 1.txt`
	if got := cleanACPReadPath(path); got != filepath.Clean(path) {
		t.Fatalf("raw path = %q, want %q", got, filepath.Clean(path))
	}
	uri := stagedFileURI(path)
	if got := cleanACPReadPath(uri); got != filepath.Clean(path) {
		t.Fatalf("round trip = %q, want %q (URI %q)", got, filepath.Clean(path), uri)
	}
}

func TestPrivateACPPromptStageChildGoneRecognizesNativeStatus(t *testing.T) {
	t.Parallel()

	for _, status := range []windows.NTStatus{
		windows.STATUS_NO_SUCH_FILE,
		windows.STATUS_OBJECT_NAME_NOT_FOUND,
		windows.STATUS_OBJECT_PATH_NOT_FOUND,
		windows.STATUS_DELETE_PENDING,
	} {
		if !privateACPPromptStageChildGone(status) {
			t.Fatalf("privateACPPromptStageChildGone(%v) = false", status)
		}
	}
	if privateACPPromptStageChildGone(windows.STATUS_ACCESS_DENIED) {
		t.Fatal("privateACPPromptStageChildGone(STATUS_ACCESS_DENIED) = true")
	}
}

func TestACPPromptStageWindowsRequiresAbsoluteLocalDrive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{path: `C:\Users\operator\AppData\Local\Temp`, want: true},
		{path: `C:/Users/operator/AppData/Local/Temp`, want: true},
		{path: `C:relative\temp`, want: false},
		{path: `\\server\share\temp`, want: false},
		{path: `\\?\C:\Temp`, want: false},
		{path: `relative\temp`, want: false},
	}
	for _, test := range tests {
		if got := isLocalDriveACPPromptStagePath(test.path); got != test.want {
			t.Errorf("isLocalDriveACPPromptStagePath(%q) = %t, want %t", test.path, got, test.want)
		}
	}
}

func TestACPPromptStageWindowsRawDrivePathReadsExactTurnFile(t *testing.T) {
	file := promptTestFile("private.txt", "text/plain", []byte("confidential"))
	blocks, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{file}}, acp.PromptCapabilities{})
	if err != nil {
		t.Fatalf("buildACPPrompt: %v", err)
	}
	defer func() {
		if err := stage.cleanup(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}()

	path := cleanACPReadPath(blocks[0].ResourceLink.Uri)
	turn := newACPTurn(1024, nil)
	if err := turn.setPromptFiles(stage.files); err != nil {
		t.Fatalf("setPromptFiles: %v", err)
	}
	for _, spelling := range []string{
		path,
		strings.ToUpper(path),
		filepath.ToSlash(strings.ToLower(path)),
	} {
		got, ok := turn.promptFile(spelling)
		if !ok || string(got) != "confidential" {
			t.Fatalf("promptFile(%q) = %q, %t", spelling, got, ok)
		}
	}
	client := &acpChatClient{workspace: filepath.Dir(stage.dir)}
	client.setTurn(turn)
	defer client.clearTurn(turn)
	extended := `\\?\` + path
	if _, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: extended}); err == nil || !strings.Contains(err.Error(), "staged prompt input is not available") {
		t.Fatalf("extended-path stage read did not fail in the private namespace: %v", err)
	}
	if _, err := client.WriteTextFile(context.Background(), acp.WriteTextFileRequest{Path: extended, Content: "overwrite"}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("extended-path stage write did not fail in the private namespace: %v", err)
	}
}

func TestACPPromptStageWindowsRedactsMixedCaseSlashAndEscapedAliases(t *testing.T) {
	file := promptTestFile("private.txt", "text/plain", []byte("confidential"))
	blocks, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{file}}, acp.PromptCapabilities{})
	if err != nil {
		t.Fatalf("buildACPPrompt: %v", err)
	}
	defer func() { _ = stage.cleanup() }()

	turn := newACPTurn(1024, nil)
	if err := turn.setPromptFiles(stage.files); err != nil {
		t.Fatalf("setPromptFiles: %v", err)
	}
	path := cleanACPReadPath(blocks[0].ResourceLink.Uri)
	aliases := []string{
		strings.ToUpper(path),
		filepath.ToSlash(strings.ToLower(path)),
		strings.ReplaceAll(strings.ToUpper(path), `\`, `\\`),
	}
	for _, alias := range aliases {
		if got := turn.redactor().redact("read " + alias); strings.Contains(strings.ToLower(got), strings.ToLower(alias)) {
			t.Fatalf("mixed Windows alias escaped redaction: input %q, output %q", alias, got)
		}
	}
	mixed := filepath.ToSlash(strings.ToUpper(path))
	split := len(mixed) / 2
	for _, fragment := range []string{mixed[:split], mixed[split:]} {
		if got := turn.redactor().redactFragment(fragment); strings.Contains(strings.ToLower(got), strings.ToLower(fragment)) {
			t.Fatalf("split Windows alias escaped redaction: input %q, output %q", fragment, got)
		}
	}
}

func TestACPPromptStageWindowsCreateDoesNotFollowSubstitutedParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "prompt-parent")
	moved := filepath.Join(root, "prompt-parent-retained")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatalf("create prompt parent: %v", err)
	}

	callbackInvoked := false
	dir, identity, err := createPrivateACPPromptStageDirAt(parent, func() error {
		callbackInvoked = true
		if err := os.Rename(parent, moved); err != nil {
			return err
		}
		return os.Mkdir(parent, 0o700)
	})
	if err == nil {
		stage := &acpPromptStage{dir: dir, identity: identity}
		_ = stage.cleanup()
		t.Fatal("stage creation accepted a substituted parent path")
	}
	if !callbackInvoked {
		t.Fatalf("stage creation failed before parent substitution: %v", err)
	}
	assertDirectoryEmpty(t, parent)
	assertDirectoryEmpty(t, moved)
}

func assertDirectoryEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read directory %q: %v", path, err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory %q contains unexpected entries: %#v", path, entries)
	}
}

func TestACPBaselineStagesPortableFilenamesOnWindows(t *testing.T) {
	for _, filename := range []string{"bad:name.txt", "CON", "trailing.", `a<>|?*b.txt`} {
		t.Run(filename, func(t *testing.T) {
			file := promptTestFile(filename, "text/plain", []byte("windows input"))
			blocks, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{file}}, acp.PromptCapabilities{})
			if err != nil {
				t.Fatalf("buildACPPrompt(%q): %v", filename, err)
			}
			path := cleanACPReadPath(blocks[0].ResourceLink.Uri)
			if got, err := os.ReadFile(path); err != nil || string(got) != "windows input" {
				t.Fatalf("read staged %q = %q, %v", path, got, err)
			}
			if blocks[0].ResourceLink.Name != filename {
				t.Fatalf("resource link name = %q, want original %q", blocks[0].ResourceLink.Name, filename)
			}
			if err := stage.cleanup(); err != nil {
				t.Fatalf("cleanup: %v", err)
			}
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatalf("staged directory survived cleanup: %v", err)
			}
		})
	}
}

func TestACPPromptStageWindowsDirectoryDACLIsProtectedAndInheritable(t *testing.T) {
	dir, identity, err := createPrivateACPPromptStageDir()
	if err != nil {
		t.Fatalf("createPrivateACPPromptStageDir: %v", err)
	}
	defer func() {
		stage := &acpPromptStage{dir: dir, identity: identity}
		if err := stage.cleanup(); err != nil {
			t.Errorf("cleanup protected directory stage: %v", err)
		}
	}()

	grants, err := acpPromptStageACLGrants(acpPromptStageFullControl, acpPromptStageFullControl)
	if err != nil {
		t.Fatalf("acpPromptStageACLGrants: %v", err)
	}
	assertACPPromptStageWindowsACL(
		t,
		dir,
		grants,
		true,
		func(flags uint8) bool {
			return flags == windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE
		},
	)
}

func TestACPPromptStageWindowsParentAllowsInheritOnlyCreatorOwner(t *testing.T) {
	grants, err := acpPromptStageACLGrants(acpPromptStageFullControl, acpPromptStageFullControl)
	if err != nil {
		t.Fatalf("acpPromptStageACLGrants: %v", err)
	}
	var sddl strings.Builder
	sddl.WriteString("O:")
	sddl.WriteString(grants[0].sid.String())
	sddl.WriteString("D:P")
	for _, grant := range grants {
		sddl.WriteString("(A;;GA;;;")
		sddl.WriteString(grant.sid.String())
		sddl.WriteByte(')')
	}
	// CREATOR OWNER is untrusted as an effective principal, but this common
	// managed-directory ACE applies only to future descendants.
	sddl.WriteString("(A;OICIIO;GA;;;CO)")
	descriptor, err := windows.SecurityDescriptorFromString(sddl.String())
	if err != nil {
		t.Fatalf("SecurityDescriptorFromString: %v", err)
	}
	if err := verifyPrivateACPPromptWindowsDirectoryDescriptor(descriptor, true); err != nil {
		t.Fatalf("verify parent descriptor with inherit-only CREATOR OWNER: %v", err)
	}
}

func TestACPPromptStageWindowsManagedAncestorAllowsTrustedInstallerControl(t *testing.T) {
	trustedInstallerSID, err := windows.StringToSid(acpPromptStageTrustedInstallerSID)
	if err != nil {
		t.Fatalf("StringToSid: %v", err)
	}
	sddl := "O:" + trustedInstallerSID.String() + "D:P(A;;GA;;;" + trustedInstallerSID.String() + ")"
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatalf("SecurityDescriptorFromString: %v", err)
	}
	if err := verifyPrivateACPPromptWindowsDirectoryDescriptor(descriptor, false); err != nil {
		t.Fatalf("verify TrustedInstaller-managed ancestor: %v", err)
	}
	if err := verifyPrivateACPPromptWindowsDirectoryDescriptor(descriptor, true); err == nil {
		t.Fatal("strict user-owned descriptor verification accepted TrustedInstaller ownership")
	}
}

func TestACPPromptStageWindowsStrictParentRejectsTrustedInstallerControl(t *testing.T) {
	grants, err := acpPromptStageACLGrants(acpPromptStageFullControl, acpPromptStageFullControl)
	if err != nil {
		t.Fatalf("acpPromptStageACLGrants: %v", err)
	}
	trustedInstallerSID, err := windows.StringToSid(acpPromptStageTrustedInstallerSID)
	if err != nil {
		t.Fatalf("StringToSid: %v", err)
	}
	sddl := "O:" + grants[0].sid.String() + "D:P(A;;GA;;;" + grants[0].sid.String() + ")" +
		"(A;;GA;;;" + trustedInstallerSID.String() + ")"
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatalf("SecurityDescriptorFromString: %v", err)
	}
	if err := verifyPrivateACPPromptWindowsDirectoryDescriptor(descriptor, false); err != nil {
		t.Fatalf("verify managed parent with TrustedInstaller control: %v", err)
	}
	if err := verifyPrivateACPPromptWindowsDirectoryDescriptor(descriptor, true); err == nil {
		t.Fatal("strict descriptor verification accepted TrustedInstaller control")
	}
}

func TestACPPromptStageWindowsManagedAncestorRejectsUntrustedControl(t *testing.T) {
	grants, err := acpPromptStageACLGrants(acpPromptStageFullControl, acpPromptStageFullControl)
	if err != nil {
		t.Fatalf("acpPromptStageACLGrants: %v", err)
	}
	worldSID, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatalf("create Everyone SID: %v", err)
	}
	usersSID, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		t.Fatalf("create Users SID: %v", err)
	}
	authenticatedUsersSID, err := windows.CreateWellKnownSid(windows.WinAuthenticatedUserSid)
	if err != nil {
		t.Fatalf("create Authenticated Users SID: %v", err)
	}
	userSID := grants[0].sid.String()
	tests := []struct {
		name string
		sddl string
	}{
		{
			name: "untrusted owner",
			sddl: "O:" + worldSID.String() + "D:P(A;;GA;;;" + userSID + ")",
		},
		{
			name: "Everyone full control",
			sddl: "O:" + userSID + "D:P(A;;GA;;;" + userSID + ")(A;;GA;;;" + worldSID.String() + ")",
		},
		{
			name: "Users delete child",
			sddl: "O:" + userSID + "D:P(A;;GA;;;" + userSID + ")(A;;0x40;;;" + usersSID.String() + ")",
		},
		{
			name: "Authenticated Users replace owner",
			sddl: "O:" + userSID + "D:P(A;;GA;;;" + userSID + ")(A;;0x80000;;;" + authenticatedUsersSID.String() + ")",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor, err := windows.SecurityDescriptorFromString(test.sddl)
			if err != nil {
				t.Fatalf("SecurityDescriptorFromString: %v", err)
			}
			if err := verifyPrivateACPPromptWindowsDirectoryDescriptor(descriptor, false); err == nil {
				t.Fatal("managed descriptor verification accepted untrusted control")
			}
		})
	}
}

func TestACPPromptStageWindowsChildStartsWithProtectedDACLBeforeWrite(t *testing.T) {
	dir, identity, err := createPrivateACPPromptStageDir()
	if err != nil {
		t.Fatalf("createPrivateACPPromptStageDir: %v", err)
	}
	defer func() {
		stage := &acpPromptStage{dir: dir, identity: identity, fileNames: []string{"child.bin"}}
		if err := stage.cleanup(); err != nil {
			t.Errorf("cleanup protected child stage: %v", err)
		}
	}()

	path := filepath.Join(dir, "child.bin")
	file, err := openPrivateACPPromptStageFile(identity, "child.bin")
	if err != nil {
		t.Fatalf("create protected child: %v", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		t.Fatalf("stat protected child: %v", err)
	}
	if info.Size() != 0 {
		_ = file.Close()
		t.Fatalf("child size before ACL verification = %d, want 0", info.Size())
	}
	grants, err := acpPromptStageACLGrants(acpPromptStageFullControl, acpPromptStageFullControl)
	if err != nil {
		_ = file.Close()
		t.Fatalf("acpPromptStageACLGrants: %v", err)
	}
	assertACPPromptStageWindowsACL(
		t,
		path,
		grants,
		true,
		func(flags uint8) bool {
			return flags == 0
		},
	)
	if err := file.Close(); err != nil {
		t.Fatalf("close protected child: %v", err)
	}
}

func TestACPPromptStageWindowsDirectorySealTransition(t *testing.T) {
	dir, identity, err := createPrivateACPPromptStageDir()
	if err != nil {
		t.Fatalf("createPrivateACPPromptStageDir: %v", err)
	}
	stage := &acpPromptStage{dir: dir, identity: identity, fileNames: []string{"child.bin"}}
	defer func() {
		if err := stage.cleanup(); err != nil {
			t.Errorf("cleanup sealed directory stage: %v", err)
		}
	}()

	file, err := openPrivateACPPromptStageFile(identity, "child.bin")
	if err != nil {
		t.Fatalf("create protected child: %v", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write([]byte("sealed transition")); err != nil {
		t.Fatalf("write child: %v", err)
	}
	if err := file.Sync(); err != nil {
		t.Fatalf("sync child: %v", err)
	}
	if err := sealPrivateACPPromptStageFile(file); err != nil {
		t.Fatalf("seal child ACL: %v", err)
	}
	if err := retainPrivateACPPromptStageFile(identity, "child.bin", file); err != nil {
		t.Fatalf("retain child identity: %v", err)
	}
	if err := sealPrivateACPPromptStageDir(dir, identity); err != nil {
		t.Fatalf("seal directory transition: %v", err)
	}
	if identity.dirMutable {
		t.Fatal("sealed directory retained mutable state")
	}
	if err := verifySealedPrivateACPPromptStageFile(filepath.Join(dir, "child.bin")); err != nil {
		t.Fatalf("verify sealed child: %v", err)
	}
	if err := verifySealedPrivateACPPromptStageDir(dir); err != nil {
		t.Fatalf("verify sealed directory: %v", err)
	}
}

func TestACPPromptStageWindowsQuarantineRenamesWithinSourceParent(t *testing.T) {
	file := promptTestFile("private.txt", "text/plain", []byte("confidential"))
	_, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{file}}, acp.PromptCapabilities{})
	if err != nil {
		t.Fatalf("buildACPPrompt: %v", err)
	}
	defer func() {
		if stage.dir != "" {
			_ = stage.cleanup()
		}
	}()
	if len(stage.identity.children) == 0 || len(stage.fileNames) != 1 {
		t.Fatalf("retained staged prompt children = %d, filenames = %v", len(stage.identity.children), stage.fileNames)
	}
	childName := stage.fileNames[0]

	original := stage.dir
	if err := quarantinePrivateACPPromptStage(original, stage.identity); err != nil {
		t.Fatalf("quarantine staged prompt input: %v", err)
	}
	if stage.identity.currentName == stage.identity.originalName {
		t.Fatal("quarantine retained the original stage name")
	}
	quarantined := filepath.Join(stage.identity.canonicalParent, stage.identity.currentName)
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatalf("original staged prompt path survived quarantine: %v", err)
	}
	if info, err := os.Stat(quarantined); err != nil || !info.IsDir() {
		t.Fatalf("quarantined staged prompt directory = %#v, %v", info, err)
	}
	if len(stage.identity.children) != 0 {
		t.Fatalf("retained child handles survived quarantine: %d", len(stage.identity.children))
	}
	if info, err := os.Stat(filepath.Join(quarantined, childName)); err != nil || info.IsDir() {
		t.Fatalf("quarantined staged prompt child = %#v, %v", info, err)
	}
	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup quarantined staged prompt input: %v", err)
	}
	if _, err := os.Stat(quarantined); !os.IsNotExist(err) {
		t.Fatalf("quarantined staged prompt path survived cleanup: %v", err)
	}
}

func TestACPPromptStageWindowsSealsFileAndDirectoryDACLs(t *testing.T) {
	file := promptTestFile("private.txt", "text/plain", []byte("confidential"))
	blocks, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{file}}, acp.PromptCapabilities{})
	if err != nil {
		t.Fatalf("buildACPPrompt: %v", err)
	}
	defer func() {
		if err := stage.cleanup(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}()

	path := cleanACPReadPath(blocks[0].ResourceLink.Uri)
	fileGrants, err := acpPromptStageACLGrants(acpPromptStageReadFile, acpPromptStageFullControl)
	if err != nil {
		t.Fatalf("file ACL grants: %v", err)
	}
	assertACPPromptStageWindowsACL(t, path, fileGrants, true, func(flags uint8) bool {
		return flags == 0
	})
	dirGrants, err := acpPromptStageACLGrants(acpPromptStageReadDir, acpPromptStageFullControl)
	if err != nil {
		t.Fatalf("directory ACL grants: %v", err)
	}
	assertACPPromptStageWindowsACL(t, stage.dir, dirGrants, true, func(flags uint8) bool {
		return flags == 0
	})
}

func TestACPPromptStageWindowsRetainedIdentityAllowsRestrictiveReaders(t *testing.T) {
	content := []byte("confidential")
	file := promptTestFile("private.txt", "text/plain", content)
	blocks, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{file}}, acp.PromptCapabilities{})
	if err != nil {
		t.Fatalf("buildACPPrompt: %v", err)
	}
	defer func() {
		if err := stage.cleanup(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}()
	if stage.identity == nil || stage.identity.dirMutable {
		t.Fatal("sealed stage retained its write-capable construction directory handle")
	}

	path := cleanACPReadPath(blocks[0].ResourceLink.Uri)
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("encode staged path: %v", err)
	}
	reader, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("open staged file with read-only sharing: %v", err)
	}
	readerOpen := true
	defer func() {
		if readerOpen {
			_ = windows.CloseHandle(reader)
		}
	}()

	dirPtr, err := windows.UTF16PtrFromString(stage.dir)
	if err != nil {
		t.Fatalf("encode staged directory path: %v", err)
	}
	dirReader, err := windows.CreateFile(
		dirPtr,
		windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		t.Fatalf("open staged directory with read-only sharing: %v", err)
	}
	dirReaderOpen := true
	defer func() {
		if dirReaderOpen {
			_ = windows.CloseHandle(dirReader)
		}
	}()

	buffer := make([]byte, len(content))
	var read uint32
	if err := windows.ReadFile(reader, buffer, &read, nil); err != nil || int(read) != len(content) || string(buffer[:read]) != string(content) {
		t.Fatalf("restrictive staged read = %q (%d bytes), %v", buffer[:read], read, err)
	}
	if err := stage.verifyIdentity(); err != nil {
		t.Fatalf("verify retained identities while restrictive readers are open: %v", err)
	}

	if err := windows.CloseHandle(reader); err != nil {
		t.Fatalf("close staged file reader: %v", err)
	}
	readerOpen = false
	if err := windows.CloseHandle(dirReader); err != nil {
		t.Fatalf("close staged directory reader: %v", err)
	}
	dirReaderOpen = false
}

func TestACPPromptStageWindowsCleanupRetriesAfterRestrictiveDirectoryReader(t *testing.T) {
	file := promptTestFile("private.txt", "text/plain", []byte("confidential"))
	_, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{file}}, acp.PromptCapabilities{})
	if err != nil {
		t.Fatalf("buildACPPrompt: %v", err)
	}
	defer func() {
		if stage.dir != "" {
			_ = stage.cleanup()
		}
	}()
	stage.waitForRetry = func(time.Duration) {}

	dir := stage.dir
	dirPtr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatalf("encode staged directory path: %v", err)
	}
	dirReader, err := windows.CreateFile(
		dirPtr,
		windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		t.Fatalf("open staged directory with read-only sharing: %v", err)
	}
	readerOpen := true
	defer func() {
		if readerOpen {
			_ = windows.CloseHandle(dirReader)
		}
	}()

	if err := stage.cleanup(); err == nil {
		t.Fatal("cleanup with a restrictive directory reader unexpectedly succeeded")
	}
	if stage.identity == nil || !stage.identity.cleanupACLReady || stage.identity.dirMutable {
		t.Fatalf("cleanup retry state = identity %#v, want restored ACL on a narrow retained handle", stage.identity)
	}
	cleanupGrants, err := acpPromptStageACLGrants(acpPromptStageFullControl, acpPromptStageFullControl)
	if err != nil {
		t.Fatalf("cleanup ACL grants: %v", err)
	}
	assertACPPromptStageWindowsACL(t, dir, cleanupGrants, true, func(flags uint8) bool {
		return flags == 0
	})

	if err := windows.CloseHandle(dirReader); err != nil {
		t.Fatalf("close staged directory reader: %v", err)
	}
	readerOpen = false
	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup after restrictive reader closed: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("staged directory survived cleanup retry: %v", err)
	}
}

func TestACPPromptStageWindowsReadMaskRejectsDeleteChild(t *testing.T) {
	actual := windows.ACCESS_MASK(
		windows.FILE_GENERIC_READ |
			windows.FILE_GENERIC_EXECUTE |
			acpPromptFileDeleteChild,
	)
	if acpPromptStageMaskMatches(actual, acpPromptStageReadDir) {
		t.Fatal("read-only staged directory mask accepted FILE_DELETE_CHILD")
	}
}

func assertACPPromptStageWindowsACL(
	t *testing.T,
	path string,
	grants []acpPromptStageACLGrant,
	wantProtected bool,
	validFlags func(uint8) bool,
) {
	t.Helper()

	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo: %v", err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		t.Fatalf("security descriptor owner: %v", err)
	}
	if owner == nil || !owner.Equals(grants[0].sid) {
		t.Fatalf("owner = %v, want current process user %s", owner, grants[0].sid.String())
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatalf("security descriptor control: %v", err)
	}
	if got := control&windows.SE_DACL_PROTECTED != 0; got != wantProtected {
		t.Fatalf("DACL protected = %t, want %t", got, wantProtected)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("security descriptor DACL: %v", err)
	}
	if dacl == nil {
		t.Fatal("security descriptor has no DACL")
	}
	if got := int(dacl.AceCount); got != len(grants) {
		t.Fatalf("DACL ACE count = %d, want %d", got, len(grants))
	}
	seen := make([]bool, len(grants))
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			t.Fatalf("GetAce(%d): %v", index, err)
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("ACE %d is not an allow ACE", index)
		}
		if !validFlags(ace.Header.AceFlags) {
			t.Fatalf("ACE %d flags = %#x", index, ace.Header.AceFlags)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		match := -1
		for grantIndex, grant := range grants {
			if !seen[grantIndex] && sid.Equals(grant.sid) {
				match = grantIndex
				break
			}
		}
		if match < 0 {
			t.Fatalf("ACE %d grants unexpected SID %s", index, sid.String())
		}
		if !acpPromptStageMaskMatches(ace.Mask, grants[match].mask) {
			t.Fatalf("ACE %d mask = %#x, want %#x", index, ace.Mask, grants[match].mask)
		}
		seen[match] = true
	}
	for index, matched := range seen {
		if !matched {
			t.Fatalf("DACL omits grant %d", index)
		}
	}
}
