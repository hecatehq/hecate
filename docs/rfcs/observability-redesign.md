# Observability Workspace Redesign

> **Status:** design notes. Not implemented. Captures the proposal
> for restructuring the Observability workspace around two distinct
> jobs-to-be-done — "is the system OK right now?" and "what
> happened with this request?" — and for the cross-surface
> drill-down that lets a chat or task row open the trace it
> produced.
> **Depends on:** the existing trace store (`GATEWAY_TRACE_STORE_BACKEND`,
> retention via `GATEWAY_RETENTION_TRACES_*` — default 24h / 2000
> records), `/hecate/v1/traces`, `/hecate/v1/system/runtime`,
> `/hecate/v1/system/mcp/cache`, and the agent-chat session model
> (`internal/agentchat/store.go` already records `trace_id`).
> **Prerequisite:** a Tier 1 cleanup PR that lands first — pure
> bugfix in `ui/src/lib/runtime-trace.ts` plus a structural split
> of the 1214-line `ObservabilityView.tsx`. That PR is in scope of
> the same effort but tracked separately. This RFC starts where
> Tier 1 ends.

## Problem

The Observability workspace today is a single stack: stat strip,
recent-traces table, and a drawer that shows a waterfall + route
candidates + event timeline for whichever trace you click. It is
optimized for one shape of question — "show me the last few
requests, let me poke at one." It answers other shapes badly:

- **"Is the system OK right now?"** No error rate, no latency
  trend, no isolated view of failed traces. The operator has to
  scan the table and pattern-match red badges.
- **"What happened with this chat?"** Chats and agent-chat sessions
  carry a `trace_id`, but the UI offers no link from a chat
  message to its trace. Operator switches workspaces and searches
  by request id manually.
- **"Where is time going across all my recent calls?"** No
  aggregation. Phase rollup exists per-trace (the waterfall
  legend) but not across the visible set.
- **"Just show me failures."** No errors-only filter. No
  attribute-key search. Time range is fixed at "most recent N."

The drawer also renders five sections of unequal importance
stacked vertically — status grid, waterfall, route summary, route
candidate explainer, event flow timeline — with nothing telling
the operator which one to look at first for a given question.

The Tier 1 cleanup makes the picture trustworthy and the file
modular; this RFC is about the structural redesign that follows.

## Goals

In rough priority order:

1. **Two surfaces with distinct jobs.** Live (continuous
   monitoring) and Inspect (request forensics). Each surface is
   focused; neither tries to be both.
2. **Cross-surface drill-down.** A chat row, an agent-chat run, or
   a task run links to the trace it produced; the trace drawer
   links back to the producing chat/task.
3. **Operator-actionable filters in Inspect.** Errors-only, time
   range within retention, status, route reason, free-text
   request-id substring, and attribute key=value.
4. **Phase rollup across the filtered set.** Stacked phase
   distribution for the visible traces — answers "is provider call
   always the bottleneck?" without clicking 50 rows.
5. **Trust the rendering.** Every visible bar reflects real data.
   Tier 1 closes the math bugs; this redesign keeps that bar.

## Non-goals (v1)

- **Full metrics dashboard.** Latency percentiles + error rate
  over time is the floor. Grafana / Datadog parity (custom panels,
  user-defined queries, alert thresholds) is not the target. If
  the operator needs that, they should point an OTel exporter at
  their own backend.
- **Long-term storage.** Retention stays at the existing
  `GATEWAY_RETENTION_TRACES_*` window (default 24h / 2000 records).
  Time-range queries are bounded by retention; no archive layer.
- **Alerting.** No "page me when error rate > X." Different
  surface, separate roadmap.
- **Anomaly detection / RCA hints.** No "looks like provider Y is
  slow today" auto-callouts. Operator interprets the data; the UI
  surfaces it cleanly.
- **Service map / dependency graph.** Hecate is a single binary.
  A topology view is the wrong shape.
- **Span attribute editing or annotation.** The operator cannot
  add notes to a trace. Spans are immutable observations.
- **Trace export / sharing URLs.** Eventually nice; not v1. The
  request-id substring filter covers the "send this id to a
  teammate" case for now.

