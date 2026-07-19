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

export type DesktopHost = "linux" | "macos" | "windows";

// Returns the desktop host only inside Tauri. Keeping this capability
// narrow lets the shared console keep one layout while small shell
// affordances (titlebar, menu copy, native typography) match the host.
export function desktopHost(): DesktopHost | null {
  if (!isTauriRuntime() || typeof navigator === "undefined") return null;
  if (/mac/i.test(navigator.platform)) return "macos";
  if (/win/i.test(navigator.platform)) return "windows";
  if (/linux/i.test(navigator.platform)) return "linux";
  return null;
}

// True only when running inside the Tauri runtime AND the host is
// macOS. Gates the overlay-titlebar layout (a 28-px native drag strip
// with traffic-light padding). On Linux/Windows
// Tauri honors `decorations: true` with a native titlebar above the
// webview, so we don't want a second titlebar-shaped strip below it.
export function isTauriOnMacOS(): boolean {
  return desktopHost() === "macos";
}
