package llamacpp

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// BinaryResolver locates an executable llama-server. Production
// resolution order (matches docs/local-models.md):
//
//  1. The explicit HECATE_LLAMA_SERVER_BIN env override (set by the
//     caller before construction; we don't read env here).
//  2. The dataDir cache at <data_dir>/llamacpp/bin/llama-server.
//  3. Lazy download from the pinned upstream llama.cpp release.
//     Atomic on success: writes to a sibling .part file, fsyncs,
//     renames, chmod +x. On sha mismatch the partial file is
//     removed and the resolver returns an error — operators must
//     verify rather than silently retry on a supply-chain anomaly.
//
// The lazy-download path is gated on AllowDownload because it
// reaches the network on first call. Tauri builds set
// AllowDownload=false (the binary is sidecar-bundled). Headless /
// dev gateway runs set AllowDownload=true and accept the cold-start
// fetch cost on first run.
type BinaryResolver struct {
	opts BinaryResolverOptions

	mu       sync.Mutex
	resolved string
}

// BinaryResolverOptions configures the resolver. All fields except
// DataDir are optional.
type BinaryResolverOptions struct {
	// DataDir is GATEWAY_DATA_DIR. Required. The cache lives at
	// <DataDir>/llamacpp/bin/llama-server.
	DataDir string

	// ExplicitPath is HECATE_LLAMA_SERVER_BIN, when set. Skips the
	// download path entirely.
	ExplicitPath string

	// AllowDownload gates the network-fetch fallback. Defaults to
	// false. The CLI gateway main wires this to true; Tauri leaves
	// it false because the sidecar bundles the binary.
	AllowDownload bool

	// Spec describes the upstream release artifact to fetch when
	// AllowDownload is true. Default is the pinned production
	// release (see DefaultBinarySpec below); tests inject a fake
	// spec pointed at an httptest server.
	Spec BinarySpec

	// HTTP is the client used for the download. Defaults to
	// http.DefaultClient.
	HTTP HTTPDoer
}

// BinarySpec pins the upstream release artifact. The default value
// resolves to llama.cpp's GitHub release for the current OS+arch.
type BinarySpec struct {
	// ReleaseTag is the llama.cpp upstream tag (e.g. "b4404"). Tests
	// override; production gets DefaultBinarySpec().
	ReleaseTag string
	// AssetURL is the full download URL for the per-platform
	// archive. Empty means "derive from ReleaseTag + GOOS/GOARCH".
	AssetURL string
	// AssetSHA256 is the lowercase hex digest of the archive. When
	// empty, sha verification is skipped (operator opts in via
	// SkipSHAVerify on dev paths).
	AssetSHA256 string
	// InnerPath is the path inside the archive where the
	// llama-server binary lives. Differs per platform — macOS arm64
	// puts it at "build/bin/llama-server", Linux x64 the same. Set
	// per platform by the default builder; override for custom
	// archives.
	InnerPath string
	// ArchiveType is "zip" or "tar.gz". Default is "zip" — every
	// upstream llama.cpp prebuilt is a zip.
	ArchiveType string
}

// DefaultBinarySpec returns the production spec for the current
// platform. The pinned values match scripts/fetch-llama-server.ts
// so the desktop sidecar and headless lazy-download converge on the
// same binary.
//
// SHA256 left empty during v1 bring-up — same as the Tauri fetch
// script. Backfill before any stable release.
func DefaultBinarySpec() BinarySpec {
	const releaseTag = "b4404"
	// Only macOS arm64 is shipped in v1; other platforms return an
	// empty AssetURL and the resolver surfaces ErrBinaryNoUpstream.
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return BinarySpec{
			ReleaseTag:  releaseTag,
			AssetURL:    fmt.Sprintf("https://github.com/ggml-org/llama.cpp/releases/download/%s/llama-%s-bin-macos-arm64.zip", releaseTag, releaseTag),
			InnerPath:   "build/bin/llama-server",
			ArchiveType: "zip",
		}
	}
	return BinarySpec{ReleaseTag: releaseTag}
}

// NewBinaryResolver wires a resolver. dataDir is required; all other
// options are optional.
func NewBinaryResolver(opts BinaryResolverOptions) (*BinaryResolver, error) {
	if strings.TrimSpace(opts.DataDir) == "" {
		return nil, errors.New("binary resolver: DataDir is required")
	}
	if opts.HTTP == nil {
		opts.HTTP = http.DefaultClient
	}
	if (opts.Spec == BinarySpec{}) {
		opts.Spec = DefaultBinarySpec()
	}
	return &BinaryResolver{opts: opts}, nil
}

