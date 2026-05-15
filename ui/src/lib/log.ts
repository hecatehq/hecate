// Logging helper that routes through @tauri-apps/plugin-log when
// running inside the Tauri webview, so messages land in the same
// rotating file the Rust side writes (<app_log_dir>/app.log).
// Outside Tauri (browser, Docker, bare binary) the helper falls
// back to console.* so dev-tools continue to surface the same
// messages.
//
// All entry points are fire-and-forget: the plugin call is
// dispatched on a void Promise so callers don't have to await.

import { isTauriRuntime } from "./tauri";

type Level = "info" | "warn" | "error";

type PluginLogModule = {
  info: (message: string) => Promise<void>;
  warn: (message: string) => Promise<void>;
  error: (message: string) => Promise<void>;
};

// Lazily resolved + cached. We dynamically import the plugin so a
// non-Tauri build doesn't drag the plugin module into the bundle
// (and so the import doesn't throw outside Tauri, where it would
// hit __TAURI_INTERNALS__ that doesn't exist).
let pluginModulePromise: Promise<PluginLogModule | null> | null = null;
function loadPluginLog(): Promise<PluginLogModule | null> {
  if (!pluginModulePromise) {
    pluginModulePromise = import("@tauri-apps/plugin-log")
      .then((m) => m as unknown as PluginLogModule)
      .catch(() => null);
  }
  return pluginModulePromise;
}

function consoleFallback(level: Level, message: string, args: unknown[]): void {
  if (level === "info") console.info(message, ...args);
  else if (level === "warn") console.warn(message, ...args);
  else console.error(message, ...args);
}

function formatArg(arg: unknown): string {
  if (arg instanceof Error) return `${arg.name}: ${arg.message}`;
  if (typeof arg === "string") return arg;
  try {
    return JSON.stringify(arg);
  } catch {
    return String(arg);
  }
}

async function dispatch(level: Level, message: string, args: unknown[]): Promise<void> {
  if (!isTauriRuntime()) {
    consoleFallback(level, message, args);
    return;
  }
  const mod = await loadPluginLog();
  if (!mod) {
    consoleFallback(level, message, args);
    return;
  }
  const combined = args.length === 0
    ? message
    : `${message} ${args.map(formatArg).join(" ")}`;
  try {
    if (level === "info") await mod.info(combined);
    else if (level === "warn") await mod.warn(combined);
    else await mod.error(combined);
  } catch (err) {
    // The plugin or capabilities are misconfigured — surface
    // through console so the message isn't fully lost.
    consoleFallback("warn", "[hecate] plugin-log dispatch failed:", [err]);
    consoleFallback(level, message, args);
  }
}

export function info(message: string, ...args: unknown[]): void {
  void dispatch("info", message, args);
}

export function warn(message: string, ...args: unknown[]): void {
  void dispatch("warn", message, args);
}

export function error(message: string, ...args: unknown[]): void {
  void dispatch("error", message, args);
}
