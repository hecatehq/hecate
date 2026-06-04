---
applyTo: "tauri/**,scripts/stamp-version.ts,scripts/release.ts,docs/operator/desktop-app.md"
---

# Tauri Desktop App

For desktop work, read `docs-ai/skills/tauri/SKILL.md` before editing.

High-signal rules:

- The desktop app wraps the Go `hecate` sidecar and loads the embedded React UI
  over loopback.
- Keep sidecar naming, permissions, schemas, and version stamping in sync.
- Do not bypass the sidecar lifecycle helpers or silently change startup/port
  behavior.
- Run the Rust/Tauri verification called for by the touched area, and report
  any platform-specific checks you could not run.
