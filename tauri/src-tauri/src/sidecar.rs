// sidecar.rs — spawn, health-poll, and supervise the gateway process.
//
// Design:
//   1. Resolve the gateway binary path:
//      - Release build: look next to the running executable (bundled app).
//      - Debug build: walk up from CARGO_MANIFEST_DIR to the repo root,
//        where `make build` places the `gateway` binary.
//      - Override: GATEWAY_BIN env var always wins.
//   2. Resolve the data directory via Tauri's platform path API so the gateway
//      writes its files to the right place on every OS:
//        macOS   ~/Library/Application Support/io.github.chicoxyzzy.hecate/
//        Windows %APPDATA%\io.github.chicoxyzzy.hecate\
//        Linux   ~/.local/share/io.github.chicoxyzzy.hecate/
//   3. Allocate a free TCP port by binding to :0, then dropping the listener.
//   4. Spawn the gateway (std::process::Child — sync, so kill() works from
//      the window-close event handler without needing an async runtime).
//   5. Poll /healthz every 250 ms (async reqwest) with a 30 s hard deadline.
//   6. On success return the base URL + Child handle. Caller is responsible
//      for calling child.kill() when the app exits.

use std::net::TcpListener;
use std::path::PathBuf;
use std::time::{Duration, Instant};

use tauri::{AppHandle, Manager};

/// Platform paths used by the gateway sidecar and native diagnostics.
#[derive(Clone, Debug)]
pub struct GatewayPaths {
    /// Writable app data directory, resolved through Tauri.
    pub data_dir: PathBuf,
    /// Gateway stderr log captured for the most recent launch.
    pub log_path: PathBuf,
}

/// Returned by [`spawn_and_wait`] on success.
pub struct GatewayHandle {
    /// Base URL the gateway is listening on, e.g. `http://127.0.0.1:52341`.
    pub base_url: String,
    /// The allocated port.
    #[allow(dead_code)] // reserved for future use (deep links, auto-update, etc.)
    pub port: u16,
    /// The spawned gateway process. Caller must kill() this when the app exits.
    pub child: std::process::Child,
}

/// Find the gateway binary. Resolution order:
///   1. `GATEWAY_BIN` env var (explicit override).
///   2. Debug build: `{repo_root}/gateway` (placed by `make build`).
///   3. Release build: next to the running executable (bundled app).
fn resolve_binary() -> Result<PathBuf, String> {
    // 1. Explicit override.
    if let Ok(p) = std::env::var("GATEWAY_BIN") {
        let path = PathBuf::from(&p);
        if path.is_file() {
            return Ok(path);
        }
        return Err(format!(
            "GATEWAY_BIN={p} does not point to an existing file"
        ));
    }

    // 2. Debug build: walk up from the Cargo manifest directory to find the
    //    repo root, where `make build` writes the gateway binary.
    #[cfg(debug_assertions)]
    {
        // CARGO_MANIFEST_DIR is tauri/src-tauri; repo root is two levels up.
        let manifest = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
        let repo_root = manifest
            .parent() // tauri/
            .and_then(|p| p.parent()) // repo root
            .map(|p| p.to_path_buf())
            .ok_or_else(|| "cannot determine repo root from CARGO_MANIFEST_DIR".to_string())?;

        let candidate = repo_root.join("gateway");
        if candidate.is_file() {
            return Ok(candidate);
        }
        return Err(format!(
            "gateway binary not found at {candidate:?}. Run `make build` first."
        ));
    }

    // 3. Release build: look next to the running executable using the names
    //    that Tauri's externalBin bundler produces. The bundler copies the
    //    binary as `gateway-{target_triple}` (e.g. `gateway-aarch64-apple-darwin`),
    //    which lets a single repo hold binaries for multiple platforms. We try
    //    the triple-suffixed name first, then fall back to plain `gateway` for
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
        let names = [format!("gateway-{triple}"), "gateway".to_string()];
        for name in &names {
            let candidate = dir.join(name);
            if candidate.is_file() {
                return Ok(candidate);
            }
        }
        Err(format!(
            "gateway binary not found next to app executable ({dir:?}). \
             Tried: {names:?}"
        ))
    }
}

/// Find a free loopback port. Binds to 127.0.0.1:0, records the assigned
/// port, then drops the listener. There is a small TOCTOU window between the
/// drop and the gateway's bind; in practice this is negligible on a desktop.
pub fn free_port() -> Result<u16, String> {
    TcpListener::bind("127.0.0.1:0")
        .map_err(|e| format!("could not allocate free port: {e}"))?
        .local_addr()
        .map(|a| a.port())
        .map_err(|e| format!("could not read local address: {e}"))
}

/// Resolve the platform-appropriate data directory for the gateway.
/// Uses Tauri's path API so the location is correct on every OS without
/// any platform-specific code here.
///
/// The directory is created if it doesn't exist yet. The gateway itself also
/// creates it, but doing it here gives a clearer error if the path is
/// unwritable (e.g. inside a read-only .app bundle — which it won't be
/// after this fix, but belt-and-suspenders).
pub fn resolve_paths(app: &AppHandle) -> Result<GatewayPaths, String> {
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("cannot resolve app data directory: {e}"))?;
    std::fs::create_dir_all(&dir)
        .map_err(|e| format!("cannot create data directory {dir:?}: {e}"))?;
    let log_path = dir.join("gateway.log");
    Ok(GatewayPaths {
        data_dir: dir,
        log_path,
    })
}

/// Spawn the gateway binary and block (async) until `/healthz` responds 200
/// or the deadline expires. Returns the gateway base URL on success.
pub async fn spawn_and_wait(app: &AppHandle) -> Result<GatewayHandle, String> {
    let bin = resolve_binary()?;
    let paths = resolve_paths(app)?;
    let port = free_port()?;
    let addr = format!("127.0.0.1:{port}");
    let base_url = format!("http://{addr}");

    // Capture sidecar stderr to <data_dir>/gateway.log so a startup failure
    // (port collision, missing data dir permissions, panic during init,
    // etc.) is diagnosable instead of just timing out the healthz poll
    // with no breadcrumb. Truncate on each launch — we don't want to
    // accumulate logs across runs of an alpha-grade app, and the file is
    // only useful for the most-recent failed start anyway.
    let stderr_log = std::fs::File::create(&paths.log_path).map_err(|e| {
        format!(
            "failed to open {:?} for sidecar stderr: {e}",
            paths.log_path
        )
    })?;

    // Use std::process::Command (not tokio) so the returned Child::kill()
    // is synchronous and can be called from the window-close event handler
    // without an async runtime.
    let child = std::process::Command::new(&bin)
        .env("GATEWAY_ADDRESS", &addr)
        .env("GATEWAY_DATA_DIR", &paths.data_dir)
        // Suppress inherited terminal so the gateway doesn't fight the Tauri
        // process for stdin/stdout in dev mode.
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::from(stderr_log))
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
                "gateway did not become healthy within 30 s (checked {healthz}). \
                 See {:?} for sidecar stderr.",
                paths.log_path
            ));
        }
        match client.get(&healthz).send().await {
            Ok(resp) if resp.status().is_success() => {
                return Ok(GatewayHandle {
                    base_url,
                    port,
                    child,
                });
            }
            _ => tokio::time::sleep(Duration::from_millis(250)).await,
        }
    }
}
