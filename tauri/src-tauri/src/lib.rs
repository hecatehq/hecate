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
use std::sync::Mutex;
use std::time::{Duration, Instant};
use tauri::menu::{AboutMetadataBuilder, MenuBuilder, SubmenuBuilder};
use tauri::{Emitter, Manager};
use tauri_plugin_clipboard_manager::ClipboardExt;
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
    {
        let label = if visible { Some("•".to_string()) } else { None };
        window.set_badge_label(label).map_err(|e| e.to_string())
    }
    #[cfg(not(target_os = "macos"))]
    {
        let count = if visible { Some(1i64) } else { None };
        window.set_badge_count(count).map_err(|e| e.to_string())
    }
}

const MIN_SPLASH_DURATION: Duration = Duration::from_secs(2);

/// Tauri managed state: the hecate child process.
/// Wrapped in Mutex<Option<…>> so the exit handler can take() it exactly once.
struct GatewayChild(Mutex<Option<std::process::Child>>);

/// Tauri managed state: filesystem paths surfaced by native diagnostics.
struct GatewayDiagnostics {
    data_dir: PathBuf,
    log_path: PathBuf,
    state_path: PathBuf,
}

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
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_window_state::Builder::default().build())
        .invoke_handler(tauri::generate_handler![set_update_badge])
        .on_menu_event(|app, event| match event.id().as_ref() {
            "check-for-updates" => {
                // The actual check lives in the renderer (useDesktopUpdate
                // owns the @tauri-apps/plugin-updater client and the
                // banner state machine). Emit an event the webview can
                // hook into; the React side handles the rest.
                if let Some(win) = app.get_webview_window("main") {
                    let _ = win.show();
                    let _ = win.set_focus();
                }
                let _ = app.emit("hecate:check-for-updates", ());
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
                            let _ = std::fs::create_dir_all(&dir);
                            let _ = std::fs::write(
                                &log_path,
                                "App log has not accumulated any entries yet.\n",
                            );
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
                        let _ = std::fs::write(
                            &paths.log_path,
                            "Gateway log has not been created yet.\n",
                        );
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

            // The main window is created by tauri.conf.json and starts on the
            // splash page (frontendDist). Grab a handle to it so we can
            // navigate once the gateway is healthy.
            let win = app
                .get_webview_window("main")
                .expect("main window defined in tauri.conf.json");

            // Seed managed state with an empty child slot. The background
            // task fills it once hecate is spawned.
            app.manage(GatewayChild(Mutex::new(None)));
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
                        // Store the child so the exit handler can kill it.
                        if let Some(state) = app_handle.try_state::<GatewayChild>() {
                            if let Ok(mut slot) = state.0.lock() {
                                *slot = Some(handle.child);
                            }
                        }
                        if let Some(delay) = remaining_splash_delay(splash_started.elapsed()) {
                            tokio::time::sleep(delay).await;
                        }
                        // Navigate to the gateway UI.
                        let url = tauri::Url::parse(&handle.base_url)
                            .expect("gateway base_url is always valid");
                        log::info!("navigating to gateway {}", handle.base_url);
                        if let Err(e) = win.navigate(url) {
                            log::error!("navigate to gateway failed: {e}");
                        }
                    }
                    Err(e) => {
                        log::error!("hecate gateway startup failed: {e}");
                        let payload = serde_json::json!({
                            "message": e,
                            "logPath": diagnostics.log_path.display().to_string(),
                            "dataDir": diagnostics.data_dir.display().to_string(),
                        });
                        let script = format!(
                            "window.__hecateStartupFailureDetails = {payload}; window.__hecateStartupFailed && window.__hecateStartupFailed(window.__hecateStartupFailureDetails);"
                        );
                        let _ = win.eval(script);
                        let _ = win.set_title("Hecate — startup failed");
                    }
                }
            });

            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error building Hecate app")
        .run(|app_handle, event| {
            if let tauri::RunEvent::Exit = event {
                // Kill the hecate process so it doesn't become an orphan.
                if let Some(state) = app_handle.try_state::<GatewayChild>() {
                    if let Ok(mut slot) = state.0.lock() {
                        if let Some(mut child) = slot.take() {
                            let _ = child.kill();
                        }
                    }
                }
                if let Some(paths) = app_handle.try_state::<GatewayDiagnostics>() {
                    sidecar::remove_gateway_state(&paths.state_path);
                }
            }
        });
}

#[cfg(test)]
mod tests {
    use super::{remaining_splash_delay, MIN_SPLASH_DURATION};
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
}
