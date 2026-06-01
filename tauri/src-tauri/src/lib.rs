// lib.rs — Hecate desktop app (Tauri 2.x)
//
// Architecture:
//   The app bundles the hecate binary as a companion process. On launch:
//   1. The main window (defined in tauri.conf.json) loads the splash page
//      (../splash/index.html via frontendDist) while the gateway boots.
//   2. sidecar::spawn_and_wait() resolves the data dir, finds a free loopback
//      port, spawns hecate, and polls /healthz every 250 ms (30 s deadline).
//   3. On success the window navigates to http://127.0.0.1:{port}/ — the gateway
//      serves the full Hecate UI from its embedded ui/dist bundle, and its own
//      bootstrap-token probe auto-logs the user in (no manual token paste).
//   4. The Child handle is stored in Tauri managed state. When the window
//      closes, the RunEvent::Exit handler kills hecate before the process exits.

mod sidecar;

use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};
use tauri::menu::{AboutMetadataBuilder, MenuBuilder, SubmenuBuilder};
use tauri::{AppHandle, Emitter, Manager, RunEvent, WindowEvent};
use tauri_plugin_clipboard_manager::ClipboardExt;
use tauri_plugin_dialog::{DialogExt, MessageDialogButtons, MessageDialogKind};
use tauri_plugin_log::{RotationStrategy, Target, TargetKind, TimezoneStrategy};

/// File name (without extension) used by tauri-plugin-log under the
/// platform log directory. The resolved path is
/// `<app_log_dir>/<APP_LOG_FILE>.log`. Kept as a constant so the
/// menu handler that opens the file uses the same name the plugin
/// writes to.
const APP_LOG_FILE: &str = "app";

/// JS-invocable command: surface or clear the dock / taskbar
/// "update available" badge. useDesktopUpdate calls this when its
/// state transitions in or out of "update detected." macOS gets a
/// "•" label (matches the rest of the system's notification dot
/// style); other platforms get a count of 1 since they don't
/// support a custom label.
#[tauri::command]
fn set_update_badge(window: tauri::WebviewWindow, visible: bool) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    let result = {
        let label = if visible {
            Some("•".to_string())
        } else {
            None
        };
        window.set_badge_label(label).map_err(|e| e.to_string())
    };
    #[cfg(not(target_os = "macos"))]
    let result = {
        let count = if visible { Some(1i64) } else { None };
        window.set_badge_count(count).map_err(|e| e.to_string())
    };
    if let Err(ref err) = result {
        log::warn!("set update badge failed visible={visible}: {err}");
    }
    result
}

const MIN_SPLASH_DURATION: Duration = Duration::from_secs(2);

/// Tauri managed state: the hecate child process.
/// Wrapped in Mutex<Option<…>> so the exit handler can take() it exactly once.
struct GatewayChild(Mutex<Option<std::process::Child>>);

/// Tauri managed state: the gateway base URL (e.g. http://127.0.0.1:54321).
/// Stored after spawn_and_wait succeeds so the close-window handler can
/// reach /hecate/v1/system/stats (to count running tasks for the
/// confirmation prompt) and /hecate/v1/system/shutdown (to trigger a
/// graceful drain instead of SIGKILL'ing the child). Empty until the
/// gateway is healthy.
struct GatewayBaseURL(Mutex<Option<String>>);

impl GatewayBaseURL {
    fn snapshot(&self) -> Option<String> {
        self.0.lock().ok().and_then(|guard| guard.clone())
    }
}

/// Tauri managed state: filesystem paths surfaced by native diagnostics.
struct GatewayDiagnostics {
    data_dir: PathBuf,
    log_path: PathBuf,
    state_path: PathBuf,
}

/// Re-entry guard for `handle_quit_request`. Set to true while a drain
/// task is in flight (between the first user trigger and the dialog
/// dismissal / drain completion). A second cmd+Q or red-X while the
/// dialog is up still hits ExitRequested / CloseRequested, but the
/// no-op early return here keeps us from starting a second drain.
static QUIT_IN_PROGRESS: AtomicBool = AtomicBool::new(false);

/// Set to true only by `handle_quit_request` immediately before its
/// terminating `app.exit(0)` call. Lets `RunEvent::ExitRequested`
/// distinguish "our own programmatic exit, let it through" from "user
/// is trying to bypass the drain by pressing cmd+Q again" — the latter
/// must be intercepted even while QUIT_IN_PROGRESS is set, otherwise
/// the second ExitRequested would close the app mid-dialog or
/// mid-drain.
static INTENTIONAL_EXIT: AtomicBool = AtomicBool::new(false);

/// How long to wait for the gateway to acknowledge a drain before
/// falling back to SIGKILL'ing the child. Generous because the drain
/// itself has a 10s budget on the Go side (see cmd/hecate/main.go's
/// `context.WithTimeout(..., 10*time.Second)`); a hair beyond that
/// covers HTTP round-trip jitter without leaving zombies.
const HECATE_DRAIN_DEADLINE: Duration = Duration::from_secs(12);