## Constraints

- **Backend largely unchanged.** All current data already exists in
  the trace store. The Live tab's latency/error timelines are the
  one place we may need a small backend addition (a recent
  histogram endpoint — see "Backend touches" below). Everything
  else is frontend-only.
- **Polling cadence preserved.** Stays at 4s for the recent-traces
  list and 10s for runtime stats. The Live tab does not multiply
  load — it derives the timelines from the same recent-traces
  feed it already polls, plus one new endpoint.
- **Single-user.** No tenant or user partitioning.
- **Local-first / SQLite-backed.** No external observability backend
  required. Operators who want one wire up an OTel exporter; this
  view stays useful with the local store alone.
- **Retention-aware empty states.** The UI distinguishes "no data
  in this window because nothing happened" from "no data because
  retention pruned it." Both are valid; both look identical
  without explicit handling.

## Information architecture

A single workspace, two tabs:

```
Observability
├─ Live      (default route)
└─ Inspect   (?tab=inspect)
```

The current view's content moves under **Inspect**. **Live** is
new. Tab choice persists in `localStorage` so an operator who
lives in Inspect doesn't get bounced back to Live every reload.

### Live tab

"Is the system OK right now?" Three rows top-to-bottom:

```
┌─────────────────────────────────────────────────────────┐
│ Runtime strip                                           │
│ queue: 0  workers: 4  in-flight: 2  approval queue: 0  │
│ MCP cache: 12 entries / 8 idle                          │
└─────────────────────────────────────────────────────────┘
┌──────────────────────────┬──────────────────────────────┐
│ Error rate (last 1h)     │ Latency p50/p95/p99 (1h)     │
│ ▁▁▂▁▁▃▁▁▁▂▁▁▁▁▁          │ ▆▆▅▆▆▇▆▆▆▆▆▆▆▆▆              │
│  current: 1.2%           │ p50 280ms · p95 1.4s · p99 4s│
└──────────────────────────┴──────────────────────────────┘
┌─────────────────────────────────────────────────────────┐
│ Recent failures                              View all → │
│ 09:41:22  rate_limit         openai/gpt-5     420ms     │
│ 09:38:11  fallback recovered openai → groq    1.1s      │
│ 09:35:02  rate_limit         openai/gpt-5     380ms     │
└─────────────────────────────────────────────────────────┘
```

Reading order: top strip says the gateway is up; middle strip says
nothing in the last hour is alarming; bottom strip says what *is*
going wrong (errors, recoveries) — three failures from the last
window, the rest of the operator's day still scrolls under
"recent traces" in Inspect.

The "Recent failures" feed is the recent-traces feed pre-filtered
to `status_code=error OR route.fallback_from!=""`, capped at 10
rows. Click a row → switches to Inspect tab with the trace drawer
pre-opened on that request.

### Inspect tab

"What happened with this request?" Two columns:

```
┌─ Filter bar ───────────────────────────────────────────────┐
│ time: last 1h ▾  status: any ▾  errors only ☐              │
│ provider: any ▾  model: any ▾  route reason: any ▾         │
│ attr: gen_ai.request.model = anthropic/claude…  +          │
│ request id: 7b08c…                                         │
└────────────────────────────────────────────────────────────┘
┌─ Trace table ──────────┐ ┌─ Trace drawer ──────────────────┐
│ time  status  prov  …  │ │  Status                          │
│ 09:41  error  openai   │ │  ─────                           │
│ 09:39  ok     groq     │ │  Span waterfall                  │
│ 09:35  ok     anthropic│ │  ─────                           │
│ ...                    │ │  Route decision                  │
│ 142 traces in window   │ │  ─────                           │
│                        │ │  Event timeline                  │
└────────────────────────┘ └─────────────────────────────────┘
```

The drawer keeps the current sections; their layout becomes a
collapsible-section pattern (status + waterfall always visible;
route decision and event timeline collapsible). The "five panels
stacked equally" problem the current view has is solved by giving
the operator ordering control without losing density.

