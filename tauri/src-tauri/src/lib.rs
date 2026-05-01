// lib.rs — Hecate desktop app (Tauri 2.x)
//
// Architecture:
//   The app bundles the hecate gateway binary as a companion process. On launch:
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

use std::sync::Mutex;
use tauri::Manager;

/// Tauri managed state: the gateway child process.
/// Wrapped in Mutex<Option<…>> so the exit handler can take() it exactly once.
struct GatewayChild(Mutex<Option<std::process::Child>>);

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .setup(|app| {
            // The main window is created by tauri.conf.json and starts on the
            // splash page (frontendDist). Grab a handle to it so we can
            // navigate once the gateway is healthy.
            let win = app
                .get_webview_window("main")
                .expect("main window defined in tauri.conf.json");

            // Seed managed state with an empty child slot. The background
            // task fills it once hecate is spawned.
            app.manage(GatewayChild(Mutex::new(None)));

            let app_handle = app.handle().clone();

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
                        // Navigate to the gateway UI.
                        let url = tauri::Url::parse(&handle.base_url)
                            .expect("gateway base_url is always valid");
                        if let Err(e) = win.navigate(url) {
                            eprintln!("navigate to gateway failed: {e}");
                        }
                    }
                    Err(e) => {
                        eprintln!("hecate gateway startup failed: {e}");
                        let _ = win.set_title(&format!("Hecate — startup failed: {e}"));
                    }
                }
            });

            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error building Hecate app")
        .run(|app_handle, event| {
            if let tauri::RunEvent::Exit = event {
                // Kill the gateway process so it doesn't become an orphan.
                if let Some(state) = app_handle.try_state::<GatewayChild>() {
                    if let Ok(mut slot) = state.0.lock() {
                        if let Some(mut child) = slot.take() {
                            let _ = child.kill();
                        }
                    }
                }
            }
        });
}
