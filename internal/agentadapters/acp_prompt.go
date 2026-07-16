package agentadapters

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"
)

const (
	maxPromptFilenameBytes = 128
	// maxACPPromptInlineWireBytes keeps rich prompt blocks well below the
	// 1 MiB line limit used by supported ACP adapter transports. The remaining
	// 256 KiB covers JSON-RPC framing, session identifiers, resource-link
	// metadata, and future protocol fields.
	maxACPPromptInlineWireBytes     = 768 << 10
	acpPromptStageCleanupTries      = 4
	acpPromptStageRemovalProofTries = 4
)

var supportedACPPromptImages = map[string]struct{}{
	"image/jpeg": {},
	"image/png":  {},
	"image/webp": {},
}

type acpPromptStage struct {
	dir             string
	files           map[string][]byte
	fileNames       []string
	identity        *acpPromptStageIdentity
	namespace       *acpPromptStageNamespace
	quarantineStage func(string, *acpPromptStageIdentity) error
	prepareStage    func(*acpPromptStageIdentity) error
	removeStage     func(*acpPromptStageIdentity, []string) error
	identityRemoved func(*acpPromptStageIdentity) bool
	waitForRetry    func(time.Duration)
	removalIssued   bool
}

func normalizePromptInput(input PromptInput) (PromptInput, error) {
	input.Text = strings.TrimSpace(input.Text)
	if len(input.Files) > MaxPromptFiles {
		return PromptInput{}, fmt.Errorf("prompt supports at most %d files", MaxPromptFiles)
	}
	normalized := PromptInput{Text: input.Text}
	if len(input.Files) > 0 {
		normalized.Files = make([]PromptFile, 0, len(input.Files))
	}
	remaining := MaxPromptFilesBytes
	for _, file := range input.Files {
		name := strings.TrimSpace(file.Filename)
		if err := validatePromptFilename(name); err != nil {
			return PromptInput{}, err
		}
		mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(file.MediaType))
		if err != nil || mediaType == "" {
			return PromptInput{}, fmt.Errorf("prompt file %q has an invalid media type", name)
		}
		mediaType = strings.ToLower(mediaType)
		size := int64(len(file.Data))
		if size == 0 || file.SizeBytes != size {
			return PromptInput{}, fmt.Errorf("prompt file %q has invalid size metadata", name)
		}
		if size > MaxPromptFileBytes || size > remaining {
			return PromptInput{}, fmt.Errorf("prompt files exceed the allowed size")
		}
		digest, err := hex.DecodeString(strings.TrimSpace(file.SHA256))
		if err != nil || len(digest) != sha256.Size {
			return PromptInput{}, fmt.Errorf("prompt file %q has invalid digest metadata", name)
		}
		actual := sha256.Sum256(file.Data)
		if !equalBytes(digest, actual[:]) {
			return PromptInput{}, fmt.Errorf("prompt file %q failed integrity validation", name)
		}
		remaining -= size
		normalized.Files = append(normalized.Files, PromptFile{
			Filename:  name,
			MediaType: mediaType,
			SizeBytes: size,
			SHA256:    hex.EncodeToString(actual[:]),
			Data:      append([]byte(nil), file.Data...),
		})
	}
	if normalized.Text == "" && len(normalized.Files) == 0 {
		return PromptInput{}, fmt.Errorf("prompt text or files are required")
	}
	return normalized, nil
}

func validatePromptFilename(name string) error {
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) || len(name) > maxPromptFilenameBytes {
		return fmt.Errorf("prompt file has an invalid filename")
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("prompt file %q has an invalid filename", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("prompt file %q has an invalid filename", name)
		}
	}
	return nil
}

