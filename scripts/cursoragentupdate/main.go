// Command cursoragentupdate prepares reviewed Cursor Agent artifact pin updates.
//
// It treats Cursor's installer as bounded metadata and never executes it. The
// command downloads both Linux archives, validates their package shape, hashes
// their exact bytes, and updates the two Hecate Dockerfiles together.
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	defaultInstallerURL    = "https://cursor.com/install"
	defaultArtifactBaseURL = "https://downloads.cursor.com"
	maxInstallerBytes      = 64 << 10
	maxArtifactBytes       = 256 << 20
	maxArchiveEntries      = 20_000
	maxArchiveStreamBytes  = 1 << 30
	maxPackageJSONBytes    = 1 << 20
)

var (
	versionPattern  = regexp.MustCompile(`^[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	downloadPattern = regexp.MustCompile(
		`(?m)^DOWNLOAD_URL="https://downloads\.cursor\.com/lab/([0-9]{4}\.[0-9]{2}\.[0-9]{2}-[A-Za-z0-9][A-Za-z0-9._-]{0,63})/\$\{OS\}/\$\{ARCH\}/agent-cli-package\.tar\.gz"\r?$`,
	)
)

type config struct {
	root            string
	installerURL    string
	artifactBaseURL string
	client          *http.Client
	allowSameDate   bool
	proposalRoot    string
}

type pin struct {
	version string
	x64     string
	arm64   string
}

type artifactSpec struct {
	architecture string
	nativePath   string
}

type artifactReport struct {
	Architecture string `json:"architecture"`
	URL          string `json:"url"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
}

type updateReport struct {
	Changed         bool             `json:"changed"`
	PreviousVersion string           `json:"previous_version"`
	Version         string           `json:"version"`
	InstallerURL    string           `json:"installer_url"`
	InstallerSHA256 string           `json:"installer_sha256"`
	Artifacts       []artifactReport `json:"artifacts"`
}

func main() {
	root := flag.String("root", ".", "repository root containing both Dockerfiles")
	allowSameDate := flag.Bool(
		"allow-same-date-transition",
		false,
		"allow a reviewed transition between distinct versions carrying the same release date",
	)
	proposalRoot := flag.String(
		"existing-proposal-root",
		"",
		"optional directory containing an existing generated proposal to guard against in-flight artifact mutation",
	)
	flag.Parse()

	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "cursoragentupdate: unexpected positional arguments")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	report, err := update(ctx, config{
		root:            *root,
		installerURL:    defaultInstallerURL,
		artifactBaseURL: defaultArtifactBaseURL,
		client:          &http.Client{Timeout: 10 * time.Minute},
		allowSameDate:   *allowSameDate,
		proposalRoot:    *proposalRoot,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cursoragentupdate: %v\n", err)
		os.Exit(1)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "cursoragentupdate: encode report: %v\n", err)
		os.Exit(1)
	}
}

