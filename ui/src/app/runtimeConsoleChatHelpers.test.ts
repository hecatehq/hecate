import { describe, expect, it } from "vitest";

import type {
  AgentChatApprovalRecord,
  AgentChatSessionRecord,
  ChatProviderCallRecord,
  ChatSessionMessageRecord,
  ChatSessionRecord,
  ModelRecord,
  ProviderPresetRecord,
  ProviderRecord,
  RuntimeHeaders,
} from "../types/runtime";
import {
  approvalRecordToPending,
  buildAssistantToolCallMessage,
  buildMessagesForSubmission,
  buildSyntheticChatResult,
  defaultModelForProvider,
  deriveChatSessionTitle,
  humanizeChatError,
  isModelValidForProvider,
  renderAgentChatSessionSummary,
  renderChatSessionSummary,
} from "./runtimeConsoleChatHelpers";

let seq = 0;
function persistedMessage(overrides: Partial<ChatSessionMessageRecord> = {}): ChatSessionMessageRecord {
  return {
    id: `m${++seq}`,
    sequence: seq,
    role: "user",
    content: "",
    ...overrides,
  };
}

function providerCall(overrides: Partial<ChatProviderCallRecord> = {}): ChatProviderCallRecord {
  return {
    id: "c1",
    request_id: "r1",
    provider: "openai",
    model: "gpt-4o",
    cost_micros_usd: 0,
    cost_usd: "0",
    prompt_tokens: 0,
    completion_tokens: 0,
    total_tokens: 0,
    ...overrides,
  };
}

function emptyRuntimeHeaders(overrides: Partial<RuntimeHeaders> = {}): RuntimeHeaders {
  return {
    requestId: "",
    traceId: "",
    spanId: "",
    provider: "",
    providerKind: "",
    routeReason: "",
    requestedModel: "",
    resolvedModel: "",
    attempts: "",
    retries: "",
    fallbackFrom: "",
    costUsd: "",
    ...overrides,
  };
}

function model(overrides: Partial<ModelRecord> = {}): ModelRecord {
  return {
    id: overrides.id ?? "gpt-4o",
    owned_by: overrides.owned_by ?? "openai",
    metadata: overrides.metadata,
  };
}

function provider(overrides: Partial<ProviderRecord> = {}): ProviderRecord {
  return {
    name: "openai",
    kind: "openai",
    healthy: true,
    status: "ok",
    ...overrides,
  };
}

describe("humanizeChatError", () => {
  it("rewrites the missing-API-key gateway error into operator-actionable copy", () => {
    expect(humanizeChatError("api key is required for cloud provider openai when stub mode is disabled"))
      .toBe("openai has no API key. Open Connections and add one.");
  });

  it("rewrites common chat runtime failures into operator-actionable copy", () => {
    expect(humanizeChatError("Hecate Agent is already running for this chat session."))
      .toBe("Hecate Chat is still working on this task. Open the task, resolve approval, or stop it before sending another message.");
    expect(humanizeChatError("workspace is required"))
      .toBe("Choose a workspace before using Hecate Chat tools or External Agent.");
    expect(humanizeChatError("model does not support tools"))
      .toBe("This model is not marked as tool-capable. Turn tools off, test it, or enable tools in Connections → Model capabilities.");
    expect(humanizeChatError('route request: no provider supports explicit model "gpt-5.4-mini"'))
      .toBe("No configured provider can route to gpt-5.4-mini. Choose another model or open Connections to repair provider readiness.");
    expect(humanizeChatError("no routable model for selected provider"))
      .toBe("No routable model is available. Choose another model or open Connections to add a provider, discover models, or check provider health.");
    expect(humanizeChatError("Authentication required. Please run 'agent login' first."))
      .toBe("The selected runtime is not signed in. Open Connections to repair or test readiness.");
    expect(humanizeChatError("Internal error: Credit balance is too low"))
      .toBe("The selected runtime reported a billing or credit problem. Check its account, subscription, or API key balance.");
    expect(humanizeChatError("connect: connection refused"))
      .toBe("The selected provider is not reachable. Start the local provider app or check its endpoint URL.");
    expect(humanizeChatError("upstream returned 401"))
      .toBe("The selected provider rejected the request with HTTP 401. Check credentials and account access.");
    expect(humanizeChatError("upstream returned 502"))
      .toBe("The selected provider returned HTTP 502. Check that the provider is running and reachable.");
    expect(humanizeChatError("upstream timeout"))
      .toBe("The selected provider did not respond before the timeout. Check that it is running, reachable, and not overloaded.");
  });

  it("returns unrelated errors verbatim", () => {
    expect(humanizeChatError("something unusual happened")).toBe("something unusual happened");
  });
});