func buildACPPrompt(input PromptInput, capabilities acp.PromptCapabilities) ([]acp.ContentBlock, *acpPromptStage, error) {
	blocks := make([]acp.ContentBlock, 0, len(input.Files)+1)
	wireBytes := 0
	if input.Text != "" {
		block := acp.TextBlock(input.Text)
		encodedBytes, err := acpContentBlockWireBytes(block)
		if err != nil {
			return nil, nil, err
		}
		if encodedBytes > maxACPPromptInlineWireBytes {
			return nil, nil, errors.New("prompt text exceeds the ACP transport limit")
		}
		wireBytes = encodedBytes
		blocks = append(blocks, block)
	}
	stage := &acpPromptStage{}
	for index, file := range input.Files {
		availableWireBytes := 0
		if wireBytes <= maxACPPromptInlineWireBytes {
			availableWireBytes = maxACPPromptInlineWireBytes - wireBytes
		}
		if acpPromptRichPayloadMayFit(file, capabilities, availableWireBytes) {
			richBlock := richACPPromptBlock(index, file, capabilities)
			encodedBytes, err := acpContentBlockWireBytes(richBlock)
			if err != nil {
				return failACPPromptStage(stage, err)
			}
			if wireBytes <= maxACPPromptInlineWireBytes && encodedBytes <= maxACPPromptInlineWireBytes-wireBytes {
				wireBytes += encodedBytes
				blocks = append(blocks, richBlock)
				continue
			}
		}
		path, err := stage.write(index, file.Filename, file.Data)
		if err != nil {
			return failACPPromptStage(stage, err)
		}
		link := resourceLinkACPBlock(file, path)
		encodedBytes, err := acpContentBlockWireBytes(link)
		if err != nil {
			return failACPPromptStage(stage, err)
		}
		if wireBytes <= maxACPPromptInlineWireBytes && encodedBytes <= maxACPPromptInlineWireBytes-wireBytes {
			wireBytes += encodedBytes
		} else {
			// Once framing has consumed the inline budget, no later rich block
			// should be admitted merely because this link is small.
			wireBytes = maxACPPromptInlineWireBytes + 1
		}
		blocks = append(blocks, link)
	}
	if stage.dir == "" {
		return blocks, nil, nil
	}
	if err := sealPrivateACPPromptStageDir(stage.dir, stage.identity); err != nil {
		return failACPPromptStage(stage, errors.New("secure staged prompt inputs"))
	}
	return blocks, stage, nil
}

func acpPromptRichPayloadMayFit(file PromptFile, capabilities acp.PromptCapabilities, availableWireBytes int) bool {
	if availableWireBytes <= 0 {
		return false
	}
	if _, image := supportedACPPromptImages[file.MediaType]; image && capabilities.Image {
		return base64.StdEncoding.EncodedLen(len(file.Data)) <= availableWireBytes
	}
	if !capabilities.EmbeddedContext {
		return false
	}
	if promptFileIsText(file) {
		return len(file.Data) <= availableWireBytes
	}
	return base64.StdEncoding.EncodedLen(len(file.Data)) <= availableWireBytes
}

func richACPPromptBlock(index int, file PromptFile, capabilities acp.PromptCapabilities) acp.ContentBlock {
	if _, image := supportedACPPromptImages[file.MediaType]; image && capabilities.Image {
		block := acp.ImageBlock(base64.StdEncoding.EncodeToString(file.Data), file.MediaType)
		uri := embeddedPromptInputURI(index, file.Filename)
		block.Image.Uri = &uri
		return block
	}
	return embeddedACPResourceBlock(index, file)
}

func acpContentBlockWireBytes(block acp.ContentBlock) (int, error) {
	encoded, err := json.Marshal(block)
	if err != nil {
		return 0, errors.New("encode ACP prompt content block")
	}
	// Count a delimiter for every block. The final block does not need one,
	// which makes this deliberately conservative.
	return len(encoded) + 1, nil
}

func embeddedACPResourceBlock(index int, file PromptFile) acp.ContentBlock {
	uri := embeddedPromptInputURI(index, file.Filename)
	mediaType := file.MediaType
	if promptFileIsText(file) {
		return acp.ResourceBlock(acp.EmbeddedResourceResource{
			TextResourceContents: &acp.TextResourceContents{
				Text:     string(file.Data),
				MimeType: &mediaType,
				Uri:      uri,
			},
		})
	}
	return acp.ResourceBlock(acp.EmbeddedResourceResource{
		BlobResourceContents: &acp.BlobResourceContents{
			Blob:     base64.StdEncoding.EncodeToString(file.Data),
			MimeType: &mediaType,
			Uri:      uri,
		},
	})
}

func embeddedPromptInputURI(index int, filename string) string {
	return (&url.URL{
		Scheme: "hecate-prompt",
		Host:   "input",
		Path:   fmt.Sprintf("/%d/%s", index+1, filename),
	}).String()
}

func promptFileIsText(file PromptFile) bool {
	if !utf8.Valid(file.Data) {
		return false
	}
	if strings.HasPrefix(file.MediaType, "text/") {
		return true
	}
	switch file.MediaType {
	case "application/json", "application/javascript", "application/xml", "application/x-yaml", "application/yaml":
		return true
	default:
		return strings.HasSuffix(file.MediaType, "+json") || strings.HasSuffix(file.MediaType, "+xml")
	}
}

