import { describe, expect, it } from "vitest";

import type { RuntimeHeaders } from "../types/runtime";
import type { ModelRecord } from "../types/model";
import type { ProviderRecord } from "../types/provider";
import type { ChatApprovalRecord, ChatSessionRecord } from "../types/chat";
import {
  approvalRecordToPending,
  buildAssistantToolCallMessage,
  buildSyntheticChatResult,
  defaultModelForProvider,
  defaultProviderForChat,
  deriveChatSessionTitle,
  humanizeChatError,
  isModelValidForProvider,
  providerHasChatRouteEvidence,
  renderChatSessionSummary,
  withConfiguredDefaultModels,
} from "./runtimeConsoleChatHelpers";

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
    expect(
      humanizeChatError("api key is required for cloud provider openai when stub mode is disabled"),
    ).toBe("openai has no API key. Open Connections and add one.");
  });

  it("rewrites common chat runtime failures into operator-actionable copy", () => {
    expect(humanizeChatError("Hecate Chat is already running for this chat session.")).toBe(
      "Hecate Chat is still working on this task. Open the task, resolve approval, or stop it before sending another message.",
    );
    expect(humanizeChatError("workspace is required")).toBe(
      "Choose a workspace before using Hecate Chat tools or External Agent.",
    );
    expect(humanizeChatError("model does not support tools")).toBe(
      "This model is not marked as tool-capable. Hecate will send directly; choose a tool-capable model for task-backed turns.",
    );
    expect(
      humanizeChatError('route request: no provider supports explicit model "gpt-5.4-mini"'),
    ).toBe(
      "No configured provider can route to gpt-5.4-mini. Choose another model or open Connections to repair provider readiness.",
    );
    expect(humanizeChatError("no routable model for selected provider")).toBe(
      "No routable model is available. Choose another model or open Connections to add a provider, discover models, or check provider health.",
    );
    expect(humanizeChatError("Authentication required. Please run 'agent login' first.")).toBe(
      "The selected runtime is not signed in. Open Connections to repair or test readiness.",
    );
    expect(humanizeChatError("Internal error: Credit balance is too low")).toBe(
      "The selected runtime reported a billing or credit problem. Check its account, subscription, or API key balance.",
    );
    expect(humanizeChatError("connect: connection refused")).toBe(
      "The selected provider is not reachable. Start the local provider app or check its endpoint URL.",
    );
    expect(humanizeChatError("upstream returned 401")).toBe(
      "The selected provider rejected the request with HTTP 401. Check credentials and account access.",
    );
    expect(humanizeChatError("upstream returned 502")).toBe(
      "The selected provider returned HTTP 502. Check that the provider is running and reachable.",
    );
    expect(humanizeChatError("upstream timeout")).toBe(
      "The selected provider did not respond before the timeout. Check that it is running, reachable, and not overloaded.",
    );
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

  it("uses null content when no assistant content exists", () => {
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
    expect(defaultModelForProvider("auto", [], [])).toBe("");
  });

  it("prefers the provider's declared default_model when set", () => {
    const providers = [provider({ name: "openai", default_model: "gpt-4o-mini" })];
    expect(defaultModelForProvider("openai", [], providers)).toBe("gpt-4o-mini");
  });

  it("falls back to the metadata-tagged default model in the model list", () => {
    const models: ModelRecord[] = [
      model({ id: "gpt-4o", metadata: { provider: "openai" } }),
      model({ id: "gpt-4o-mini", metadata: { provider: "openai", default: true } }),
    ];
    const providers = [provider({ name: "openai" })];
    expect(defaultModelForProvider("openai", models, providers)).toBe("gpt-4o-mini");
  });

  it("does not treat catalog preset defaults as routable models", () => {
    expect(defaultModelForProvider("anthropic", [], [])).toBe("");
  });

  it("falls back to the first reported model when no provider default is reported", () => {
    const models: ModelRecord[] = [
      model({ id: "model-a", metadata: { provider: "openai" } }),
      model({ id: "model-b", metadata: { provider: "openai" } }),
    ];
    const providers = [provider({ name: "openai", models: ["model-a", "model-b"] })];
    expect(defaultModelForProvider("openai", models, providers)).toBe("model-a");
  });

  it("falls back to the provider status model list when catalog models are not loaded", () => {
    const providers = [provider({ name: "ollama", models: ["llama3.1:8b"] })];
    expect(defaultModelForProvider("ollama", [], providers)).toBe("llama3.1:8b");
  });

  it("falls back to configured provider defaults before discovery reports models", () => {
    const configured = [
      {
        id: "fireworks",
        name: "fireworks",
        kind: "cloud",
        protocol: "openai",
        base_url: "",
        default_model: "accounts/fireworks/models/deepseek-v3p1",
        credential_configured: true,
      },
    ];

    expect(defaultModelForProvider("fireworks", [], [], configured, [])).toBe(
      "accounts/fireworks/models/deepseek-v3p1",
    );
  });

  it("matches discovered models when configured provider ids differ from runtime ids", () => {
    const configured = [
      {
        id: "fireworks-ai",
        name: "fireworks",
        preset_id: "fireworks",
        kind: "cloud",
        protocol: "openai",
        base_url: "",
        default_model: "accounts/fireworks/models/deepseek-v3p1",
        credential_configured: true,
      },
    ];
    const models = [
      model({
        id: "accounts/fireworks/models/llama-v3p1-405b",
        metadata: { provider: "fireworks", default: true },
      }),
    ];
    const providers = [
      provider({
        name: "fireworks",
        default_model: "accounts/fireworks/models/llama-v3p1-405b",
      }),
    ];

    expect(defaultModelForProvider("fireworks-ai", models, providers, configured, [])).toBe(
      "accounts/fireworks/models/llama-v3p1-405b",
    );
  });
});

