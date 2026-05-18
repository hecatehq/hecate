// Tauri runtime detection. Returns true when the gateway UI is
// loaded inside the Tauri webview (the desktop app), false in any
// other host — browser tab, Docker, bare binary serving the
// embedded UI. Anything that calls Tauri-only APIs should gate on
// this check.

export function isTauriRuntime(): boolean {
  return (
    typeof window !== "undefined" &&
    (Object.prototype.hasOwnProperty.call(window, "__TAURI_INTERNALS__") ||
      Object.prototype.hasOwnProperty.call(window, "__TAURI__"))
  );
}

// True only when running inside the Tauri runtime AND the host is
// macOS. Gates the overlay-titlebar layout (28-px strip with
// traffic-light pad + draggable banner). On Linux/Windows Tauri
// honors `decorations: true` with a native titlebar above the
// webview, so we don't want a second titlebar-shaped strip below it.
export function isTauriOnMacOS(): boolean {
  return isTauriRuntime() && typeof navigator !== "undefined" && /mac/i.test(navigator.platform);
}
