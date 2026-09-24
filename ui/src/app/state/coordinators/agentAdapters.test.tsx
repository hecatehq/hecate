import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

import { useAgentAdapterActions } from "./agentAdapters";
import { ProvidersAndModelsProvider, useProvidersAndModels } from "../providersAndModels";
import { resolveExternalAgentReadiness } from "../../../lib/external-agent-readiness";
import type { AgentAdapterExecutableTrust } from "../../../types/agent-adapter";

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

const changedExecutableTrust: AgentAdapterExecutableTrust = {
  schema_version: "hecate.external-agent-executable.v1",
  state: "changed",
  reason: "identity_changed",
  current: {
    ...executableIdentity,
    identity_token: "identity-v2",
    sha256: "b".repeat(64),
  },
  approved: executableIdentity,
};

const unavailableExecutableTrust: AgentAdapterExecutableTrust = {
  schema_version: "hecate.external-agent-executable.v1",
  state: "unavailable",
  reason: "executable_not_found",
};

function adapterWithExecutableTrust(executableTrust: AgentAdapterExecutableTrust) {
  return {
    id: "codex",
    name: "Codex",
    kind: "acp",
    command: "codex",
    available: executableTrust.state !== "unavailable",
    status: executableTrust.state === "unavailable" ? "missing" : "available",
    supports_authenticate: true,
    supports_logout: true,
    executable_trust: executableTrust,
  };
}

