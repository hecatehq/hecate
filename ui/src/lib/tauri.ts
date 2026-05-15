// Tauri runtime detection. Returns true when the gateway UI is
// loaded inside the Tauri webview (the desktop app), false in any
// other host — browser tab, Docker, bare binary serving the
// embedded UI. Anything that calls Tauri-only APIs should gate on
// this check.

export function isTauriRuntime(): boolean {
  return typeof window !== "undefined"
    && (Object.prototype.hasOwnProperty.call(window, "__TAURI_INTERNALS__")
      || Object.prototype.hasOwnProperty.call(window, "__TAURI__"));
}

export type TauriPlatform = "macos" | "windows" | "linux";

// Cheap host-platform detection that doesn't round-trip to Rust.
// Used to apply platform-specific UI tweaks (the macOS overlay
// titlebar, traffic-light safe area, etc.). Returns null outside
// the Tauri runtime — those tweaks shouldn't apply when the gateway
// UI is served in a regular browser.
export function detectTauriPlatform(): TauriPlatform | null {
  if (!isTauriRuntime()) return null;
  if (typeof navigator === "undefined") return null;
  const ua = navigator.userAgent.toLowerCase();
  if (ua.includes("mac os") || ua.includes("macintosh")) return "macos";
  if (ua.includes("windows")) return "windows";
  if (ua.includes("linux")) return "linux";
  return null;
}
