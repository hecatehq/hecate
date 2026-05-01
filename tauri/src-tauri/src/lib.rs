// lib.rs — Hecate desktop app (Tauri 2.x)
//
// Architecture:
//   The app bundles the hecate gateway binary as a Tauri sidecar. On launch:
//   1. The main window (defined in tauri.conf.json) loads the splash page
//      (../splash/index.html via frontendDist) while the gateway boots.
//   2. sidecar::spawn_and_wait() finds a free loopback port, spawns hecate,
//      and polls /healthz every 250 ms (30 s hard deadline).
//   3. On success the window navigates to http://127.0.0.1:{port}/ — the gateway
//      serves the full Hecate UI from its embedded ui/dist bundle.
//
// No frontend build step is required; no API bridge is wired. The webview is
// essentially a chrome-frame pointed at the local gateway.

mod sidecar;

use tauri::Manager; // get_webview_window

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

            let app_handle = app.handle().clone();

            // Spawn the gateway in a background task. Once healthy, navigate
            // the window to the gateway URL. On failure, update the title so
            // the user has a signal.
            tauri::async_runtime::spawn(async move {
                match sidecar::spawn_and_wait(&app_handle).await {
                    Ok(handle) => {
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
        .run(tauri::generate_context!())
        .expect("error running Hecate app");
}
