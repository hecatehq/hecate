package main

import (
	"testing"
)

// AutoImportEnvProviders tests live with the function in
// internal/controlplane/env_import_test.go.

func TestResolveBootstrapPath_Default(t *testing.T) {
	got := resolveBootstrapPath("", ".data")
	want := ".data/hecate.bootstrap.json"
	if got != want {
		t.Fatalf("resolveBootstrapPath(\"\", .data) = %q, want %q", got, want)
	}
}

func TestResolveBootstrapPath_DockerDataDir(t *testing.T) {
	got := resolveBootstrapPath("", "/data")
	want := "/data/hecate.bootstrap.json"
	if got != want {
		t.Fatalf("resolveBootstrapPath(\"\", /data) = %q, want %q", got, want)
	}
}

func TestResolveBootstrapPath_ExplicitFileWins(t *testing.T) {
	got := resolveBootstrapPath("/run/secrets/bootstrap.json", ".data")
	want := "/run/secrets/bootstrap.json"
	if got != want {
		t.Fatalf("explicit GATEWAY_BOOTSTRAP_FILE not honored: got %q, want %q", got, want)
	}
}
