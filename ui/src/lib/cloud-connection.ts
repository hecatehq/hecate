import { isTauriRuntime } from "./tauri";

export type DesktopCloudConnectionStatus = {
  available: boolean;
  running: boolean;
  gateway_ready: boolean;
  auto_start_enabled: boolean;
  hec_path: string | null;
  base_url: string | null;
  message: string;
  last_exit_status: string | null;
};

export function canUseDesktopCloudConnection(): boolean {
  return isTauriRuntime();
}

export async function getDesktopCloudConnectionStatus(): Promise<DesktopCloudConnectionStatus> {
  return invokeCloudConnection("cloud_connection_status");
}

export async function startDesktopCloudConnection(): Promise<DesktopCloudConnectionStatus> {
  return invokeCloudConnection("cloud_connection_start");
}

export async function stopDesktopCloudConnection(): Promise<DesktopCloudConnectionStatus> {
  return invokeCloudConnection("cloud_connection_stop");
}

async function invokeCloudConnection(command: string): Promise<DesktopCloudConnectionStatus> {
  if (!canUseDesktopCloudConnection()) {
    throw new Error("Hecate Cloud connection is only available in the desktop app.");
  }
  const { invoke } = await import("@tauri-apps/api/core");
  return normalizeStatus(await invoke(command));
}

function normalizeStatus(value: unknown): DesktopCloudConnectionStatus {
  if (!value || typeof value !== "object") {
    throw new Error("Hecate Cloud connection returned an invalid status.");
  }
  const record = value as Record<string, unknown>;
  return {
    available: record.available === true,
    running: record.running === true,
    gateway_ready: record.gateway_ready === true,
    auto_start_enabled: record.auto_start_enabled === true,
    hec_path: typeof record.hec_path === "string" ? record.hec_path : null,
    base_url: typeof record.base_url === "string" ? record.base_url : null,
    message:
      typeof record.message === "string"
        ? record.message
        : "Hecate Cloud connection status is unavailable.",
    last_exit_status: typeof record.last_exit_status === "string" ? record.last_exit_status : null,
  };
}