/// How long to wait between /healthz polls while draining. Short
/// enough that a 1-2 second clean drain feels responsive; long enough
/// not to pin a CPU.
const HECATE_DRAIN_POLL_INTERVAL: Duration = Duration::from_millis(200);

fn build_diagnostics_report(app: &tauri::AppHandle) -> String {
    let app_log = app
        .path()
        .app_log_dir()
        .map(|d| d.join(format!("{APP_LOG_FILE}.log")).display().to_string())
        .unwrap_or_else(|_| "<unavailable>".to_string());
    let (gateway_log, data_dir) = if let Some(paths) = app.try_state::<GatewayDiagnostics>() {
        (
            paths.log_path.display().to_string(),
            paths.data_dir.display().to_string(),
        )
    } else {
        ("<unavailable>".to_string(), "<unavailable>".to_string())
    };
    format!(
        "Hecate diagnostics\n\
         ------------------\n\
         App version:   {version}\n\
         OS / arch:     {os} / {arch}\n\
         App log:       {app_log}\n\
         Gateway log:   {gateway_log}\n\
         Data dir:      {data_dir}\n",
        version = env!("CARGO_PKG_VERSION"),
        os = std::env::consts::OS,
        arch = std::env::consts::ARCH,
    )
}

fn startup_failure_hint(message: &str) -> Option<&'static str> {
    let lower = message.to_ascii_lowercase();
    if lower.contains("bootstrap key setup failed")
        || lower.contains("secure bootstrap file")
        || lower.contains("set bootstrap file permissions to 0600")
        || lower.contains("control-plane secret key")
    {
        return Some(
            "Open the data directory, check hecate.bootstrap.json, and fix ownership, ACLs, or POSIX mode bits so the bootstrap key is private. If HECATE_CONTROL_PLANE_SECRET_KEY is set, it must be base64 for exactly 32 bytes.",
        );
    }
    None
}

fn open_path(path: &Path) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    let mut command = {
        let mut command = std::process::Command::new("open");
        command.arg(path);
        command
    };

    #[cfg(target_os = "windows")]
    let mut command = {
        let mut command = std::process::Command::new("explorer");
        command.arg(path);
        command
    };

    #[cfg(all(unix, not(target_os = "macos")))]
    let mut command = {
        let mut command = std::process::Command::new("xdg-open");
        command.arg(path);
        command
    };

    command
        .spawn()
        .map(|_| ())
        .map_err(|e| format!("failed to open {}: {e}", path.display()))
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum WorkspaceOpenTarget {
    VSCode,
    VSCodeInsiders,
    Cursor,
    Zed,
    Finder,
    Terminal,
    ITerm2,
    Xcode,
}

impl WorkspaceOpenTarget {
    fn from_id(id: &str) -> Option<Self> {
        match id.trim() {
            "vscode" => Some(Self::VSCode),
            "vscode_insiders" => Some(Self::VSCodeInsiders),
            "cursor" => Some(Self::Cursor),
            "zed" => Some(Self::Zed),
            "finder" => Some(Self::Finder),
            "terminal" => Some(Self::Terminal),
            "iterm2" => Some(Self::ITerm2),
            "xcode" => Some(Self::Xcode),
            _ => None,
        }
    }
}

fn validate_workspace_open_request(
    path: &str,
    target: &str,
) -> Result<(PathBuf, WorkspaceOpenTarget), String> {
    let target = WorkspaceOpenTarget::from_id(target)
        .ok_or_else(|| format!("unknown workspace open target {target:?}"))?;
    let path = path.trim();
    if path.is_empty() {
        return Err("workspace path is required".to_string());
    }
    let path = PathBuf::from(path);
    if !path.is_absolute() {
        return Err("workspace path must be absolute".to_string());
    }
    let metadata = std::fs::metadata(&path)
        .map_err(|e| format!("workspace {} is not accessible: {e}", path.display()))?;
    if !metadata.is_dir() {
        return Err(format!("workspace {} is not a directory", path.display()));
    }
    let path = path
        .canonicalize()
        .map_err(|e| format!("resolve workspace {}: {e}", path.display()))?;
    Ok((path, target))
}

fn spawn_workspace_command(mut command: std::process::Command, label: &str) -> Result<(), String> {
    command
        .spawn()
        .map(|_| ())
        .map_err(|e| format!("failed to open workspace in {label}: {e}"))
}

#[cfg(target_os = "macos")]
fn open_workspace_with_app(path: &Path, app_name: &str) -> Result<(), String> {
    let mut command = std::process::Command::new("open");
    command.arg("-a").arg(app_name).arg(path);
    spawn_workspace_command(command, app_name)
}

#[cfg(not(target_os = "macos"))]
fn open_workspace_with_cli(path: &Path, command_name: &str) -> Result<(), String> {
    let mut command = std::process::Command::new(command_name);
    command.arg(path);
    spawn_workspace_command(command, command_name)
}

