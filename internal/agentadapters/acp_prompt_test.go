package agentadapters

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

func TestNormalizePromptInputValidatesAndCopiesFiles(t *testing.T) {
	t.Parallel()

	file := promptTestFile("notes.txt", "text/plain", []byte("private notes"))
	input, err := normalizePromptInput(PromptInput{Text: "  inspect  ", Files: []PromptFile{file}})
	if err != nil {
		t.Fatalf("normalizePromptInput: %v", err)
	}
	if input.Text != "inspect" || len(input.Files) != 1 || string(input.Files[0].Data) != "private notes" {
		t.Fatalf("normalized input = %#v", input)
	}
	file.Data[0] = 'X'
	if string(input.Files[0].Data) != "private notes" {
		t.Fatal("normalized prompt retained caller-owned attachment bytes")
	}
	clearPromptInput(&input)
	if input.Text != "" || input.Files != nil {
		t.Fatalf("cleared prompt = %#v", input)
	}
	if string(file.Data) != "Xrivate notes" {
		t.Fatal("clearing normalized prompt mutated caller-owned attachment bytes")
	}

	tests := []struct {
		name  string
		input PromptInput
	}{
		{name: "empty", input: PromptInput{}},
		{name: "path filename", input: PromptInput{Files: []PromptFile{promptTestFile("../notes.txt", "text/plain", []byte("x"))}}},
		{name: "backslash filename", input: PromptInput{Files: []PromptFile{promptTestFile(`..\notes.txt`, "text/plain", []byte("x"))}}},
		{name: "invalid media type", input: PromptInput{Files: []PromptFile{promptTestFile("notes.txt", "not a media type", []byte("x"))}}},
		{name: "size mismatch", input: PromptInput{Files: []PromptFile{func() PromptFile {
			item := promptTestFile("notes.txt", "text/plain", []byte("x"))
			item.SizeBytes++
			return item
		}()}}},
		{name: "digest mismatch", input: PromptInput{Files: []PromptFile{func() PromptFile {
			item := promptTestFile("notes.txt", "text/plain", []byte("x"))
			item.SHA256 = strings.Repeat("0", sha256.Size*2)
			return item
		}()}}},
		{name: "file too large", input: PromptInput{Files: []PromptFile{promptTestFile("large.bin", "application/octet-stream", make([]byte, MaxPromptFileBytes+1))}}},
		{name: "combined size", input: PromptInput{Files: []PromptFile{
			promptTestFile("one.bin", "application/octet-stream", make([]byte, 4<<20)),
			promptTestFile("two.bin", "application/octet-stream", make([]byte, 4<<20)),
			promptTestFile("three.bin", "application/octet-stream", make([]byte, (4<<20)+1)),
		}}},
		{name: "too many files", input: PromptInput{Files: []PromptFile{
			promptTestFile("one.txt", "text/plain", []byte("1")),
			promptTestFile("two.txt", "text/plain", []byte("2")),
			promptTestFile("three.txt", "text/plain", []byte("3")),
			promptTestFile("four.txt", "text/plain", []byte("4")),
			promptTestFile("five.txt", "text/plain", []byte("5")),
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := normalizePromptInput(test.input); err == nil {
				t.Fatal("normalizePromptInput error = nil")
			}
		})
	}
}

func TestBuildACPPromptUsesOnlyNegotiatedBlocks(t *testing.T) {
	t.Parallel()

	image := promptTestFile("diagram.png", "image/png", []byte("raster bytes"))
	text := promptTestFile("notes.txt", "text/plain", []byte("hello\nworld"))
	binary := promptTestFile("archive.bin", "application/octet-stream", []byte{0xff, 0x00, 0x01})

	t.Run("image capability", func(t *testing.T) {
		blocks, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{image}}, acp.PromptCapabilities{Image: true})
		if err != nil {
			t.Fatalf("buildACPPrompt: %v", err)
		}
		if stage != nil || len(blocks) != 1 || blocks[0].Image == nil || blocks[0].Resource != nil || blocks[0].ResourceLink != nil {
			t.Fatalf("blocks = %#v, stage = %#v", blocks, stage)
		}
	})

	t.Run("embedded context", func(t *testing.T) {
		blocks, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{image, text, binary}}, acp.PromptCapabilities{EmbeddedContext: true})
		if err != nil {
			t.Fatalf("buildACPPrompt: %v", err)
		}
		if stage != nil || len(blocks) != 3 {
			t.Fatalf("blocks = %#v, stage = %#v", blocks, stage)
		}
		if blocks[0].Resource == nil || blocks[0].Resource.Resource.BlobResourceContents == nil || blocks[0].Image != nil {
			t.Fatalf("image without image capability = %#v, want embedded blob resource", blocks[0])
		}
		if blocks[1].Resource == nil || blocks[1].Resource.Resource.TextResourceContents == nil {
			t.Fatalf("text resource = %#v", blocks[1])
		}
		if blocks[2].Resource == nil || blocks[2].Resource.Resource.BlobResourceContents == nil {
			t.Fatalf("binary resource = %#v", blocks[2])
		}
	})

	t.Run("baseline resource link", func(t *testing.T) {
		blocks, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{text}}, acp.PromptCapabilities{})
		if err != nil {
			t.Fatalf("buildACPPrompt: %v", err)
		}
		if stage == nil || len(blocks) != 1 || blocks[0].ResourceLink == nil {
			t.Fatalf("blocks = %#v, stage = %#v", blocks, stage)
		}
		link := blocks[0].ResourceLink
		path := cleanACPReadPath(link.Uri)
		if !strings.HasSuffix(path, "input-1-notes.txt") {
			t.Fatalf("staged path = %q", path)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "hello\nworld" {
			t.Fatalf("read staged input = %q, %v", data, err)
		}
		if err := verifySealedPrivateACPPromptStageFile(path); err != nil {
			t.Fatalf("verify staged file security: %v", err)
		}
		dir := stage.dir
		if err := verifySealedPrivateACPPromptStageDir(dir); err != nil {
			t.Fatalf("verify staged directory security: %v", err)
		}
		if err := stage.cleanup(); err != nil {
			t.Fatalf("cleanup: %v", err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("staged directory still exists: %v", err)
		}
	})
}

