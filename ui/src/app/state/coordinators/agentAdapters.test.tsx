import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

import { useAgentAdapterActions } from "./agentAdapters";
import { ProvidersAndModelsProvider, useProvidersAndModels } from "../providersAndModels";
import { resolveExternalAgentReadiness } from "../../../lib/external-agent-readiness";

const authenticateAgentAdapterMock = vi.fn();
const logoutAgentAdapterMock = vi.fn();
const probeAgentAdapterMock = vi.fn();
const approveAgentAdapterExecutableMock = vi.fn();
const revokeAgentAdapterExecutableMock = vi.fn();
const getAgentAdaptersMock = vi.fn();

vi.mock("../../../lib/api", () => ({
  getProviders: vi.fn(),
  getModels: vi.fn(),
  getProviderPresets: vi.fn(),
  probeAgentAdapter: (...args: unknown[]) => probeAgentAdapterMock(...args),
  getAgentAdapters: (...args: unknown[]) => getAgentAdaptersMock(...args),
  authenticateAgentAdapter: (...args: unknown[]) => authenticateAgentAdapterMock(...args),
  logoutAgentAdapter: (...args: unknown[]) => logoutAgentAdapterMock(...args),
  approveAgentAdapterExecutable: (...args: unknown[]) => approveAgentAdapterExecutableMock(...args),
  revokeAgentAdapterExecutable: (...args: unknown[]) => revokeAgentAdapterExecutableMock(...args),
}));

const approvedExecutableTrust = {
  schema_version: "hecate.external-agent-executable.v1",
  state: "approved" as const,
  reason: "operator_approved",
};

const executableIdentity = {
  schema_version: "hecate.external-agent-executable.v1",
  identity_token: "identity-v1",
  invocation_path: "/usr/local/bin/codex",
  canonical_path: "/opt/codex/bin/codex",
  sha256: "a".repeat(64),
  coverage: "binary" as const,
  launcher_chain: [],
  file_id: "dev:1:ino:2",
  mode: 0o755,
  size_bytes: 2048,
  publisher: { status: "unavailable" },
};

function UnapprovedWrapper({ children }: { children: ReactNode }) {
  return (
    <ProvidersAndModelsProvider
      initialState={{
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex",
            available: true,
            status: "available",
            auth_status: "unknown",
            supports_authenticate: true,
            supports_logout: true,
            executable_trust: {
              schema_version: "hecate.external-agent-executable.v1",
              state: "unapproved",
              reason: "approval_required",
              current: executableIdentity,
            },
          },
        ],
        agentAdapterHealthByID: new Map([
          [
            "codex",
            {
              adapter_id: "codex",
              status: "ready",
              stage: "ready",
              duration_ms: 42,
            },
          ],
        ]),
      }}
    >
      {children}
    </ProvidersAndModelsProvider>
  );
}

function ApprovedIdentityWrapper({ children }: { children: ReactNode }) {
  return (
    <ProvidersAndModelsProvider
      initialState={{
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex",
            available: true,
            status: "available",
            auth_status: "ok",
            supports_authenticate: true,
            supports_logout: true,
            executable_trust: {
              schema_version: "hecate.external-agent-executable.v1",
              state: "approved",
              reason: "operator_approved",
              current: executableIdentity,
              approved: executableIdentity,
            },
          },
        ],
        agentAdapterHealthByID: new Map([
          [
            "codex",
            {
              adapter_id: "codex",
              status: "ready",
              stage: "ready",
              duration_ms: 42,
            },
          ],
        ]),
      }}
    >
      {children}
    </ProvidersAndModelsProvider>
  );
}

function AuthRequiredWrapper({ children }: { children: ReactNode }) {
  return (
    <ProvidersAndModelsProvider
      initialState={{
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex",
            available: true,
            status: "available",
            auth_status: "unauthenticated",
            auth_error: "Sign in required.",
            supports_authenticate: true,
            supports_logout: true,
            executable_trust: approvedExecutableTrust,
          },
        ],
        agentAdapterHealthByID: new Map([
          [
            "codex",
            {
              adapter_id: "codex",
              status: "auth_required",
              stage: "authenticate",
              error: "Sign in required.",
              duration_ms: 42,
            },
          ],
        ]),
      }}
    >
      {children}
    </ProvidersAndModelsProvider>
  );
}

function ReadyWrapper({ children }: { children: ReactNode }) {
  return (
    <ProvidersAndModelsProvider
      initialState={{
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex",
            available: true,
            status: "available",
            auth_status: "ok",
            supports_authenticate: true,
            supports_logout: true,
            executable_trust: approvedExecutableTrust,
          },
        ],
        agentAdapterHealthByID: new Map([
          [
            "codex",
            {
              adapter_id: "codex",
              status: "ready",
              stage: "ready",
              duration_ms: 42,
            },
          ],
        ]),
      }}
    >
      {children}
    </ProvidersAndModelsProvider>
  );
}

beforeEach(() => {
  authenticateAgentAdapterMock.mockReset();
  logoutAgentAdapterMock.mockReset();
  probeAgentAdapterMock.mockReset();
  approveAgentAdapterExecutableMock.mockReset();
  revokeAgentAdapterExecutableMock.mockReset();
  getAgentAdaptersMock.mockReset();
});

