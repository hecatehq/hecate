package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateLeavesMatchingPinsUnchanged(t *testing.T) {
	t.Parallel()

	version := "2026.07.20-8cc9c0b"
	server, artifacts := newFixtureServer(t, version, nil)
	root := writeDockerfileFixture(t, version, digest(artifacts["x64"]), digest(artifacts["arm64"]))

	report, err := update(context.Background(), fixtureConfig(root, server))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if report.Changed {
		t.Fatal("Changed = true, want false for matching pins")
	}
	if report.Version != version || report.PreviousVersion != version {
		t.Fatalf("version report = %#v, want %s unchanged", report, version)
	}
	if len(report.Artifacts) != 2 {
		t.Fatalf("artifacts = %d, want 2", len(report.Artifacts))
	}
}

func TestUpdateRewritesBothDockerfilesForNewVersion(t *testing.T) {
	t.Parallel()

	latest := "2026.07.20-8cc9c0b"
	server, artifacts := newFixtureServer(t, latest, nil)
	root := writeDockerfileFixture(t, "2026.07.17-3e2a980", strings.Repeat("a", 64), strings.Repeat("b", 64))

	report, err := update(context.Background(), fixtureConfig(root, server))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !report.Changed {
		t.Fatal("Changed = false, want true for a newer version")
	}
	for _, name := range []string{"Dockerfile", "Dockerfile.release"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(data)
		for _, want := range []string{
			"ARG CURSOR_AGENT_VERSION=" + latest,
			"ARG CURSOR_AGENT_LINUX_X64_SHA256=" + digest(artifacts["x64"]),
			"ARG CURSOR_AGENT_LINUX_ARM64_SHA256=" + digest(artifacts["arm64"]),
			"RUN preserve-this-line",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s is missing %q after update", name, want)
			}
		}
	}
}

func TestUpdateRejectsArtifactMutationAtSameVersion(t *testing.T) {
	t.Parallel()

	version := "2026.07.20-8cc9c0b"
	server, _ := newFixtureServer(t, version, nil)
	root := writeDockerfileFixture(t, version, strings.Repeat("a", 64), strings.Repeat("b", 64))

	_, err := update(context.Background(), fixtureConfig(root, server))
	if err == nil || !strings.Contains(err.Error(), "artifact bytes changed in place") {
		t.Fatalf("update error = %v, want in-place mutation rejection", err)
	}
}

func TestUpdateRejectsMutationOfExistingProposal(t *testing.T) {
	t.Parallel()

	latest := "2026.07.20-8cc9c0b"
	server, _ := newFixtureServer(t, latest, nil)
	root := writeDockerfileFixture(t, "2026.07.17-3e2a980", strings.Repeat("a", 64), strings.Repeat("b", 64))
	proposalRoot := writeDockerfileFixture(t, latest, strings.Repeat("c", 64), strings.Repeat("d", 64))
	cfg := fixtureConfig(root, server)
	cfg.proposalRoot = proposalRoot

	_, err := update(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "refusing to replace an in-flight reviewed pin") {
		t.Fatalf("update error = %v, want existing-proposal mutation rejection", err)
	}
}

func TestUpdateRejectsNewReleaseWhileProposalIsInFlight(t *testing.T) {
	t.Parallel()

	server, _ := newFixtureServer(t, "2026.07.21-new", nil)
	root := writeDockerfileFixture(t, "2026.07.19-old", strings.Repeat("a", 64), strings.Repeat("b", 64))
	proposalRoot := writeDockerfileFixture(t, "2026.07.20-proposed", strings.Repeat("c", 64), strings.Repeat("d", 64))
	cfg := fixtureConfig(root, server)
	cfg.proposalRoot = proposalRoot

	_, err := update(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "refusing to replace an in-flight reviewed proposal") {
		t.Fatalf("update error = %v, want newer release to preserve the open proposal", err)
	}
}