function deferredValue<T = unknown>() {
  let resolve: (value: T) => void = () => {};
  const promise = new Promise<T>((next) => {
    resolve = next;
  });
  return { promise, resolve };
}

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

  it("preserves a confirmed approval when the passive follow-up refresh fails", async () => {
    const approved = {
      schema_version: "hecate.external-agent-executable.v1",
      state: "approved" as const,
      reason: "operator_approved",
      current: executableIdentity,
      approved: executableIdentity,
    };
    approveAgentAdapterExecutableMock.mockResolvedValue({
      object: "agent_adapter_executable_trust",
      data: approved,
    });
    getAgentAdaptersMock.mockRejectedValue(new Error("catalog refresh unavailable"));
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

    let succeeded = false;
    await act(async () => {
      succeeded = await result.current.adapterActions.approveAgentAdapterExecutable(
        "codex",
        executableIdentity.identity_token,
      );
    });

    expect(succeeded).toBe(true);
    expect(result.current.providersAndModels.state.agentAdapters[0]?.executable_trust).toEqual(
      approved,
    );
    expect(notices).toEqual([["success", "External agent app approved."]]);
  });

  it("confirms approval when its response is lost after commit", async () => {
    const approved = {
      schema_version: "hecate.external-agent-executable.v1",
      state: "approved" as const,
      reason: "operator_approved",
      current: executableIdentity,
      approved: executableIdentity,
    };
    approveAgentAdapterExecutableMock.mockRejectedValue(new Error("response lost"));
    getAgentAdaptersMock.mockResolvedValue({
      object: "agent_adapters",
      data: [adapterWithExecutableTrust(approved)],
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

    let succeeded = false;
    await act(async () => {
      succeeded = await result.current.adapterActions.approveAgentAdapterExecutable(
        "codex",
        executableIdentity.identity_token,
      );
    });

    expect(succeeded).toBe(true);
    expect(getAgentAdaptersMock).toHaveBeenCalledTimes(1);
    expect(result.current.providersAndModels.state.agentAdapters[0]?.executable_trust).toEqual(
      approved,
    );
    expect(notices).toEqual([["success", "External agent app approved."]]);
  });

  it("does not confirm a lost approval response from a different reviewed identity", async () => {
    const otherIdentity = {
      ...executableIdentity,
      identity_token: "identity-v2",
      sha256: "b".repeat(64),
    };
    const approvedOtherIdentity = {
      schema_version: "hecate.external-agent-executable.v1",
      state: "approved" as const,
      reason: "operator_approved",
      current: otherIdentity,
      approved: otherIdentity,
    };
    approveAgentAdapterExecutableMock.mockRejectedValue(new Error("response lost"));
    getAgentAdaptersMock.mockResolvedValue({
      object: "agent_adapters",
      data: [adapterWithExecutableTrust(approvedOtherIdentity)],
    });
    const notices: Array<[string, string]> = [];
    const { result } = renderHook(
      () =>
        useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
      { wrapper: UnapprovedWrapper },
    );

    let succeeded = true;
    await act(async () => {
      succeeded = await result.current.approveAgentAdapterExecutable(
        "codex",
        executableIdentity.identity_token,
      );
    });

    expect(succeeded).toBe(false);
    expect(notices).toEqual([["error", "response lost"]]);
  });

  it.each([
    [
      "changed",
      changedExecutableTrust,
      "The external agent app changed before approval could be confirmed. Review and approve its current identity.",
    ],
    [
      "unavailable",
      unavailableExecutableTrust,
      "The external agent app became unavailable before approval could be confirmed. Refresh discovery and try again.",
    ],
  ] as const)(
    "does not report success when the approval response is already %s",
    async (_state, trust, expectedMessage) => {
      approveAgentAdapterExecutableMock.mockResolvedValue({
        object: "agent_adapter_executable_trust",
        data: trust,
      });
      getAgentAdaptersMock.mockResolvedValue({
        object: "agent_adapters",
        data: [adapterWithExecutableTrust(trust)],
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

      let succeeded = true;
      await act(async () => {
        succeeded = await result.current.adapterActions.approveAgentAdapterExecutable(
          "codex",
          executableIdentity.identity_token,
        );
      });

      expect(succeeded).toBe(false);
      expect(result.current.providersAndModels.state.agentAdapters[0]?.executable_trust).toEqual(
        trust,
      );
      expect(notices).toEqual([["error", expectedMessage]]);
    },
  );

  it.each([
    [
      "changed",
      changedExecutableTrust,
      "The external agent app changed before approval could be confirmed. Review and approve its current identity.",
    ],
    [
      "unavailable",
      unavailableExecutableTrust,
      "The external agent app became unavailable before approval could be confirmed. Refresh discovery and try again.",
    ],
  ] as const)(
    "uses the refreshed %s state instead of an earlier approved response",
    async (_state, refreshedTrust, expectedMessage) => {
      const approved = {
        schema_version: "hecate.external-agent-executable.v1",
        state: "approved" as const,
        reason: "operator_approved",
        current: executableIdentity,
        approved: executableIdentity,
      };
      approveAgentAdapterExecutableMock.mockResolvedValue({
        object: "agent_adapter_executable_trust",
        data: approved,
      });
      getAgentAdaptersMock.mockResolvedValue({
        object: "agent_adapters",
        data: [adapterWithExecutableTrust(refreshedTrust)],
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

      let succeeded = true;
      await act(async () => {
        succeeded = await result.current.adapterActions.approveAgentAdapterExecutable(
          "codex",
          executableIdentity.identity_token,
        );
      });

      expect(succeeded).toBe(false);
      expect(result.current.providersAndModels.state.agentAdapters[0]?.executable_trust).toEqual(
        refreshedTrust,
      );
      expect(notices).toEqual([["error", expectedMessage]]);
    },
  );

  it("does not trust an approved refresh response superseded by newer committed evidence", async () => {
    const approved = {
      schema_version: "hecate.external-agent-executable.v1",
      state: "approved" as const,
      reason: "operator_approved",
      current: executableIdentity,
      approved: executableIdentity,
    };
    let resolveApprovalRefresh: (value: unknown) => void = () => {};
    let resolveNewerRefresh: (value: unknown) => void = () => {};
    approveAgentAdapterExecutableMock.mockResolvedValue({
      object: "agent_adapter_executable_trust",
      data: approved,
    });
    getAgentAdaptersMock
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveApprovalRefresh = resolve;
        }),
      )
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveNewerRefresh = resolve;
        }),
      );
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

    let approvalSucceeded = true;
    let approvalRequest!: Promise<void>;
    act(() => {
      approvalRequest = result.current.adapterActions
        .approveAgentAdapterExecutable("codex", executableIdentity.identity_token)
        .then((succeeded) => {
          approvalSucceeded = succeeded;
        });
    });
    await waitFor(() => expect(getAgentAdaptersMock).toHaveBeenCalledTimes(1));

    let newerRefresh!: Promise<unknown>;
    act(() => {
      newerRefresh = result.current.providersAndModels.actions.refreshAgentAdapters();
    });
    await waitFor(() => expect(getAgentAdaptersMock).toHaveBeenCalledTimes(2));
    await act(async () => {
      resolveNewerRefresh({
        object: "agent_adapters",
        data: [adapterWithExecutableTrust(changedExecutableTrust)],
      });
      await newerRefresh;
      resolveApprovalRefresh({
        object: "agent_adapters",
        data: [adapterWithExecutableTrust(approved)],
      });
      await approvalRequest;
    });

    expect(approvalSucceeded).toBe(false);
    expect(result.current.providersAndModels.state.agentAdapters[0]?.executable_trust).toEqual(
      changedExecutableTrust,
    );
    expect(notices).toEqual([
      [
        "error",
        "The external agent app changed before approval could be confirmed. Review and approve its current identity.",
      ],
    ]);
  });

  it("uses a newer matching catalog when it supersedes the approval refresh", async () => {
    const approved = {
      schema_version: "hecate.external-agent-executable.v1",
      state: "approved" as const,
      reason: "operator_approved",
      current: executableIdentity,
      approved: executableIdentity,
    };
    const approvalRefresh = deferredValue();
    const newerRefresh = deferredValue();
    approveAgentAdapterExecutableMock.mockResolvedValue({
      object: "agent_adapter_executable_trust",
      data: approved,
    });
    getAgentAdaptersMock
      .mockReturnValueOnce(approvalRefresh.promise)
      .mockReturnValueOnce(newerRefresh.promise);
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

    let approval!: Promise<boolean>;
    act(() => {
      approval = result.current.adapterActions.approveAgentAdapterExecutable(
        "codex",
        executableIdentity.identity_token,
      );
    });
    await waitFor(() => expect(getAgentAdaptersMock).toHaveBeenCalledTimes(1));

    let newer!: Promise<unknown>;
    act(() => {
      newer = result.current.providersAndModels.actions.refreshAgentAdapters();
    });
    await waitFor(() => expect(getAgentAdaptersMock).toHaveBeenCalledTimes(2));

    let succeeded = false;
    await act(async () => {
      newerRefresh.resolve({
        object: "agent_adapters",
        data: [adapterWithExecutableTrust(approved)],
      });
      await newer;
      approvalRefresh.resolve({
        object: "agent_adapters",
        data: [adapterWithExecutableTrust(changedExecutableTrust)],
      });
      succeeded = await approval;
    });

    expect(succeeded).toBe(true);
    expect(result.current.providersAndModels.state.agentAdapters[0]?.executable_trust).toEqual(
      approved,
    );
    expect(notices).toEqual([["success", "External agent app approved."]]);
  });

  it("uses a newer matching catalog after an approval response is lost", async () => {
    const approved = {
      schema_version: "hecate.external-agent-executable.v1",
      state: "approved" as const,
      reason: "operator_approved",
      current: executableIdentity,
      approved: executableIdentity,
    };
    const approvalRefresh = deferredValue();
    const newerRefresh = deferredValue();
    approveAgentAdapterExecutableMock.mockRejectedValue(new Error("response lost"));
    getAgentAdaptersMock
      .mockReturnValueOnce(approvalRefresh.promise)
      .mockReturnValueOnce(newerRefresh.promise);
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

    let approval!: Promise<boolean>;
    act(() => {
      approval = result.current.adapterActions.approveAgentAdapterExecutable(
        "codex",
        executableIdentity.identity_token,
      );
    });
    await waitFor(() => expect(getAgentAdaptersMock).toHaveBeenCalledTimes(1));

    let newer!: Promise<unknown>;
    act(() => {
      newer = result.current.providersAndModels.actions.refreshAgentAdapters();
    });
    await waitFor(() => expect(getAgentAdaptersMock).toHaveBeenCalledTimes(2));

    let succeeded = false;
    await act(async () => {
      newerRefresh.resolve({
        object: "agent_adapters",
        data: [adapterWithExecutableTrust(approved)],
      });
      await newer;
      approvalRefresh.resolve({
        object: "agent_adapters",
        data: [adapterWithExecutableTrust(changedExecutableTrust)],
      });
      succeeded = await approval;
    });

    expect(succeeded).toBe(true);
    expect(notices).toEqual([["success", "External agent app approved."]]);
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

  it("reconciles executable trust when a revoke response is lost after commit", async () => {
    revokeAgentAdapterExecutableMock.mockRejectedValue(new Error("response lost"));
    const unapproved = {
      schema_version: "hecate.external-agent-executable.v1",
      state: "unapproved" as const,
      reason: "approval_required",
      current: executableIdentity,
    };
    getAgentAdaptersMock.mockResolvedValue({
      object: "agent_adapters",
      data: [adapterWithExecutableTrust(unapproved)],
    });
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

    let revoked = true;
    await act(async () => {
      revoked = await result.current.adapterActions.revokeAgentAdapterExecutable("codex");
    });

    expect(revoked).toBe(true);
    expect(getAgentAdaptersMock).toHaveBeenCalledTimes(1);
    expect(result.current.providersAndModels.state.agentAdapters[0]?.executable_trust).toEqual(
      unapproved,
    );
    expect(notices).toEqual([["success", "External agent app approval revoked."]]);
  });

  it.each([
    [
      "does not infer deletion when the app also becomes unavailable",
      unavailableExecutableTrust,
      false,
      ["error", "response lost"],
    ],
    [
      "retains failure when an unavailable app still has an approval",
      { ...unavailableExecutableTrust, approved: executableIdentity },
      false,
      ["error", "response lost"],
    ],
  ] as const)("%s", async (_name, trust, wantSuccess, expectedNotice) => {
    revokeAgentAdapterExecutableMock.mockRejectedValue(new Error("response lost"));
    getAgentAdaptersMock.mockResolvedValue({
      object: "agent_adapters",
      data: [adapterWithExecutableTrust(trust)],
    });
    const notices: Array<[string, string]> = [];
    const { result } = renderHook(
      () =>
        useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
      { wrapper: ApprovedIdentityWrapper },
    );

    let succeeded = !wantSuccess;
    await act(async () => {
      succeeded = await result.current.revokeAgentAdapterExecutable("codex");
    });

    expect(succeeded).toBe(wantSuccess);
    expect(notices).toEqual([expectedNotice]);
  });

  it("uses a newer matching catalog after a revoke response is lost", async () => {
    const unapproved = {
      schema_version: "hecate.external-agent-executable.v1",
      state: "unapproved" as const,
      reason: "approval_required",
      current: executableIdentity,
    };
    const revokeRefresh = deferredValue();
    const newerRefresh = deferredValue();
    revokeAgentAdapterExecutableMock.mockRejectedValue(new Error("response lost"));
    getAgentAdaptersMock
      .mockReturnValueOnce(revokeRefresh.promise)
      .mockReturnValueOnce(newerRefresh.promise);
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

    let revoke!: Promise<boolean>;
    act(() => {
      revoke = result.current.adapterActions.revokeAgentAdapterExecutable("codex");
    });
    await waitFor(() => expect(getAgentAdaptersMock).toHaveBeenCalledTimes(1));

    let newer!: Promise<unknown>;
    act(() => {
      newer = result.current.providersAndModels.actions.refreshAgentAdapters();
    });
    await waitFor(() => expect(getAgentAdaptersMock).toHaveBeenCalledTimes(2));

    let succeeded = false;
    await act(async () => {
      newerRefresh.resolve({
        object: "agent_adapters",
        data: [adapterWithExecutableTrust(unapproved)],
      });
      await newer;
      revokeRefresh.resolve({
        object: "agent_adapters",
        data: [adapterWithExecutableTrust(changedExecutableTrust)],
      });
      succeeded = await revoke;
    });

    expect(succeeded).toBe(true);
    expect(result.current.providersAndModels.state.agentAdapters[0]?.executable_trust).toEqual(
      unapproved,
    );
    expect(notices).toEqual([["success", "External agent app approval revoked."]]);
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