func update(ctx context.Context, cfg config) (updateReport, error) {
	if cfg.client == nil {
		return updateReport{}, errors.New("HTTP client is required")
	}
	if cfg.root == "" {
		return updateReport{}, errors.New("repository root is required")
	}

	installer, err := fetchBounded(ctx, cfg.client, cfg.installerURL, maxInstallerBytes)
	if err != nil {
		return updateReport{}, fmt.Errorf("fetch installer metadata: %w", err)
	}
	installerHash := sha256.Sum256(installer)
	latestVersion, err := parseInstallerVersion(installer)
	if err != nil {
		return updateReport{}, fmt.Errorf("parse installer metadata: %w", err)
	}

	current, contents, modes, err := readDockerfilePins(cfg.root)
	if err != nil {
		return updateReport{}, err
	}
	if err := rejectAmbiguousVersion(current.version, latestVersion, cfg.allowSameDate); err != nil {
		return updateReport{}, err
	}

	specs := []artifactSpec{
		{
			architecture: "x64",
			nativePath:   "dist-package/file_service.linux-x64-gnu.node",
		},
		{
			architecture: "arm64",
			nativePath:   "dist-package/file_service.linux-arm64-gnu.node",
		},
	}
	artifacts := make([]artifactReport, 0, len(specs))
	for _, spec := range specs {
		artifactURL := strings.TrimRight(cfg.artifactBaseURL, "/") +
			"/lab/" + url.PathEscape(latestVersion) +
			"/linux/" + spec.architecture + "/agent-cli-package.tar.gz"
		artifact, err := fetchAndValidateArtifact(ctx, cfg.client, artifactURL, spec)
		if err != nil {
			return updateReport{}, fmt.Errorf("validate %s artifact: %w", spec.architecture, err)
		}
		artifacts = append(artifacts, artifact)
	}

	report := updateReport{
		PreviousVersion: current.version,
		Version:         latestVersion,
		InstallerURL:    cfg.installerURL,
		InstallerSHA256: hex.EncodeToString(installerHash[:]),
		Artifacts:       artifacts,
	}
	latest := pin{
		version: latestVersion,
		x64:     artifactSHA(artifacts, "x64"),
		arm64:   artifactSHA(artifacts, "arm64"),
	}
	if cfg.proposalRoot != "" {
		proposal, _, _, err := readDockerfilePins(cfg.proposalRoot)
		if err != nil {
			return updateReport{}, fmt.Errorf("read existing update proposal: %w", err)
		}
		if err := validateExistingProposal(proposal, latest); err != nil {
			return updateReport{}, err
		}
	}

	if current.version == latest.version {
		if current.x64 != latest.x64 || current.arm64 != latest.arm64 {
			return updateReport{}, fmt.Errorf(
				"Cursor Agent %s artifact bytes changed in place (x64 %s -> %s, arm64 %s -> %s); refusing to rewrite an existing version pin",
				current.version,
				current.x64,
				latest.x64,
				current.arm64,
				latest.arm64,
			)
		}
		return report, nil
	}

	if err := writeDockerfilePins(cfg.root, contents, modes, latest); err != nil {
		return updateReport{}, err
	}
	report.Changed = true
	return report, nil
}

func validateExistingProposal(proposal pin, latest pin) error {
	if proposal.version != latest.version {
		return fmt.Errorf(
			"existing proposal pins Cursor Agent %s while the installer advertises %s; refusing to replace an in-flight reviewed proposal",
			proposal.version,
			latest.version,
		)
	}
	if proposal.x64 != latest.x64 || proposal.arm64 != latest.arm64 {
		return fmt.Errorf(
			"existing proposal for Cursor Agent %s no longer matches its artifact bytes (x64 %s -> %s, arm64 %s -> %s); refusing to replace an in-flight reviewed pin",
			proposal.version,
			proposal.x64,
			latest.x64,
			proposal.arm64,
			latest.arm64,
		)
	}
	return nil
}

func fetchBounded(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, error) {
	response, err := getSameOrigin(ctx, client, rawURL)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: unexpected HTTP status %s", rawURL, response.Status)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("GET %s: content length %d exceeds %d-byte limit", rawURL, response.ContentLength, limit)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rawURL, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GET %s: response exceeds %d-byte limit", rawURL, limit)
	}
	return data, nil
}

func getSameOrigin(ctx context.Context, client *http.Client, rawURL string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create GET request: %w", err)
	}
	request.Header.Set("Accept", "application/octet-stream, text/plain;q=0.9")
	request.Header.Set("User-Agent", "hecate-cursor-agent-pin-updater/1")

	initial := request.URL
	requestClient := *client
	priorCheckRedirect := client.CheckRedirect
	requestClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if next.URL.Scheme != initial.Scheme || !strings.EqualFold(next.URL.Host, initial.Host) {
			return fmt.Errorf("refusing cross-origin redirect from %s to %s", initial.Redacted(), next.URL.Redacted())
		}
		if priorCheckRedirect != nil {
			return priorCheckRedirect(next, via)
		}
		return nil
	}

	response, err := requestClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", initial.Redacted(), err)
	}
	return response, nil
}

func parseInstallerVersion(installer []byte) (string, error) {
	matches := downloadPattern.FindAllSubmatch(installer, 2)
	if len(matches) != 1 {
		return "", fmt.Errorf("found %d strict official DOWNLOAD_URL declarations, want exactly 1", len(matches))
	}
	version := string(matches[0][1])
	if !versionPattern.MatchString(version) {
		return "", fmt.Errorf("invalid version %q", version)
	}
	if _, err := time.Parse("2006.01.02", version[:10]); err != nil {
		return "", fmt.Errorf("invalid version date in %q: %w", version, err)
	}
	return version, nil
}

