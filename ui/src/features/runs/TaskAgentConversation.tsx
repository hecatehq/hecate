import { useState } from "react";

import { formatMicrosUSD } from "../../lib/format";
import type { TaskStepRecord } from "../../types/runtime";
import { Modal } from "../shared/ui";

// AgentConversationMessage mirrors pkg/types.Message (the shape the
// agent loop persists). Only fields the viewer renders are typed —
// extra fields on the wire (cache control, etc.) are ignored.
type AgentConversationMessage = {
  role: "user" | "assistant" | "tool" | string;
  content?: string;
  tool_call_id?: string;
  tool_calls?: Array<{
    id: string;
    type?: string;
    function?: { name?: string; arguments?: string };
  }>;
};

// AgentConversationView renders the agent_loop conversation as a
// chat-bubble timeline. User prompts on the right, assistant turns on
// the left (with their tool calls expanded), tool results in muted
// frames. The conversation is the agent's reasoning trail — operators
// scan it to understand WHY the agent did what it did, not just WHAT
// it did (the step timeline already covers that).
//
// Robustness: the artifact's content is JSON parsed inline. If the
// JSON is corrupt we render an inline error and continue rendering
// the rest of the run UI — losing the conversation viewer is much
// better than crashing the whole page.
export function AgentConversationView({
  raw,
  canRetryFromTurn = false,
  busy = false,
  onRetryFromTurn,
  steps = [],
  streamTurnCosts,
}: {
  raw: string;
  canRetryFromTurn?: boolean;
  busy?: boolean;
  // reason is collected via the inline modal before the call is made.
  onRetryFromTurn?: (turn: number, reason: string) => void;
  steps?: TaskStepRecord[];
  // streamTurnCosts is a turn → µUSD map pushed by the SSE stream.
  // Used to fill in costs missing from the model-step output_summary
  // path. Optional — the steps path is the primary source.
  streamTurnCosts?: Map<number, number>;
}) {
  // pendingRetryTurn is set when the operator clicks "↻ retry from here"
  // on a bubble. The inline modal collects an optional reason, then
  // fires onRetryFromTurn(turn, reason) on confirm.
  const [pendingRetryTurn, setPendingRetryTurn] = useState<number | null>(null);

  let messages: AgentConversationMessage[] = [];
  try {
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) messages = parsed as AgentConversationMessage[];
  } catch {
    return (
      <div style={{ padding: "10px 16px", borderBottom: "1px solid var(--border)", fontSize: 11, color: "var(--red)", fontFamily: "var(--font-mono)" }}>
        Could not parse agent conversation artifact (invalid JSON).
      </div>
    );
  }
  if (messages.length === 0) return null;
  const visibleMessages = messages.filter(isVisibleConversationMessage);
  if (visibleMessages.length === 0) return null;

  // Compute per-message turn numbers up-front so each bubble can render
  // its own "↻ retry from turn N" affordance. Only assistant messages
  // get a turn number — user/tool/system messages get 0 and won't show
  // the button. Counting in a single pass here keeps the bubble itself
  // O(1) at render time.
  let assistantSeen = 0;
  const turnByIndex: number[] = visibleMessages.map(m => {
    if (m.role === "assistant") {
      assistantSeen++;
      return assistantSeen;
    }
    return 0;
  });

  // Build turn → cost map by walking model-kind steps in step.index
  // order. The Nth model step corresponds to the Nth assistant turn,
  // so we just zip them. Steps whose OutputSummary lacks the cost
  // field (older runs, or resumed-after-approval steps that didn't
  // re-call the LLM) map to undefined; we fall back to the SSE-
  // streamed per-turn cost (when available) so the figure still
  // shows up; otherwise the bubble renders nothing rather than a
  // misleading "$0.000".
  const costByTurn = buildTurnCostMap(steps);
  if (streamTurnCosts) {
    for (const [turn, micros] of streamTurnCosts) {
      if (!costByTurn.has(turn) && micros > 0) {
        costByTurn.set(turn, micros);
      }
    }
  }

  return (
    <>
      <div style={{ padding: "12px 16px", borderBottom: "1px solid var(--border)", display: "flex", flexDirection: "column", gap: 8 }}>
        <div className="kicker" style={{ marginBottom: 4 }}>
          Agent conversation · {visibleMessages.length} message{visibleMessages.length === 1 ? "" : "s"}
        </div>
        {visibleMessages.map((m, i) => (
          <ConversationBubble
            key={i}
            message={m}
            turn={turnByIndex[i]}
            turnCostMicros={turnByIndex[i] > 0 ? costByTurn.get(turnByIndex[i]) : undefined}
            canRetryFromTurn={canRetryFromTurn}
            busy={busy || pendingRetryTurn !== null}
            onRetryFromTurn={canRetryFromTurn ? (turn) => setPendingRetryTurn(turn) : undefined}
          />
        ))}
      </div>
      {pendingRetryTurn !== null && (
        <RetryFromTurnModal
          turn={pendingRetryTurn}
          busy={busy}
          onConfirm={(reason) => {
            onRetryFromTurn?.(pendingRetryTurn, reason);
            setPendingRetryTurn(null);
          }}
          onClose={() => setPendingRetryTurn(null)}
        />
      )}
    </>
  );
}