func resourceLinkACPBlock(file PromptFile, path string) acp.ContentBlock {
	mediaType := file.MediaType
	title := file.Filename
	size := int(file.SizeBytes)
	return acp.ContentBlock{ResourceLink: &acp.ContentBlockResourceLink{
		Name:     file.Filename,
		Title:    &title,
		MimeType: &mediaType,
		Size:     &size,
		Type:     "resource_link",
		Uri:      stagedFileURI(path),
	}}
}

func stagedFileURI(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func cleanACPReadPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	// ACP filesystem callbacks carry absolute OS paths. On Windows, URL
	// parsing would mistake a raw drive prefix such as C:\ for a URL scheme.
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" {
		if parsed.Scheme != "file" || (parsed.Host != "" && parsed.Host != "localhost") {
			return ""
		}
		value = parsed.Path
	}
	value = filepath.FromSlash(value)
	if strings.HasPrefix(value, string(filepath.Separator)) {
		withoutLeadingSeparator := strings.TrimPrefix(value, string(filepath.Separator))
		if filepath.VolumeName(withoutLeadingSeparator) != "" {
			value = withoutLeadingSeparator
		}
	}
	if !filepath.IsAbs(value) {
		return ""
	}
	return filepath.Clean(value)
}

func (stage *acpPromptStage) write(index int, filename string, data []byte) (string, error) {
	if stage.dir == "" {
		dir, identity, err := createPrivateACPPromptStageDir()
		if err != nil {
			return "", errors.New("create private staged prompt input directory")
		}
		stage.dir = dir
		stage.identity = identity
		stage.files = make(map[string][]byte)
	}
	stagedName := stagedPromptFilename(index, filename)
	path := filepath.Join(stage.dir, stagedName)
	// Record the intended child before exclusive creation. If creation or a
	// later write/seal step partially succeeds and the immediate best-effort
	// delete also fails, process-owned cleanup must still know the exact
	// handle-relative name to remove.
	stage.fileNames = append(stage.fileNames, stagedName)
	file, err := openPrivateACPPromptStageFile(stage.identity, stagedName)
	if err != nil {
		return "", errors.New("create private staged prompt input")
	}
	_, writeErr := file.Write(data)
	var secureErr error
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if writeErr == nil {
		secureErr = sealPrivateACPPromptStageFile(file)
	}
	var retainErr error
	if writeErr == nil && secureErr == nil {
		retainErr = retainPrivateACPPromptStageFile(stage.identity, stagedName, file)
	}
	if writeErr != nil || secureErr != nil || retainErr != nil {
		_ = file.Close()
		_ = deletePrivateACPPromptStageChild(stage.identity, stagedName)
		return "", errors.New("write private staged prompt input")
	}
	stage.files[filepath.Clean(path)] = data
	return path, nil
}

func randomACPPromptStageName(prefix string) (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", errors.New("generate private staged prompt input name")
	}
	return prefix + hex.EncodeToString(entropy[:]), nil
}

func pendingACPPromptStageQuarantineName(pending *string) (string, error) {
	if pending == nil {
		return "", errors.New("private staged prompt input quarantine identity is unavailable")
	}
	if *pending != "" {
		return *pending, nil
	}
	name, err := randomACPPromptStageName(".hecate-acp-cleanup-")
	if err != nil {
		return "", err
	}
	*pending = name
	return name, nil
}

func stagedPromptFilename(index int, filename string) string {
	var safe strings.Builder
	safe.Grow(len(filename))
	for _, r := range filename {
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			safe.WriteByte('_')
		default:
			safe.WriteRune(r)
		}
	}
	basename := strings.TrimRight(safe.String(), " .")
	if basename == "" {
		basename = "file"
	}
	stem := basename
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
	if isWindowsReservedFilename(stem) {
		basename = "_" + basename
	}
	return fmt.Sprintf("input-%d-%s", index+1, basename)
}

func isWindowsReservedFilename(stem string) bool {
	stem = strings.ToUpper(strings.TrimRight(stem, " ."))
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(stem) != 4 || (stem[:3] != "COM" && stem[:3] != "LPT") {
		return false
	}
	return stem[3] >= '1' && stem[3] <= '9'
}