describe("deriveChatSessionTitle", () => {
  it("returns 'New chat' for empty/whitespace input", () => {
    expect(deriveChatSessionTitle("")).toBe("New chat");
    expect(deriveChatSessionTitle("   \n\t  ")).toBe("New chat");
  });

  it("normalizes internal whitespace and uses short messages verbatim", () => {
    expect(deriveChatSessionTitle("  hello   world  ")).toBe("hello world");
  });

  it("ellipsizes messages longer than 48 characters", () => {
    const long = "a".repeat(60);
    const out = deriveChatSessionTitle(long);
    expect(out).toHaveLength(48);
    expect(out.endsWith("...")).toBe(true);
  });
});

describe("buildMessagesForSubmission", () => {
  it("prepends the system prompt and appends the new user message", () => {
    const out = buildMessagesForSubmission(null, "ping", "be terse");
    expect(out).toEqual([
      { role: "system", content: "be terse" },
      { role: "user", content: "ping" },
    ]);
  });

  it("includes prior session messages and skips pending placeholders", () => {
    const session: ChatSessionRecord = {
      id: "s1",
      title: "t",
      messages: [
        persistedMessage({ id: "m1", role: "user", content: "earlier" }),
        persistedMessage({ id: "pending-9", role: "assistant", content: "incomplete draft" }),
        persistedMessage({ id: "m2", role: "assistant", content: "earlier reply" }),
      ],
    };
    const out = buildMessagesForSubmission(session, "follow up");
    expect(out.map((m) => m.content)).toEqual(["earlier", "earlier reply", "follow up"]);
    // No system prompt prepended when none was supplied.
    expect(out[0].role).toBe("user");
  });
});

describe("buildAssistantToolCallMessage", () => {
  it("packs tool calls into the OpenAI-shaped function call array", () => {
    const out = buildAssistantToolCallMessage("partial", [
      { id: "tc1", name: "search", arguments: '{"q":"x"}' },
    ]);
    expect(out.role).toBe("assistant");
    expect(out.content).toBe("partial");
    if (out.role !== "assistant") {
      throw new Error("expected assistant message");
    }
    expect(out.tool_calls).toEqual([
      { id: "tc1", type: "function", function: { name: "search", arguments: '{"q":"x"}' } },
    ]);
  });

  it("uses null content when no draft exists", () => {
    const out = buildAssistantToolCallMessage("", []);
    expect(out.content).toBeNull();
  });
});

describe("buildSyntheticChatResult", () => {
  it("falls back to the selected model when headers don't carry a resolved model", () => {
    const out = buildSyntheticChatResult(emptyRuntimeHeaders(), "gpt-4o", "hello");
    expect(out.model).toBe("gpt-4o");
    expect(out.id).toBe("stream");
    expect(out.choices[0].message.content).toBe("hello");
  });
});