export function isVisibleConversationMessage(message: AgentConversationMessage): boolean {
  return message.role === "user" || message.role === "assistant" || message.role === "tool";
}

// RetryFromTurnModal collects an optional reason before submitting the
// retry-from-turn request. The reason is stored in the run.resumed_from_event event
// and shown in the run timeline so operators can annotate why they
// branched from a particular turn.
function RetryFromTurnModal({
  turn,
  busy,
  onConfirm,
  onClose,
}: {
  turn: number;
  busy: boolean;
  onConfirm: (reason: string) => void;
  onClose: () => void;
}) {
  const [reason, setReason] = useState("");
  return (
    <Modal
      title={`Retry from turn ${turn}`}
      onClose={onClose}
      width={440}
      footer={
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
          <button className="btn btn-ghost" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            className="btn btn-primary"
            disabled={busy}
            onClick={() => onConfirm(reason.trim())}
          >
            {busy ? "Working…" : "Retry"}
          </button>
        </div>
      }
    >
      <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
        <p style={{ margin: 0, fontSize: 13, color: "var(--t1)", lineHeight: 1.5 }}>
          A new run will be created with the conversation truncated to just before
          turn {turn}'s assistant message. The prior steps and file state are preserved.
        </p>
        <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          <span style={{ fontSize: 11, fontWeight: 500, color: "var(--t2)", textTransform: "uppercase", letterSpacing: "0.05em" }}>
            Reason <span style={{ fontWeight: 400, color: "var(--t3)" }}>(optional)</span>
          </span>
          <textarea
            autoFocus
            rows={2}
            value={reason}
            onChange={e => setReason(e.target.value)}
            placeholder="Why are you branching from this turn?"
            style={{
              resize: "vertical", fontFamily: "var(--font-sans)", fontSize: 13,
              color: "var(--t0)", background: "var(--bg2)",
              border: "1px solid var(--border)", borderRadius: "var(--radius-sm)",
              padding: "6px 8px", lineHeight: 1.5, width: "100%", boxSizing: "border-box",
            }}
          />
        </label>
      </div>
    </Modal>
  );
}

// buildTurnCostMap walks `steps` in step.index order, picks out the
// model-kind ones, and pairs them with their turn ordinal. The agent
// loop emits exactly one model step per turn (resumed-after-approval
// turns use a different ToolName but still count as model steps), so
// "first model step" = turn 1, "second" = turn 2, etc. Returns a map
// keyed by turn number with the cost in µUSD; turns whose step has no
// cost in OutputSummary are simply absent from the map.
export function buildTurnCostMap(steps: TaskStepRecord[]): Map<number, number> {
  const sorted = [...steps].sort((a, b) => (a.index ?? 0) - (b.index ?? 0));
  const out = new Map<number, number>();
  let turn = 0;
  for (const step of sorted) {
    if (step.kind !== "model") continue;
    turn++;
    const summary = step.output_summary as Record<string, unknown> | undefined;
    if (!summary) continue;
    const raw = summary["cost_micros_usd"];
    if (typeof raw === "number" && raw > 0) {
      out.set(turn, raw);
    }
  }
  return out;
}

