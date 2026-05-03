package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGatewayBaseURLUsesExplicitPublicURL(t *testing.T) {
	got, err := gatewayBaseURL("127.0.0.1:8765", "https://hecate.example.test/")
	if err != nil {
		t.Fatalf("gatewayBaseURL() error = %v", err)
	}
	if got != "https://hecate.example.test" {
		t.Fatalf("gatewayBaseURL() = %q", got)
	}
}

func TestGatewayBaseURLDerivesLocalURLFromWildcardListenAddr(t *testing.T) {
	got, err := gatewayBaseURL("[::]:8765", "")
	if err != nil {
		t.Fatalf("gatewayBaseURL() error = %v", err)
	}
	if got != "http://127.0.0.1:8765" {
		t.Fatalf("gatewayBaseURL() = %q", got)
	}
}

func TestWriteGatewayRuntimeState(t *testing.T) {
	dir := t.TempDir()

	path, err := writeGatewayRuntimeState(dir, "127.0.0.1:52341", "")
	if err != nil {
		t.Fatalf("writeGatewayRuntimeState() error = %v", err)
	}
	if path != filepath.Join(dir, gatewayStateFile) {
		t.Fatalf("state path = %q", path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var state gatewayRuntimeState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.BaseURL != "http://127.0.0.1:52341" {
		t.Fatalf("base_url = %q", state.BaseURL)
	}
	if state.ListenAddr != "127.0.0.1:52341" {
		t.Fatalf("listen_addr = %q", state.ListenAddr)
	}
	if state.PID != os.Getpid() {
		t.Fatalf("pid = %d, want %d", state.PID, os.Getpid())
	}
	if state.UpdatedUnix == 0 {
		t.Fatal("updated_unix = 0")
	}
}
