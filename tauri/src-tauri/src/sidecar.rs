// sidecar.rs — spawn, health-poll, and supervise the hecate gateway process.
//
// Design:
//   1. Resolve the hecate binary path:
//      - Release build: look next to the running executable (bundled app).
//      - Debug build: walk up from CARGO_MANIFEST_DIR to the repo root,
//        where `make build` places the `hecate` binary.
//      - Override: HECATE_BIN env var always wins.
//   2. Allocate a free TCP port by binding to :0, then dropping the listener.
//   3. Spawn hecate with GATEWAY_ADDRESS=127.0.0.1:{port}.
//   4. Poll /healthz every 250 ms with a 30 s hard deadline.
//   5. On success return the base URL. On failure return an error string.

use std::net::TcpListener;
use std::path::PathBuf;
use std::time::{Duration, Instant};

use tauri::AppHandle;

/// Returned by [`spawn_and_wait`] on success.
#[allow(dead_code)] // `port` reserved for future use (deep links, auto-update, etc.)
pub struct GatewayHandle {
    /// Base URL the gateway is listening on, e.g. `http://127.0.0.1:52341`.
    pub base_url: String,
    /// The allocated port.
    pub port: u16,
}

/// Find the hecate binary. Resolution order:
///   1. `HECATE_BIN` env var (explicit override).
///   2. Debug build: `{repo_root}/hecate` (placed by `make build`).
///   3. Release build: next to the running executable (bundled app).
fn resolve_binary() -> Result<PathBuf, String> {
    // 1. Explicit override.
    if let Ok(p) = std::env::var("HECATE_BIN") {
        let path = PathBuf::from(&p);
        if path.is_file() {
            return Ok(path);
        }
        return Err(format!("HECATE_BIN={p} does not point to an existing file"));
    }

    // 2. Debug build: walk up from the Cargo manifest directory to find the
    //    repo root, where `make build` writes the hecate binary.
    #[cfg(debug_assertions)]
    {
        // CARGO_MANIFEST_DIR is tauri/src-tauri; repo root is two levels up.
        let manifest = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
        let repo_root = manifest
            .parent() // tauri/
            .and_then(|p| p.parent()) // repo root
            .map(|p| p.to_path_buf())
            .ok_or_else(|| "cannot determine repo root from CARGO_MANIFEST_DIR".to_string())?;

        let candidate = repo_root.join("hecate");
        if candidate.is_file() {
            return Ok(candidate);
        }
        return Err(format!(
            "hecate binary not found at {candidate:?}. Run `make build` first."
        ));
    }

    // 3. Release build: look next to the running executable using the names
    //    that Tauri's externalBin bundler produces. The bundler copies the
    //    binary as `hecate-{target_triple}` (e.g. `hecate-aarch64-apple-darwin`),
    //    which lets a single repo hold binaries for multiple platforms. We try
    //    the triple-suffixed name first, then fall back to plain `hecate` for
    //    any hand-built layouts.
    #[cfg(not(debug_assertions))]
    {
        let exe = std::env::current_exe()
            .map_err(|e| format!("cannot resolve current executable: {e}"))?;
        let dir = exe
            .parent()
            .ok_or_else(|| "executable has no parent directory".to_string())?;

        // TARGET is the Rust target triple baked in at compile time,
        // e.g. "aarch64-apple-darwin" or "x86_64-pc-windows-msvc".
        let triple = env!("TARGET");
        let names = [format!("hecate-{triple}"), "hecate".to_string()];
        for name in &names {
            let candidate = dir.join(name);
            if candidate.is_file() {
                return Ok(candidate);
            }
        }
        Err(format!(
            "hecate binary not found next to app executable ({dir:?}). \
             Tried: {names:?}"
        ))
    }
}

/// Find a free loopback port. Binds to 127.0.0.1:0, records the assigned
/// port, then drops the listener. There is a small TOCTOU window between the
/// drop and hecate's bind; in practice this is negligible on a desktop.
pub fn free_port() -> Result<u16, String> {
    TcpListener::bind("127.0.0.1:0")
        .map_err(|e| format!("could not allocate free port: {e}"))?
        .local_addr()
        .map(|a| a.port())
        .map_err(|e| format!("could not read local address: {e}"))
}

/// Spawn the hecate binary and block (async) until `/healthz` responds 200
/// or the deadline expires. Returns the gateway base URL on success.
pub async fn spawn_and_wait(_app: &AppHandle) -> Result<GatewayHandle, String> {
    let bin = resolve_binary()?;
    let port = free_port()?;
    let addr = format!("127.0.0.1:{port}");
    let base_url = format!("http://{addr}");

    tokio::process::Command::new(&bin)
        .env("GATEWAY_ADDRESS", &addr)
        // Suppress inherited terminal so the gateway doesn't fight the Tauri
        // process for stdin/stdout in dev mode.
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .spawn()
        .map_err(|e| format!("failed to spawn {bin:?}: {e}"))?;

    // Poll /healthz. Hard deadline: 30 s.
    let deadline = Instant::now() + Duration::from_secs(30);
    let healthz = format!("{base_url}/healthz");
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(2))
        .build()
        .map_err(|e| format!("failed to build HTTP client: {e}"))?;

    loop {
        if Instant::now() >= deadline {
            return Err(format!(
                "hecate did not become healthy within 30 s (checked {healthz})"
            ));
        }
        match client.get(&healthz).send().await {
            Ok(resp) if resp.status().is_success() => {
                return Ok(GatewayHandle { base_url, port });
            }
            _ => tokio::time::sleep(Duration::from_millis(250)).await,
        }
    }
}
