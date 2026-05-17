// sidecar.rs — spawn, health-poll, and supervise the hecate sidecar process.
//
// Design:
//   1. Resolve the hecate sidecar binary path:
//      - Release build: look next to the running executable (bundled app).
//      - Debug build: walk up from CARGO_MANIFEST_DIR to the repo root,
//        where `just build` places the `hecate` binary.
//      - Override: HECATE_BIN env var always wins.
//   2. Resolve the data directory via Tauri's platform path API so the gateway
//      writes its files to the right place on every OS:
//        macOS   ~/Library/Application Support/sh.hecate.app/
//        Windows %APPDATA%\sh.hecate.app\
//        Linux   ~/.local/share/sh.hecate.app/
//   3. Allocate a free TCP port by binding to :0, then dropping the listener.
//   4. Spawn the gateway (std::process::Child — sync, so kill() works from
//      the window-close event handler without needing an async runtime).
//   5. Poll /healthz every 250 ms (async reqwest) with a 30 s hard deadline.
//   6. Pass GATEWAY_PUBLIC_URL so hecate.runtime.json advertises the sidecar URL.
//   7. On success return the base URL + Child handle. Caller is responsible
//      for calling child.kill() when the app exits.

use std::net::TcpListener;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use tauri::{AppHandle, Manager};

/// Platform paths used by the hecate sidecar and native diagnostics.
#[derive(Clone, Debug)]
pub struct GatewayPaths {
    /// Writable app data directory, resolved through Tauri.
    pub data_dir: PathBuf,
    /// Gateway stderr log captured for the most recent launch.
    pub log_path: PathBuf,
    /// Runtime state consumed by hecate-acp to discover the native sidecar URL.
    pub state_path: PathBuf,
}

/// Returned by [`spawn_and_wait`] on success.
pub struct GatewayHandle {
    /// Base URL the gateway is listening on, e.g. `http://127.0.0.1:52341`.
    pub base_url: String,
    /// The allocated port.
    #[allow(dead_code)] // reserved for future use (deep links, auto-update, etc.)
    pub port: u16,
    /// The spawned hecate process. Caller must kill() this when the app exits.
    pub child: std::process::Child,
}

