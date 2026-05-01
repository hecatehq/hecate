// main.rs — entry point for the Hecate desktop app.
//
// Tauri 2.x requires this thin shim; all app logic lives in lib.rs so it is
// reachable from both the desktop entry point (here) and mobile targets.

// Prevents an additional console window from appearing on Windows in release.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    hecate_app_lib::run()
}
