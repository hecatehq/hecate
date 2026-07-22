import { describe, expect, it } from "vitest";

import { resolveExternalAgentReadiness } from "./external-agent-readiness";
import type { AgentAdapterRecord } from "../types/agent-adapter";

function adapter(overrides: Partial<AgentAdapterRecord> = {}): AgentAdapterRecord {
  return {
    id: "cursor_agent",
    name: "Cursor Agent",
    kind: "acp",
    command: "cursor-agent",
    available: true,
    status: "available",
    cost_mode: "external",
    supports_authenticate: false,
    supports_logout: false,
    ...overrides,
  };
}

describe("resolveExternalAgentReadiness", () => {
  it("lets a ready probe override stale auth metadata", () => {
    const readiness = resolveExternalAgentReadiness(
      adapter({ auth_status: "unauthenticated", auth_error: "old auth error" }),
      {
        adapter_id: "cursor_agent",
        status: "ready",
        stage: "session",
        duration_ms: 200,
      },
    );

    expect(readiness).toMatchObject({
      kind: "ready",
      label: "checked",
      needsRepair: false,
      launchBlocked: false,
      authStatus: "ok",
      authError: "",
      verifiedByProbe: true,
    });
  });

  it("shows cached auth diagnostics without blocking a currently discovered agent", () => {
    const readiness = resolveExternalAgentReadiness(adapter(), {
      adapter_id: "cursor_agent",
      status: "auth_required",
      stage: "initialize",
      hint: "Run cursor-agent login.",
      duration_ms: 200,
    });

    expect(readiness).toMatchObject({
      kind: "sign_in",
      loginCommand: "cursor-agent login",
      detail: "Run cursor-agent login.",
      needsRepair: true,
      launchBlocked: false,
      verifiedByProbe: false,
    });
  });

  it.each([
    {
      status: "error",
      stage: "initialize",
      hint: "The last diagnostic failed.",
      expectedKind: "issue",
    },
    {
      status: "not_installed",
      stage: "resolve",
      hint: "The executable was missing during the last diagnostic.",
      expectedKind: "setup",
    },
  ])(
    "keeps a currently discovered agent launchable after a cached $status diagnostic",
    ({ status, stage, hint, expectedKind }) => {
      const readiness = resolveExternalAgentReadiness(adapter(), {
        adapter_id: "cursor_agent",
        status,
        stage,
        hint,
        duration_ms: 200,
      });

      expect(readiness).toMatchObject({
        kind: expectedKind,
        needsRepair: true,
        launchBlocked: false,
        verifiedByProbe: false,
      });
    },
  );

  it("uses sign-in guidance instead of install copy for installed unauthenticated agents", () => {
    const readiness = resolveExternalAgentReadiness(
      adapter({ auth_status: "unauthenticated", auth_error: "" }),
      null,
    );

    expect(readiness).toMatchObject({
      kind: "sign_in",
      detail:
        "Run cursor-agent login, or set CURSOR_API_KEY for the adapter environment, then retry the chat.",
      needsRepair: true,
      launchBlocked: false,
    });
    expect(readiness.detail).not.toContain("Install");
  });

  it("includes Claude Code token alternatives in sign-in guidance", () => {
    const readiness = resolveExternalAgentReadiness(
      adapter({
        id: "claude_code",
        name: "Claude Code",
        command: "claude",
        auth_status: "unauthenticated",
        auth_error: "",
      }),
      null,
    );

    expect(readiness).toMatchObject({
      kind: "sign_in",
      detail:
        "Run claude /login in Terminal, or set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN for the adapter environment, then retry the chat.",
      needsRepair: true,
      launchBlocked: false,
    });
  });

  it("explains that the Claude ACP adapter is built in", () => {
    const readiness = resolveExternalAgentReadiness(
      adapter({
        id: "claude_code",
        name: "Claude Code",
        command: "claude",
        available: false,
        status: "missing",
      }),
      null,
    );

    expect(readiness).toMatchObject({
      kind: "setup",
      needsRepair: true,
      launchBlocked: true,
      verifiedByProbe: false,
    });
    expect(readiness.setupHint).toContain("separately");
    expect(readiness.setupHint).toContain("standard install locations and PATH");
    expect(readiness.setupHint).toContain("ACP adapter is built in");
    expect(readiness.setupHint).toContain("claude /login");
  });

  it("labels an unprobed discovered agent available and explains launch-time verification", () => {
    const readiness = resolveExternalAgentReadiness(adapter({ auth_status: "unknown" }), null);

    expect(readiness).toMatchObject({
      kind: "unverified",
      label: "available",
      tone: "muted",
      needsRepair: false,
      launchBlocked: false,
      verifiedByProbe: false,
    });
    expect(readiness.detail).toContain("Starting a chat launches the installed app");
    expect(readiness.detail).toContain("verifies its ACP connection");
    expect(readiness.detail).toContain("Diagnostics are optional");
  });

  it("does not let a stale ready diagnostic override current passive discovery", () => {
    const readiness = resolveExternalAgentReadiness(
      adapter({ available: false, status: "missing" }),
      {
        adapter_id: "cursor_agent",
        status: "ready",
        stage: "session",
        duration_ms: 200,
      },
    );

    expect(readiness).toMatchObject({
      kind: "setup",
      launchBlocked: true,
      verifiedByProbe: false,
    });
  });

  it("keeps a failed current remote credential gate authoritative over a stale ready diagnostic", () => {
    const readiness = resolveExternalAgentReadiness(
      adapter({
        remote_credential_mode: "api_key",
        remote_credential_ok: false,
        remote_credential_hint: "Set the credential in the runtime environment.",
      }),
      {
        adapter_id: "cursor_agent",
        status: "ready",
        stage: "session",
        duration_ms: 200,
      },
    );

    expect(readiness).toMatchObject({
      kind: "sign_in",
      label: "credential",
      launchBlocked: true,
      detail: "Set the credential in the runtime environment.",
      verifiedByProbe: false,
    });
    expect(readiness.kind).not.toBe("ready");
  });

  it("uses adapter-specific setup guidance for Grok Build", () => {
    const readiness = resolveExternalAgentReadiness(
      adapter({
        id: "grok_build",
        name: "Grok Build",
        command: "grok",
        available: false,
        status: "missing",
      }),
      null,
    );

    expect(readiness).toMatchObject({
      kind: "setup",
      loginCommand: "grok login",
      needsRepair: true,
      launchBlocked: true,
    });
    expect(readiness.setupHint).toContain("Grok CLI");
    expect(readiness.setupHint).toContain("model selected");
  });
});