// Resolve returns an absolute path to an executable llama-server,
// downloading + extracting on first call if necessary. Cached after
// the first success — repeated calls return the cached path without
// touching the network.
func (r *BinaryResolver) Resolve(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resolved != "" {
		return r.resolved, nil
	}
	// Step 1: explicit override.
	if p := strings.TrimSpace(r.opts.ExplicitPath); p != "" {
		if err := assertExecutable(p); err != nil {
			return "", fmt.Errorf("explicit binary: %w", err)
		}
		r.resolved = p
		return p, nil
	}
	// Step 2: cache.
	cached := r.cachePath()
	if err := assertExecutable(cached); err == nil {
		r.resolved = cached
		return cached, nil
	}
	// Step 3: lazy download — gated.
	if !r.opts.AllowDownload {
		return "", ErrBinaryUnavailable
	}
	if r.opts.Spec.AssetURL == "" {
		return "", fmt.Errorf("%w: no upstream defined for %s/%s",
			ErrBinaryNoUpstream, runtime.GOOS, runtime.GOARCH)
	}
	if err := r.downloadAndExtract(ctx); err != nil {
		return "", err
	}
	r.resolved = cached
	return cached, nil
}

// CachePath exposes the path the resolver would write to / read from
// without performing any network I/O. Useful for diagnostics + the
// Tauri sidecar's "is the cache already populated?" check before
// falling back to the bundled binary.
func (r *BinaryResolver) CachePath() string {
	return r.cachePath()
}

func (r *BinaryResolver) cachePath() string {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	return filepath.Join(r.opts.DataDir, "llamacpp", "bin", "llama-server"+suffix)
}

// downloadAndExtract fetches the upstream archive, verifies sha
// (when pinned), extracts the inner binary, and writes it
// atomically into the cache.
func (r *BinaryResolver) downloadAndExtract(ctx context.Context) error {
	final := r.cachePath()
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return fmt.Errorf("mkdir cache: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.opts.Spec.AssetURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := r.opts.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	// Read archive fully so we can sha-verify before extracting.
	// Upstream archives are tens of MB so the cost is acceptable;
	// streaming-extract would defer the verification past the point
	// of trust.
	hasher := sha256.New()
	buf := &bytes.Buffer{}
	if _, err := io.Copy(io.MultiWriter(buf, hasher), resp.Body); err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if want := strings.ToLower(strings.TrimSpace(r.opts.Spec.AssetSHA256)); want != "" {
		if got != want {
			return fmt.Errorf("%w: expected %q, got %q",
				ErrBinarySHAMismatch, want, got)
		}
	}

	// Extract — only ZIP is supported in v1. Every upstream
	// llama.cpp prebuilt is .zip; tar.gz support lands when we add
	// a platform that uses it.
	if r.opts.Spec.ArchiveType != "" && r.opts.Spec.ArchiveType != "zip" {
		return fmt.Errorf("unsupported archive type %q", r.opts.Spec.ArchiveType)
	}
	innerPath := r.opts.Spec.InnerPath
	if innerPath == "" {
		return errors.New("binary resolver: spec missing InnerPath")
	}

	zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	var entry *zip.File
	for _, f := range zipReader.File {
		if f.Name == innerPath {
			entry = f
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("%w: %q not found in archive",
			ErrBinaryInnerMissing, innerPath)
	}

	partPath := final + ".part"
	src, err := entry.Open()
	if err != nil {
		return fmt.Errorf("open entry: %w", err)
	}
	defer src.Close()
	dst, err := os.OpenFile(partPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("open part: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(partPath)
		return fmt.Errorf("copy entry: %w", err)
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		_ = os.Remove(partPath)
		return fmt.Errorf("fsync part: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close part: %w", err)
	}
	if err := os.Chmod(partPath, 0o755); err != nil {
		return fmt.Errorf("chmod part: %w", err)
	}
	if err := os.Rename(partPath, final); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// assertExecutable returns nil when path is a regular file with the
// owner-executable bit set. Same shape as Service.FeatureAvailability's
// inline check; centralized here so the resolver and the service
// agree.
func assertExecutable(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("path is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return fmt.Errorf("%q is not executable", path)
	}
	return nil
}

var (
	// ErrBinaryUnavailable is returned by Resolve when no cached
	// binary is present and AllowDownload is false. The
	// service-level dormant path returns this when neither the
	// Tauri sidecar nor the headless cache produced a binary.
	ErrBinaryUnavailable = errors.New("llama-server binary not available; enable lazy download or pre-install")

	// ErrBinaryNoUpstream is returned when AllowDownload is true
	// but DefaultBinarySpec has no entry for the current
	// GOOS/GOARCH (e.g. linux/amd64 before that target lands).
	ErrBinaryNoUpstream = errors.New("no upstream llama.cpp binary defined for this platform")

	// ErrBinarySHAMismatch is returned by the download path when
	// the archive's sha256 doesn't match the pinned value. The
	// partial file is not persisted; operators must investigate
	// rather than retry.
	ErrBinarySHAMismatch = errors.New("downloaded llama.cpp archive sha256 mismatch")

	// ErrBinaryInnerMissing is returned when the archive
	// downloaded successfully but didn't contain the InnerPath
	// (typically because upstream renamed the binary in a release
	// past our pin). Treat as "bump the pin".
	ErrBinaryInnerMissing = errors.New("llama-server binary not found at expected path inside archive")
)
