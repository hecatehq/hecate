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

export type DesktopCloudRuntimeConnection = {
  id: string;
  kind: "hosted_runtime" | "desktop_host";
  org_id: string;
  project_id: string | null;
  name: string;
  status: string;
  reachable: boolean;
  can_start: boolean;
  remote_enabled: boolean;
  version: string | null;
  capabilities: string[];
  last_seen_at: string | null;
};

export type DesktopCloudRuntimeStartResult = {
  connection_id: string;
  name: string;
  status: string;
  reachable: boolean;
  message: string;
};

export type DesktopCloudRuntimeOpenResult = {
  connection_id: string;
  name: string;
  message: string;
};

export function canUseDesktopCloudConnection(): boolean {
  return isTauriRuntime();
}

export async function getDesktopCloudConnectionStatus(): Promise<DesktopCloudConnectionStatus> {
  return normalizeStatus(await invokeCloudCommand("cloud_connection_status"));
}

export async function signInDesktopCloudAccount(): Promise<DesktopCloudConnectionStatus> {
  return normalizeStatus(await invokeCloudCommand("cloud_account_sign_in"));
}

export async function startDesktopCloudConnection(): Promise<DesktopCloudConnectionStatus> {
  return normalizeStatus(await invokeCloudCommand("cloud_connection_start"));
}

export async function stopDesktopCloudConnection(): Promise<DesktopCloudConnectionStatus> {
  return normalizeStatus(await invokeCloudCommand("cloud_connection_stop"));
}

export async function signOutDesktopCloudConnection(): Promise<DesktopCloudConnectionStatus> {
  return normalizeStatus(await invokeCloudCommand("cloud_connection_sign_out"));
}

export async function getDesktopCloudRuntimeConnections(): Promise<
  DesktopCloudRuntimeConnection[]
> {
  const value = await invokeCloudCommand("cloud_runtime_connections");
  if (!Array.isArray(value)) {
    throw new Error("Hecate Cloud returned an invalid connection list.");
  }
  return value.map((connection) => normalizeRuntimeConnection(connection));
}

export async function startDesktopCloudRuntime(
  connectionId: string,
): Promise<DesktopCloudRuntimeStartResult> {
  return normalizeRuntimeStartResult(
    await invokeCloudCommand("cloud_runtime_start", {
      connectionId: requireConnectionID(connectionId),
    }),
  );
}

export async function openDesktopCloudRuntime(
  connectionId: string,
): Promise<DesktopCloudRuntimeOpenResult> {
  return normalizeRuntimeOpenResult(
    await invokeCloudCommand("cloud_runtime_open", {
      connectionId: requireConnectionID(connectionId),
    }),
  );
}

async function invokeCloudCommand(
  command: string,
  args?: Record<string, unknown>,
): Promise<unknown> {
  if (!canUseDesktopCloudConnection()) {
    throw new Error("Hecate Cloud connection is only available in the desktop app.");
  }
  const { invoke } = await import("@tauri-apps/api/core");
  return invoke(command, args);
}

function normalizeStatus(value: unknown): DesktopCloudConnectionStatus {
  const record = requireRecord(value, "Hecate Cloud connection returned an invalid status.");
  const phase = record.phase;
  if (
    typeof phase !== "string" ||
    !["disconnected", "authorizing", "connecting", "connected", "reconnecting", "error"].includes(
      phase,
    )
  ) {
    throw new Error("Hecate Cloud connection returned an invalid status.");
  }
  return {
    available: requireBoolean(record.available),
    phase: phase as DesktopCloudConnectionStatus["phase"],
    running: requireBoolean(record.running),
    authorizing: requireBoolean(record.authorizing),
    signed_in: requireBoolean(record.signed_in),
    gateway_ready: requireBoolean(record.gateway_ready),
    auto_start_enabled: requireBoolean(record.auto_start_enabled),
    account_email: requireNullableString(record.account_email),
    cloud_url: requireNonEmptyString(record.cloud_url),
    base_url: requireNullableString(record.base_url),
    message: requireNonEmptyString(record.message),
    last_error: requireNullableString(record.last_error),
  };
}

function normalizeRuntimeConnection(value: unknown): DesktopCloudRuntimeConnection {
  const error = "Hecate Cloud returned an invalid runtime connection.";
  const record = requireRecord(value, error);
  if (record.kind !== "hosted_runtime" && record.kind !== "desktop_host") {
    throw new Error(error);
  }
  if (!Array.isArray(record.capabilities)) {
    throw new Error(error);
  }
  const capabilities = record.capabilities.map((capability) => {
    try {
      return requireNonEmptyString(capability);
    } catch {
      throw new Error(error);
    }
  });
  try {
    return {
      id: requireNonEmptyString(record.id),
      kind: record.kind,
      org_id: requireNonEmptyString(record.org_id),
      project_id: requireNullableString(record.project_id),
      name: requireNonEmptyString(record.name),
      status: requireNonEmptyString(record.status),
      reachable: requireBoolean(record.reachable),
      can_start: requireBoolean(record.can_start),
      remote_enabled: requireBoolean(record.remote_enabled),
      version: requireNullableString(record.version),
      capabilities,
      last_seen_at: requireNullableString(record.last_seen_at),
    };
  } catch {
    throw new Error(error);
  }
}

function normalizeRuntimeStartResult(value: unknown): DesktopCloudRuntimeStartResult {
  const error = "Hecate Cloud returned an invalid runtime start result.";
  const record = requireRecord(value, error);
  try {
    return {
      connection_id: requireNonEmptyString(record.connection_id),
      name: requireNonEmptyString(record.name),
      status: requireNonEmptyString(record.status),
      reachable: requireBoolean(record.reachable),
      message: requireNonEmptyString(record.message),
    };
  } catch {
    throw new Error(error);
  }
}

function normalizeRuntimeOpenResult(value: unknown): DesktopCloudRuntimeOpenResult {
  const error = "Hecate Cloud returned an invalid runtime open result.";
  const record = requireRecord(value, error);
  try {
    return {
      connection_id: requireNonEmptyString(record.connection_id),
      name: requireNonEmptyString(record.name),
      message: requireNonEmptyString(record.message),
    };
  } catch {
    throw new Error(error);
  }
}

function requireRecord(value: unknown, message: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(message);
  }
  return value as Record<string, unknown>;
}

function requireBoolean(value: unknown): boolean {
  if (typeof value !== "boolean") throw new Error("Expected a boolean.");
  return value;
}

function requireNonEmptyString(value: unknown): string {
  if (typeof value !== "string" || value.trim() === "") {
    throw new Error("Expected a non-empty string.");
  }
  return value;
}

function requireNullableString(value: unknown): string | null {
  if (value === null || value === undefined) return null;
  return requireNonEmptyString(value);
}

function requireConnectionID(value: string): string {
  const connectionID = value.trim();
  if (!connectionID) {
    throw new Error("Choose a valid Hecate connection.");
  }
  return connectionID;
}