function ConversationBubble({
  message,
  turn,
  turnCostMicros,
  canRetryFromTurn = false,
  busy = false,
  onRetryFromTurn,
}: {
  message: AgentConversationMessage;
  turn?: number;
  turnCostMicros?: number;
  canRetryFromTurn?: boolean;
  busy?: boolean;
  onRetryFromTurn?: (turn: number) => void;
}) {
  if (message.role === "user") {
    return (
      <div style={{ display: "flex", justifyContent: "flex-end" }}>
        <div style={{
          maxWidth: "80%", padding: "8px 12px",
          background: "var(--teal-bg)", border: "1px solid var(--teal-border)",
          borderRadius: "var(--radius)", color: "var(--t0)", fontSize: 13, lineHeight: 1.5,
          whiteSpace: "pre-wrap", wordBreak: "break-word",
        }}>
          {message.content || ""}
        </div>
      </div>
    );
  }
  if (message.role === "tool") {
    // Tool results are typically formatted "status=…\n--- stdout ---\n…"
    // — render as a code block with monospace + scroll for long outputs.
    const callRef = message.tool_call_id ? ` · ${message.tool_call_id.slice(0, 12)}` : "";
    return (
      <div style={{ display: "flex", justifyContent: "flex-start" }}>
        <div style={{
          maxWidth: "90%", padding: "6px 10px",
          background: "var(--bg2)", border: "1px solid var(--border)",
          borderRadius: "var(--radius-sm)", fontSize: 11,
          fontFamily: "var(--font-mono)", color: "var(--t1)",
        }}>
          <div className="kicker" style={{ marginBottom: 4 }}>
            tool result{callRef}
          </div>
          <pre style={{ margin: 0, whiteSpace: "pre-wrap", wordBreak: "break-word", maxHeight: 200, overflowY: "auto", color: "var(--t1)" }}>
            {message.content || ""}
          </pre>
        </div>
      </div>
    );
  }
  // assistant — content + any tool calls
  const showRetry = canRetryFromTurn && !!turn && turn > 0 && !!onRetryFromTurn;
  const showCost = typeof turnCostMicros === "number" && turnCostMicros > 0;
  return (
    <div style={{ display: "flex", justifyContent: "flex-start", flexDirection: "column", gap: 6, alignItems: "stretch" }}>
      <div className="kicker" style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <span>turn {turn || "?"}</span>
        {showCost && (
          <span
            title={`LLM cost for turn ${turn}: ${formatMicrosUSD(turnCostMicros!)}`}
            style={{ color: "var(--t3)", fontFamily: "var(--font-mono)" }}
          >
            · {formatMicrosUSD(turnCostMicros!)}
          </span>
        )}
        {showRetry && (
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            disabled={busy}
            onClick={() => onRetryFromTurn?.(turn!)}
            title={`Re-run from turn ${turn} with the prior conversation preserved`}
            style={{ fontFamily: "var(--font-mono)", fontSize: 10, padding: "2px 6px" }}
          >
            ↻ retry from here
          </button>
        )}
      </div>
      {message.content && (
        <div style={{
          alignSelf: "flex-start", maxWidth: "80%", padding: "8px 12px",
          background: "var(--bg3)", border: "1px solid var(--border)",
          borderRadius: "var(--radius)", color: "var(--t0)", fontSize: 13, lineHeight: 1.5,
          whiteSpace: "pre-wrap", wordBreak: "break-word",
        }}>
          {message.content}
        </div>
      )}
      {message.tool_calls && message.tool_calls.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          {message.tool_calls.map((tc, i) => (
            <ToolCallChip key={tc.id || i} call={tc} />
          ))}
        </div>
      )}
    </div>
  );
}

function ToolCallChip({ call }: { call: NonNullable<AgentConversationMessage["tool_calls"]>[number] }) {
  // Pretty-print the JSON arguments when possible — collapsed to a
  // single line for compactness, with a click-to-expand affordance.
  const argsText = (() => {
    if (!call.function?.arguments) return "";
    try {
      const parsed = JSON.parse(call.function.arguments);
      return JSON.stringify(parsed);
    } catch {
      return call.function.arguments;
    }
  })();
  return (
    <div style={{
      alignSelf: "flex-start", maxWidth: "90%",
      padding: "6px 10px", background: "var(--bg2)",
      border: "1px solid var(--teal-border)", borderRadius: "var(--radius-sm)",
      fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t1)",
    }}>
      <span style={{ color: "var(--teal)", fontWeight: 500 }}>→ {call.function?.name || "(unknown)"}</span>
      {argsText && (
        <>
          <span style={{ color: "var(--t3)" }}> </span>
          <span title={argsText} style={{ color: "var(--t2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: "100%", display: "inline-block", verticalAlign: "bottom" }}>
            {argsText.length > 200 ? argsText.slice(0, 200) + "…" : argsText}
          </span>
        </>
      )}
    </div>
  );
}