/// Find the hecate sidecar binary. Resolution order:
///   1. `HECATE_BIN` env var (explicit override).
///   2. Debug build: `{repo_root}/hecate[.exe]` (placed by `just build`).
///   3. Release build: next to the running executable (bundled app).
fn resolve_binary() -> Result<PathBuf, String> {
    // 1. Explicit override.
    if let Ok(p) = std::env::var("HECATE_BIN") {
        return resolve_env_binary_path(&p);
    }

    // 2. Debug build: walk up from the Cargo manifest directory to find the
    //    repo root, where `just build` writes the hecate binary.
    #[cfg(debug_assertions)]
    {
        // CARGO_MANIFEST_DIR is tauri/src-tauri; repo root is two levels up.
        let manifest = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
        let repo_root = manifest
            .parent() // tauri/
            .and_then(|p| p.parent()) // repo root
            .map(|p| p.to_path_buf())
            .ok_or_else(|| "cannot determine repo root from CARGO_MANIFEST_DIR".to_string())?;

        for name in sidecar_binary_names("hecate", None) {
            let candidate = repo_root.join(&name);
            if candidate.is_file() {
                return Ok(candidate);
            }
        }
        return Err(format!(
            "hecate binary not found in {repo_root:?}. Run `just build` first."
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
        let names = sidecar_binary_names("hecate", Some(triple));
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

/// Find the bundled llama-server binary the gateway spawns when the
/// operator starts a local model. Resolution mirrors resolve_binary()
/// but for "llama-server":
///
///   1. `HECATE_LLAMA_SERVER_BIN` env var (explicit override).
///   2. Debug build: alongside the hecate binary at the repo root —
///      tauri/scripts/fetch-llama-server.sh stages it there.
///   3. Release build: next to the running executable, triple-suffixed
///      via Tauri's externalBin bundler.
///
/// Returns Ok(None) when the binary cannot be located. The gateway
/// gates the feature on the env var being set, so a missing binary
/// in dev is non-fatal — the local-models endpoints surface 503 and
/// the UI renders "not available in this build". Tauri startup
/// reports the resolved path in the splash diagnostics either way.
pub fn resolve_llama_server_binary() -> Option<PathBuf> {
    if let Ok(p) = std::env::var("HECATE_LLAMA_SERVER_BIN") {
        let path = PathBuf::from(p);
        if path.is_file() && !is_llama_server_placeholder(&path) {
            return Some(path);
        }
    }

    #[cfg(debug_assertions)]
    {
        let manifest = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
        // The dev resolver checks two places, in order:
        //   1. tauri/src-tauri/binaries/llama-server-<triple> — where
        //      scripts/fetch-llama-server.ts stages the binary. This
        //      is the documented dev path; missing it before meant
        //      operators ran the fetch script and still got dormant.
        //   2. <repo_root>/llama-server — historical fallback for
        //      operators who built the binary themselves and dropped
        //      it at the repo root.
        let triple = env!("TARGET");
        let staged_dir = manifest.join("binaries");
        for name in sidecar_binary_names("llama-server", Some(triple)) {
            let candidate = staged_dir.join(&name);
            if candidate.is_file() && !is_llama_server_placeholder(&candidate) {
                return Some(candidate);
            }
        }
        if let Some(repo_root) = manifest.parent().and_then(|p| p.parent()) {
            for name in sidecar_binary_names("llama-server", None) {
                let candidate = repo_root.join(&name);
                if candidate.is_file() && !is_llama_server_placeholder(&candidate) {
                    return Some(candidate);
                }
            }
        }
        return None;
    }

    #[cfg(not(debug_assertions))]
    {
        let exe = match std::env::current_exe() {
            Ok(p) => p,
            Err(_) => return None,
        };
        let dir = match exe.parent() {
            Some(d) => d,
            None => return None,
        };
        let triple = env!("TARGET");
        for name in sidecar_binary_names("llama-server", Some(triple)) {
            let candidate = dir.join(&name);
            if candidate.is_file() && !is_llama_server_placeholder(&candidate) {
                return Some(candidate);
            }
        }
        None
    }
}

/// The Justfile and CI workflow stage an executable shell stub for
/// targets that are not aarch64-apple-darwin so Tauri's externalBin
/// resolution succeeds at build time. That stub MUST NOT be passed to
/// the gateway as if it were a real llama-server — doing so makes the
/// UI advertise local-models as available and the auto-registered
/// provider live, with calls failing only when the operator picks a
/// model. Detect the sentinel comment in the stub's first 512 bytes
/// and treat the file as missing for runtime purposes.
fn is_llama_server_placeholder(path: &Path) -> bool {
    use std::io::Read;
    let mut file = match std::fs::File::open(path) {
        Ok(f) => f,
        Err(_) => return false, // Can't read → leave the is_file() check to decide.
    };
    let mut buf = [0u8; 512];
    let n = match file.read(&mut buf) {
        Ok(n) => n,
        Err(_) => return false,
    };
    let head = &buf[..n];
    // Real llama-server is a native Mach-O / ELF / PE binary; the
    // shell-script sentinel is plain text and starts with "#!/".
    // Reject only files that contain the sentinel — preserves
    // operator-provided non-elf binaries (unlikely but cheap).
    twoway_search(head, b"hecate-llama-server-placeholder")
}

fn twoway_search(haystack: &[u8], needle: &[u8]) -> bool {
    if needle.is_empty() || haystack.len() < needle.len() {
        return false;
    }
    haystack.windows(needle.len()).any(|w| w == needle)
}

fn sidecar_binary_names(base: &str, triple: Option<&str>) -> Vec<String> {
    let suffix = std::env::consts::EXE_SUFFIX;
    let mut names = Vec::new();
    if let Some(triple) = triple {
        names.push(format!("{base}-{triple}{suffix}"));
        if !suffix.is_empty() {
            names.push(format!("{base}-{triple}"));
        }
    }
    names.push(format!("{base}{suffix}"));
    if !suffix.is_empty() {
        names.push(base.to_string());
    }
    names
}

fn resolve_env_binary_path(value: &str) -> Result<PathBuf, String> {
    let path = PathBuf::from(value);
    if path.is_file() {
        return Ok(path);
    }
    Err(format!(
        "HECATE_BIN={value} does not point to an existing file"
    ))
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
    let paths = diagnostic_paths(app)?;
    std::fs::create_dir_all(&paths.data_dir)
        .map_err(|e| format!("cannot create data directory {:?}: {e}", paths.data_dir))?;
    Ok(paths)
}

/// Resolve paths for diagnostics without creating the data directory.
/// This lets the Tauri setup path install the splash and native menu first;
/// if directory creation fails, the async sidecar startup reports that failure
/// through the splash instead of aborting app initialization.
pub fn diagnostic_paths(app: &AppHandle) -> Result<GatewayPaths, String> {
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("cannot resolve app data directory: {e}"))?;
    Ok(paths_for_data_dir(dir))
}

fn paths_for_data_dir(dir: PathBuf) -> GatewayPaths {
    GatewayPaths {
        log_path: dir.join("gateway.log"),
        state_path: dir.join("hecate.runtime.json"),
        data_dir: dir,
    }
}

pub fn remove_gateway_state(path: &std::path::Path) {
    match std::fs::remove_file(path) {
        Ok(()) => {}
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => {}
        Err(err) => log::warn!(
            "failed to remove hecate runtime state {}: {err}",
            path.display()
        ),
    }
}

/// Spawn the hecate binary and block (async) until `/healthz` responds 200
/// or the deadline expires. Returns the gateway base URL on success.
pub async fn spawn_and_wait(app: &AppHandle) -> Result<GatewayHandle, String> {
    let bin = resolve_binary()?;
    let paths = resolve_paths(app)?;
    let port = free_port()?;
    let addr = format!("127.0.0.1:{port}");
    let base_url = format!("http://{addr}");
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(2))
        .build()
        .map_err(|e| format!("failed to build HTTP client: {e}"))?;

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
    let mut cmd = std::process::Command::new(&bin);
    cmd.env("GATEWAY_ADDRESS", &addr)
        .env("GATEWAY_PUBLIC_URL", &base_url)
        .env("GATEWAY_DATA_DIR", &paths.data_dir)
        // Suppress inherited terminal so the gateway doesn't fight the Tauri
        // process for stdin/stdout in dev mode.
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::from(stderr_log));

    // Local-models feature wiring. The gateway gates the feature on
    // HECATE_LLAMA_SERVER_BIN — when set + executable, the local-models
    // API routes mount and the auto-registered llamacpp provider gets
    // created. Resolve the bundled binary now so the gateway doesn't
    // have to know about Tauri's bundling layout.
    if let Some(llama_bin) = resolve_llama_server_binary() {
        cmd.env("HECATE_LLAMA_SERVER_BIN", &llama_bin)
            .env("HECATE_LOCAL_MODELS", "on");
    }

    let mut child = cmd
        .spawn()
        .map_err(|e| format!("failed to spawn {bin:?}: {e}"))?;

    // Poll /healthz. Hard deadline: 30 s.
    let deadline = Instant::now() + Duration::from_secs(30);
    let healthz = format!("{base_url}/healthz");
    loop {
        if Instant::now() >= deadline {
            let _ = child.kill();
            let _ = child.wait();
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

#[cfg(test)]
mod tests {
    use super::{free_port, paths_for_data_dir, resolve_env_binary_path, sidecar_binary_names};
    use std::fs;
    use std::net::TcpListener;
    use std::path::PathBuf;
    use std::time::{SystemTime, UNIX_EPOCH};

    fn temp_path(name: &str) -> PathBuf {
        let suffix = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("system time should be after unix epoch")
            .as_nanos();
        std::env::temp_dir().join(format!(
            "hecate-sidecar-test-{}-{suffix}-{name}",
            std::process::id()
        ))
    }

    #[test]
    fn test_sidecar_resolve_env_binary_path_accepts_existing_file() {
        let path = temp_path("hecate");
        fs::write(&path, b"test hecate").expect("write temp hecate");

        let resolved = resolve_env_binary_path(path.to_str().expect("utf8 path"))
            .expect("existing file should resolve");

        assert_eq!(resolved, path);
        let _ = fs::remove_file(resolved);
    }

    #[test]
    fn test_sidecar_resolve_env_binary_path_rejects_missing_file() {
        let path = temp_path("missing-hecate");

        let err = resolve_env_binary_path(path.to_str().expect("utf8 path"))
            .expect_err("missing file should fail");

        assert!(err.contains("HECATE_BIN="));
        assert!(err.contains("does not point to an existing file"));
    }

    #[test]
    fn test_sidecar_paths_for_data_dir_adds_gateway_log() {
        let data_dir = temp_path("data-dir");

        let paths = paths_for_data_dir(data_dir.clone());

        assert_eq!(paths.data_dir, data_dir);
        assert_eq!(paths.log_path, paths.data_dir.join("gateway.log"));
        assert_eq!(paths.state_path, paths.data_dir.join("hecate.runtime.json"));
    }

    #[test]
    fn test_sidecar_binary_names_include_platform_executable_suffix() {
        let names = sidecar_binary_names("hecate", Some("x86_64-pc-windows-msvc"));
        let expected_primary = format!(
            "hecate-x86_64-pc-windows-msvc{}",
            std::env::consts::EXE_SUFFIX
        );
        let expected_plain = format!("hecate{}", std::env::consts::EXE_SUFFIX);

        assert_eq!(names.first(), Some(&expected_primary));
        assert!(names.contains(&expected_plain));
    }

    #[test]
    fn test_sidecar_free_port_returns_bindable_loopback_port() {
        let port = match free_port() {
            Ok(port) => port,
            Err(err) if err.contains("Operation not permitted") => return,
            Err(err) => panic!("free port: {err}"),
        };
        let listener = match TcpListener::bind(("127.0.0.1", port)) {
            Ok(listener) => listener,
            Err(err) if err.kind() == std::io::ErrorKind::PermissionDenied => return,
            Err(err) => panic!("reported free port should be bindable: {err}"),
        };

        drop(listener);
    }
}