The filter bar's `attr: key = value +` button opens a key
autocomplete sourced from attributes observed in the visible
trace set, then a value autocomplete sourced from values for that
key in the same set. "Scan deeper" button extends both
autocompletes to the full retention window with a one-time
endpoint call.

Below the trace table footer, when the filtered set has ≥3
traces:

```
┌─ Phase rollup (142 traces) ────────────────────────────┐
│ provider ████████████████████████████████  62%  890ms  │
│ tool     ████████  18%  240ms                          │
│ routing  ██  4%   60ms                                 │
│ governor ▏  1%   15ms                                  │
│ other    █████  15%  210ms                             │
└────────────────────────────────────────────────────────┘
```

Sums `durMs` per phase across the filtered set and renders a
stacked horizontal bar. Answers the aggregate question without
forcing the operator to click into 142 individual waterfalls.

### Cross-surface drill-down

Three new affordances, all backed by the existing `trace_id`
field on chat sessions / messages / task runs:

1. **Chat message → trace.** A small "trace" link in the message
   metadata strip (next to model and cost). Opens Inspect tab
   with the drawer pre-targeted.
2. **Agent-chat run → trace.** Same affordance on agent-chat
   assistant messages.
3. **Task run → trace.** Same affordance in the task-run details
   strip.

Inverse direction:

4. **Trace drawer → producing chat/task.** A "View producing
   conversation" link in the drawer header when the trace has
   `gen_ai.session_id` or equivalent attributes the gateway
   already emits.

Implementation: a `<TraceLink requestID={…} />` component the chat
/ task code drops in. Routes to `/observability?tab=inspect&request_id=…`.
The Inspect tab reads `request_id` from the query string,
auto-runs the lookup, opens the drawer.

## Backend touches

Almost zero. Two specific items:

- **Latency / error timeline endpoint.** New
  `GET /hecate/v1/system/runtime/timeline?window=1h&buckets=60`
  returns 1-minute (or 1/N-of-window) buckets with `count`,
  `error_count`, `p50_ms`, `p95_ms`, `p99_ms`. Computed from the
  same trace store the recent-traces feed reads from. Live tab
  polls this every 10s alongside `runtime`. Without this endpoint
  the Live timelines have to be approximated from the
  recent-traces list, which only carries 50 entries — not enough
  for a clean trend line.
- **Attribute autocomplete endpoint (optional, deferred).**
  `GET /hecate/v1/traces/attributes?key=…&window=…` for the
  "scan deeper" affordance. v1 can scope autocomplete to the
  visible filtered set client-side; the deeper scan is a
  follow-up.

No schema changes. No retention changes.

## Phasing

Tier 1 (prerequisite, separate PR — not part of this RFC's
implementation scope):

| Item | Scope |
|---|---|
| Bugfix `runtime-trace.ts` | `t0`/`totalMs` math (filter unparseable spans before reducing); DFS sort by parent → children; multi-root critical-path handling; rename "critical" → "longest descent" or fix algorithm; cap detail-fetch retries; mark negative-duration spans visually |
| Test fixtures | `runtime-trace.test.ts` with synthetic bad-data inputs |
| File split | `ObservabilityView.tsx` → `RuntimeStrip`, `TraceTable`, `TraceDrawer`, `SpanWaterfall`, `EventTimeline`, `PhaseLegend` (each in its own file under `ui/src/features/overview/observability/`) |

Then this RFC's redesign:

| Phase | Scope | Done when |
|---|---|---|
| 1 | Two-tab IA. `Live` and `Inspect` tabs, route via query param, persist last selection. Existing content lives entirely under Inspect. | Existing flow works under Inspect; Live is a placeholder. |
| 2 | Filter bar under Inspect: time range, errors-only, status, route reason, request-id substring. | Operator can answer "show me the last hour's errors" in one action. |
| 3 | Cross-surface drill-down. `<TraceLink>` component; chat / agent-chat / task surfaces drop it in; Inspect reads `request_id` from the URL. | Click a chat → Observability opens with drawer pre-targeted. |
| 4 | Phase rollup viz under Inspect. | Stacked phase bar appears under the trace table when filtered set ≥ 3. |
| 5 | Live tab content. New `/system/runtime/timeline` endpoint. Error rate + latency timelines + recent-failures feed. | Live tab is the meaningful default. |
| 6 | Attribute key=value filter (visible-set autocomplete first; "scan deeper" via the optional endpoint follows). | Operator can filter by `gen_ai.request.model = X`. |

