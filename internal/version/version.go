// Package version exposes the gateway's build version as a single
// package-level variable so both cmd/hecate and internal/api can read
// it without an import cycle.
//
// Defaults to "dev" for local builds. goreleaser overrides it via
// `-ldflags '-X github.com/hecate/agent-runtime/internal/version.Version=<git tag>'`
// during a release build, and the value is surfaced on /healthz so the
// UI status bar can show what's actually running.
package version

// Version is the build identifier for this gateway. Defaults to "dev"
// for local / source builds; release builds override it via -ldflags.
var Version = "dev"