func TestBuildACPPromptCapsCumulativeInlineWireBytes(t *testing.T) {
	t.Parallel()

	t.Run("preflight avoids oversized rich payload encoding", func(t *testing.T) {
		largeImage := promptTestFile("large.png", "image/png", make([]byte, MaxPromptFileBytes))
		if acpPromptRichPayloadMayFit(largeImage, acp.PromptCapabilities{Image: true}, maxACPPromptInlineWireBytes) {
			t.Fatal("5 MiB image passed the inline rich-payload preflight")
		}
		largeBlob := promptTestFile("large.bin", "application/octet-stream", make([]byte, MaxPromptFileBytes))
		if acpPromptRichPayloadMayFit(largeBlob, acp.PromptCapabilities{EmbeddedContext: true}, maxACPPromptInlineWireBytes) {
			t.Fatal("5 MiB blob passed the inline rich-payload preflight")
		}
		if !acpPromptRichPayloadMayFit(
			promptTestFile("small.png", "image/png", []byte("small")),
			acp.PromptCapabilities{Image: true},
			maxACPPromptInlineWireBytes,
		) {
			t.Fatal("small image failed the inline rich-payload preflight")
		}
	})

	t.Run("oversized text fails before dispatch", func(t *testing.T) {
		blocks, stage, err := buildACPPrompt(
			PromptInput{Text: strings.Repeat("x", maxACPPromptInlineWireBytes)},
			acp.PromptCapabilities{},
		)
		if err == nil || blocks != nil || stage != nil {
			t.Fatalf("buildACPPrompt(oversized text) = %#v, %#v, %v", blocks, stage, err)
		}
	})

	t.Run("exact boundary and one-byte overflow", func(t *testing.T) {
		file := promptTestFile("notes.txt", "text/plain", []byte("bounded context"))
		fileWireBytes, err := acpContentBlockWireBytes(embeddedACPResourceBlock(0, file))
		if err != nil {
			t.Fatalf("resource wire bytes: %v", err)
		}
		emptyTextWireBytes, err := acpContentBlockWireBytes(acp.TextBlock(""))
		if err != nil {
			t.Fatalf("text wire bytes: %v", err)
		}
		textBytes := maxACPPromptInlineWireBytes - fileWireBytes - emptyTextWireBytes
		if textBytes <= 0 {
			t.Fatalf("test fixture has no text budget: %d", textBytes)
		}
		text := strings.Repeat("x", textBytes)

		blocks, stage, err := buildACPPrompt(
			PromptInput{Text: text, Files: []PromptFile{file}},
			acp.PromptCapabilities{EmbeddedContext: true},
		)
		if err != nil {
			t.Fatalf("buildACPPrompt(exact): %v", err)
		}
		if stage != nil || len(blocks) != 2 || blocks[1].Resource == nil {
			t.Fatalf("exact-boundary blocks = %#v, stage = %#v", blocks, stage)
		}
		if got := promptBlocksWireBytes(t, blocks); got != maxACPPromptInlineWireBytes {
			t.Fatalf("exact-boundary wire bytes = %d, want %d", got, maxACPPromptInlineWireBytes)
		}
		envelope, err := json.Marshal(struct {
			JSONRPC string            `json:"jsonrpc"`
			ID      int               `json:"id"`
			Method  string            `json:"method"`
			Params  acp.PromptRequest `json:"params"`
		}{
			JSONRPC: "2.0",
			ID:      1,
			Method:  "session/prompt",
			Params: acp.PromptRequest{
				SessionId: acp.SessionId(strings.Repeat("s", 128)),
				Prompt:    blocks,
			},
		})
		if err != nil {
			t.Fatalf("marshal prompt envelope: %v", err)
		}
		if len(envelope) >= 1<<20 {
			t.Fatalf("exact-boundary JSON-RPC envelope = %d bytes, want below 1 MiB", len(envelope))
		}

		blocks, stage, err = buildACPPrompt(
			PromptInput{Text: text + "x", Files: []PromptFile{file}},
			acp.PromptCapabilities{EmbeddedContext: true},
		)
		if err != nil {
			t.Fatalf("buildACPPrompt(overflow): %v", err)
		}
		if stage == nil || len(blocks) != 2 || blocks[1].ResourceLink == nil || blocks[1].Resource != nil {
			t.Fatalf("overflow blocks = %#v, stage = %#v", blocks, stage)
		}
		if err := stage.cleanup(); err != nil {
			t.Fatalf("cleanup overflow stage: %v", err)
		}
	})

	t.Run("mixed rich and staged blocks share one budget", func(t *testing.T) {
		image := promptTestFile("diagram.png", "image/png", make([]byte, 128))
		notes := promptTestFile("notes.txt", "text/plain", []byte("must be staged after the image"))
		imageBlock := acp.ImageBlock(base64.StdEncoding.EncodeToString(image.Data), image.MediaType)
		imageURI := embeddedPromptInputURI(0, image.Filename)
		imageBlock.Image.Uri = &imageURI
		imageWireBytes, err := acpContentBlockWireBytes(imageBlock)
		if err != nil {
			t.Fatalf("image wire bytes: %v", err)
		}
		emptyTextWireBytes, err := acpContentBlockWireBytes(acp.TextBlock(""))
		if err != nil {
			t.Fatalf("text wire bytes: %v", err)
		}
		const remainingAfterImage = 8
		textBytes := maxACPPromptInlineWireBytes - imageWireBytes - emptyTextWireBytes - remainingAfterImage
		if textBytes <= 0 {
			t.Fatalf("test fixture has no text budget: %d", textBytes)
		}

		blocks, stage, err := buildACPPrompt(
			PromptInput{Text: strings.Repeat("x", textBytes), Files: []PromptFile{image, notes}},
			acp.PromptCapabilities{Image: true, EmbeddedContext: true},
		)
		if err != nil {
			t.Fatalf("buildACPPrompt: %v", err)
		}
		if stage == nil || len(stage.files) != 1 || len(blocks) != 3 {
			t.Fatalf("mixed blocks = %#v, stage = %#v", blocks, stage)
		}
		if blocks[1].Image == nil || blocks[2].ResourceLink == nil || blocks[2].Resource != nil {
			t.Fatalf("mixed block types = %#v", blocks)
		}
		if err := stage.cleanup(); err != nil {
			t.Fatalf("cleanup mixed stage: %v", err)
		}
	})

	t.Run("base64 expansion and escaped resource text fall back", func(t *testing.T) {
		tests := []struct {
			name         string
			file         PromptFile
			capabilities acp.PromptCapabilities
		}{
			{
				name:         "image base64 expansion",
				file:         promptTestFile("large.png", "image/png", make([]byte, maxACPPromptInlineWireBytes*3/4)),
				capabilities: acp.PromptCapabilities{Image: true},
			},
			{
				name:         "embedded text JSON escaping",
				file:         promptTestFile("markup.txt", "text/plain", []byte(strings.Repeat("<", maxACPPromptInlineWireBytes/6))),
				capabilities: acp.PromptCapabilities{EmbeddedContext: true},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				blocks, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{test.file}}, test.capabilities)
				if err != nil {
					t.Fatalf("buildACPPrompt: %v", err)
				}
				if stage == nil || len(blocks) != 1 || blocks[0].ResourceLink == nil || blocks[0].Image != nil || blocks[0].Resource != nil {
					t.Fatalf("blocks = %#v, stage = %#v", blocks, stage)
				}
				if err := stage.cleanup(); err != nil {
					t.Fatalf("cleanup: %v", err)
				}
			})
		}
	})
}

