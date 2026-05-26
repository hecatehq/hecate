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
      label: "ready",
      needsRepair: false,
      authStatus: "ok",
      authError: "",
      verifiedByProbe: true,
    });
  });

  it("turns auth probe failures into a sign-in repair", () => {
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
    });
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
    });
    expect(readiness.setupHint).toContain("Grok CLI");
    expect(readiness.setupHint).toContain("model selected");
  });
});
