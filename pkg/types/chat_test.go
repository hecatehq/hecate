package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContentBlock_AttachmentProvenanceIsInternal(t *testing.T) {
	block := ContentBlock{Type: "text", Text: "content", AttachmentInput: true}
	encoded, err := json.Marshal(block)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "AttachmentInput") || strings.Contains(string(encoded), "attachment_input") {
		t.Fatalf("internal provenance leaked: %s", encoded)
	}
	var incoming ContentBlock
	if err := json.Unmarshal([]byte(`{"type":"text","text":"ordinary","AttachmentInput":true,"attachment_input":true}`), &incoming); err != nil {
		t.Fatal(err)
	}
	if incoming.AttachmentInput {
		t.Fatal("client supplied internal attachment provenance")
	}
}