func TestACPStagedPromptReadIsExactAndCleanupSafe(t *testing.T) {
	t.Parallel()

	input := promptTestFile("notes.txt", "text/plain", []byte("first\nsecond\nthird"))
	blocks, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{input}}, acp.PromptCapabilities{})
	if err != nil {
		t.Fatalf("buildACPPrompt: %v", err)
	}
	path := cleanACPReadPath(blocks[0].ResourceLink.Uri)
	turn := newACPTurn(1024, nil)
	turn.setPromptFiles(stage.files)
	client := &acpChatClient{workspace: t.TempDir()}
	client.setTurn(turn)

	line, limit := 2, 1
	read, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: blocks[0].ResourceLink.Uri, Line: &line, Limit: &limit})
	if err != nil || read.Content != "second" {
		t.Fatalf("ReadTextFile(staged) = %q, %v", read.Content, err)
	}
	if _, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: filepath.Join(filepath.Dir(path), "sibling")}); err == nil {
		t.Fatal("ReadTextFile accepted an unlisted staged sibling")
	}
	if _, err := client.WriteTextFile(context.Background(), acp.WriteTextFileRequest{Path: path, Content: "overwrite"}); err == nil {
		t.Fatal("WriteTextFile accepted a read-only staged prompt input")
	}

	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 100 {
				read, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: path})
				if err == nil && read.Content != "first\nsecond\nthird" {
					t.Errorf("concurrent staged read = %q", read.Content)
					return
				}
			}
		}()
	}
	turn.clearPromptFiles()
	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	wait.Wait()
	client.clearTurn(turn)
}

func TestStagedFileURIRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "space and #hash.txt")
	uri := stagedFileURI(path)
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" {
		t.Fatalf("staged URI = %q, parsed=%#v, err=%v", uri, parsed, err)
	}
	if got := cleanACPReadPath(uri); got != filepath.Clean(path) {
		t.Fatalf("round trip = %q, want %q", got, filepath.Clean(path))
	}
}

func TestStagedPromptFilenameIsPortableWithoutChangingLinkMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		original string
		want     string
	}{
		{name: "alternate stream separator", original: "bad:name.txt", want: "input-1-bad_name.txt"},
		{name: "reserved device", original: "CON.txt", want: "input-1-_CON.txt"},
		{name: "reserved port", original: "lpt9", want: "input-1-_lpt9"},
		{name: "trailing dot", original: "notes.txt.", want: "input-1-notes.txt"},
		{name: "invalid punctuation", original: `a<>|?*b.json`, want: "input-1-a_____b.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := stagedPromptFilename(0, test.original); got != test.want {
				t.Fatalf("stagedPromptFilename(0, %q) = %q, want %q", test.original, got, test.want)
			}
		})
	}

	file := promptTestFile("bad:name.txt", "text/plain", []byte("portable staging"))
	blocks, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{file}}, acp.PromptCapabilities{})
	if err != nil {
		t.Fatalf("buildACPPrompt: %v", err)
	}
	t.Cleanup(func() { _ = stage.cleanup() })
	link := blocks[0].ResourceLink
	if link.Name != file.Filename || link.Title == nil || *link.Title != file.Filename {
		t.Fatalf("resource link metadata = %#v, want original filename %q", link, file.Filename)
	}
	if got := filepath.Base(cleanACPReadPath(link.Uri)); got != "input-1-bad_name.txt" {
		t.Fatalf("staged basename = %q, want portable basename", got)
	}
}