describe("defaultModelForProvider", () => {
  it("returns an empty string for the auto router", () => {
    expect(defaultModelForProvider("auto", [], [], [])).toBe("");
  });

  it("prefers the provider's declared default_model when set", () => {
    const providers = [provider({ name: "openai", default_model: "gpt-4o-mini" })];
    expect(defaultModelForProvider("openai", [], providers, [])).toBe("gpt-4o-mini");
  });

  it("falls back to the metadata-tagged default model in the model list", () => {
    const models: ModelRecord[] = [
      model({ id: "gpt-4o", metadata: { provider: "openai" } }),
      model({ id: "gpt-4o-mini", metadata: { provider: "openai", default: true } }),
    ];
    const providers = [provider({ name: "openai" })];
    expect(defaultModelForProvider("openai", models, providers, [])).toBe("gpt-4o-mini");
  });

  it("falls back to the preset default when no provider record is configured", () => {
    const presets: ProviderPresetRecord[] = [
      { id: "anthropic", name: "Anthropic", kind: "anthropic", protocol: "anthropic", base_url: "", default_model: "claude-3-5-sonnet" },
    ];
    expect(defaultModelForProvider("anthropic", [], [], presets)).toBe("claude-3-5-sonnet");
  });
});

describe("isModelValidForProvider", () => {
  it("returns true for any model under the auto router", () => {
    expect(isModelValidForProvider("anything", "auto", [], [], [])).toBe(true);
  });

  it("matches when the model carries the provider in its metadata", () => {
    const models: ModelRecord[] = [model({ id: "gpt-4o", metadata: { provider: "openai" } })];
    expect(isModelValidForProvider("gpt-4o", "openai", models, [], [])).toBe(true);
  });

  it("matches when the provider record explicitly lists the model", () => {
    const providers = [provider({ name: "openai", models: ["gpt-4o"] })];
    expect(isModelValidForProvider("gpt-4o", "openai", [], providers, [])).toBe(true);
  });

  it("rejects models not listed by a provider record", () => {
    const providers = [provider({ name: "openai", default_model: "gpt-4o", models: ["gpt-4o"] })];
    expect(isModelValidForProvider("gpt-3.5", "openai", [], providers, [])).toBe(false);
  });
});

describe("renderChatSessionSummary", () => {
  it("derives counts and the last provider-call metadata", () => {
    const session: ChatSessionRecord = {
      id: "s1",
      title: "t",
      messages: [persistedMessage({ id: "m1", role: "user", content: "hi" })],
      provider_calls: [
        providerCall({ id: "c1", model: "old", request_id: "r1", cost_usd: "0.001" }),
        providerCall({ id: "c2", model: "gpt-4o", request_id: "r2", cost_usd: "0.002" }),
      ],
    };
    const out = renderChatSessionSummary(session);
    expect(out.message_count).toBe(1);
    expect(out.provider_call_count).toBe(2);
    expect(out.last_model).toBe("gpt-4o");
    expect(out.last_cost_usd).toBe("0.002");
  });
});

describe("renderAgentChatSessionSummary", () => {
  it("counts messages and forwards adapter/workspace metadata", () => {
    const session: AgentChatSessionRecord = {
      id: "ac1",
      title: "agent t",
      adapter_id: "codex",
      driver_kind: "acp",
      native_session_id: "n1",
      workspace: "/repo",
      workspace_branch: "main",
      status: "running",
      messages: [
        { id: "m1", role: "user", content: "do x" },
        { id: "m2", role: "assistant", content: "done" },
      ],
    };
    const out = renderAgentChatSessionSummary(session);
    expect(out.message_count).toBe(2);
    expect(out.adapter_id).toBe("codex");
    expect(out.workspace_branch).toBe("main");
    expect(out.status).toBe("running");
  });
});

describe("approvalRecordToPending", () => {
  it("projects an approval row into the pending banner shape", () => {
    const approval: AgentChatApprovalRecord = {
      id: "ap_1",
      session_id: "s1",
      adapter_id: "codex",
      tool_kind: "fs",
      tool_name: "write_file",
      status: "pending",
      acp_options: [],
      scope_choices: ["once", "session"],
      created_at: "2026-04-21T10:00:00Z",
      expires_at: "2026-04-21T10:05:00Z",
    };
    const out = approvalRecordToPending(approval);
    expect(out.approval_id).toBe("ap_1");
    expect(out.scope_choices).toEqual(["once", "session"]);
    expect(out.tool_name).toBe("write_file");
  });
});