func TestUpdateAcceptsUnchangedExistingProposal(t *testing.T) {
	t.Parallel()

	latest := "2026.07.20-8cc9c0b"
	server, artifacts := newFixtureServer(t, latest, nil)
	root := writeDockerfileFixture(t, "2026.07.17-3e2a980", strings.Repeat("a", 64), strings.Repeat("b", 64))
	proposalRoot := writeDockerfileFixture(t, latest, digest(artifacts["x64"]), digest(artifacts["arm64"]))
	cfg := fixtureConfig(root, server)
	cfg.proposalRoot = proposalRoot

	report, err := update(context.Background(), cfg)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !report.Changed || report.Version != latest {
		t.Fatalf("report = %#v, want matching proposal to remain publishable", report)
	}
}

func TestUpdateRejectsAdvertisedDowngrade(t *testing.T) {
	t.Parallel()

	server, _ := newFixtureServer(t, "2026.07.17-3e2a980", nil)
	root := writeDockerfileFixture(t, "2026.07.20-8cc9c0b", strings.Repeat("a", 64), strings.Repeat("b", 64))

	_, err := update(context.Background(), fixtureConfig(root, server))
	if err == nil || !strings.Contains(err.Error(), "is older than pinned version") {
		t.Fatalf("update error = %v, want downgrade rejection", err)
	}
}

func TestUpdateRequiresOverrideForSameDateTransition(t *testing.T) {
	t.Parallel()

	server, _ := newFixtureServer(t, "2026.07.20-newbuild", nil)
	root := writeDockerfileFixture(t, "2026.07.20-oldbuild", strings.Repeat("a", 64), strings.Repeat("b", 64))
	cfg := fixtureConfig(root, server)

	_, err := update(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "same release date") {
		t.Fatalf("update error = %v, want same-date transition rejection", err)
	}

	cfg.allowSameDate = true
	report, err := update(context.Background(), cfg)
	if err != nil {
		t.Fatalf("reviewed same-date update: %v", err)
	}
	if !report.Changed || report.Version != "2026.07.20-newbuild" {
		t.Fatalf("reviewed same-date report = %#v", report)
	}
}

func TestUpdateRejectsUnsafeArchivePath(t *testing.T) {
	t.Parallel()

	version := "2026.07.20-8cc9c0b"
	server, _ := newFixtureServer(t, version, map[string][]tarEntry{
		"x64": {
			{name: "../escape", mode: 0o644, body: []byte("unsafe")},
		},
	})
	root := writeDockerfileFixture(t, "2026.07.17-3e2a980", strings.Repeat("a", 64), strings.Repeat("b", 64))

	_, err := update(context.Background(), fixtureConfig(root, server))
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("update error = %v, want unsafe archive rejection", err)
	}
}

func TestUpdateRejectsWrongArtifactArchitecture(t *testing.T) {
	t.Parallel()

	version := "2026.07.20-8cc9c0b"
	entries := validEntries("x64")
	for index := range entries {
		if entries[index].name == "dist-package/node" {
			entries[index].body = elfFor("arm64")
		}
	}
	server, _ := newFixtureServer(t, version, map[string][]tarEntry{"x64": entries})
	root := writeDockerfileFixture(t, "2026.07.17-3e2a980", strings.Repeat("a", 64), strings.Repeat("b", 64))

	_, err := update(context.Background(), fixtureConfig(root, server))
	if err == nil || !strings.Contains(err.Error(), "ELF machine") {
		t.Fatalf("update error = %v, want architecture rejection", err)
	}
}

func TestUpdateRejectsDangerousArchiveModes(t *testing.T) {
	t.Parallel()

	for name, mode := range map[string]int64{
		"setuid":         0o4755,
		"world-writable": 0o0777,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			version := "2026.07.20-8cc9c0b"
			entries := validEntries("x64")
			for index := range entries {
				if entries[index].name == "dist-package/node" {
					entries[index].mode = mode
				}
			}
			server, _ := newFixtureServer(t, version, map[string][]tarEntry{"x64": entries})
			root := writeDockerfileFixture(t, "2026.07.17-3e2a980", strings.Repeat("a", 64), strings.Repeat("b", 64))

			_, err := update(context.Background(), fixtureConfig(root, server))
			if err == nil || (!strings.Contains(err.Error(), "permission") && !strings.Contains(err.Error(), "world-writable")) {
				t.Fatalf("update error = %v, want dangerous mode rejection", err)
			}
		})
	}
}