func TestACPPromptStageCleanupAfterCreateError(t *testing.T) {
	t.Parallel()

	stage := &acpPromptStage{}
	data := []byte("private input")
	if _, err := stage.write(0, "notes.txt", data); err != nil {
		t.Fatalf("first write: %v", err)
	}
	dir := stage.dir
	if _, err := stage.write(0, "notes.txt", []byte("duplicate")); err == nil {
		t.Fatal("duplicate exclusive create error = nil")
	}
	if len(stage.fileNames) != 2 || stage.fileNames[0] != stage.fileNames[1] {
		t.Fatalf("cleanup manifest after failed create = %#v, want both attempted exact child names", stage.fileNames)
	}
	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup after create error: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("staged directory survived cleanup: %v", err)
	}
	for index, value := range data {
		if value != 0 {
			t.Fatalf("staged byte %d survived cleanup", index)
		}
	}
}

func TestACPPromptStageCleanupRetriesFullHandleSafeTransition(t *testing.T) {
	t.Parallel()

	data := []byte("private input")
	quarantineAttempts := 0
	prepareAttempts := 0
	removeAttempts := 0
	var waits []time.Duration
	stage := &acpPromptStage{
		dir:       "private-stage",
		files:     map[string][]byte{"private-stage/input.txt": data},
		fileNames: []string{"input.txt"},
		identity:  &acpPromptStageIdentity{},
	}
	stage.quarantineStage = func(dir string, identity *acpPromptStageIdentity) error {
		quarantineAttempts++
		if quarantineAttempts == 1 {
			return os.ErrPermission
		}
		return nil
	}
	stage.prepareStage = func(identity *acpPromptStageIdentity) error {
		prepareAttempts++
		if prepareAttempts == 1 {
			return os.ErrPermission
		}
		return nil
	}
	stage.removeStage = func(identity *acpPromptStageIdentity, filenames []string) error {
		removeAttempts++
		if removeAttempts == 1 {
			return os.ErrPermission
		}
		return nil
	}
	stage.identityRemoved = func(*acpPromptStageIdentity) bool { return true }
	stage.waitForRetry = func(delay time.Duration) {
		waits = append(waits, delay)
	}
	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup after transient failures: %v", err)
	}
	if quarantineAttempts != 4 || prepareAttempts != 3 || removeAttempts != 2 {
		t.Fatalf("transition attempts = quarantine %d, prepare %d, remove %d; want 4, 3, 2", quarantineAttempts, prepareAttempts, removeAttempts)
	}
	if len(waits) != 3 || waits[0] != 10*time.Millisecond || waits[1] != 20*time.Millisecond || waits[2] != 40*time.Millisecond {
		t.Fatalf("retry waits = %v, want [10ms 20ms 40ms]", waits)
	}
	if stage.dir != "" {
		t.Fatalf("stage dir after cleanup = %q, want empty", stage.dir)
	}
	for index, value := range data {
		if value != 0 {
			t.Fatalf("staged byte %d survived cleanup", index)
		}
	}
}