describe("defaultProviderForChat", () => {
  it("prefers a configured provider with a discovered default model", () => {
    const models: ModelRecord[] = [
      model({ id: "smollm2", owned_by: "ollama", metadata: { provider: "ollama" } }),
      model({
        id: "gpt-4o-mini",
        owned_by: "openai",
        metadata: { provider: "openai", default: true },
      }),
    ];
    const configured = [
      {
        id: "ollama",
        name: "Ollama",
        kind: "local",
        protocol: "openai",
        base_url: "",
        credential_configured: true,
      },
      {
        id: "openai",
        name: "OpenAI",
        kind: "cloud",
        protocol: "openai",
        base_url: "",
        credential_configured: true,
      },
    ];

    expect(defaultProviderForChat(models, configured, [])).toBe("openai");
  });

  it("ignores cloud providers without credentials when picking a default", () => {
    const models: ModelRecord[] = [
      model({
        id: "gpt-4o-mini",
        owned_by: "openai",
        metadata: { provider: "openai", default: true },
      }),
      model({ id: "llama3", owned_by: "ollama", metadata: { provider: "ollama" } }),
    ];
    const configured = [
      {
        id: "openai",
        name: "OpenAI",
        kind: "cloud",
        protocol: "openai",
        base_url: "",
        credential_configured: false,
      },
      {
        id: "ollama",
        name: "Ollama",
        kind: "local",
        protocol: "openai",
        base_url: "",
        credential_configured: true,
      },
    ];

    expect(defaultProviderForChat(models, configured, [])).toBe("ollama");
  });

  it("falls back to the configured provider when no model has been discovered yet", () => {
    const configured = [
      {
        id: "lmstudio",
        name: "LM Studio",
        kind: "local",
        protocol: "openai",
        base_url: "",
        credential_configured: true,
      },
    ];

    expect(defaultProviderForChat([], configured, [])).toBe("lmstudio");
  });

  it("returns the canonical route key for configured providers with custom ids", () => {
    const configured = [
      {
        id: "fireworks-ai",
        name: "fireworks",
        preset_id: "fireworks",
        kind: "cloud",
        protocol: "openai",
        base_url: "",
        credential_configured: true,
      },
    ];
    const models = [
      model({
        id: "accounts/fireworks/models/llama-v3p1-405b",
        metadata: { provider: "fireworks", default: true },
      }),
    ];

    expect(defaultProviderForChat(models, configured, [])).toBe("fireworks");
  });
});

describe("isModelValidForProvider", () => {
  it("returns true for any model under the auto router", () => {
    expect(isModelValidForProvider("anything", "auto", [], [])).toBe(true);
  });

  it("matches when the model carries the provider in its metadata", () => {
    const models: ModelRecord[] = [model({ id: "gpt-4o", metadata: { provider: "openai" } })];
    expect(isModelValidForProvider("gpt-4o", "openai", models, [])).toBe(true);
  });

  it("matches when the provider record explicitly lists the model", () => {
    const providers = [provider({ name: "openai", models: ["gpt-4o"] })];
    expect(isModelValidForProvider("gpt-4o", "openai", [], providers)).toBe(true);
  });

  it("rejects models not listed by a provider record", () => {
    const providers = [provider({ name: "openai", default_model: "gpt-4o", models: ["gpt-4o"] })];
    expect(isModelValidForProvider("gpt-3.5", "openai", [], providers)).toBe(false);
  });

  it("accepts configured provider defaults before discovery reports models", () => {
    const configured = [
      {
        id: "fireworks",
        name: "fireworks",
        kind: "cloud",
        protocol: "openai",
        base_url: "",
        default_model: "accounts/fireworks/models/deepseek-v3p1",
        credential_configured: true,
      },
    ];

    expect(
      isModelValidForProvider(
        "accounts/fireworks/models/deepseek-v3p1",
        "fireworks",
        [],
        [],
        configured,
        [],
      ),
    ).toBe(true);
  });

  it("accepts discovered models through configured provider aliases", () => {
    const configured = [
      {
        id: "fireworks-ai",
        name: "fireworks",
        preset_id: "fireworks",
        kind: "cloud",
        protocol: "openai",
        base_url: "",
        credential_configured: true,
      },
    ];
    const models = [
      model({
        id: "accounts/fireworks/models/llama-v3p1-405b",
        metadata: { provider: "fireworks" },
      }),
    ];

    expect(
      isModelValidForProvider(
        "accounts/fireworks/models/llama-v3p1-405b",
        "fireworks-ai",
        models,
        [],
        configured,
        [],
      ),
    ).toBe(true);
  });
});