func fetchAndValidateArtifact(
	ctx context.Context,
	client *http.Client,
	rawURL string,
	spec artifactSpec,
) (artifactReport, error) {
	response, err := getSameOrigin(ctx, client, rawURL)
	if err != nil {
		return artifactReport{}, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return artifactReport{}, fmt.Errorf("GET %s: unexpected HTTP status %s", rawURL, response.Status)
	}
	if response.ContentLength > maxArtifactBytes {
		return artifactReport{}, fmt.Errorf(
			"GET %s: content length %d exceeds %d-byte limit",
			rawURL,
			response.ContentLength,
			maxArtifactBytes,
		)
	}

	temporary, err := os.CreateTemp("", "hecate-cursor-agent-*.tar.gz")
	if err != nil {
		return artifactReport{}, fmt.Errorf("create temporary archive: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hasher), io.LimitReader(response.Body, maxArtifactBytes+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		return artifactReport{}, fmt.Errorf("download %s: %w", rawURL, copyErr)
	}
	if closeErr != nil {
		return artifactReport{}, fmt.Errorf("close temporary archive: %w", closeErr)
	}
	if written > maxArtifactBytes {
		return artifactReport{}, fmt.Errorf("GET %s: response exceeds %d-byte limit", rawURL, maxArtifactBytes)
	}

	archive, err := os.Open(temporaryPath)
	if err != nil {
		return artifactReport{}, fmt.Errorf("reopen temporary archive: %w", err)
	}
	inspectErr := inspectArchive(archive, spec)
	closeErr = archive.Close()
	if inspectErr != nil {
		return artifactReport{}, inspectErr
	}
	if closeErr != nil {
		return artifactReport{}, fmt.Errorf("close archive after inspection: %w", closeErr)
	}

	return artifactReport{
		Architecture: spec.architecture,
		URL:          rawURL,
		SHA256:       hexDigest(hasher),
		Bytes:        written,
	}, nil
}

func inspectArchive(archive io.Reader, spec artifactSpec) error {
	return inspectArchiveWithLimit(archive, spec, maxArchiveStreamBytes)
}

func inspectArchiveWithLimit(archive io.Reader, spec artifactSpec, streamLimit int64) error {
	if streamLimit <= 0 {
		return errors.New("archive stream limit must be positive")
	}
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gzipReader.Close()

	required := map[string]bool{
		"dist-package/cursor-agent": false,
		"dist-package/node":         false,
		"dist-package/index.js":     false,
		"dist-package/package.json": false,
		spec.nativePath:             false,
	}
	seen := make(map[string]struct{})
	limitedStream := &io.LimitedReader{R: gzipReader, N: streamLimit + 1}
	reader := tar.NewReader(limitedStream)
	var totalBytes int64
	entryCount := 0
	packageName := ""

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar archive: %w", err)
		}
		entryCount++
		if entryCount > maxArchiveEntries {
			return fmt.Errorf("archive exceeds %d entries", maxArchiveEntries)
		}

		name, err := safeArchiveName(header.Name)
		if err != nil {
			return err
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("archive contains duplicate path %q", name)
		}
		seen[name] = struct{}{}

		if header.Size < 0 || totalBytes > streamLimit-header.Size {
			return fmt.Errorf("archive entries exceed %d uncompressed bytes", streamLimit)
		}
		totalBytes += header.Size

		switch header.Typeflag {
		case tar.TypeDir:
			if header.Size != 0 {
				return fmt.Errorf("archive directory %q has non-zero size", name)
			}
		case tar.TypeReg, tar.TypeRegA:
		default:
			return fmt.Errorf("archive path %q has unsupported tar type %d", name, header.Typeflag)
		}
		mode := header.FileInfo().Mode()
		if mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return fmt.Errorf("archive path %q has special permission bits %s", name, mode)
		}
		if mode.Perm()&0o002 != 0 {
			return fmt.Errorf("archive path %q is world-writable (%s)", name, mode.Perm())
		}

		if _, ok := required[name]; ok {
			if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
				return fmt.Errorf("required archive path %q is not a regular file", name)
			}
			required[name] = true
		}
		if name == "dist-package/cursor-agent" || name == "dist-package/node" {
			if header.FileInfo().Mode().Perm()&0o111 == 0 {
				return fmt.Errorf("required archive executable %q is not executable", name)
			}
		}
		if name == "dist-package/node" || name == spec.nativePath {
			if err := validateELFArchitecture(reader, header.Size, spec.architecture); err != nil {
				return fmt.Errorf("validate %q: %w", name, err)
			}
		}
		if name == "dist-package/package.json" {
			if header.Size > maxPackageJSONBytes {
				return fmt.Errorf("package.json exceeds %d bytes", maxPackageJSONBytes)
			}
			manifest, err := io.ReadAll(io.LimitReader(reader, maxPackageJSONBytes+1))
			if err != nil {
				return fmt.Errorf("read package.json: %w", err)
			}
			var metadata struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(manifest, &metadata); err != nil {
				return fmt.Errorf("parse package.json: %w", err)
			}
			packageName = metadata.Name
		}
	}

	if _, err := io.Copy(io.Discard, limitedStream); err != nil {
		return fmt.Errorf("finish gzip stream: %w", err)
	}
	if limitedStream.N == 0 {
		return fmt.Errorf("archive decompressed stream exceeds %d bytes", streamLimit)
	}
	for name, present := range required {
		if !present {
			return fmt.Errorf("archive is missing required path %q", name)
		}
	}
	if packageName != "@anysphere/agent-cli-runtime" {
		return fmt.Errorf("package.json name = %q, want @anysphere/agent-cli-runtime", packageName)
	}
	return nil
}