func TestACPPromptStageQuarantineCandidateRemainsBoundedAcrossRetries(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	namespace := newACPPromptStageNamespace(filepath.Join(parent, ".hecate-acp-stage"))
	namespace.mu.RLock()
	baselineCount := len(namespace.dirs)
	namespace.mu.RUnlock()
	pending := ""
	var first string
	firstAttemptCount := 0
	for attempt := 0; attempt < 1_000; attempt++ {
		name, err := pendingACPPromptStageQuarantineName(&pending)
		if err != nil {
			t.Fatalf("quarantine candidate attempt %d: %v", attempt, err)
		}
		if attempt == 0 {
			first = name
		} else if name != first {
			t.Fatalf("quarantine candidate changed on attempt %d: got %q, want %q", attempt, name, first)
		}
		namespace.addDirectory(filepath.Join(parent, name))
		if attempt == 0 {
			namespace.mu.RLock()
			firstAttemptCount = len(namespace.dirs)
			namespace.mu.RUnlock()
		}
	}
	namespace.mu.RLock()
	directoryCount := len(namespace.dirs)
	namespace.mu.RUnlock()
	if firstAttemptCount <= baselineCount || firstAttemptCount > baselineCount+2 {
		t.Fatalf("first quarantine alias added %d namespace spellings from baseline %d", firstAttemptCount, baselineCount)
	}
	if directoryCount != firstAttemptCount {
		t.Fatalf("retry namespace grew from %d to %d directories", firstAttemptCount, directoryCount)
	}
}

func TestACPPromptStageCleanupRetriesOnlyProofAfterRemovalWasIssued(t *testing.T) {
	t.Parallel()

	quarantineAttempts := 0
	prepareAttempts := 0
	removeAttempts := 0
	proofAttempts := 0
	proofAllowed := false
	stage := &acpPromptStage{
		dir:       "private-stage",
		fileNames: []string{"input.txt"},
		identity:  &acpPromptStageIdentity{},
		quarantineStage: func(string, *acpPromptStageIdentity) error {
			quarantineAttempts++
			return nil
		},
		prepareStage: func(*acpPromptStageIdentity) error {
			prepareAttempts++
			return nil
		},
		removeStage: func(*acpPromptStageIdentity, []string) error {
			removeAttempts++
			return nil
		},
		identityRemoved: func(*acpPromptStageIdentity) bool {
			proofAttempts++
			return proofAllowed
		},
		waitForRetry: func(time.Duration) {},
	}

	if err := stage.cleanup(); err == nil {
		t.Fatal("cleanup error = nil while removal proof remains unavailable")
	}
	if !stage.removalIssued || stage.dir == "" || stage.identity == nil {
		t.Fatalf("cleanup lost proof-only state: removalIssued=%t dir=%q identity=%#v", stage.removalIssued, stage.dir, stage.identity)
	}
	if quarantineAttempts != 1 || prepareAttempts != 1 || removeAttempts != 1 {
		t.Fatalf("destructive transition repeated after removal: quarantine=%d prepare=%d remove=%d", quarantineAttempts, prepareAttempts, removeAttempts)
	}
	if proofAttempts != acpPromptStageRemovalProofTries {
		t.Fatalf("removal proof attempts = %d, want %d", proofAttempts, acpPromptStageRemovalProofTries)
	}

	proofAllowed = true
	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup after removal proof became available: %v", err)
	}
	if quarantineAttempts != 1 || prepareAttempts != 1 || removeAttempts != 1 {
		t.Fatalf("cleanup re-entered pathname transition after removal: quarantine=%d prepare=%d remove=%d", quarantineAttempts, prepareAttempts, removeAttempts)
	}
	if stage.removalIssued || stage.dir != "" || stage.identity != nil {
		t.Fatalf("proof-only cleanup retained ownership: removalIssued=%t dir=%q identity=%#v", stage.removalIssued, stage.dir, stage.identity)
	}
}

func promptTestFile(name, mediaType string, data []byte) PromptFile {
	digest := sha256.Sum256(data)
	return PromptFile{
		Filename:  name,
		MediaType: mediaType,
		SizeBytes: int64(len(data)),
		SHA256:    hex.EncodeToString(digest[:]),
		Data:      data,
	}
}

func promptBlocksWireBytes(t *testing.T, blocks []acp.ContentBlock) int {
	t.Helper()
	total := 0
	for _, block := range blocks {
		encodedBytes, err := acpContentBlockWireBytes(block)
		if err != nil {
			t.Fatalf("content block wire bytes: %v", err)
		}
		total += encodedBytes
	}
	return total
}
