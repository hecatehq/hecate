package agentadapters

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

const (
	executableIdentityPrefixLimit = 4096
	executableIdentityMaxBytes    = int64(1 << 30)
)

// measureExecutableIdentity measures the filesystem object that would be used
// for an external-agent launch. The invocation path remains distinct from the
// canonical target because shared shims such as Volta dispatch using argv[0].
func measureExecutableIdentity(path string) (ExecutableIdentity, error) {
	return measureExecutableIdentityContext(context.Background(), path)
}

func measureExecutableIdentityContext(ctx context.Context, path string) (ExecutableIdentity, error) {
	return measureExecutableIdentityWithContextAndHook(ctx, path, nil)
}

// The hook keeps mutation tests deterministic without adding a process-wide
// seam that production callers could accidentally share.
func measureExecutableIdentityWithHook(path string, afterOpen func()) (ExecutableIdentity, error) {
	return measureExecutableIdentityWithContextAndHook(context.Background(), path, afterOpen)
}

func measureExecutableIdentityWithContextAndHook(ctx context.Context, path string, afterOpen func()) (ExecutableIdentity, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ExecutableIdentity{}, err
	}
	invocationPath, canonicalPath, err := executableIdentityPaths(path)
	if err != nil {
		return ExecutableIdentity{}, err
	}
	if err := validateAgentProcessLauncher(invocationPath); err != nil {
		return ExecutableIdentity{}, executableIdentityUnavailable("validate executable launcher", err)
	}

	file, err := os.Open(canonicalPath)
	if err != nil {
		return ExecutableIdentity{}, executableIdentityUnavailable("open executable", err)
	}
	defer file.Close()

	before, err := file.Stat()
	if err != nil {
		return ExecutableIdentity{}, executableIdentityUnavailable("inspect opened executable", err)
	}
	if err := validateExecutableIdentityFile(before); err != nil {
		return ExecutableIdentity{}, err
	}
	beforeID, err := executableFileIdentity(file, before)
	if err != nil {
		return ExecutableIdentity{}, executableIdentityUnavailable("read executable file identity", err)
	}

	if afterOpen != nil {
		afterOpen()
	}

	digest := sha256.New()
	prefix := make([]byte, executableIdentityPrefixLimit)
	reader := executableIdentityContextReader{ctx: ctx, reader: file}
	prefixSize, readErr := io.ReadFull(reader, prefix)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		if err := ctx.Err(); err != nil {
			return ExecutableIdentity{}, err
		}
		return ExecutableIdentity{}, executableIdentityUnavailable("read executable", readErr)
	}
	if _, err := digest.Write(prefix[:prefixSize]); err != nil {
		return ExecutableIdentity{}, executableIdentityUnavailable("hash executable prefix", err)
	}
	if _, err := io.Copy(digest, reader); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ExecutableIdentity{}, ctxErr
		}
		return ExecutableIdentity{}, executableIdentityUnavailable("hash executable", err)
	}

	after, err := file.Stat()
	if err != nil {
		return ExecutableIdentity{}, executableIdentityRaced("inspect executable after hashing", err)
	}
	afterID, err := executableFileIdentity(file, after)
	if err != nil {
		return ExecutableIdentity{}, executableIdentityRaced("read executable file identity after hashing", err)
	}
	if !sameExecutableSnapshot(before, beforeID, after, afterID) {
		return ExecutableIdentity{}, executableIdentityRaced("executable metadata changed while hashing", nil)
	}
	if err := verifyExecutableIdentityPath(invocationPath, canonicalPath, after); err != nil {
		return ExecutableIdentity{}, err
	}

	coverage, launcherChain := classifyExecutableIdentity(invocationPath, canonicalPath, prefix[:prefixSize])
	identity := ExecutableIdentity{
		SchemaVersion:  ExecutableIdentitySchemaVersion,
		InvocationPath: invocationPath,
		CanonicalPath:  canonicalPath,
		SHA256:         hex.EncodeToString(digest.Sum(nil)),
		Coverage:       coverage,
		LauncherChain:  launcherChain,
		FileID:         afterID,
		// Keep every portable os.FileMode bit in the approved identity. Permission
		// bits alone would miss a later setuid/setgid/sticky-bit change on Unix.
		Mode:      uint32(after.Mode()),
		SizeBytes: after.Size(),
		Publisher: ExecutablePublisherEvidence{
			Status:   ExecutablePublisherUnavailable,
			Platform: runtime.GOOS,
		},
	}
	identity.IdentityToken = executableIdentityToken(identity)
	return identity, nil
}

type executableIdentityContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r executableIdentityContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func executableIdentityPaths(path string) (string, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", executableIdentityUnavailable("validate executable path", errors.New("path is empty"))
	}
	if strings.ContainsRune(path, '\x00') {
		return "", "", executableIdentityUnavailable("validate executable path", errors.New("path contains a NUL byte"))
	}
	invocationPath, err := filepath.Abs(path)
	if err != nil {
		return "", "", executableIdentityUnavailable("make executable path absolute", err)
	}
	invocationPath = filepath.Clean(invocationPath)
	canonicalPath, err := filepath.EvalSymlinks(invocationPath)
	if err != nil {
		return "", "", executableIdentityUnavailable("resolve executable symlinks", err)
	}
	if !filepath.IsAbs(canonicalPath) {
		canonicalPath, err = filepath.Abs(canonicalPath)
		if err != nil {
			return "", "", executableIdentityUnavailable("make canonical executable path absolute", err)
		}
	}
	return invocationPath, filepath.Clean(canonicalPath), nil
}

func validateExecutableIdentityFile(info fs.FileInfo) error {
	if !info.Mode().IsRegular() {
		return executableIdentityUnavailable("validate executable", errors.New("target is not a regular file"))
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return executableIdentityUnavailable("validate executable", errors.New("target is not executable"))
	}
	if info.Size() < 0 || info.Size() > executableIdentityMaxBytes {
		return executableIdentityUnavailable("validate executable", errors.New("target exceeds the executable identity measurement limit"))
	}
	return nil
}

func sameExecutableSnapshot(before fs.FileInfo, beforeID string, after fs.FileInfo, afterID string) bool {
	return beforeID != "" && beforeID == afterID &&
		os.SameFile(before, after) &&
		before.Mode() == after.Mode() &&
		before.Size() == after.Size() &&
		before.ModTime().Equal(after.ModTime())
}

func verifyExecutableIdentityPath(invocationPath, canonicalPath string, openedInfo fs.FileInfo) error {
	resolvedAgain, err := filepath.EvalSymlinks(invocationPath)
	if err != nil {
		return executableIdentityRaced("resolve executable symlinks after hashing", err)
	}
	if !filepath.IsAbs(resolvedAgain) {
		resolvedAgain, err = filepath.Abs(resolvedAgain)
		if err != nil {
			return executableIdentityRaced("make re-resolved executable path absolute", err)
		}
	}
	if filepath.Clean(resolvedAgain) != canonicalPath {
		return executableIdentityRaced("executable symlink target changed while hashing", nil)
	}
	current, err := os.Stat(canonicalPath)
	if err != nil {
		return executableIdentityRaced("inspect executable path after hashing", err)
	}
	if !os.SameFile(openedInfo, current) {
		return executableIdentityRaced("executable path was replaced while hashing", nil)
	}
	return nil
}

func classifyExecutableIdentity(invocationPath, canonicalPath string, prefix []byte) (string, []string) {
	sharedLauncher := isSharedExecutableLauncher(canonicalPath)
	interpreters, script := executableShebangInterpreters(prefix)
	if !sharedLauncher && !script && isNativeExecutable(prefix) {
		return ExecutableCoverageBinary, nil
	}

	chain := make([]string, 0, 3)
	chain = appendUniqueExecutableChain(chain, invocationPath)
	chain = appendUniqueExecutableChain(chain, canonicalPath)
	for _, interpreter := range interpreters {
		chain = appendUniqueExecutableChain(chain, interpreter)
	}
	return ExecutableCoverageLauncherOnly, chain
}