Each phase is a real PR, reviewable independently. Phase 1–2 land
the IA shift. Phase 3 unlocks the cross-surface use case. Phases
4–6 add the new capability the redesign exists for.

## Open questions

- **Live → Inspect transition ergonomics.** Click an error in the
  Live tab's recent-failures feed → I'm proposing this switches
  tabs and pre-opens the drawer. Does the back button restore Live
  state cleanly, or does that feel like a context switch the
  operator didn't want? Lean: switch tabs, browser back returns to
  Live with previous scroll position; if it feels jarring,
  alternative is a modal-on-Live that doesn't switch tabs at all
  but loses the filter context.
- **Time-range vs retention pruning empty state.** "Last 6h" with
  retention at 24h / 2000 records — the cap may have evicted
  records that fall in the requested window. The UI should say
  "showing 142 traces; retention may have evicted older entries
  from this window" rather than "142 traces" alone. How loud the
  callout is depends on whether evictions happened during the
  window. Lean: show the disclaimer only when the requested
  window's earliest minute has fewer records than the average for
  later minutes (statistical heuristic — rough but useful).
- **Phase rollup math.** Summing per-phase durMs across N traces
  is straightforward, but provider-call durations are usually 10×
  any other phase, so the bar is dominated by them and the others
  visually disappear. Two options: (a) show absolute time as the
  primary axis with a note that other phases are smaller, (b)
  add a "log" toggle. Lean: (a) with a clearly labeled "smaller
  phases not to scale" note; (b) only if operator feedback
  requests it.
- **Error rate bucket size.** 1-minute buckets for a 1-hour window
  give 60 points. 1-minute buckets for a 6-hour window give 360 —
  too dense for a sparkline. Either fix the bucket count
  ("60 buckets" regardless of window — bucket size scales) or fix
  the bucket size and accept the variable detail. Lean: fix bucket
  count at 60. Same approach as DataDog's small-format charts.
- **Attribute autocomplete scope.** Client-side over visible
  filtered set is fast but partial. Server-side over full
  retention window is complete but adds a roundtrip per
  keystroke. Lean: client-side instant; "scan deeper" button
  triggers the server scan once per session.
- **Mobile / narrow viewport.** The Inspect two-column layout
  collapses to drawer-as-modal at <900px today. The Live tab is
  more constrained — three rows of charts on a phone is too much.
  Lean: Live tab becomes a single column at narrow viewports
  (runtime strip stacks vertically; charts share full width
  sequentially).

## Migration / rollback

Pure UI redesign on top of unchanged backend (modulo the optional
Phase 5 timeline endpoint, which is additive). Each phase is its
own PR and reverts cleanly. The Tier 1 cleanup is independent and
stays merged on a Tier 2 rollback — operators get the bugfixes
even if the redesign is reverted.

The optional `/system/runtime/timeline` endpoint added in Phase 5
is additive: deleting the route returns the gateway to its
pre-RFC state. The Live tab degrades to "no chart, just stats" if
the endpoint returns 404 — handled as a soft fallback so a
forward UI on a backend without the endpoint still loads.

## Out of scope but worth noting

- **OTel exporter.** Operators who want long-term storage, custom
  dashboards, or alerting should point Hecate's existing OTel
  exporter at their own backend. The local Observability surface
  is for "what's happening on this gateway right now"; not a
  replacement for proper APM.
- **Span link visualization.** OTel spans support `links` to
  cross-trace causality (e.g. fan-out from one parent into N
  child traces). The current ingestion drops them; rendering
  them is a future RFC if/when Hecate emits them.
- **Custom user-defined views.** Saved filter presets, dashboard
  layouts. Useful eventually; out of scope at the alpha stage.
- **Heat-map / scatter views of latency.** Useful for spotting
  bimodal distributions. Phase rollup already shows the per-phase
  story; heat maps are a later add.
