//! Hecate native application entry point.
//!
//! Desktop keeps the existing local gateway sidecar architecture. Mobile is a
//! deliberately smaller Cloud companion: it never bundles or starts the Go
//! runtime and only exposes the commands implemented in `mobile`.

#[cfg(desktop)]
mod desktop;
#[cfg(any(mobile, test))]
#[cfg_attr(all(test, not(mobile)), allow(dead_code))]
mod mobile;

#[cfg(desktop)]
pub use desktop::run;

#[cfg(mobile)]
pub use mobile::run;
