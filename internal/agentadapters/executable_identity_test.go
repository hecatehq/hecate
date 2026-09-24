package agentadapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMeasureExecutableIdentityHonorsCancelledContext(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := measureExecutableIdentityContext(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled measurement error = %v, want context.Canceled", err)
	}
}

func TestMeasureExecutableIdentity_NativeBinaryIsDeterministic(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	first, err := measureExecutableIdentity(path)
	if err != nil {
		t.Fatalf("measureExecutableIdentity: %v", err)
	}
	second, err := measureExecutableIdentity(path)
	if err != nil {
		t.Fatalf("measureExecutableIdentity second call: %v", err)
	}

	if first.SchemaVersion != ExecutableIdentitySchemaVersion {
		t.Fatalf("schema version = %q, want %q", first.SchemaVersion, ExecutableIdentitySchemaVersion)
	}
	if first.InvocationPath == "" || !filepath.IsAbs(first.InvocationPath) {
		t.Fatalf("invocation path = %q, want absolute path", first.InvocationPath)
	}
	if first.CanonicalPath == "" || !filepath.IsAbs(first.CanonicalPath) {
		t.Fatalf("canonical path = %q, want absolute path", first.CanonicalPath)
	}
	if first.Coverage != ExecutableCoverageBinary {
		t.Fatalf("coverage = %q, want %q", first.Coverage, ExecutableCoverageBinary)
	}
	if first.FileID == "" {
		t.Fatal("file ID is empty")
	}
	if len(first.SHA256) != sha256.Size*2 {
		t.Fatalf("SHA-256 length = %d, want %d", len(first.SHA256), sha256.Size*2)
	}
	if !strings.HasPrefix(first.IdentityToken, "sha256:") {
		t.Fatalf("identity token = %q, want sha256 prefix", first.IdentityToken)
	}
	if first.Publisher.Status != ExecutablePublisherUnavailable || first.Publisher.Platform != runtime.GOOS {
		t.Fatalf("publisher = %#v, want unavailable evidence for %s", first.Publisher, runtime.GOOS)
	}
	if first.IdentityToken != second.IdentityToken {
		t.Fatalf("identity token changed without a file change: %q != %q", first.IdentityToken, second.IdentityToken)
	}
}

func TestMeasureExecutableIdentity_ScriptPreservesSymlinkInvocation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows external agents require native .exe files")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-script")
	contents := []byte("#!/usr/bin/env bash --credential=do-not-record\nexit 0\n")
	writeExecutableIdentityFixture(t, target, contents)
	invocation := filepath.Join(dir, "agent")
	if err := os.Symlink(target, invocation); err != nil {
		t.Fatalf("create executable symlink: %v", err)
	}

	identity, err := measureExecutableIdentity(invocation)
	if err != nil {
		t.Fatalf("measureExecutableIdentity: %v", err)
	}
	wantInvocation, _ := filepath.Abs(invocation)
	wantCanonical, _ := filepath.EvalSymlinks(invocation)
	if identity.InvocationPath != filepath.Clean(wantInvocation) {
		t.Fatalf("invocation path = %q, want %q", identity.InvocationPath, wantInvocation)
	}
	if identity.CanonicalPath != filepath.Clean(wantCanonical) {
		t.Fatalf("canonical path = %q, want %q", identity.CanonicalPath, wantCanonical)
	}
	if identity.Coverage != ExecutableCoverageLauncherOnly {
		t.Fatalf("coverage = %q, want %q", identity.Coverage, ExecutableCoverageLauncherOnly)
	}
	chain := strings.Join(identity.LauncherChain, "\n")
	for _, want := range []string{identity.InvocationPath, identity.CanonicalPath, "/usr/bin/env", "bash"} {
		if !strings.Contains(chain, want) {
			t.Errorf("launcher chain %q does not contain %q", chain, want)
		}
	}
	if strings.Contains(chain, "credential") || strings.Contains(chain, "do-not-record") {
		t.Fatalf("launcher chain exposed shebang arguments: %q", chain)
	}
	wantDigest := sha256.Sum256(contents)
	if identity.SHA256 != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("SHA-256 = %q, want %x", identity.SHA256, wantDigest)
	}
}

func TestMeasureExecutableIdentity_VoltaShimIsLauncherOnly(t *testing.T) {
	dir := t.TempDir()
	name := "volta-shim"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	writeExecutableIdentityFixture(t, path, append(nativeExecutableFixturePrefix(), []byte("shared launcher")...))

	identity, err := measureExecutableIdentity(path)
	if err != nil {
		t.Fatalf("measureExecutableIdentity: %v", err)
	}
	if identity.Coverage != ExecutableCoverageLauncherOnly {
		t.Fatalf("coverage = %q, want %q", identity.Coverage, ExecutableCoverageLauncherOnly)
	}
	if len(identity.LauncherChain) == 0 || identity.LauncherChain[0] != identity.InvocationPath {
		t.Fatalf("launcher chain = %#v, want invocation path", identity.LauncherChain)
	}
}