func TestInspectArchiveBoundsConcatenatedGzipData(t *testing.T) {
	t.Parallel()

	archive := writeArchive(t, validEntries("x64"))
	firstReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("open fixture gzip: %v", err)
	}
	firstStream, err := io.ReadAll(firstReader)
	if err != nil {
		t.Fatalf("read fixture gzip: %v", err)
	}
	if err := firstReader.Close(); err != nil {
		t.Fatalf("close fixture gzip: %v", err)
	}

	var trailing bytes.Buffer
	trailingWriter := gzip.NewWriter(&trailing)
	if _, err := trailingWriter.Write(bytes.Repeat([]byte("x"), 1024)); err != nil {
		t.Fatalf("write trailing gzip stream: %v", err)
	}
	if err := trailingWriter.Close(); err != nil {
		t.Fatalf("close trailing gzip stream: %v", err)
	}
	combined := append(append([]byte(nil), archive...), trailing.Bytes()...)
	limit := int64(len(firstStream) + 512)

	err = inspectArchiveWithLimit(bytes.NewReader(combined), artifactSpec{
		architecture: "x64",
		nativePath:   "dist-package/file_service.linux-x64-gnu.node",
	}, limit)
	if err == nil || !strings.Contains(err.Error(), "decompressed stream exceeds") {
		t.Fatalf("inspectArchiveWithLimit error = %v, want decompression bound", err)
	}
}

func TestParseInstallerVersionRequiresOneStrictOfficialURL(t *testing.T) {
	t.Parallel()

	line := installerFor("2026.07.20-8cc9c0b")
	version, err := parseInstallerVersion([]byte(line))
	if err != nil {
		t.Fatalf("parseInstallerVersion: %v", err)
	}
	if version != "2026.07.20-8cc9c0b" {
		t.Fatalf("version = %q", version)
	}

	for name, installer := range map[string]string{
		"missing":  "#!/bin/sh\n",
		"multiple": line + line,
		"host":     strings.Replace(line, "downloads.cursor.com", "example.com", 1),
		"path":     strings.Replace(line, "agent-cli-package.tar.gz", "other.tar.gz", 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseInstallerVersion([]byte(installer)); err == nil {
				t.Fatal("parseInstallerVersion succeeded, want rejection")
			}
		})
	}
}

func TestGetSameOriginRejectsCrossOriginRedirect(t *testing.T) {
	t.Parallel()

	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL, http.StatusFound)
	}))
	t.Cleanup(source.Close)

	_, err := fetchBounded(context.Background(), source.Client(), source.URL, 1024)
	if err == nil || !strings.Contains(err.Error(), "refusing cross-origin redirect") {
		t.Fatalf("fetchBounded error = %v, want cross-origin redirect rejection", err)
	}
}

func TestWriteDockerfilePinsReportsRollbackFailure(t *testing.T) {
	t.Parallel()

	originals := map[string][]byte{
		"Dockerfile":         []byte("ARG CURSOR_AGENT_VERSION=2026.07.19-old\nARG CURSOR_AGENT_LINUX_X64_SHA256=" + strings.Repeat("a", 64) + "\nARG CURSOR_AGENT_LINUX_ARM64_SHA256=" + strings.Repeat("b", 64) + "\n"),
		"Dockerfile.release": []byte("ARG CURSOR_AGENT_VERSION=2026.07.19-old\nARG CURSOR_AGENT_LINUX_X64_SHA256=" + strings.Repeat("a", 64) + "\nARG CURSOR_AGENT_LINUX_ARM64_SHA256=" + strings.Repeat("b", 64) + "\n"),
	}
	modes := map[string]os.FileMode{
		"Dockerfile":         0o644,
		"Dockerfile.release": 0o644,
	}
	calls := 0
	writer := func(_ string, _ []byte, _ os.FileMode) error {
		calls++
		switch calls {
		case 2:
			return errors.New("second write failed")
		case 3:
			return errors.New("rollback failed")
		default:
			return nil
		}
	}

	err := writeDockerfilePinsWithWriter("/unused", originals, modes, pin{
		version: "2026.07.20-new",
		x64:     strings.Repeat("c", 64),
		arm64:   strings.Repeat("d", 64),
	}, writer)
	if err == nil || !strings.Contains(err.Error(), "write Dockerfile.release: second write failed") ||
		!strings.Contains(err.Error(), "rollback Dockerfile: rollback failed") {
		t.Fatalf("write error = %v, want write and rollback failures", err)
	}
}

