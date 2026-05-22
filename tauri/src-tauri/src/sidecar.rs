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
//   6. Pass HECATE_PUBLIC_URL so hecate.runtime.json advertises the sidecar URL.
//   7. On success return the base URL + Child handle. Caller is responsible
//      for calling child.kill() when the app exits.

use std::net::TcpListener;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};
use std::{fs::File, io::Read, io::Seek, io::SeekFrom};

use tauri::{AppHandle, Manager};

/// Platform paths used by the hecate sidecar and native diagnostics.
#[derive(Clone, Debug)]
pub struct GatewayPaths {
    /// Writable app data directory, resolved through Tauri.
    pub data_dir: PathBuf,
    /// Gateway stderr log captured for the most recent launch.
    pub log_path: PathBuf,
    /// Runtime state written for native diagnostics and future local helpers.
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

fn gateway_log_tail(path: &Path) -> Option<String> {
    const MAX_TAIL_BYTES: usize = 8192;

    let mut file = File::open(path).ok()?;
    let len = file.metadata().ok()?.len();
    if len == 0 {
        return None;
    }
    let tail_len = len.min(MAX_TAIL_BYTES as u64);
    file.seek(SeekFrom::End(-(tail_len as i64))).ok()?;
    let mut data = Vec::with_capacity(tail_len as usize);
    file.take(tail_len).read_to_end(&mut data).ok()?;
    if data.is_empty() {
        return None;
    }
    let tail = String::from_utf8_lossy(&data).trim().to_string();
    if tail.is_empty() {
        None
    } else {
        Some(tail)
    }
}

fn gateway_log_last_line(log: &str) -> Option<&str> {
    log.lines()
        .rev()
        .map(str::trim)
        .find(|line| !line.is_empty())
}

fn classify_gateway_log(log: &str) -> Option<&'static str> {
    let lower = log.to_ascii_lowercase();
    if lower.contains("secure bootstrap file")
        || lower.contains("set bootstrap file permissions to 0600")
        || lower.contains("control-plane secret key")
    {
        return Some(
            "Bootstrap key setup failed. Hecate requires hecate.bootstrap.json to contain a valid 32-byte base64 key and use private file permissions.",
        );
    }
    None
}

fn startup_failure_details(log_path: &Path) -> String {
    let Some(log) = gateway_log_tail(log_path) else {
        return format!("See {log_path:?} for sidecar stderr.");
    };

    let mut details = String::new();
    if let Some(classification) = classify_gateway_log(&log) {
        details.push_str(classification);
        details.push(' ');
    }
    if let Some(line) = gateway_log_last_line(&log) {
        details.push_str("Last gateway log line: ");
        details.push_str(line);
        details.push(' ');
    }
    details.push_str(&format!("See {log_path:?} for sidecar stderr."));
    details
}

/// Spawn the hecate binary and block (async) until `/healthz` responds 200
/// or the deadline expires. Returns the gateway base URL on success.
pub async fn spawn_and_wait(app: &AppHandle) -> Result<GatewayHandle, String> {
    let bin = resolve_binary()?;
    let paths = resolve_paths(app)?;
    let port = free_port()?;
    let addr = format!("127.0.0.1:{port}");
    let base_url = format!("http://{addr}");
    log::info!(
        "starting gateway sidecar bin={} addr={} data_dir={} gateway_log={}",
        bin.display(),
        addr,
        paths.data_dir.display(),
        paths.log_path.display()
    );
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
    let mut child = std::process::Command::new(&bin)
        .env("HECATE_ADDRESS", &addr)
        .env("HECATE_PUBLIC_URL", &base_url)
        .env("HECATE_DATA_DIR", &paths.data_dir)
        // Suppress inherited terminal so the gateway doesn't fight the Tauri
        // process for stdin/stdout in dev mode.
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::from(stderr_log))
        .spawn()
        .map_err(|e| format!("failed to spawn {bin:?}: {e}"))?;
    let child_pid = child.id();
    log::info!(
        "gateway sidecar spawned pid={} base_url={}",
        child_pid,
        base_url
    );

    // Poll /healthz. Hard deadline: 30 s.
    let started = Instant::now();
    let deadline = started + Duration::from_secs(30);
    let healthz = format!("{base_url}/healthz");
    loop {
        if Instant::now() >= deadline {
            log::warn!(
                "gateway sidecar health check timed out pid={} base_url={} startup_ms={}",
                child_pid,
                base_url,
                started.elapsed().as_millis()
            );
            let _ = child.kill();
            let _ = child.wait();
            return Err(format!(
                "gateway did not become healthy within 30 s (checked {healthz}). {}",
                startup_failure_details(&paths.log_path)
            ));
        }
        if let Ok(Some(status)) = child.try_wait() {
            log::warn!(
                "gateway sidecar exited before healthy pid={} status={} startup_ms={}",
                child_pid,
                status,
                started.elapsed().as_millis()
            );
            return Err(format!(
                "gateway exited before becoming healthy ({status}). {}",
                startup_failure_details(&paths.log_path)
            ));
        }
        match client.get(&healthz).send().await {
            Ok(resp) if resp.status().is_success() => {
                log::info!(
                    "gateway sidecar healthy pid={} base_url={} startup_ms={}",
                    child_pid,
                    base_url,
                    started.elapsed().as_millis()
                );
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
    use super::{
        free_port, paths_for_data_dir, resolve_env_binary_path, sidecar_binary_names,
        startup_failure_details,
    };
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
    fn test_sidecar_startup_failure_details_classifies_bootstrap_log() {
        let path = temp_path("gateway.log");
        fs::write(
            &path,
            "time=2026-05-18T00:00:00Z level=ERROR msg=\"bootstrap secret init failed\" path=/tmp/hecate.bootstrap.json hint=\"Hecate requires hecate.bootstrap.json to contain a valid 32-byte base64 key and use private file permissions\" error=\"secure bootstrap file: permission denied\"\n",
        )
        .expect("write gateway log fixture");

        let details = startup_failure_details(&path);

        assert!(details.contains("Bootstrap key setup failed"));
        assert!(details.contains("private file permissions"));
        assert!(details.contains("Last gateway log line"));

        let _ = fs::remove_file(path);
    }

    #[test]
    fn test_sidecar_startup_failure_details_keeps_generic_save_errors_unclassified() {
        let path = temp_path("gateway.log");
        fs::write(
            &path,
            "time=2026-05-18T00:00:00Z level=ERROR msg=\"bootstrap secret init failed\" path=/tmp/hecate.bootstrap.json error=\"persist bootstrap file: no space left on device\"\n",
        )
        .expect("write gateway log fixture");

        let details = startup_failure_details(&path);

        assert!(!details.contains("Bootstrap key setup failed"));
        assert!(details.contains("Last gateway log line"));
        assert!(details.contains("no space left on device"));

        let _ = fs::remove_file(path);
    }

    #[test]
    fn test_sidecar_startup_failure_details_reads_bounded_log_tail() {
        let path = temp_path("large-gateway.log");
        let mut data = vec![b'a'; 20 * 1024];
        data.extend_from_slice(b"\nsmall final line\n");
        fs::write(&path, data).expect("write large gateway log fixture");

        let details = startup_failure_details(&path);

        assert!(details.contains("small final line"));
        assert!(!details.contains(&"a".repeat(20 * 1024)));

        let _ = fs::remove_file(path);
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
