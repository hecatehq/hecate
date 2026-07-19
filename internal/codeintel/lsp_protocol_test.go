package codeintel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestLSPProtocol_ContentLengthRoundTrip(t *testing.T) {
	var wire bytes.Buffer
	writer := newLSPConn(strings.NewReader(""), &wire, 1024, 4096)
	if err := writer.request(7, "example/method", map[string]string{"value": "café"}); err != nil {
		t.Fatalf("write request: %v", err)
	}

	reader := newLSPConn(&wire, &bytes.Buffer{}, 1024, 4096)
	frame, err := reader.read()
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	if frame.Method != "example/method" {
		t.Fatalf("method = %q, want example/method", frame.Method)
	}
	var params map[string]string
	if err := json.Unmarshal(frame.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params["value"] != "café" {
		t.Fatalf("value = %q, want café", params["value"])
	}
}

func TestLSPProtocol_RejectsMalformedAndOversizedFrames(t *testing.T) {
	tests := []struct {
		name string
		wire string
		want string
	}{
		{name: "missing length", wire: "Content-Type: application/json\r\n\r\n{}", want: "missing Content-Length"},
		{name: "duplicate length", wire: "Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}", want: "duplicate"},
		{name: "invalid length", wire: "Content-Length: nope\r\n\r\n", want: "invalid"},
		{name: "oversized", wire: "Content-Length: 17\r\n\r\n", want: "message limit"},
		{name: "malformed json", wire: "Content-Length: 2\r\n\r\n{}", want: "JSON-RPC version"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conn := newLSPConn(strings.NewReader(test.wire), &bytes.Buffer{}, 16, 64)
			_, err := conn.read()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestLSPProtocol_EnforcesCumulativeOutputLimit(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1}`
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	conn := newLSPConn(strings.NewReader(frame+frame), &bytes.Buffer{}, 64, 40)
	if _, err := conn.read(); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if _, err := conn.read(); err == nil || !strings.Contains(err.Error(), "query limit") {
		t.Fatalf("second error = %v, want query limit", err)
	}
}