func isSharedExecutableLauncher(canonicalPath string) bool {
	base := strings.ToLower(filepath.Base(canonicalPath))
	return base == "volta-shim" || base == "volta-shim.exe"
}

func executableShebangInterpreters(prefix []byte) ([]string, bool) {
	if len(prefix) < 2 || prefix[0] != '#' || prefix[1] != '!' {
		return nil, false
	}
	line := prefix[2:]
	if index := bytes.IndexByte(line, '\n'); index >= 0 {
		line = line[:index]
	}
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if !utf8.Valid(line) {
		return nil, true
	}
	fields := strings.Fields(string(line))
	if len(fields) == 0 {
		return nil, true
	}
	interpreter := fields[0]
	if strings.EqualFold(filepath.Base(interpreter), "env") {
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "-") || strings.ContainsRune(field, '=') {
				continue
			}
			if isSafeLauncherName(field) {
				return []string{interpreter, field}, true
			}
			break
		}
	}
	return []string{interpreter}, true
}

func isSafeLauncherName(value string) bool {
	if value == "" || filepath.Base(value) != value {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._+-", r) {
			continue
		}
		return false
	}
	return true
}

func appendUniqueExecutableChain(chain []string, value string) []string {
	if value == "" {
		return chain
	}
	for _, existing := range chain {
		if existing == value {
			return chain
		}
	}
	return append(chain, value)
}

func isNativeExecutable(prefix []byte) bool {
	if runtime.GOOS == "windows" {
		return len(prefix) >= 2 && prefix[0] == 'M' && prefix[1] == 'Z'
	}
	if len(prefix) < 4 {
		return false
	}
	magic := binary.BigEndian.Uint32(prefix[:4])
	if runtime.GOOS == "darwin" {
		switch magic {
		case 0xfeedface, 0xcefaedfe, // Mach-O 32-bit
			0xfeedfacf, 0xcffaedfe, // Mach-O 64-bit
			0xcafebabe, 0xbebafeca, // universal Mach-O
			0xcafebabf, 0xbfbafeca: // universal Mach-O, 64-bit table
			return true
		}
		return false
	}
	// Linux, the BSDs, and Solaris use ELF for the agent binaries Hecate
	// supports. Unknown formats remain launcher-only rather than receiving a
	// stronger coverage claim Hecate cannot substantiate.
	return magic == 0x7f454c46
}

func executableIdentityToken(identity ExecutableIdentity) string {
	digest := sha256.New()
	writeIdentityTokenField(digest, identity.SchemaVersion)
	writeIdentityTokenField(digest, identity.InvocationPath)
	writeIdentityTokenField(digest, identity.CanonicalPath)
	writeIdentityTokenField(digest, identity.SHA256)
	writeIdentityTokenField(digest, identity.Coverage)
	writeIdentityTokenField(digest, identity.FileID)
	writeIdentityTokenField(digest, fmt.Sprintf("%o", identity.Mode))
	writeIdentityTokenField(digest, fmt.Sprintf("%d", identity.SizeBytes))
	writeIdentityTokenField(digest, identity.Publisher.Status)
	writeIdentityTokenField(digest, identity.Publisher.Platform)
	writeIdentityTokenField(digest, identity.Publisher.Identifier)
	writeIdentityTokenField(digest, identity.Publisher.TeamID)
	writeIdentityTokenField(digest, fmt.Sprintf("%d", len(identity.LauncherChain)))
	for _, item := range identity.LauncherChain {
		writeIdentityTokenField(digest, item)
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

func writeIdentityTokenField(writer io.Writer, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = io.WriteString(writer, value)
}

func executableIdentityUnavailable(operation string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrExecutableIdentityUnavailable, operation, err)
}

func executableIdentityRaced(operation string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s", ErrExecutableIdentityRaced, operation)
	}
	return fmt.Errorf("%w: %s: %v", ErrExecutableIdentityRaced, operation, err)
}
