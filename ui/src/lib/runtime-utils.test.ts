import { describe, expect, it } from "vitest";

import {
  buildSpanWaterfall,
  buildTraceTimeline,
  countRouteHealthStatuses,
  describeUsageScope,
  describeHealthStatus,
  describeRouteCandidateOutcome,
  describeRouteReason,
  describeRouteRecovery,
  describeRouteSkipReason,
  explainRouteCandidate,
  filterModelsByKind,
  filterModelsByProvider,
  findModelInTrace,
  findProviderInTrace,
  formatRelativeTime,
  formatTraceAttributeKey,
  formatTraceAttributeValue,
  findProvider,
  healthStatusTone,
  isRemoteRuntimeSession,
  parseCSV,
  providerStatusTone,
  routeOutcomeTone,
  traceStatusBadge,
  tracePhaseFromEvent,
  tracePhaseFromSpan,
} from "./runtime-utils";
import type { TraceRouteRecord } from "./runtime-utils";
import type { RuntimeHeaders } from "../types/runtime";
import type { ModelRecord } from "../types/model";
import type { ConfiguredProviderRecord, ProviderRecord } from "../types/provider";
import type { TraceListItem, TraceSpanRecord } from "../types/trace";
import type { UsageSummaryRecord } from "../types/usage";

const models: ModelRecord[] = [
  {
    id: "gpt-4o-mini",
    owned_by: "openai",
    metadata: { provider: "openai", provider_kind: "cloud" },
  },
  {
    id: "llama3.1:8b",
    owned_by: "ollama",
    metadata: { provider: "ollama", provider_kind: "local" },
  },
];

const providers: ProviderRecord[] = [
  { name: "openai", kind: "cloud", healthy: true, status: "healthy", default_model: "gpt-4o-mini" },
  { name: "ollama", kind: "local", healthy: false, status: "open", default_model: "llama3.1:8b" },
];

const runtimeHeaders: RuntimeHeaders = {
  requestId: "req-1",
  traceId: "trace-1",
  spanId: "span-1",
  provider: "ollama",
  providerKind: "local",
  routeReason: "provider_default_model",
  requestedModel: "llama3.1:8b",
  resolvedModel: "llama3.1:8b",
  attempts: "1",
  retries: "0",
  fallbackFrom: "",
  costUsd: "0.000000",
};

