fn main() {
    // Forward Cargo's build-script TARGET into a rustc env so the crate
    // can read it via env!("TARGET"). Cargo sets TARGET when invoking
    // build.rs, but not when compiling regular crate code — without this
    // re-export, env!("TARGET") in src/sidecar.rs fails to resolve.
    println!(
        "cargo:rustc-env=TARGET={}",
        std::env::var("TARGET").expect("TARGET set by cargo for build scripts")
    );
    tauri_build::build()
}