fn open_workspace_terminal(path: &Path) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    {
        return open_workspace_with_app(path, "Terminal");
    }

    #[cfg(target_os = "windows")]
    {
        let mut command = std::process::Command::new("cmd");
        command
            .arg("/C")
            .arg("start")
            .arg("")
            .arg("wt")
            .arg("-d")
            .arg(path);
        return spawn_workspace_command(command, "Terminal");
    }

    #[cfg(all(unix, not(target_os = "macos")))]
    {
        let attempts: [(&str, &[&str]); 4] = [
            ("x-terminal-emulator", &["--working-directory"]),
            ("gnome-terminal", &["--working-directory"]),
            ("konsole", &["--workdir"]),
            ("xfce4-terminal", &["--working-directory"]),
        ];
        let mut last_err = None;
        for (program, args) in attempts {
            let mut command = std::process::Command::new(program);
            command.args(args).arg(path);
            match command.spawn() {
                Ok(_) => return Ok(()),
                Err(err) => last_err = Some(err),
            }
        }
        return Err(format!(
            "failed to open workspace in Terminal: {}",
            last_err
                .map(|e| e.to_string())
                .unwrap_or_else(|| "no terminal command configured".to_string())
        ));
    }
}

fn open_workspace_target_path(path: &Path, target: WorkspaceOpenTarget) -> Result<(), String> {
    match target {
        WorkspaceOpenTarget::Finder => open_path(path),
        WorkspaceOpenTarget::Terminal => open_workspace_terminal(path),
        WorkspaceOpenTarget::ITerm2 => {
            #[cfg(target_os = "macos")]
            {
                open_workspace_with_app(path, "iTerm")
            }
            #[cfg(not(target_os = "macos"))]
            {
                Err("iTerm2 is only available on macOS".to_string())
            }
        }
        WorkspaceOpenTarget::Xcode => {
            #[cfg(target_os = "macos")]
            {
                open_workspace_with_app(path, "Xcode")
            }
            #[cfg(not(target_os = "macos"))]
            {
                Err("Xcode is only available on macOS".to_string())
            }
        }
        WorkspaceOpenTarget::VSCode => {
            #[cfg(target_os = "macos")]
            {
                open_workspace_with_app(path, "Visual Studio Code")
            }
            #[cfg(not(target_os = "macos"))]
            {
                open_workspace_with_cli(path, "code")
            }
        }
        WorkspaceOpenTarget::VSCodeInsiders => {
            #[cfg(target_os = "macos")]
            {
                open_workspace_with_app(path, "Visual Studio Code - Insiders")
            }
            #[cfg(not(target_os = "macos"))]
            {
                open_workspace_with_cli(path, "code-insiders")
            }
        }
        WorkspaceOpenTarget::Cursor => {
            #[cfg(target_os = "macos")]
            {
                open_workspace_with_app(path, "Cursor")
            }
            #[cfg(not(target_os = "macos"))]
            {
                open_workspace_with_cli(path, "cursor")
            }
        }
        WorkspaceOpenTarget::Zed => {
            #[cfg(target_os = "macos")]
            {
                open_workspace_with_app(path, "Zed")
            }
            #[cfg(not(target_os = "macos"))]
            {
                open_workspace_with_cli(path, "zed")
            }
        }
    }
}

/// JS-invocable command: open the selected workspace in an editor,
/// terminal, or file manager. Exposed only to the desktop UI through
/// Tauri capabilities; browser UI uses the local gateway endpoint instead.
#[tauri::command]
fn open_workspace_target(path: String, target: String) -> Result<(), String> {
    let (path, target) = validate_workspace_open_request(&path, &target)?;
    open_workspace_target_path(&path, target)
}

fn install_menu(app: &mut tauri::App) -> tauri::Result<()> {
    // Standard macOS application menu structure:
    //   App / Edit / View / Window
    // Predefined items carry their canonical keyboard shortcuts
    // (⌘H, ⌥⌘H, ⌘Q, ⌘W, ⌘M, ⌃⌘F, Cut/Copy/Paste, …) and route to
    // native handlers, so we only need on_menu_event for the
    // custom items below ("check-for-updates", "open-app-log",
    // "open-gateway-log", "open-data-directory", "copy-diagnostics").
    let about_metadata = AboutMetadataBuilder::new()
        .name(Some("Hecate"))
        .version(Some(env!("CARGO_PKG_VERSION")))
        .website(Some("https://hecate.sh"))
        .website_label(Some("hecate.sh"))
        .build();
    let app_menu = SubmenuBuilder::new(app, "Hecate")
        .about(Some(about_metadata))
        .separator()
        .text("check-for-updates", "Check for Updates\u{2026}")
        .separator()
        .services()
        .separator()
        .hide()
        .hide_others()
        .show_all()
        .separator()
        .text("open-app-log", "Open App Log")
        .text("open-gateway-log", "Open Gateway Log")
        .text("open-data-directory", "Open Data Directory")
        .text("copy-diagnostics", "Copy Diagnostics")
        .separator()
        .quit()
        .build()?;
    let edit = SubmenuBuilder::new(app, "Edit")
        .undo()
        .redo()
        .separator()
        .cut()
        .copy()
        .paste()
        .select_all()
        .build()?;
    let view = SubmenuBuilder::new(app, "View").fullscreen().build()?;
    let window = SubmenuBuilder::new(app, "Window")
        .minimize()
        .maximize()
        .separator()
        .close_window()
        .bring_all_to_front()
        .build()?;
    let menu = MenuBuilder::new(app)
        .item(&app_menu)
        .item(&edit)
        .item(&view)
        .item(&window)
        .build()?;
    app.set_menu(menu).map(|_| ())
}

