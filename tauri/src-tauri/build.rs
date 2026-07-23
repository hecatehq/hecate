fn main() {
    // Forward Cargo's build-script TARGET into a rustc env so the crate
    // can read it via env!("TARGET"). Cargo sets TARGET when invoking
    // build.rs, but not when compiling regular crate code — without this
    // re-export, env!("TARGET") in src/sidecar.rs fails to resolve.
    println!(
        "cargo:rustc-env=TARGET={}",
        std::env::var("TARGET").expect("TARGET set by cargo for build scripts")
    );
    // Auto-generate ACL permissions (allow-/deny-) for each custom command on
    // both target families. Platform-scoped capabilities decide which set is
    // reachable from a given webview.
    let attributes =
        tauri_build::Attributes::new().app_manifest(tauri_build::AppManifest::new().commands(&[
            "set_update_badge",
            "take_pending_desktop_update_check",
            "cloud_connection_status",
            "cloud_connection_start",
            "cloud_connection_stop",
            "cloud_connection_sign_out",
            "mobile_status",
            "mobile_sign_in",
            "mobile_reopen_authorization",
            "mobile_connections",
            "mobile_notification_status",
            "mobile_enable_notifications",
            "mobile_open_notification_settings",
            "mobile_disable_notifications",
            "mobile_open_connection",
            "mobile_sign_out",
        ]));
    tauri_build::try_build(attributes).expect("tauri-build failed");
}
