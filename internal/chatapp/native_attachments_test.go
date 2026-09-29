package chatapp

import (
	"strings"
	"testing"
)

func TestValidateNativeTextAttachment(t *testing.T) {
	for _, test := range []struct {
		name, media string
		data        []byte
		valid       bool
	}{
		{"plain", "text/plain", []byte("  hello\t世界\r\n"), true},
		{"code without MIME", "", []byte("package main\nfunc main() {}\n"), true},
		{"unknown MIME", "application/octet-stream", []byte("# Notes\n"), true},
		{"json", "application/json", []byte(`{"private":true}`), true},
		{"JSON subtype", "application/vnd.api+json", []byte(`{"private":true}`), true},
		{"XML subtype", "application/atom+xml", []byte("<feed></feed>"), true},
		{"html is inert text", "text/html", []byte("<html><script>alert(1)</script></html>"), true},
		{"limit", "text/plain", []byte(strings.Repeat("a", MaxNativeTextAttachmentBytes)), true},
		{"oversize", "text/plain", []byte(strings.Repeat("a", MaxNativeTextAttachmentBytes+1)), false},
		{"empty", "text/plain", nil, false},
		{"invalid UTF8", "text/plain", []byte{0xff, 0xfe}, false},
		{"NUL", "text/plain", []byte("secret\x00data"), false},
		{"escape", "text/plain", []byte("\x1b[31mred"), false},
		{"C1 control", "text/plain", []byte("a\u0085b"), false},
		{"PDF", "application/pdf", []byte("%PDF-1.7\n"), false},
		{"disguised PDF", "text/plain", []byte("%PDF-1.7\n"), false},
		{"archive", "application/zip", []byte("PK\x03\x04"), false},
		{"false binary MIME", "application/pdf", []byte("plain text"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateNativeTextAttachment(test.data, test.media); (err == nil) != test.valid {
				t.Fatalf("ValidateNativeTextAttachment valid=%t: %v", test.valid, err)
			}
		})
	}
}
