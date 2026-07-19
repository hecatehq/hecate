import { isTauriRuntime } from "./tauri";

export type DesktopCloudConnectionStatus = {
  available: boolean;
  phase: "disconnected" | "authorizing" | "connecting" | "connected" | "reconnecting" | "error";
  running: boolean;
  authorizing: boolean;
  signed_in: boolean;
  gateway_ready: boolean;
  auto_start_enabled: boolean;
  account_email: string | null;
  cloud_url: string;
  base_url: string | null;
  message: string;
  last_error: string | null;
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

export async function signOutDesktopCloudConnection(): Promise<DesktopCloudConnectionStatus> {
  return invokeCloudConnection("cloud_connection_sign_out");
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
  const phase =
    typeof record.phase === "string" &&
    ["disconnected", "authorizing", "connecting", "connected", "reconnecting", "error"].includes(
      record.phase,
    )
      ? (record.phase as DesktopCloudConnectionStatus["phase"])
      : "error";
  return {
    available: record.available === true,
    phase,
    running: record.running === true,
    authorizing: record.authorizing === true,
    signed_in: record.signed_in === true,
    gateway_ready: record.gateway_ready === true,
    auto_start_enabled: record.auto_start_enabled === true,
    account_email: typeof record.account_email === "string" ? record.account_email : null,
    cloud_url:
      typeof record.cloud_url === "string" ? record.cloud_url : "https://console.hecatehq.com",
    base_url: typeof record.base_url === "string" ? record.base_url : null,
    message:
      typeof record.message === "string"
        ? record.message
        : "Hecate Cloud connection status is unavailable.",
    last_error: typeof record.last_error === "string" ? record.last_error : null,
  };
}