describe("withConfiguredDefaultModels", () => {
  it("adds configured default models when discovery has not reported them yet", () => {
    const configured = [
      {
        id: "fireworks",
        name: "fireworks",
        kind: "cloud",
        protocol: "openai",
        base_url: "",
        default_model: "accounts/fireworks/models/deepseek-v3p1",
        credential_configured: true,
      },
    ];

    expect(withConfiguredDefaultModels([], "fireworks", configured, [])).toEqual([
      {
        id: "accounts/fireworks/models/deepseek-v3p1",
        owned_by: "fireworks",
        metadata: {
          provider: "fireworks",
          provider_kind: "cloud",
          default: true,
          discovery_source: "configured_default",
        },
      },
    ]);
  });

  it("uses preset defaults when configured providers inherit the preset model", () => {
    const configured = [
      {
        id: "fireworks",
        name: "fireworks",
        preset_id: "fireworks",
        kind: "cloud",
        protocol: "openai",
        base_url: "",
        credential_configured: true,
      },
    ];
    const presets = [
      {
        id: "fireworks",
        name: "Fireworks AI",
        kind: "cloud",
        protocol: "openai",
        base_url: "https://api.fireworks.ai/inference/v1",
        default_model: "accounts/fireworks/models/deepseek-v3p1",
      },
    ];

    expect(withConfiguredDefaultModels([], "fireworks", configured, presets)).toHaveLength(1);
    expect(withConfiguredDefaultModels([], "fireworks", configured, presets)[0].id).toBe(
      "accounts/fireworks/models/deepseek-v3p1",
    );
  });

  it("adds configured default models under the canonical route key for custom ids", () => {
    const configured = [
      {
        id: "fireworks-ai",
        name: "fireworks",
        preset_id: "fireworks",
        kind: "cloud",
        protocol: "openai",
        base_url: "",
        default_model: "accounts/fireworks/models/deepseek-v3p1",
        credential_configured: true,
      },
    ];

    expect(withConfiguredDefaultModels([], "fireworks", configured, [])).toEqual([
      {
        id: "accounts/fireworks/models/deepseek-v3p1",
        owned_by: "fireworks",
        metadata: {
          provider: "fireworks",
          provider_kind: "cloud",
          default: true,
          discovery_source: "configured_default",
        },
      },
    ]);
  });
});

describe("providerHasChatRouteEvidence", () => {
  it("does not count catalog presets as live provider evidence", () => {
    expect(providerHasChatRouteEvidence("ollama", [], [], [])).toBe(false);
  });

  it("accepts configured providers, discovered models, and provider status rows", () => {
    expect(
      providerHasChatRouteEvidence(
        "ollama",
        [],
        [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            credential_configured: false,
          },
        ],
        [],
      ),
    ).toBe(true);
    expect(
      providerHasChatRouteEvidence(
        "ollama",
        [model({ id: "llama3.1:8b", metadata: { provider: "ollama" } })],
        [],
        [],
      ),
    ).toBe(true);
    expect(providerHasChatRouteEvidence("ollama", [], [], [provider({ name: "ollama" })])).toBe(
      true,
    );
  });

  it("accepts runtime and model evidence through configured provider aliases", () => {
    const configured = [
      {
        id: "fireworks-ai",
        name: "fireworks",
        preset_id: "fireworks",
        kind: "cloud",
        protocol: "openai",
        base_url: "https://api.fireworks.ai/inference/v1",
        credential_configured: true,
      },
    ];

    expect(
      providerHasChatRouteEvidence(
        "fireworks-ai",
        [
          model({
            id: "accounts/fireworks/models/llama-v3p1-405b",
            metadata: { provider: "fireworks" },
          }),
        ],
        configured,
        [provider({ name: "fireworks" })],
      ),
    ).toBe(true);
  });
});

describe("renderChatSessionSummary", () => {
  it("counts messages and forwards adapter/workspace metadata", () => {
    const session: ChatSessionRecord = {
      id: "ac1",
      title: "agent t",
      agent_id: "codex",
      project_id: "proj_1",
      driver_kind: "acp",
      native_session_id: "n1",
      workspace: "/repo",
      workspace_mode: "persistent",
      workspace_branch: "main",
      status: "running",
      messages: [
        { id: "m1", role: "user", content: "do x" },
        { id: "m2", role: "assistant", content: "done" },
      ],
    };
    const out = renderChatSessionSummary(session);
    expect(out.message_count).toBe(2);
    expect(out.agent_id).toBe("codex");
    expect(out.project_id).toBe("proj_1");
    expect(out.workspace_mode).toBe("persistent");
    expect(out.workspace_branch).toBe("main");
    expect(out.status).toBe("running");
  });
});

describe("approvalRecordToPending", () => {
  it("projects an approval row into the pending banner shape", () => {
    const approval: ChatApprovalRecord = {
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