type tarEntry struct {
	name string
	mode int64
	body []byte
}

func newFixtureServer(
	t *testing.T,
	version string,
	overrides map[string][]tarEntry,
) (*httptest.Server, map[string][]byte) {
	t.Helper()

	artifacts := make(map[string][]byte, 2)
	for _, architecture := range []string{"x64", "arm64"} {
		entries, overridden := overrides[architecture]
		if !overridden {
			entries = validEntries(architecture)
		}
		artifacts[architecture] = writeArchive(t, entries)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/install":
			response.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(response, installerFor(version))
		case request.URL.Path == "/lab/"+version+"/linux/x64/agent-cli-package.tar.gz":
			response.Header().Set("Content-Type", "application/gzip")
			_, _ = response.Write(artifacts["x64"])
		case request.URL.Path == "/lab/"+version+"/linux/arm64/agent-cli-package.tar.gz":
			response.Header().Set("Content-Type", "application/gzip")
			_, _ = response.Write(artifacts["arm64"])
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	return server, artifacts
}

func fixtureConfig(root string, server *httptest.Server) config {
	return config{
		root:            root,
		installerURL:    server.URL + "/install",
		artifactBaseURL: server.URL,
		client:          server.Client(),
	}
}

func installerFor(version string) string {
	return "#!/bin/sh\n" +
		"DOWNLOAD_URL=\"https://downloads.cursor.com/lab/" + version + "/${OS}/${ARCH}/agent-cli-package.tar.gz\"\n"
}

func validEntries(architecture string) []tarEntry {
	native := "dist-package/file_service.linux-" + architecture + "-gnu.node"
	return []tarEntry{
		{name: "dist-package/", mode: 0o755},
		{name: "dist-package/cursor-agent", mode: 0o755, body: []byte("agent")},
		{name: "dist-package/node", mode: 0o755, body: elfFor(architecture)},
		{name: "dist-package/index.js", mode: 0o644, body: []byte("index")},
		{name: "dist-package/package.json", mode: 0o644, body: []byte(`{"name":"@anysphere/agent-cli-runtime"}`)},
		{name: native, mode: 0o644, body: elfFor(architecture)},
	}
}

func elfFor(architecture string) []byte {
	header := make([]byte, 64)
	copy(header[:4], []byte{0x7f, 'E', 'L', 'F'})
	header[4] = 2 // ELFCLASS64
	header[5] = 1 // ELFDATA2LSB
	header[6] = 1 // EV_CURRENT
	machine := uint16(62)
	if architecture == "arm64" {
		machine = 183
	}
	binary.LittleEndian.PutUint16(header[18:20], machine)
	return header
}

func writeArchive(t *testing.T, entries []tarEntry) []byte {
	t.Helper()

	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		typeflag := byte(tar.TypeReg)
		if strings.HasSuffix(entry.name, "/") {
			typeflag = tar.TypeDir
		}
		header := &tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Size:     int64(len(entry.body)),
			Typeflag: typeflag,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tarWriter.Write(entry.body); err != nil {
			t.Fatalf("write tar body: %v", err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buffer.Bytes()
}

func writeDockerfileFixture(t *testing.T, version string, x64 string, arm64 string) string {
	t.Helper()

	root := t.TempDir()
	contents := strings.Join([]string{
		"FROM scratch",
		"ARG CURSOR_AGENT_VERSION=" + version,
		"ARG CURSOR_AGENT_LINUX_X64_SHA256=" + x64,
		"ARG CURSOR_AGENT_LINUX_ARM64_SHA256=" + arm64,
		"RUN preserve-this-line",
		"",
	}, "\n")
	for _, name := range []string{"Dockerfile", "Dockerfile.release"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