func (stage *acpPromptStage) cleanup() error {
	if stage == nil || stage.dir == "" {
		return nil
	}
	for path, data := range stage.files {
		clear(data)
		delete(stage.files, path)
	}
	filenames := append([]string(nil), stage.fileNames...)
	dir := stage.dir
	identity := stage.identity
	if identity == nil {
		return errors.New("remove private staged prompt inputs: stage identity is unavailable")
	}
	removeStage := stage.removeStage
	if removeStage == nil {
		removeStage = removePrivateACPPromptStage
	}
	quarantineStage := stage.quarantineStage
	if quarantineStage == nil {
		quarantineStage = quarantinePrivateACPPromptStage
	}
	prepareStage := stage.prepareStage
	if prepareStage == nil {
		prepareStage = preparePrivateACPPromptStageCleanup
	}
	waitForRetry := stage.waitForRetry
	if waitForRetry == nil {
		waitForRetry = time.Sleep
	}
	identityRemoved := stage.identityRemoved
	if identityRemoved == nil {
		identityRemoved = privateACPPromptStageIdentityRemoved
	}
	finishRemoval := func() {
		closePrivateACPPromptStageIdentity(identity)
		stage.identity = nil
		stage.dir = ""
		stage.fileNames = nil
		stage.removalIssued = false
		namespace := stage.namespace
		stage.namespace = nil
		if namespace != nil {
			namespace.markRemoved()
		}
	}
	// Retry the complete handle-safe transition. Quarantine is idempotent once
	// the retained stage has moved, while preparation and removal operate only
	// on retained handles or names relative to those handles.
	if !stage.removalIssued {
		for attempt := 0; attempt < acpPromptStageCleanupTries; attempt++ {
			err := quarantineStage(dir, identity)
			if err == nil && stage.namespace != nil {
				stage.namespace.registerDirectory(currentPrivateACPPromptStageDirectory(identity))
			}
			if err == nil {
				err = prepareStage(identity)
			}
			if err == nil {
				err = removeStage(identity, filenames)
				if err == nil {
					// Once removal has been issued successfully, the pathname may
					// legitimately be gone. Never restart quarantine or permission
					// preparation through that missing name; retain the guarded
					// identity and retry only the fail-closed removal proof.
					stage.removalIssued = true
				}
			}
			if stage.removalIssued {
				break
			}
			if attempt+1 < acpPromptStageCleanupTries {
				waitForRetry(time.Duration(1<<attempt) * 10 * time.Millisecond)
			}
		}
	}
	if !stage.removalIssued {
		return errors.New("remove private staged prompt inputs")
	}
	// Removal success and removal proof are separate states. A successful
	// unlink/delete can make the pathname disappear before the retained-handle
	// proof becomes observable. Give that proof its own bounded retries and, on
	// later cleanup calls, never re-enter pathname-based transition work.
	for attempt := 0; attempt < acpPromptStageRemovalProofTries; attempt++ {
		if identityRemoved(identity) {
			finishRemoval()
			return nil
		}
		if attempt+1 < acpPromptStageRemovalProofTries {
			waitForRetry(time.Duration(1<<attempt) * 10 * time.Millisecond)
		}
	}
	return errors.New("remove private staged prompt inputs")
}

func (stage *acpPromptStage) verifyIdentity() error {
	if stage == nil || stage.dir == "" || stage.identity == nil {
		return errors.New("verify private staged prompt input identity")
	}
	if err := verifyPrivateACPPromptStageIdentity(stage.dir, stage.identity); err != nil {
		return errors.New("verify private staged prompt input identity")
	}
	return nil
}

func cleanupACPPromptStageError(stage *acpPromptStage, cause error) error {
	if cleanupErr := stage.cleanup(); cleanupErr != nil {
		return errors.Join(cause, cleanupErr)
	}
	return cause
}

func failACPPromptStage(stage *acpPromptStage, cause error) ([]acp.ContentBlock, *acpPromptStage, error) {
	err := cleanupACPPromptStageError(stage, cause)
	// A non-empty stage after cleanup is still holding the exact private file
	// identities needed for a safe retry. Return that ownership to the session
	// instead of letting the only reference disappear with the error path.
	if stage != nil && stage.dir != "" {
		return nil, stage, err
	}
	return nil, nil, err
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var mismatch byte
	for i := range left {
		mismatch |= left[i] ^ right[i]
	}
	return mismatch == 0
}

func clearPromptInput(input *PromptInput) {
	if input == nil {
		return
	}
	for i := range input.Files {
		clear(input.Files[i].Data)
		input.Files[i].Data = nil
	}
	input.Files = nil
	input.Text = ""
}

func clonePromptInput(input PromptInput) PromptInput {
	cloned := PromptInput{Text: input.Text}
	if len(input.Files) == 0 {
		return cloned
	}
	cloned.Files = make([]PromptFile, len(input.Files))
	for index, file := range input.Files {
		cloned.Files[index] = file
		cloned.Files[index].Data = append([]byte(nil), file.Data...)
	}
	return cloned
}