describe("useAgentAdapterActions", () => {
  it("approves the reviewed executable identity and invalidates prior session evidence", async () => {
    const approved = {
      schema_version: "hecate.external-agent-executable.v1",
      state: "approved" as const,
      reason: "operator_approved",
      current: executableIdentity,
      approved: executableIdentity,
      approved_by: "operator",
      approved_at: "2026-09-24T10:00:00Z",
    };
    approveAgentAdapterExecutableMock.mockResolvedValue({
      object: "agent_adapter_executable_trust",
      data: approved,
    });
    getAgentAdaptersMock.mockResolvedValue({
      object: "list",
      data: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex",
          available: true,
          status: "available",
          supports_authenticate: true,
          supports_logout: true,
          executable_trust: approved,
        },
      ],
    });
    const notices: Array<[string, string]> = [];
    const { result } = renderHook(
      () => ({
        adapterActions: useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
        providersAndModels: useProvidersAndModels(),
      }),
      { wrapper: UnapprovedWrapper },
    );

    await act(async () => {
      await result.current.adapterActions.approveAgentAdapterExecutable(
        "codex",
        executableIdentity.identity_token,
      );
    });

    expect(approveAgentAdapterExecutableMock).toHaveBeenCalledWith("codex", "identity-v1");
    expect(result.current.providersAndModels.state.agentAdapters[0]?.executable_trust).toEqual(
      approved,
    );
    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(false);
    expect(notices).toContainEqual(["success", "External agent app approved."]);
  });

  it("revokes executable approval immediately and invalidates prior session evidence", async () => {
    revokeAgentAdapterExecutableMock.mockResolvedValue(undefined);
    getAgentAdaptersMock.mockRejectedValue(new Error("catalog refresh unavailable"));
    const notices: Array<[string, string]> = [];
    const { result } = renderHook(
      () => ({
        adapterActions: useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
        providersAndModels: useProvidersAndModels(),
      }),
      { wrapper: ApprovedIdentityWrapper },
    );

    await act(async () => {
      await result.current.adapterActions.revokeAgentAdapterExecutable("codex");
    });

    expect(revokeAgentAdapterExecutableMock).toHaveBeenCalledWith("codex");
    expect(
      result.current.providersAndModels.state.agentAdapters[0]?.executable_trust,
    ).toMatchObject({
      state: "unapproved",
      reason: "approval_required",
      current: executableIdentity,
    });
    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(false);
    expect(notices).toContainEqual(["success", "External agent app approval revoked."]);
  });

  it("refreshes a stale reviewed identity after approval is rejected", async () => {
    const changedIdentity = {
      ...executableIdentity,
      identity_token: "identity-v2",
      sha256: "b".repeat(64),
    };
    const changedTrust = {
      schema_version: "hecate.external-agent-executable.v1",
      state: "changed" as const,
      reason: "identity_changed",
      current: changedIdentity,
      approved: executableIdentity,
    };
    approveAgentAdapterExecutableMock.mockRejectedValue(
      new Error("External agent executable approval is stale."),
    );
    getAgentAdaptersMock.mockResolvedValue({
      object: "agent_adapters",
      data: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex",
          available: true,
          status: "available",
          supports_authenticate: true,
          supports_logout: true,
          executable_trust: changedTrust,
        },
      ],
    });
    const notices: Array<[string, string]> = [];
    const { result } = renderHook(
      () => ({
        adapterActions: useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
        providersAndModels: useProvidersAndModels(),
      }),
      { wrapper: UnapprovedWrapper },
    );

    let approved = true;
    await act(async () => {
      approved = await result.current.adapterActions.approveAgentAdapterExecutable(
        "codex",
        "identity-v1",
      );
    });

    expect(approved).toBe(false);
    expect(getAgentAdaptersMock).toHaveBeenCalledTimes(1);
    expect(result.current.providersAndModels.state.agentAdapters[0]?.executable_trust).toEqual(
      changedTrust,
    );
    expect(notices).toContainEqual(["error", "External agent executable approval is stale."]);
  });

  it("authenticates an adapter and atomically replaces stale auth diagnostics", async () => {
    authenticateAgentAdapterMock.mockResolvedValue({
      object: "agent_adapter_authenticate",
      data: {
        adapter_id: "codex",
        status: "authenticated",
        method_id: "agent-login",
        duration_ms: 12,
      },
    });
    const notices: Array<[string, string]> = [];

    const { result } = renderHook(
      () => ({
        adapterActions: useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
        providersAndModels: useProvidersAndModels(),
      }),
      { wrapper: AuthRequiredWrapper },
    );

    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(true);

    await act(async () => {
      await result.current.adapterActions.authenticateAgentAdapter("codex");
    });

    expect(authenticateAgentAdapterMock).toHaveBeenCalledWith("codex");
    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(false);
    expect(result.current.providersAndModels.state.agentAdapters[0]).toMatchObject({
      auth_status: "ok",
    });
    expect(result.current.providersAndModels.state.agentAdapters[0]?.auth_error).toBeUndefined();
    expect(
      resolveExternalAgentReadiness(result.current.providersAndModels.state.agentAdapters[0], null),
    ).toMatchObject({ kind: "unverified", authStatus: "ok" });
    expect(notices).toContainEqual(["success", "External agent sign-in completed."]);
  });

  it("keeps cached health when authenticate fails", async () => {
    authenticateAgentAdapterMock.mockRejectedValue(new Error("authenticate failed"));
    const notices: Array<[string, string]> = [];

    const { result } = renderHook(
      () => ({
        adapterActions: useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
        providersAndModels: useProvidersAndModels(),
      }),
      { wrapper: AuthRequiredWrapper },
    );

    await act(async () => {
      await result.current.adapterActions.authenticateAgentAdapter("codex");
    });

    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(true);
    expect(result.current.providersAndModels.state.agentAdapters[0]).toMatchObject({
      auth_status: "unauthenticated",
      auth_error: "Sign in required.",
    });
    expect(notices).toContainEqual(["error", "authenticate failed"]);
  });

  it("does not surface a diagnostic failure superseded by successful sign-in", async () => {
    let rejectProbe: (reason?: unknown) => void = () => {};
    probeAgentAdapterMock.mockReturnValueOnce(
      new Promise((_resolve, reject) => {
        rejectProbe = reject;
      }),
    );
    authenticateAgentAdapterMock.mockResolvedValue({
      object: "agent_adapter_authenticate",
      data: {
        adapter_id: "codex",
        status: "authenticated",
        method_id: "agent-login",
        duration_ms: 12,
      },
    });
    const notices: Array<[string, string]> = [];
    const { result } = renderHook(
      () => ({
        adapterActions: useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
        providersAndModels: useProvidersAndModels(),
      }),
      { wrapper: AuthRequiredWrapper },
    );
    let diagnostic!: Promise<unknown>;

    act(() => {
      diagnostic = result.current.adapterActions.probeAgentAdapter("codex");
    });
    await act(async () => {
      await result.current.adapterActions.authenticateAgentAdapter("codex");
    });
    let diagnosticResult: unknown;
    await act(async () => {
      rejectProbe(new Error("obsolete diagnostic failure"));
      diagnosticResult = await diagnostic;
    });

    expect(diagnosticResult).toEqual({ ok: true, health: null });
    expect(result.current.providersAndModels.state.agentAdapters[0]).toMatchObject({
      auth_status: "ok",
    });
    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(false);
    expect(notices).toEqual([["success", "External agent sign-in completed."]]);
  });

  it("keeps a silent check failure local to the adapter row", async () => {
    probeAgentAdapterMock.mockRejectedValue(new Error("adapter launch failed"));
    const notices: Array<[string, string]> = [];
    const { result } = renderHook(
      () =>
        useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
      { wrapper: ReadyWrapper },
    );

    let checkResult: unknown;
    await act(async () => {
      checkResult = await result.current.probeAgentAdapter("codex", {
        notify: false,
        refreshCatalog: false,
      });
    });

    expect(checkResult).toEqual({ ok: false, error: "adapter launch failed" });
    expect(notices).toEqual([]);
  });

  it("logs out an adapter and atomically replaces stale auth diagnostics", async () => {
    logoutAgentAdapterMock.mockResolvedValue({
      object: "agent_adapter_logout",
      data: { adapter_id: "codex", status: "logged_out", duration_ms: 12 },
    });
    const notices: Array<[string, string]> = [];

    const { result } = renderHook(
      () => ({
        adapterActions: useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
        providersAndModels: useProvidersAndModels(),
      }),
      { wrapper: ReadyWrapper },
    );

    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(true);

    await act(async () => {
      await result.current.adapterActions.logoutAgentAdapter("codex");
    });

    expect(logoutAgentAdapterMock).toHaveBeenCalledWith("codex");
    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(false);
    expect(result.current.providersAndModels.state.agentAdapters[0]).toMatchObject({
      auth_status: "unauthenticated",
    });
    expect(result.current.providersAndModels.state.agentAdapters[0]?.auth_error).toBeUndefined();
    expect(
      resolveExternalAgentReadiness(result.current.providersAndModels.state.agentAdapters[0], null),
    ).toMatchObject({ kind: "sign_in", authStatus: "unauthenticated" });
    expect(notices).toContainEqual(["success", "External agent signed out."]);
  });

  it("keeps cached health when logout fails", async () => {
    logoutAgentAdapterMock.mockRejectedValue(new Error("logout failed"));
    const notices: Array<[string, string]> = [];

    const { result } = renderHook(
      () => ({
        adapterActions: useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
        providersAndModels: useProvidersAndModels(),
      }),
      { wrapper: ReadyWrapper },
    );

    await act(async () => {
      await result.current.adapterActions.logoutAgentAdapter("codex");
    });

    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(true);
    expect(result.current.providersAndModels.state.agentAdapters[0]).toMatchObject({
      auth_status: "ok",
    });
    expect(notices).toContainEqual(["error", "logout failed"]);
  });
});
