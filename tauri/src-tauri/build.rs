fn main() {
    // Forward Cargo's build-script TARGET into a rustc env so the crate
    // can read it via env!("TARGET"). Cargo sets TARGET when invoking
    // build.rs, but not when compiling regular crate code — without this
    // re-export, env!("TARGET") in src/sidecar.rs fails to resolve.
    println!(
        "cargo:rustc-env=TARGET={}",
        std::env::var("TARGET").expect("TARGET set by cargo for build scripts")
    );
    // Auto-generate ACL permissions (allow-/deny-) for each custom
    // invoke_handler command. Without this, invoke("set_update_badge", …)
    // is rejected with "not allowed. Plugin not found" on the gateway
    // origin (http://127.0.0.1:*) — Tauri 2's ACL needs an explicit
    // permission entry per command for any non-local URL.
    let attributes =
        tauri_build::Attributes::new().app_manifest(tauri_build::AppManifest::new().commands(&[
            "set_update_badge",
            "take_pending_desktop_update_check",
            "cloud_connection_status",
            "cloud_connection_start",
            "cloud_connection_stop",
            "cloud_connection_sign_out",
        ]));
    tauri_build::try_build(attributes).expect("tauri-build failed");
}