func validateELFArchitecture(reader io.Reader, size int64, architecture string) error {
	const elfHeaderBytes = 20
	if size < elfHeaderBytes {
		return fmt.Errorf("ELF file is only %d bytes", size)
	}
	header := make([]byte, elfHeaderBytes)
	if _, err := io.ReadFull(reader, header); err != nil {
		return fmt.Errorf("read ELF header: %w", err)
	}
	if string(header[:4]) != "\x7fELF" || header[4] != 2 || header[6] != 1 {
		return errors.New("file is not a 64-bit ELF file")
	}

	var order binary.ByteOrder
	switch header[5] {
	case 1:
		order = binary.LittleEndian
	case 2:
		order = binary.BigEndian
	default:
		return fmt.Errorf("unsupported ELF byte order %d", header[5])
	}
	machine := order.Uint16(header[18:20])
	want := uint16(0)
	switch architecture {
	case "x64":
		want = 62 // EM_X86_64
	case "arm64":
		want = 183 // EM_AARCH64
	default:
		return fmt.Errorf("unsupported architecture %q", architecture)
	}
	if machine != want {
		return fmt.Errorf("ELF machine = %d, want %d for %s", machine, want, architecture)
	}
	return nil
}

func safeArchiveName(name string) (string, error) {
	if name == "" || strings.Contains(name, `\`) {
		return "", fmt.Errorf("archive contains unsafe path %q", name)
	}
	trimmed := strings.TrimSuffix(name, "/")
	cleaned := path.Clean(trimmed)
	if cleaned != trimmed || path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("archive contains unsafe path %q", name)
	}
	if cleaned != "dist-package" && !strings.HasPrefix(cleaned, "dist-package/") {
		return "", fmt.Errorf("archive path %q is outside dist-package", name)
	}
	return cleaned, nil
}

func readDockerfilePins(root string) (pin, map[string][]byte, map[string]os.FileMode, error) {
	contents := make(map[string][]byte, 2)
	modes := make(map[string]os.FileMode, 2)
	var common pin
	for index, name := range []string{"Dockerfile", "Dockerfile.release"} {
		filePath := filepath.Join(root, name)
		data, err := os.ReadFile(filePath)
		if err != nil {
			return pin{}, nil, nil, fmt.Errorf("read %s: %w", name, err)
		}
		info, err := os.Stat(filePath)
		if err != nil {
			return pin{}, nil, nil, fmt.Errorf("stat %s: %w", name, err)
		}
		parsed, err := parseDockerfilePin(string(data))
		if err != nil {
			return pin{}, nil, nil, fmt.Errorf("parse %s: %w", name, err)
		}
		if index == 0 {
			common = parsed
		} else if parsed != common {
			return pin{}, nil, nil, fmt.Errorf("Cursor Agent pins differ between Dockerfile and Dockerfile.release")
		}
		contents[name] = data
		modes[name] = info.Mode()
	}
	return common, contents, modes, nil
}

func parseDockerfilePin(dockerfile string) (pin, error) {
	version, err := dockerfileArg(dockerfile, "CURSOR_AGENT_VERSION")
	if err != nil {
		return pin{}, err
	}
	x64, err := dockerfileArg(dockerfile, "CURSOR_AGENT_LINUX_X64_SHA256")
	if err != nil {
		return pin{}, err
	}
	arm64, err := dockerfileArg(dockerfile, "CURSOR_AGENT_LINUX_ARM64_SHA256")
	if err != nil {
		return pin{}, err
	}
	if !versionPattern.MatchString(version) {
		return pin{}, fmt.Errorf("CURSOR_AGENT_VERSION = %q, want strict version", version)
	}
	if !validSHA256(x64) || !validSHA256(arm64) {
		return pin{}, errors.New("Cursor Agent artifact hashes must be lowercase SHA-256 values")
	}
	return pin{version: version, x64: x64, arm64: arm64}, nil
}

func dockerfileArg(dockerfile string, name string) (string, error) {
	prefix := "ARG " + name + "="
	value := ""
	count := 0
	for _, line := range strings.Split(dockerfile, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		count++
		value = strings.TrimSpace(strings.TrimPrefix(line, prefix))
	}
	if count != 1 || value == "" {
		return "", fmt.Errorf("%s occurs %d times, want exactly one non-empty ARG", name, count)
	}
	return value, nil
}

func rejectAmbiguousVersion(current string, latest string, allowSameDate bool) error {
	currentDate, err := time.Parse("2006.01.02", current[:10])
	if err != nil {
		return fmt.Errorf("parse current Cursor Agent version date: %w", err)
	}
	latestDate, err := time.Parse("2006.01.02", latest[:10])
	if err != nil {
		return fmt.Errorf("parse advertised Cursor Agent version date: %w", err)
	}
	if latestDate.Before(currentDate) {
		return fmt.Errorf("advertised Cursor Agent version %s is older than pinned version %s", latest, current)
	}
	if latestDate.Equal(currentDate) && latest != current && !allowSameDate {
		return fmt.Errorf(
			"advertised Cursor Agent version %s has the same release date as pinned version %s; refusing an unordered transition without --allow-same-date-transition",
			latest,
			current,
		)
	}
	return nil
}

func writeDockerfilePins(root string, originals map[string][]byte, modes map[string]os.FileMode, latest pin) error {
	return writeDockerfilePinsWithWriter(root, originals, modes, latest, writeAtomic)
}

func writeDockerfilePinsWithWriter(
	root string,
	originals map[string][]byte,
	modes map[string]os.FileMode,
	latest pin,
	writer func(string, []byte, os.FileMode) error,
) error {
	updated := make(map[string][]byte, len(originals))
	for _, name := range []string{"Dockerfile", "Dockerfile.release"} {
		data := originals[name]
		var err error
		for arg, value := range map[string]string{
			"CURSOR_AGENT_VERSION":            latest.version,
			"CURSOR_AGENT_LINUX_X64_SHA256":   latest.x64,
			"CURSOR_AGENT_LINUX_ARM64_SHA256": latest.arm64,
		} {
			data, err = replaceDockerfileArg(data, arg, value)
			if err != nil {
				return fmt.Errorf("update %s: %w", name, err)
			}
		}
		updated[name] = data
	}

	written := make([]string, 0, len(updated))
	for _, name := range []string{"Dockerfile", "Dockerfile.release"} {
		if err := writer(filepath.Join(root, name), updated[name], modes[name]); err != nil {
			failures := []error{fmt.Errorf("write %s: %w", name, err)}
			for _, rollbackName := range written {
				if rollbackErr := writer(
					filepath.Join(root, rollbackName),
					originals[rollbackName],
					modes[rollbackName],
				); rollbackErr != nil {
					failures = append(failures, fmt.Errorf("rollback %s: %w", rollbackName, rollbackErr))
				}
			}
			return errors.Join(failures...)
		}
		written = append(written, name)
	}
	return nil
}

func replaceDockerfileArg(data []byte, name string, value string) ([]byte, error) {
	prefix := "ARG " + name + "="
	lines := strings.Split(string(data), "\n")
	count := 0
	for index, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		count++
		lines[index] = prefix + value
	}
	if count != 1 {
		return nil, fmt.Errorf("%s occurs %d times, want exactly one ARG", name, count)
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func writeAtomic(filePath string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(filePath), ".cursor-agent-pin-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(mode.Perm()); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, filePath)
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func artifactSHA(artifacts []artifactReport, architecture string) string {
	for _, artifact := range artifacts {
		if artifact.Architecture == architecture {
			return artifact.SHA256
		}
	}
	return ""
}

func hexDigest(hasher hash.Hash) string {
	return hex.EncodeToString(hasher.Sum(nil))
}