describe("runtime-utils", () => {
  it("parses csv into trimmed items", () => {
    expect(parseCSV(" openai, ollama , ,localai ")).toEqual(["openai", "ollama", "localai"]);
  });

  it("filters models by kind", () => {
    expect(filterModelsByKind(models, "local")).toEqual([models[1]]);
    expect(filterModelsByKind(models, "cloud")).toEqual([models[0]]);
    expect(filterModelsByKind(models, "all")).toEqual(models);
  });

  it("filters models by provider", () => {
    expect(filterModelsByProvider(models, "ollama")).toEqual([models[1]]);
    expect(filterModelsByProvider(models, "auto")).toEqual(models);
  });

  it("filters models through configured provider aliases", () => {
    const configuredProviders: ConfiguredProviderRecord[] = [
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
    const fireworksModel: ModelRecord = {
      id: "accounts/fireworks/models/deepseek-v3",
      owned_by: "fireworks",
      metadata: { provider: "fireworks", provider_kind: "cloud" },
    };

    expect(filterModelsByProvider([fireworksModel], "fireworks-ai", configuredProviders)).toEqual([
      fireworksModel,
    ]);
  });

  it("detects remote runtime sessions from cloud identity", () => {
    expect(isRemoteRuntimeSession(null)).toBe(false);
    expect(isRemoteRuntimeSession({ role: "operator" })).toBe(false);
    expect(
      isRemoteRuntimeSession({
        role: "operator",
        remote_identity: {
          actor_id: "actor-1",
          org_id: "org-1",
          project_id: "project-1",
          runtime_id: "runtime-1",
        },
      }),
    ).toBe(true);
  });

  it("builds a trace timeline with derived phases", () => {
    const spans: TraceSpanRecord[] = [
      {
        trace_id: "trace-1",
        span_id: "span-1",
        name: "gateway.request",
        start_time: "2026-04-21T10:00:00Z",
        events: [
          { name: "request.received", timestamp: "2026-04-21T10:00:00Z" },
          { name: "router.selected", timestamp: "2026-04-21T10:00:01Z" },
        ],
      },
    ];

    expect(buildTraceTimeline(spans, "2026-04-21T10:00:00Z")).toEqual([
      expect.objectContaining({ name: "request.received", phase: "request", offsetMs: 0 }),
      expect.objectContaining({ name: "router.selected", phase: "routing", offsetMs: 1000 }),
    ]);
  });

  it("builds a span waterfall with depth, phase, and a critical-path mark", () => {
    const t0 = "2026-04-21T10:00:00.000Z";
    const at = (ms: number) => new Date(Date.parse(t0) + ms).toISOString();
    const spans: TraceSpanRecord[] = [
      {
        trace_id: "t",
        span_id: "root",
        name: "gateway.request",
        start_time: at(0),
        end_time: at(400),
      },
      {
        trace_id: "t",
        span_id: "long",
        parent_span_id: "root",
        name: "provider.openai",
        start_time: at(50),
        end_time: at(380),
      },
      {
        trace_id: "t",
        span_id: "short",
        parent_span_id: "root",
        name: "gateway.usage",
        start_time: at(385),
        end_time: at(395),
      },
    ];
    const wf = buildSpanWaterfall(spans);
    expect(wf.totalMs).toBe(400);
    expect(wf.spans).toHaveLength(3);
    const root = wf.spans.find((s) => s.span.span_id === "root")!;
    const long = wf.spans.find((s) => s.span.span_id === "long")!;
    const short = wf.spans.find((s) => s.span.span_id === "short")!;
    expect(root.depth).toBe(0);
    expect(long.depth).toBe(1);
    expect(short.depth).toBe(1);
    expect(long.phase).toBe("provider");
    expect(long.critical).toBe(true);
    expect(short.critical).toBe(false);
    expect(wf.phases).toContain("request");
    expect(wf.phases).toContain("provider");
  });

  it("finds a model from OTel-shaped trace attributes", () => {
    const spans: TraceSpanRecord[] = [
      {
        trace_id: "trace-1",
        span_id: "span-1",
        name: "gateway.provider",
        events: [
          {
            name: "provider.call.finished",
            timestamp: "2026-04-21T10:00:01Z",
            attributes: {
              "gen_ai.provider.name": "ollama",
              "gen_ai.request.model": "llama3.1:8b",
            },
          },
        ],
      },
    ];

    expect(findModelInTrace(spans, "ollama")).toBe("llama3.1:8b");
    expect(findModelInTrace(spans, "openai")).toBe("");
    expect(findProviderInTrace(spans)).toBe("ollama");
  });

  it("formats route and provider diagnostics", () => {
    expect(describeRouteReason("provider_default_model_failover")).toBe(
      "Provider default model after failover",
    );
    expect(findProvider(providers, "ollama")).toEqual(providers[1]);
    expect(providerStatusTone(providers[1])).toBe("danger");
    expect(providerStatusTone(providers[0])).toBe("healthy");
    expect(routeOutcomeTone("failed")).toBe("danger");
    expect(formatTraceAttributeKey("hecate_retry_count")).toBe("hecate retry count");
    expect(formatTraceAttributeValue({ ok: true })).toBe('{"ok":true}');
  });

  // ── tone/label exhaustiveness ─────────────────────────────────────────
  // Switch-based helpers — drive every case (incl. the default fallback)
  // so a refactor that adds a new status without updating the mapping
  // gets flagged. The wire-level enums for outcome / health_status are
  // tied to operator-facing colors; a bad mapping silently shows the
  // wrong tone in the trace inspector.

  it("routeOutcomeTone covers every wire outcome", () => {
    expect(routeOutcomeTone("selected")).toBe("healthy");
    expect(routeOutcomeTone("completed")).toBe("healthy");
    expect(routeOutcomeTone("failed")).toBe("danger");
    expect(routeOutcomeTone("denied")).toBe("warning");
    expect(routeOutcomeTone("skipped")).toBe("warning");
    expect(routeOutcomeTone("unknown")).toBe("neutral");
    expect(routeOutcomeTone(undefined)).toBe("neutral");
  });

  it("healthStatusTone covers every health enum", () => {
    expect(healthStatusTone("healthy")).toBe("healthy");
    expect(healthStatusTone("degraded")).toBe("warning");
    expect(healthStatusTone("half_open")).toBe("warning");
    expect(healthStatusTone("open")).toBe("danger");
    expect(healthStatusTone("unhealthy")).toBe("danger");
    expect(healthStatusTone(undefined)).toBe("neutral");
  });

  it("describeHealthStatus produces a label for each enum", () => {
    expect(describeHealthStatus("healthy")).toBe("Healthy");
    expect(describeHealthStatus("degraded")).toBe("Degraded");
    expect(describeHealthStatus("half_open")).toBe("Recovery probe");
    expect(describeHealthStatus("open")).toBe("Circuit open");
    expect(describeHealthStatus("unhealthy")).toBe("Unhealthy");
    expect(describeHealthStatus(undefined)).toBe("Unknown health");
    expect(describeHealthStatus("brand-new-state")).toBe("Unknown health");
  });

  // ── trace attribute formatting ────────────────────────────────────────

  it("formatTraceAttributeValue stringifies every supported type", () => {
    expect(formatTraceAttributeValue(null)).toBe("n/a");
    expect(formatTraceAttributeValue(undefined)).toBe("n/a");
    expect(formatTraceAttributeValue("hello")).toBe("hello");
    expect(formatTraceAttributeValue(42)).toBe("42");
    expect(formatTraceAttributeValue(true)).toBe("true");
    expect(formatTraceAttributeValue({ a: 1 })).toBe(`{"a":1}`);
    expect(formatTraceAttributeValue([1, 2])).toBe("[1,2]");
  });

  it("formatTraceAttributeKey replaces underscores with spaces", () => {
    expect(formatTraceAttributeKey("provider_kind")).toBe("provider kind");
    expect(formatTraceAttributeKey("snake_case_key")).toBe("snake case key");
    // No underscores → unchanged.
    expect(formatTraceAttributeKey("alreadyClean")).toBe("alreadyClean");
  });

  // ── tracePhaseFromEvent ──────────────────────────────────────────────

  it("tracePhaseFromEvent maps every prefix the gateway emits", () => {
    expect(tracePhaseFromEvent("request.received")).toBe("request");
    expect(tracePhaseFromEvent("router.selected")).toBe("routing");
    expect(tracePhaseFromEvent("provider.invoked")).toBe("provider");
    expect(tracePhaseFromEvent("governor.allowed")).toBe("governor");
    expect(tracePhaseFromEvent("usage.recorded")).toBe("usage");
    expect(tracePhaseFromEvent("queue.claimed")).toBe("queue");
    expect(tracePhaseFromEvent("orchestrator.run.started")).toBe("orchestration");
    expect(tracePhaseFromEvent("orchestrator.step.completed")).toBe("tool");
    expect(tracePhaseFromEvent("orchestrator.approval.requested")).toBe("approval");
    expect(tracePhaseFromEvent("orchestrator.artifact.created")).toBe("artifact");
    expect(tracePhaseFromEvent("policy.tool_blocked")).toBe("approval");
    expect(tracePhaseFromEvent("tool.completed")).toBe("tool");
    expect(tracePhaseFromEvent("retention.run.finished")).toBe("retention");
    expect(tracePhaseFromEvent("chat.run.finished")).toBe("chat");
    expect(tracePhaseFromEvent("response.returned")).toBe("response");
    // Unknown prefix → "other" (default branch).
    expect(tracePhaseFromEvent("custom.event")).toBe("other");
  });

  it("tracePhaseFromSpan maps OTel span names used by the gateway", () => {
    expect(tracePhaseFromSpan("gateway.request")).toBe("request");
    expect(tracePhaseFromSpan("gateway.router")).toBe("routing");
    expect(tracePhaseFromSpan("gateway.provider")).toBe("provider");
    expect(tracePhaseFromSpan("gateway.usage")).toBe("usage");
    expect(tracePhaseFromSpan("orchestrator.queue")).toBe("queue");
    expect(tracePhaseFromSpan("orchestrator.run")).toBe("orchestration");
    expect(tracePhaseFromSpan("orchestrator.step")).toBe("tool");
    expect(tracePhaseFromSpan("orchestrator.approval")).toBe("approval");
    expect(tracePhaseFromSpan("orchestrator.artifact")).toBe("artifact");
    expect(tracePhaseFromSpan("retention.run")).toBe("retention");
    expect(tracePhaseFromSpan("chat.run")).toBe("chat");
    expect(tracePhaseFromSpan("gateway.runtime")).toBe("other");
  });

  // ── route helpers ────────────────────────────────────────────────────

  it("describeRouteRecovery prefers half-open probe over fallback messages", () => {
    const route: TraceRouteRecord = {
      candidates: [
        { provider: "openai", model: "gpt-4o", outcome: "selected", health_status: "half_open" },
      ],
    } as unknown as TraceRouteRecord;
    expect(describeRouteRecovery(route, runtimeHeaders)).toBe(
      "Recovered via half-open provider probe",
    );
  });

  it("describeRouteRecovery names the fallback source when one is present", () => {
    const headers: RuntimeHeaders = { ...runtimeHeaders, fallbackFrom: "anthropic" };
    expect(describeRouteRecovery(undefined, headers)).toBe("Failed over from anthropic");
  });

  it("describeRouteRecovery describes failovers without a named source", () => {
    const route: TraceRouteRecord = {
      candidates: [
        { provider: "openai", model: "gpt-4o", outcome: "selected", health_status: "healthy" },
      ],
      failovers: [{ from: "openai", to: "anthropic" }],
    } as unknown as TraceRouteRecord;
    expect(describeRouteRecovery(route)).toBe("Recovered after one or more failover hops");
  });

  it("describeRouteRecovery returns the no-op label when nothing recovered", () => {
    expect(describeRouteRecovery(undefined, undefined)).toBe("No recovery path needed");
  });

  it("countRouteHealthStatuses tallies tones across candidates", () => {
    const route: TraceRouteRecord = {
      candidates: [
        { provider: "a", model: "x", outcome: "selected", health_status: "healthy" },
        { provider: "b", model: "y", outcome: "skipped", health_status: "degraded" },
        { provider: "c", model: "z", outcome: "failed", health_status: "open" },
      ],
    } as unknown as TraceRouteRecord;
    expect(countRouteHealthStatuses(route)).toEqual({ healthy: 1, warning: 1, danger: 1 });
    // Empty / missing route → all-zero summary.
    expect(countRouteHealthStatuses(undefined)).toEqual({ healthy: 0, warning: 0, danger: 0 });
    expect(countRouteHealthStatuses(null)).toEqual({ healthy: 0, warning: 0, danger: 0 });
  });

  it("describeRouteCandidateOutcome labels router candidate states", () => {
    expect(describeRouteCandidateOutcome({ outcome: "selected" } as any)).toBe("Selected route");
    expect(describeRouteCandidateOutcome({ outcome: "completed" } as any)).toBe("Completed route");
    expect(describeRouteCandidateOutcome({ outcome: "failed" } as any)).toBe("Failed attempt");
    expect(describeRouteCandidateOutcome({ outcome: "skipped" } as any)).toBe("Skipped");
    expect(describeRouteCandidateOutcome({ outcome: "half_open_probe" } as any)).toBe(
      "Half Open Probe",
    );
    expect(describeRouteCandidateOutcome({} as any)).toBe("Candidate");
  });

  it("explainRouteCandidate turns route outcomes into operator-readable reasons", () => {
    expect(
      explainRouteCandidate({ outcome: "selected", reason: "pinned_provider_model" } as any),
    ).toBe("Chosen because pinned provider and model.");
    expect(explainRouteCandidate({ outcome: "selected" } as any)).toBe("Chosen by the router.");
    expect(
      explainRouteCandidate({ outcome: "skipped", skip_reason: "provider_rate_limited" } as any),
    ).toBe("Skipped because cooling down after upstream 429.");
    expect(
      explainRouteCandidate({ outcome: "failed", detail: "upstream returned 503" } as any),
    ).toBe("upstream returned 503");
    expect(
      explainRouteCandidate({
        outcome: "denied",
        policy_reason: "cloud providers are blocked",
      } as any),
    ).toBe("cloud providers are blocked");
    expect(explainRouteCandidate({ outcome: "candidate" } as any)).toBe(
      "Considered by the router.",
    );
  });

  // ── provider helpers ─────────────────────────────────────────────────

  it("providerStatusTone treats !healthy + status=healthy as a warning, otherwise mirrors status", () => {
    // The "looks healthy but isn't" mismatch is a real wire state — the
    // health tracker can flag a provider as unhealthy after threshold-N
    // failures even while its self-reported status is still "healthy".
    expect(
      providerStatusTone({
        name: "x",
        kind: "cloud",
        healthy: false,
        status: "healthy",
        default_model: "",
      }),
    ).toBe("warning");
    expect(
      providerStatusTone({
        name: "x",
        kind: "cloud",
        healthy: true,
        status: "healthy",
        default_model: "",
      }),
    ).toBe("healthy");
    expect(
      providerStatusTone({
        name: "x",
        kind: "cloud",
        healthy: false,
        status: "open",
        default_model: "",
      }),
    ).toBe("danger");
    expect(providerStatusTone(undefined)).toBe("neutral");
  });

  it("findProvider returns the matching record or null", () => {
    const list: ProviderRecord[] = [
      { name: "openai", kind: "cloud", healthy: true, status: "healthy", default_model: "gpt-4o" },
      { name: "ollama", kind: "local", healthy: false, status: "open", default_model: "llama3" },
    ];
    expect(findProvider(list, "openai")?.name).toBe("openai");
    expect(findProvider(list, "missing")).toBeNull();
    expect(findProvider(list, undefined)).toBeNull();
    expect(findProvider(list, "")).toBeNull();
  });

  // ── usage helpers ───────────────────────────────────────────────────

  it("describeUsageScope joins parts that are populated", () => {
    expect(describeUsageScope(undefined)).toBe("No scope");
    expect(describeUsageScope(null)).toBe("No scope");

    const base: UsageSummaryRecord = {
      key: "x",
      scope: "global",
      backend: "memory",
      used_micros_usd: 0,
      used_usd: "$0.000000",
    };
    // scope only → just the scope label.
    expect(describeUsageScope({ ...base })).toBe("global");
    // scope + provider.
    expect(describeUsageScope({ ...base, scope: "provider", provider: "openai" })).toBe(
      "provider / provider openai",
    );
  });

  it("describeRouteSkipReason labels known and unknown skip reasons", () => {
    expect(describeRouteSkipReason("policy_denied")).toBe("Policy denied");
    expect(describeRouteSkipReason("provider_not_found")).toBe("Provider missing");
    expect(describeRouteSkipReason("route_denied")).toBe("Route denied");
    expect(describeRouteSkipReason("provider_retry_exhausted")).toBe("Retry exhausted");
    expect(describeRouteSkipReason("provider_unavailable")).toBe("Provider unavailable");
    expect(describeRouteSkipReason("provider_slow")).toBe("Slower than peers");
    expect(describeRouteSkipReason("provider_less_stable")).toBe("Recent failures");
    expect(describeRouteSkipReason("provider_rate_limited")).toBe(
      "Cooling down after upstream 429",
    );
    // Unknown code falls back to titleized form
    expect(describeRouteSkipReason("some_new_reason")).toBe("Some New Reason");
    // Missing value returns empty string
    expect(describeRouteSkipReason(undefined)).toBe("");
  });

  it("traceStatusBadge maps trace items to Badge tones", () => {
    const base: TraceListItem = { request_id: "r", span_count: 1 };
    expect(traceStatusBadge({ ...base, status_code: "ok" })).toEqual({
      status: "healthy",
      label: "Healthy",
    });
    expect(traceStatusBadge({ ...base, status_code: "error" })).toEqual({
      status: "down",
      label: "Error",
    });
    expect(
      traceStatusBadge({ ...base, status_code: "ok", route: { fallback_from: "openai" } }),
    ).toEqual({ status: "degraded", label: "Recovered" });
    // Missing status_code falls through to a degraded "Issue" or
    // describeRouteReason when present.
    expect(traceStatusBadge({ ...base })).toEqual({ status: "degraded", label: "Issue" });
    expect(traceStatusBadge({ ...base, route: { final_reason: "requested_model" } })).toEqual({
      status: "degraded",
      label: "Requested model",
    });
  });

  it("formatRelativeTime renders short relative durations", () => {
    const now = Date.now();
    // Empty / invalid input
    expect(formatRelativeTime("")).toEqual({ label: "—", iso: "" });
    expect(formatRelativeTime("not-a-date").label).toBe("not-a-date");
    // Future timestamp clamps to "just now"
    const future = new Date(now + 5_000).toISOString();
    expect(formatRelativeTime(future).label).toBe("just now");
    // 0-59s -> seconds
    const justNow = new Date(now - 3_000).toISOString();
    expect(formatRelativeTime(justNow).label).toBe("3s ago");
    // 1-59m -> minutes
    const fiveMin = new Date(now - 5 * 60_000).toISOString();
    expect(formatRelativeTime(fiveMin).label).toBe("5m ago");
    // 1-23h -> hours
    const twoHr = new Date(now - 2 * 60 * 60_000).toISOString();
    expect(formatRelativeTime(twoHr).label).toBe("2h ago");
  });
});
