import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { UsageView } from "./UsageView";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";

const localSession = { label: "Local" };

function setup(stateOverrides: Record<string, unknown> = {}) {
  const state = createRuntimeConsoleFixture({ session: localSession, ...stateOverrides });
  const actions = createRuntimeConsoleActions();
  render(withRuntimeConsole(<UsageView />, { state, actions }));
}

describe("UsageView", () => {
  it("renders Usage as the primary surface", () => {
    setup({ usageSummary: null, usageEvents: [] });

    expect(screen.getByText("Usage")).toBeInTheDocument();
    expect(screen.getByText(/Cloud-provider token usage measured by Hecate/i)).toBeInTheDocument();
    expect(screen.getByText(/No cloud usage recorded yet/i)).toBeInTheDocument();
    expect(
      screen.getByText(/Local models do not spend cloud-provider tokens/i),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Budget guardrail/i)).toBeNull();
    expect(screen.queryByText(/balance/i)).toBeNull();
    expect(screen.queryByText(/top up/i)).toBeNull();
    expect(screen.queryByText(/reset/i)).toBeNull();
    expect(screen.queryByText(/External agent context/i)).toBeNull();
    expect(screen.queryByText(/No agent-reported usage/i)).toBeNull();
  });

  it("aggregates cloud-provider tokens and hides local-provider usage rows", () => {
    setup({
      settingsConfig: {
        backend: "memory",
        policy_rules: [],
        events: [],
        providers: [
          { id: "openai", name: "OpenAI", kind: "openai", enabled: true },
          { id: "ollama", name: "Ollama", kind: "local", enabled: true },
        ],
      },
      usageEvents: [
        {
          type: "usage",
          request_id: "cloud-request",
          timestamp: "2026-04-25T10:00:00Z",
          provider: "openai",
          model: "gpt-5.4-mini",
          prompt_tokens: 80,
          completion_tokens: 50,
          total_tokens: 130,
          amount_micros_usd: 123_000,
          amount_usd: "$0.123",
        },
        {
          type: "usage",
          request_id: "local-request",
          timestamp: "2026-04-25T10:01:00Z",
          provider: "ollama",
          model: "ministral-3:latest",
          prompt_tokens: 999,
          completion_tokens: 999,
          total_tokens: 1_998,
          amount_micros_usd: 0,
          amount_usd: "$0.000",
        },
      ],
    });

    expect(screen.getByText("130")).toBeInTheDocument();
    expect(screen.getByText("80 prompt · 50 output")).toBeInTheDocument();
    expect(screen.getByText("Reported cloud cost")).toBeInTheDocument();
    expect(screen.getByText("Recent cloud calls")).toBeInTheDocument();
    expect(screen.getAllByText("$0.123").length).toBeGreaterThan(0);
    expect(screen.getByText("cloud-request")).toBeInTheDocument();
    expect(screen.queryByText("local-request")).toBeNull();
    expect(screen.queryByText("ministral-3:latest")).toBeNull();
  });

  it("keeps zero-cost cloud usage visible as token usage", () => {
    setup({
      settingsConfig: {
        backend: "memory",
        policy_rules: [],
        events: [],
        providers: [{ id: "anthropic", name: "Anthropic", kind: "anthropic", enabled: true }],
      },
      usageEvents: [
        {
          type: "usage",
          request_id: "zero-cost-cloud-request",
          timestamp: "2026-04-25T10:00:00Z",
          provider: "anthropic",
          model: "claude-sonnet-4.5",
          prompt_tokens: 33,
          completion_tokens: 11,
          total_tokens: 44,
          amount_micros_usd: 0,
          amount_usd: "$0.000",
        },
      ],
    });

    expect(screen.getByText("44")).toBeInTheDocument();
    expect(screen.getByText("33 prompt · 11 output")).toBeInTheDocument();
    expect(screen.getByText("zero-cost-cloud-request")).toBeInTheDocument();
    expect(screen.getAllByText("$0.000").length).toBeGreaterThan(0);
  });

  it("keeps active external-agent usage out of the global usage page", () => {
    setup({
      activeChatSession: {
        id: "chat_1",
        title: "Codex work",
        adapter_id: "codex",
        workspace: "/tmp/project",
        status: "completed",
        messages: [
          {
            id: "old",
            role: "assistant",
            content: "old",
            usage: { context_used: 100, context_size: 10_000 },
          },
          {
            id: "latest",
            role: "assistant",
            content: "latest",
            usage: {
              context_used: 12_345,
              context_size: 258_400,
              reported_cost_amount: "0.42",
              reported_cost_currency: "USD",
            },
          },
        ],
      },
    });

    expect(screen.queryByText("12,345 / 258,400")).toBeNull();
    expect(screen.queryByText("0.42 USD")).toBeNull();
    expect(screen.queryByText("Active external-agent usage")).toBeNull();
    expect(screen.getByText(/external-agent usage is shown in the chat/i)).toBeInTheDocument();
  });
});
