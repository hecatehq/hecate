package codeintel

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/workspacefs"
)

const (
	maxDocumentBytes    = 512 * 1024
	maxSourceCacheBytes = 16 * 1024 * 1024
)

type positionEncoding string

const (
	positionUTF8  positionEncoding = "utf-8"
	positionUTF16 positionEncoding = "utf-16"
	positionUTF32 positionEncoding = "utf-32"
)

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type sourceFile struct {
	relative string
	absolute string
	uri      string
	data     []byte
}

type sourceCache struct {
	fsys     *workspacefs.FS
	files    map[string]*sourceFile
	bytes    int64
	maxBytes int64
}

func newSourceCache(fsys *workspacefs.FS) *sourceCache {
	return &sourceCache{
		fsys:     fsys,
		files:    make(map[string]*sourceFile),
		maxBytes: maxSourceCacheBytes,
	}
}

func (c *sourceCache) openRelative(relative string) (*sourceFile, error) {
	relative = filepath.Clean(strings.TrimSpace(relative))
	if relative == "." || relative == "" {
		return nil, fmt.Errorf("file path is required")
	}
	if cached := c.files[relative]; cached != nil {
		return cached, nil
	}
	handle, info, absolute, err := c.fsys.OpenReadNonBlocking(relative)
	if err != nil {
		return nil, fmt.Errorf("open workspace file %q: %w", relative, err)
	}
	defer handle.Close()
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("workspace path %q is not a regular file", relative)
	}
	if info.Size() > maxDocumentBytes {
		return nil, fmt.Errorf("workspace file %q exceeds the %d-byte code-intelligence limit", relative, maxDocumentBytes)
	}
	data, err := io.ReadAll(io.LimitReader(handle, maxDocumentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read workspace file %q: %w", relative, err)
	}
	currentInfo, err := handle.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect workspace file %q after read: %w", relative, err)
	}
	if !currentInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("workspace path %q is not a regular file", relative)
	}
	if len(data) > maxDocumentBytes {
		return nil, fmt.Errorf("workspace file %q exceeds the %d-byte code-intelligence limit", relative, maxDocumentBytes)
	}
	if currentInfo.Size() > maxDocumentBytes {
		return nil, fmt.Errorf("workspace file %q exceeds the %d-byte code-intelligence limit", relative, maxDocumentBytes)
	}
	if currentInfo.Size() != int64(len(data)) {
		return nil, fmt.Errorf("workspace file %q changed while it was being read", relative)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("workspace file %q is not valid UTF-8", relative)
	}
	file := &sourceFile{
		relative: filepath.ToSlash(relative),
		absolute: absolute,
		uri:      pathToFileURI(absolute),
		data:     data,
	}
	// A provider response may reference many different source files. Retain a
	// bounded working set for repeated locations; files beyond that set are
	// still normalized correctly but become collectible after the item is
	// built. Combined with max_results, this bounds both retained memory and
	// total source reads for one query.
	if c.maxBytes <= 0 || c.bytes+int64(len(data)) <= c.maxBytes {
		c.files[relative] = file
		c.bytes += int64(len(data))
	}
	return file, nil
}

func (c *sourceCache) openURI(rawURI string) (*sourceFile, error) {
	relative, err := workspaceRelativeURI(c.fsys, rawURI)
	if err != nil {
		return nil, err
	}
	return c.openRelative(relative)
}

func pathToFileURI(path string) string {
	slashPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

func workspaceRelativeURI(fsys *workspacefs.FS, rawURI string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURI))
	if err != nil || u == nil {
		return "", fmt.Errorf("invalid file URI")
	}
	if !strings.EqualFold(u.Scheme, "file") || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("LSP result is not a local file URI")
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return "", fmt.Errorf("LSP result uses a non-local file URI host")
	}
	path := filepath.FromSlash(u.Path)
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == filepath.Separator && path[2] == ':' {
		path = path[1:]
	}
	if strings.ContainsRune(path, '\x00') || !filepath.IsAbs(path) {
		return "", fmt.Errorf("LSP result file URI is not an absolute path")
	}
	path = filepath.Clean(path)
	relative, err := filepath.Rel(fsys.Root(), path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("LSP result is outside the workspace")
	}
	resolved, err := fsys.Resolve(relative)
	if err != nil {
		return "", fmt.Errorf("LSP result path is unsafe: %w", err)
	}
	if !samePath(resolved, path) {
		return "", fmt.Errorf("LSP result path is not canonical within the workspace")
	}
	return relative, nil
}

func samePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func (f *sourceFile) requestPosition(line, byteColumn int, encoding positionEncoding) (lspPosition, error) {
	if line <= 0 || byteColumn <= 0 {
		return lspPosition{}, fmt.Errorf("line and column must be positive 1-based values")
	}
	lineData, err := sourceLine(f.data, line-1)
	if err != nil {
		return lspPosition{}, err
	}
	byteOffset := byteColumn - 1
	if byteOffset > len(lineData) {
		return lspPosition{}, fmt.Errorf("column %d is past line %d", byteColumn, line)
	}
	if byteOffset < len(lineData) && !utf8.RuneStart(lineData[byteOffset]) {
		return lspPosition{}, fmt.Errorf("column %d splits a UTF-8 character on line %d", byteColumn, line)
	}
	character, err := bytesToLSPCharacter(lineData[:byteOffset], encoding)
	if err != nil {
		return lspPosition{}, err
	}
	return lspPosition{Line: line - 1, Character: character}, nil
}

func normalizedRange(file *sourceFile, value lspRange, encoding positionEncoding) (Item, error) {
	startColumn, err := lspCharacterToBytes(file.data, value.Start, encoding)
	if err != nil {
		return Item{}, err
	}
	endColumn, err := lspCharacterToBytes(file.data, value.End, encoding)
	if err != nil {
		return Item{}, err
	}
	if value.End.Line < value.Start.Line || value.End.Line == value.Start.Line && endColumn < startColumn {
		return Item{}, fmt.Errorf("LSP result range is reversed")
	}
	return Item{
		Path:        file.relative,
		StartLine:   value.Start.Line + 1,
		StartColumn: startColumn + 1,
		EndLine:     value.End.Line + 1,
		EndColumn:   endColumn + 1,
		Preview:     previewLine(file.data, value.Start.Line),
	}, nil
}

func lspCharacterToBytes(data []byte, position lspPosition, encoding positionEncoding) (int, error) {
	if position.Line < 0 || position.Character < 0 {
		return 0, fmt.Errorf("LSP result contains a negative position")
	}
	line, err := sourceLine(data, position.Line)
	if err != nil {
		return 0, err
	}
	switch encoding {
	case positionUTF8:
		if position.Character > len(line) {
			return 0, fmt.Errorf("LSP UTF-8 position is past the source line")
		}
		if position.Character < len(line) && !utf8.RuneStart(line[position.Character]) {
			return 0, fmt.Errorf("LSP UTF-8 position splits a character")
		}
		return position.Character, nil
	case positionUTF16, positionUTF32:
		units := 0
		for byteOffset, runeValue := range string(line) {
			if units == position.Character {
				return byteOffset, nil
			}
			increment := 1
			if encoding == positionUTF16 {
				increment = len(utf16.Encode([]rune{runeValue}))
			}
			if units+increment > position.Character {
				return 0, fmt.Errorf("LSP position splits a UTF-16 surrogate pair")
			}
			units += increment
		}
		if units == position.Character {
			return len(line), nil
		}
		return 0, fmt.Errorf("LSP position is past the source line")
	default:
		return 0, fmt.Errorf("unsupported LSP position encoding %q", encoding)
	}
}

func bytesToLSPCharacter(prefix []byte, encoding positionEncoding) (int, error) {
	if !utf8.Valid(prefix) {
		return 0, fmt.Errorf("source prefix is not valid UTF-8")
	}
	switch encoding {
	case positionUTF8:
		return len(prefix), nil
	case positionUTF16:
		return len(utf16.Encode([]rune(string(prefix)))), nil
	case positionUTF32:
		return utf8.RuneCount(prefix), nil
	default:
		return 0, fmt.Errorf("unsupported LSP position encoding %q", encoding)
	}
}

func sourceLine(data []byte, zeroBasedLine int) ([]byte, error) {
	if zeroBasedLine < 0 {
		return nil, fmt.Errorf("line must not be negative")
	}
	start := 0
	for line := 0; line < zeroBasedLine; line++ {
		newline := bytes.IndexByte(data[start:], '\n')
		if newline < 0 {
			return nil, fmt.Errorf("line %d is past end of file", zeroBasedLine+1)
		}
		start += newline + 1
	}
	end := bytes.IndexByte(data[start:], '\n')
	if end < 0 {
		end = len(data) - start
	}
	line := data[start : start+end]
	line = bytes.TrimSuffix(line, []byte{'\r'})
	return line, nil
}

func previewLine(data []byte, zeroBasedLine int) string {
	line, err := sourceLine(data, zeroBasedLine)
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(line))
	const maxPreviewBytes = 240
	if len(value) <= maxPreviewBytes {
		return value
	}
	value = value[:maxPreviewBytes]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "..."
}
