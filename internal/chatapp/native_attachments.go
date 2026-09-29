package chatapp

import (
	"errors"
	"mime"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxNativeTextAttachmentBytes = 5 << 20
)

var (
	ErrNativeTextAttachmentTooLarge = errors.New("text attachment exceeds the 5 MiB limit")
	ErrNativeTextContextTooLarge    = errors.New("text attachments exceed the selected model's inline context budget; turn Tools on to read files in parts, or attach a smaller excerpt")
)

// ValidateNativeTextAttachment admits bounded UTF-8 source material, never a
// document parser or a binary-to-text conversion. Accepted original bytes are
// stored unchanged and served as inert text/plain downloads.
func ValidateNativeTextAttachment(data []byte, declared string) error {
	if len(data) == 0 {
		return errors.New("text attachment is empty")
	}
	if len(data) > MaxNativeTextAttachmentBytes {
		return ErrNativeTextAttachmentTooLarge
	}
	if !utf8.Valid(data) {
		return errors.New("text attachment must contain valid UTF-8")
	}
	for _, r := range string(data) {
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
			return errors.New("text attachment contains binary data or unsupported control characters")
		}
	}
	if declared = strings.TrimSpace(declared); declared != "" {
		mediaType, _, err := mime.ParseMediaType(declared)
		if err != nil || !nativeTextMediaType(strings.ToLower(mediaType), true) {
			return errors.New("only PNG, JPEG, WebP, and UTF-8 text or code attachments are supported")
		}
	}
	detected, _, err := mime.ParseMediaType(http.DetectContentType(data))
	if err != nil || !nativeTextMediaType(detected, false) {
		return errors.New("attachment is not supported UTF-8 text or code")
	}
	return nil
}

func nativeTextMediaType(mediaType string, allowUnknown bool) bool {
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	if strings.HasPrefix(mediaType, "application/") && (strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml")) {
		return true
	}
	switch mediaType {
	case "application/json", "application/ld+json", "application/xml", "application/javascript", "application/x-javascript", "application/yaml", "application/x-yaml", "application/toml", "application/sql":
		return true
	case "application/octet-stream":
		return allowUnknown
	default:
		return false
	}
}
