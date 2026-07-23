// Tauri runtime detection. The mobile companion deliberately keeps a Tauri
// bridge while it navigates to a hosted runtime, so bridge presence alone no
// longer identifies the desktop shell. Both mobile configs stamp the webview
// user agent with this stable marker.

export const MOBILE_TAURI_USER_AGENT_MARKER = "HecateMobile";
export const MOBILE_INSTANCES_URL = "hecate-mobile://connections/";

function hasTauriBridge(): boolean {
  return (
    typeof window !== "undefined" &&
    (Object.prototype.hasOwnProperty.call(window, "__TAURI_INTERNALS__") ||
      Object.prototype.hasOwnProperty.call(window, "__TAURI__"))
  );
}

export function isMobileTauriRuntime(): boolean {
  return (
    hasTauriBridge() &&
    typeof navigator !== "undefined" &&
    navigator.userAgent.includes(MOBILE_TAURI_USER_AGENT_MARKER)
  );
}

// Returns true only for the desktop Tauri shell. Mobile companion pages use
// ordinary web/gateway behavior for updater, opener, and workspace actions.

export function isTauriRuntime(): boolean {
  return hasTauriBridge() && !isMobileTauriRuntime();
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
