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
