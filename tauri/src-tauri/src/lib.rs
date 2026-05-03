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
use tauri::menu::{MenuBuilder, SubmenuBuilder};
use tauri::Manager;

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
    let help = SubmenuBuilder::new(app, "Hecate")
        .text("open-hecate", "Open Hecate")
        .separator()
        .text("open-gateway-log", "Open Gateway Log")
        .text("open-data-directory", "Open Data Directory")
        .separator()
        .text("quit-hecate", "Quit Hecate")
        .build()?;
    let menu = MenuBuilder::new(app).item(&help).build()?;
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
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_window_state::Builder::default().build())
        .on_menu_event(|app, event| match event.id().as_ref() {
            "open-hecate" => {
                if let Some(win) = app.get_webview_window("main") {
                    let _ = win.show();
                    let _ = win.set_focus();
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
                        eprintln!("{e}");
                    }
                }
            }
            "open-data-directory" => {
                if let Some(paths) = app.try_state::<GatewayDiagnostics>() {
                    if let Err(e) = open_path(&paths.data_dir) {
                        eprintln!("{e}");
                    }
                }
            }
            "quit-hecate" => app.exit(0),
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
                        if let Err(e) = win.navigate(url) {
                            eprintln!("navigate to gateway failed: {e}");
                        }
                    }
                    Err(e) => {
                        eprintln!("hecate gateway startup failed: {e}");
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