func TestMeasureExecutableIdentity_ContentAndReplacementChangeIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("atomic executable replacement has different sharing semantics on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "agent")
	original := append(nativeExecutableFixturePrefix(), []byte("first payload")...)
	writeExecutableIdentityFixture(t, path, original)

	first, err := measureExecutableIdentity(path)
	if err != nil {
		t.Fatalf("measure original: %v", err)
	}
	changed := append(nativeExecutableFixturePrefix(), []byte("other payload")...)
	writeExecutableIdentityFixture(t, path, changed)
	second, err := measureExecutableIdentity(path)
	if err != nil {
		t.Fatalf("measure changed contents: %v", err)
	}
	if first.SHA256 == second.SHA256 || first.IdentityToken == second.IdentityToken {
		t.Fatalf("content change did not change identity: before=%#v after=%#v", first, second)
	}

	replacement := filepath.Join(dir, "replacement")
	writeExecutableIdentityFixture(t, replacement, changed)
	if err := os.Rename(replacement, path); err != nil {
		t.Fatalf("replace executable: %v", err)
	}
	third, err := measureExecutableIdentity(path)
	if err != nil {
		t.Fatalf("measure replacement: %v", err)
	}
	if second.SHA256 != third.SHA256 {
		t.Fatalf("replacement SHA-256 = %q, want unchanged %q", third.SHA256, second.SHA256)
	}
	if second.FileID == third.FileID || second.IdentityToken == third.IdentityToken {
		t.Fatalf("same-byte file replacement did not change filesystem identity: before=%#v after=%#v", second, third)
	}
}

func TestExecutableIdentityTokenIncludesSpecialModeBits(t *testing.T) {
	identity := ExecutableIdentity{
		SchemaVersion:  ExecutableIdentitySchemaVersion,
		InvocationPath: "/agent",
		CanonicalPath:  "/agent",
		SHA256:         strings.Repeat("a", sha256.Size*2),
		Coverage:       ExecutableCoverageBinary,
		FileID:         "fixture",
		Mode:           0o700,
		SizeBytes:      1,
		Publisher: ExecutablePublisherEvidence{
			Status: ExecutablePublisherUnavailable,
		},
	}
	ordinary := executableIdentityToken(identity)
	identity.Mode |= uint32(fs.ModeSetuid)
	if elevated := executableIdentityToken(identity); elevated == ordinary {
		t.Fatal("setuid mode change did not change executable identity token")
	}
}

func TestMeasureExecutableIdentity_DetectsChangeDuringMeasurement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows mutation while a file is open depends on sharing flags")
	}
	path := filepath.Join(t.TempDir(), "agent")
	writeExecutableIdentityFixture(t, path, append(nativeExecutableFixturePrefix(), []byte("before")...))

	_, err := measureExecutableIdentityWithHook(path, func() {
		if writeErr := os.WriteFile(path, append(nativeExecutableFixturePrefix(), []byte("a longer payload after open")...), 0o700); writeErr != nil {
			t.Fatalf("mutate executable: %v", writeErr)
		}
	})
	if !errors.Is(err, ErrExecutableIdentityRaced) {
		t.Fatalf("error = %v, want ErrExecutableIdentityRaced", err)
	}
}

func TestMeasureExecutableIdentity_DetectsSymlinkRetargetDuringMeasurement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows external agents require native .exe files and symlink privileges vary")
	}
	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	writeExecutableIdentityFixture(t, first, append(nativeExecutableFixturePrefix(), []byte("first")...))
	writeExecutableIdentityFixture(t, second, append(nativeExecutableFixturePrefix(), []byte("second")...))
	invocation := filepath.Join(dir, "agent")
	if err := os.Symlink(first, invocation); err != nil {
		t.Fatalf("create initial symlink: %v", err)
	}

	_, err := measureExecutableIdentityWithHook(invocation, func() {
		if removeErr := os.Remove(invocation); removeErr != nil {
			t.Fatalf("remove initial symlink: %v", removeErr)
		}
		if linkErr := os.Symlink(second, invocation); linkErr != nil {
			t.Fatalf("retarget symlink: %v", linkErr)
		}
	})
	if !errors.Is(err, ErrExecutableIdentityRaced) {
		t.Fatalf("error = %v, want ErrExecutableIdentityRaced", err)
	}
}

func TestMeasureExecutableIdentity_RejectsInvalidTargets(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		_, err := measureExecutableIdentity(t.TempDir())
		if !errors.Is(err, ErrExecutableIdentityUnavailable) {
			t.Fatalf("error = %v, want ErrExecutableIdentityUnavailable", err)
		}
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("error = %v, want useful regular-file detail", err)
		}
	})

	if runtime.GOOS != "windows" {
		t.Run("not executable", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent")
			if err := os.WriteFile(path, nativeExecutableFixturePrefix(), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			_, err := measureExecutableIdentity(path)
			if !errors.Is(err, ErrExecutableIdentityUnavailable) {
				t.Fatalf("error = %v, want ErrExecutableIdentityUnavailable", err)
			}
			if err == nil || !strings.Contains(err.Error(), "not executable") {
				t.Fatalf("error = %v, want useful executable-mode detail", err)
			}
		})
	}
}

func writeExecutableIdentityFixture(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o700); err != nil {
		t.Fatalf("write executable fixture: %v", err)
	}
}

func nativeExecutableFixturePrefix() []byte {
	switch runtime.GOOS {
	case "windows":
		return []byte{'M', 'Z', 0, 0}
	case "darwin":
		return []byte{0xcf, 0xfa, 0xed, 0xfe}
	default:
		return []byte{0x7f, 'E', 'L', 'F'}
	}
}