fn remaining_splash_delay(elapsed: Duration) -> Option<Duration> {
    if elapsed >= MIN_SPLASH_DURATION {
        return None;
    }
    Some(MIN_SPLASH_DURATION - elapsed)
}

/// Extracts data.running_runs from a /hecate/v1/system/stats response
/// body. Any deviation — missing field, wrong type, malformed JSON —
/// returns 0 (the safe default when the gateway is already misbehaving
/// and there's nothing useful to confirm about). Pure to keep the
/// HTTP-side fetch easy to test indirectly via this seam.
fn parse_running_runs(body: &str) -> u64 {
    let parsed: serde_json::Value = match serde_json::from_str(body) {
        Ok(v) => v,
        Err(_) => return 0,
    };
    parsed
        .get("data")
        .and_then(|d| d.get("running_runs"))
        .and_then(|v| v.as_u64())
        .unwrap_or(0)
}

/// Composes the body of the running-tasks confirmation dialog. Pulled
/// out as a pure function so the singular/plural copy stays under test
/// without spinning up a Tauri runtime to drive the real dialog.
fn format_running_tasks_message(running: u64) -> String {
    let plural = if running == 1 { "" } else { "s" };
    let pronoun = if running == 1 { "it" } else { "them" };
    format!("{running} task{plural} still running. Quitting Hecate will stop {pronoun}.")
}

/// Fetches GET /hecate/v1/system/stats and returns running_runs. Used
/// by the close-window flow to decide whether to prompt before quitting
/// — zero running runs means we can drain silently. Any error (gateway
/// unreachable, stats endpoint unavailable, parse failure) returns 0:
/// from the operator's perspective, if the gateway is already wedged
/// there's nothing useful to confirm about.
async fn fetch_running_runs(base_url: &str) -> u64 {
    let client = match reqwest::Client::builder()
        .timeout(Duration::from_secs(2))
        .build()
    {
        Ok(c) => c,
        Err(e) => {
            log::warn!("fetch_running_runs: build HTTP client failed: {e}");
            return 0;
        }
    };
    let url = format!("{}/hecate/v1/system/stats", base_url.trim_end_matches('/'));
    let response = match client.get(&url).send().await {
        Ok(r) => r,
        Err(e) => {
            log::warn!("fetch_running_runs: GET {url} failed: {e}");
            return 0;
        }
    };
    let status = response.status();
    if !status.is_success() {
        log::warn!("fetch_running_runs: GET {url} returned {status}");
        return 0;
    }
    let text = match response.text().await {
        Ok(s) => s,
        Err(e) => {
            log::warn!("fetch_running_runs: read body from {url} failed: {e}");
            return 0;
        }
    };
    parse_running_runs(&text)
}

/// POSTs /hecate/v1/system/shutdown and waits for the gateway to stop
/// responding to /healthz (proof that the drain completed and the
/// child is exiting). Returns Ok(()) on a clean drain, Err on timeout
/// or HTTP error — caller falls through to SIGKILL via child.kill()
/// in RunEvent::Exit.
async fn drain_gateway(base_url: &str) -> Result<(), String> {
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(2))
        .build()
        .map_err(|e| format!("build http client: {e}"))?;

    let base = base_url.trim_end_matches('/');
    let shutdown_url = format!("{base}/hecate/v1/system/shutdown");
    let response = client
        .post(&shutdown_url)
        .send()
        .await
        .map_err(|e| format!("POST {shutdown_url} failed: {e}"))?;
    let status = response.status();
    if !status.is_success() {
        let body = response.text().await.unwrap_or_default();
        return Err(format!("POST {shutdown_url} returned {status}: {body}"));
    }

    // Poll /healthz until it stops responding — that's our signal the
    // gateway has finished its drain and the process is exiting. The
    // 202 above is just an ack; the actual drain runs async on the Go
    // side.
    let healthz_url = format!("{base}/healthz");
    let deadline = Instant::now() + HECATE_DRAIN_DEADLINE;
    while Instant::now() < deadline {
        tokio::time::sleep(HECATE_DRAIN_POLL_INTERVAL).await;
        match client.get(&healthz_url).send().await {
            Ok(_) => continue,       // still up — keep polling
            Err(_) => return Ok(()), // gateway is gone
        }
    }
    Err(format!(
        "gateway did not exit within {HECATE_DRAIN_DEADLINE:?}"
    ))
}

/// Shows a native blocking-by-callback confirmation dialog asking the
/// operator whether to interrupt running tasks. Returns the user's
/// choice over a oneshot channel so the caller can await it. The
/// dialog runs on Tauri's event loop, not the calling thread, so the
/// async runtime stays unblocked while the OS owns the UI.
async fn confirm_quit_with_running_tasks(app: &AppHandle, running: u64) -> bool {
    let (tx, rx) = tokio::sync::oneshot::channel::<bool>();
    let tx = Arc::new(Mutex::new(Some(tx)));
    let tx_for_dialog = tx.clone();
    app.dialog()
        .message(format_running_tasks_message(running))
        .title("Quit Hecate?")
        .kind(MessageDialogKind::Warning)
        .buttons(MessageDialogButtons::OkCancelCustom(
            "Quit anyway".into(),
            "Keep running".into(),
        ))
        .show(move |confirmed| {
            if let Ok(mut slot) = tx_for_dialog.lock() {
                if let Some(sender) = slot.take() {
                    let _ = sender.send(confirmed);
                }
            }
        });
    rx.await.unwrap_or(false)
}

/// Funnels every "user wants to quit" trigger (red-X close, cmd+Q,
/// menu Quit, etc.) through the same confirmation + graceful-drain
/// path. Idempotent against re-entry via QUIT_IN_PROGRESS — app.exit()
/// fires RunEvent::ExitRequested again on its way out, and we'd
/// otherwise re-prompt.
fn handle_quit_request(app: AppHandle) {
    if QUIT_IN_PROGRESS.swap(true, Ordering::SeqCst) {
        log::info!("quit request ignored because graceful shutdown is already in progress");
        return;
    }
    tauri::async_runtime::spawn(async move {
        let base_url = app.try_state::<GatewayBaseURL>().and_then(|s| s.snapshot());
        log::info!(
            "quit requested app_pid={} gateway_ready={}",
            std::process::id(),
            base_url.is_some()
        );
        if let Some(ref base) = base_url {
            let running = fetch_running_runs(base).await;
            log::info!("gateway running task count before quit running_runs={running}");
            if running > 0 {
                let confirmed = confirm_quit_with_running_tasks(&app, running).await;
                if !confirmed {
                    log::info!("quit cancelled by operator; keeping gateway running");
                    QUIT_IN_PROGRESS.store(false, Ordering::SeqCst);
                    return;
                }
                log::info!("operator confirmed quit with running tasks; draining gateway");
            }
            match drain_gateway(base).await {
                Ok(()) => log::info!("gateway drain completed before app exit"),
                Err(e) => {
                    // Non-fatal: RunEvent::Exit's child.kill() is the
                    // fallback. Log so operators have a breadcrumb.
                    log::warn!("graceful gateway drain failed, falling back to child.kill(): {e}");
                }
            }
        } else {
            log::debug!("quit requested before gateway base URL was ready; exiting without drain");
        }
        // Flag the next ExitRequested as our own programmatic exit so
        // the run-event handler lets it through instead of intercepting
        // and re-spawning this same task.
        INTENTIONAL_EXIT.store(true, Ordering::SeqCst);
        app.exit(0);
    });
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        // tauri-plugin-log must be registered first so subsequent
        // plugin initialization can produce log lines via the
        // `log` facade. Targets:
        //  - Stderr: keeps existing dev-mode visibility (Tauri's
        //    own console output now flows through the same sink
        //    instead of bare eprintln!).
        //  - LogDir: rotating file at
        //    <app_log_dir>/app.log (macOS:
        //    ~/Library/Logs/sh.hecate.app/, Windows:
        //    %LOCALAPPDATA%\sh.hecate.app\logs\, Linux:
        //    $XDG_DATA_HOME/sh.hecate.app/logs). 5 MB cap, keep
        //    five rotated files — enough to survive a crash loop
        //    without bloating disk.
        //  - Webview: routes webview console.* calls and JS
        //    `@tauri-apps/plugin-log` calls into the same sink so
        //    the file captures both layers.
        .plugin(
            tauri_plugin_log::Builder::new()
                .level(log::LevelFilter::Info)
                .targets([
                    Target::new(TargetKind::Stderr),
                    Target::new(TargetKind::LogDir {
                        file_name: Some(APP_LOG_FILE.into()),
                    }),
                    Target::new(TargetKind::Webview),
                ])
                .max_file_size(5 * 1024 * 1024)
                .rotation_strategy(RotationStrategy::KeepSome(5))
                .timezone_strategy(TimezoneStrategy::UseLocal)
                .build(),
        )
        .plugin(tauri_plugin_clipboard_manager::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_window_state::Builder::default().build())
        .invoke_handler(tauri::generate_handler![
            set_update_badge,
            open_workspace_target
        ])
        .on_menu_event(|app, event| match event.id().as_ref() {
            "check-for-updates" => {
                // The actual check lives in the renderer (useDesktopUpdate
                // owns the @tauri-apps/plugin-updater client and the
                // banner state machine). Emit an event the webview can
                // hook into; the React side handles the rest.
                if let Some(win) = app.get_webview_window("main") {
                    if let Err(e) = win.show() {
                        log::warn!("show main window for update check failed: {e}");
                    }
                    if let Err(e) = win.set_focus() {
                        log::warn!("focus main window for update check failed: {e}");
                    }
                } else {
                    log::warn!("check-for-updates requested but main window was not found");
                }
                if let Err(e) = app.emit("hecate:check-for-updates", ()) {
                    log::warn!("emit hecate:check-for-updates failed: {e}");
                }
            }
            "open-app-log" => {
                // tauri-plugin-log writes to
                // <app_log_dir>/<APP_LOG_FILE>.log; resolve the
                // same path so the menu item points at the file
                // the plugin is actually rotating.
                match app.path().app_log_dir() {
                    Ok(dir) => {
                        let log_path = dir.join(format!("{APP_LOG_FILE}.log"));
                        if !log_path.exists() {
                            if let Err(e) = std::fs::create_dir_all(&dir) {
                                log::warn!("create app log dir {} failed: {e}", dir.display());
                            }
                            if let Err(e) = std::fs::write(
                                &log_path,
                                "App log has not accumulated any entries yet.\n",
                            ) {
                                log::warn!("write app log placeholder {} failed: {e}", log_path.display());
                            }
                        }
                        if let Err(e) = open_path(&log_path) {
                            log::warn!("open app log failed: {e}");
                        }
                    }
                    Err(e) => log::warn!("resolve app log dir failed: {e}"),
                }
            }
            "open-gateway-log" => {
                if let Some(paths) = app.try_state::<GatewayDiagnostics>() {
                    if !paths.log_path.exists() {
                        if let Err(e) = std::fs::write(
                            &paths.log_path,
                            "Gateway log has not been created yet.\n",
                        ) {
                            log::warn!(
                                "write gateway log placeholder {} failed: {e}",
                                paths.log_path.display()
                            );
                        }
                    }
                    if let Err(e) = open_path(&paths.log_path) {
                        log::warn!("open gateway log failed: {e}");
                    }
                }
            }
            "open-data-directory" => {
                if let Some(paths) = app.try_state::<GatewayDiagnostics>() {
                    if let Err(e) = open_path(&paths.data_dir) {
                        log::warn!("open data dir failed: {e}");
                    }
                }
            }
            "copy-diagnostics" => {
                // Bundle app + gateway log paths, version, OS, and
                // arch into one block so a bug report becomes a
                // single Cmd+V. Doesn't read the log contents —
                // that's intentional (logs can be huge, and the
                // user often wants to inspect first before pasting).
                let report = build_diagnostics_report(app);
                if let Err(e) = app.clipboard().write_text(report) {
                    log::warn!("copy diagnostics to clipboard failed: {e}");
                } else {
                    log::info!("diagnostics report copied to clipboard");
                }
            }
            _ => {}
        })
        .setup(|app| {
            install_menu(app)?;

            let diagnostics = sidecar::diagnostic_paths(app.handle())
                .map_err(|e| std::io::Error::new(std::io::ErrorKind::Other, e))?;
            log::info!(
                "hecate desktop starting version={} app_pid={} data_dir={} gateway_log={}",
                env!("CARGO_PKG_VERSION"),
                std::process::id(),
                diagnostics.data_dir.display(),
                diagnostics.log_path.display()
            );

            // The main window is created by tauri.conf.json and starts on the
            // splash page (frontendDist). Grab a handle to it so we can
            // navigate once the gateway is healthy.
            let win = app
                .get_webview_window("main")
                .expect("main window defined in tauri.conf.json");

            // Intercept the close button so red-X also quits (on macOS the
            // default leaves the app dock-resident with no window), and so
            // running tasks get a confirmation prompt + a graceful drain
            // instead of an instant SIGKILL of the gateway child. The
            // ExitRequested handler in .run() below covers cmd+Q and the
            // menu Quit item; both funnel through handle_quit_request.
            let close_app_handle = app.handle().clone();
            win.on_window_event(move |event| {
                if let WindowEvent::CloseRequested { api, .. } = event {
                    api.prevent_close();
                    handle_quit_request(close_app_handle.clone());
                }
            });

            // Seed managed state with an empty child slot. The background
            // task fills it once hecate is spawned.
            app.manage(GatewayChild(Mutex::new(None)));
            // Seed an empty base URL slot. Filled by the spawn task once
            // /healthz returns 200; read by handle_quit_request to reach
            // /system/stats and /system/shutdown.
            app.manage(GatewayBaseURL(Mutex::new(None)));
            app.manage(GatewayDiagnostics {
                data_dir: diagnostics.data_dir.clone(),
                log_path: diagnostics.log_path.clone(),
                state_path: diagnostics.state_path.clone(),
            });

            let app_handle = app.handle().clone();
            let splash_started = Instant::now();

            // Spawn the gateway in a background task.
            tauri::async_runtime::spawn(async move {
                match sidecar::spawn_and_wait(&app_handle).await {
                    Ok(handle) => {
                        let sidecar_pid = handle.child.id();
                        let sidecar_port = handle.port;
                        let base_url = handle.base_url.clone();
                        // Store the child so the exit handler can kill it.
                        if let Some(state) = app_handle.try_state::<GatewayChild>() {
                            if let Ok(mut slot) = state.0.lock() {
                                *slot = Some(handle.child);
                            }
                        }
                        // Publish the base URL so the close-window handler
                        // can call /system/stats and /system/shutdown.
                        if let Some(state) = app_handle.try_state::<GatewayBaseURL>() {
                            if let Ok(mut slot) = state.0.lock() {
                                *slot = Some(handle.base_url.clone());
                            }
                        }
                        if let Some(delay) = remaining_splash_delay(splash_started.elapsed()) {
                            tokio::time::sleep(delay).await;
                        }
                        // Navigate to the gateway UI.
                        let url = tauri::Url::parse(&base_url)
                            .expect("gateway base_url is always valid");
                        log::info!(
                            "navigating to gateway base_url={} port={} app_pid={} sidecar_pid={}",
                            base_url,
                            sidecar_port,
                            std::process::id(),
                            sidecar_pid
                        );
                        if let Err(e) = win.navigate(url) {
                            log::error!("navigate to gateway failed: {e}");
                        }
                    }
                    Err(e) => {
                        log::error!("hecate gateway startup failed: {e}");
                        let payload = serde_json::json!({
                            "message": e,
                            "hint": startup_failure_hint(&e),
                            "logPath": diagnostics.log_path.display().to_string(),
                            "dataDir": diagnostics.data_dir.display().to_string(),
                        });
                        let script = format!(
                            "window.__hecateStartupFailureDetails = {payload}; window.__hecateStartupFailed && window.__hecateStartupFailed(window.__hecateStartupFailureDetails);"
                        );
                        if let Err(eval_err) = win.eval(script) {
                            log::warn!("show startup failure details in splash failed: {eval_err}");
                        }
                        if let Err(title_err) = win.set_title("Hecate — startup failed") {
                            log::warn!("set startup failure window title failed: {title_err}");
                        }
                    }
                }
            });

            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error building Hecate app")
        .run(|app_handle, event| match event {
            // cmd+Q, menu Quit, and any other "exit requested" trigger
            // funnels through the same drain path as the red-X button.
            // We prevent the default exit, run the confirmation + drain
            // off the main thread, then call app.exit() ourselves once
            // the gateway has acknowledged. INTENTIONAL_EXIT distinguishes
            // our own programmatic exit (let it through) from the user
            // triggering another quit mid-drain (intercept so the drain
            // doesn't get bypassed and the app doesn't close mid-dialog).
            RunEvent::ExitRequested { api, .. } => {
                if INTENTIONAL_EXIT.load(Ordering::SeqCst) {
                    log::info!("intentional app exit requested; allowing process shutdown");
                    return;
                }
                api.prevent_exit();
                handle_quit_request(app_handle.clone());
            }
            RunEvent::Exit => {
                // Belt-and-suspenders kill in case the graceful drain
                // didn't complete (gateway unreachable when we asked,
                // /system/shutdown returned non-2xx, drain deadline
                // exceeded). Wait briefly so a child that's already
                // exiting on its own gets to finish before we SIGKILL.
                if let Some(state) = app_handle.try_state::<GatewayChild>() {
                    if let Ok(mut slot) = state.0.lock() {
                        if let Some(mut child) = slot.take() {
                            let sidecar_pid = child.id();
                            log::info!("app exiting; waiting for gateway sidecar pid={} to stop", sidecar_pid);
                            let deadline = Instant::now() + Duration::from_secs(2);
                            let mut exited = false;
                            while Instant::now() < deadline {
                                match child.try_wait() {
                                    Ok(Some(_)) => {
                                        exited = true;
                                        break;
                                    }
                                    Ok(None) => {
                                        std::thread::sleep(Duration::from_millis(100));
                                    }
                                    Err(e) => {
                                        log::warn!(
                                            "poll gateway sidecar exit status failed pid={}: {e}",
                                            sidecar_pid
                                        );
                                        break;
                                    }
                                }
                            }
                            if !exited {
                                log::warn!(
                                    "gateway sidecar still running at app exit; killing pid={}",
                                    sidecar_pid
                                );
                                if let Err(e) = child.kill() {
                                    log::warn!("kill gateway sidecar failed pid={}: {e}", sidecar_pid);
                                }
                            } else {
                                log::info!(
                                    "gateway sidecar exited before fallback kill pid={}",
                                    sidecar_pid
                                );
                            }
                        }
                    }
                }
                if let Some(paths) = app_handle.try_state::<GatewayDiagnostics>() {
                    log::info!(
                        "removing gateway runtime state path={}",
                        paths.state_path.display()
                    );
                    sidecar::remove_gateway_state(&paths.state_path);
                }
            }
            _ => {}
        });
}

#[cfg(test)]
mod tests {
    use super::{
        format_running_tasks_message, parse_running_runs, remaining_splash_delay,
        startup_failure_hint, validate_workspace_open_request, WorkspaceOpenTarget,
        MIN_SPLASH_DURATION,
    };
    use std::fs;
    use std::time::Duration;

    #[test]
    fn test_remaining_splash_delay_waits_for_unelapsed_minimum() {
        assert_eq!(
            remaining_splash_delay(Duration::from_millis(200)),
            Some(MIN_SPLASH_DURATION - Duration::from_millis(200)),
        );
    }

    #[test]
    fn test_remaining_splash_delay_returns_none_after_minimum() {
        assert_eq!(remaining_splash_delay(MIN_SPLASH_DURATION), None);
        assert_eq!(
            remaining_splash_delay(MIN_SPLASH_DURATION + Duration::from_millis(1)),
            None,
        );
    }

    #[test]
    fn test_startup_failure_hint_recognizes_bootstrap_failures() {
        let hint = startup_failure_hint(
            "gateway exited before becoming healthy. Bootstrap key setup failed.",
        )
        .expect("bootstrap startup failure should include an operator hint");

        assert!(hint.contains("hecate.bootstrap.json"));
        assert!(hint.contains("private"));
    }

    #[test]
    fn test_startup_failure_hint_ignores_generic_persist_failures() {
        let hint = startup_failure_hint(
            "gateway exited before becoming healthy. Last gateway log line: bootstrap secret init failed error=\"persist bootstrap file: no space left on device\"",
        );

        assert_eq!(hint, None);
    }

    #[test]
    fn test_parse_running_runs_well_formed_body() {
        let body = r#"{"object":"runtime_stats","data":{"running_runs":3,"queue_depth":0}}"#;
        assert_eq!(parse_running_runs(body), 3);
    }

    #[test]
    fn test_parse_running_runs_zero_when_field_missing() {
        // Stats response without the field — caller must not crash; it
        // gets the "no running tasks, skip the prompt" default.
        let body = r#"{"object":"runtime_stats","data":{"queue_depth":0}}"#;
        assert_eq!(parse_running_runs(body), 0);
    }

    #[test]
    fn test_parse_running_runs_zero_when_wrong_type() {
        // Gateway change accidentally returns a string. We don't want to
        // crash — fall through to the no-prompt-needed default.
        let body = r#"{"data":{"running_runs":"three"}}"#;
        assert_eq!(parse_running_runs(body), 0);
    }

    #[test]
    fn test_parse_running_runs_zero_when_malformed_json() {
        // Reverse proxy returning HTML, partial body, etc. — same
        // graceful fallback so a wedged gateway doesn't trap the user
        // in a stale dialog.
        assert_eq!(parse_running_runs("<html>503</html>"), 0);
        assert_eq!(parse_running_runs(""), 0);
    }

    #[test]
    fn test_format_running_tasks_message_singular_uses_singular_pronoun() {
        let message = format_running_tasks_message(1);
        assert!(message.contains("1 task still running"), "got: {message}");
        assert!(message.contains("stop it"), "got: {message}");
    }

    #[test]
    fn test_format_running_tasks_message_plural_uses_plural_pronoun() {
        let message = format_running_tasks_message(5);
        assert!(message.contains("5 tasks still running"), "got: {message}");
        assert!(message.contains("stop them"), "got: {message}");
    }

    #[test]
    fn test_workspace_open_target_from_id() {
        assert_eq!(
            WorkspaceOpenTarget::from_id("vscode"),
            Some(WorkspaceOpenTarget::VSCode)
        );
        assert_eq!(
            WorkspaceOpenTarget::from_id("terminal"),
            Some(WorkspaceOpenTarget::Terminal)
        );
        assert_eq!(WorkspaceOpenTarget::from_id("missing"), None);
    }

    #[test]
    fn test_validate_workspace_open_request_accepts_directory() {
        let dir =
            std::env::temp_dir().join(format!("hecate-open-workspace-test-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).expect("create temp workspace");
        let (path, target) =
            validate_workspace_open_request(&dir.display().to_string(), "terminal")
                .expect("valid workspace");
        assert_eq!(target, WorkspaceOpenTarget::Terminal);
        assert_eq!(path, dir.canonicalize().expect("canonical temp workspace"));
        fs::remove_dir_all(&dir).expect("cleanup temp workspace");
    }

    #[test]
    fn test_validate_workspace_open_request_rejects_files() {
        let path =
            std::env::temp_dir().join(format!("hecate-open-workspace-file-{}", std::process::id()));
        fs::write(&path, "not a workspace").expect("write temp file");
        let err = validate_workspace_open_request(&path.display().to_string(), "finder")
            .expect_err("file should not be accepted as a workspace");
        assert!(err.contains("not a directory"), "got: {err}");
        fs::remove_file(&path).expect("cleanup temp file");
    }
}
