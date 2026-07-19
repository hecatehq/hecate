import { useState } from "react";

import { formatMicrosUSD } from "../../lib/format";
import type { TaskStepRecord } from "../../types/task";
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

// AgentConversationView renders the agent_loop checkpoint context as an
// execution timeline. User inputs appear on the right, model responses on
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
  canRetryFromModelCall = false,
  busy = false,
  onRetryFromModelCall,
  steps = [],
  streamModelCallCosts,
  runModelCallCount,
  runActive = false,
}: {
  raw: string;
  canRetryFromModelCall?: boolean;
  busy?: boolean;
  // reason is collected via the inline modal before the call is made.
  onRetryFromModelCall?: (modelCall: number, reason: string) => void;
  steps?: TaskStepRecord[];
  // streamModelCallCosts is a model call → µUSD map pushed by the SSE stream.
  // Used to fill in costs missing from the model-step output_summary
  // path. Optional — the steps path is the primary source.
  streamModelCallCosts?: Map<number, number>;
  // model-call indices are scoped to the selected Run. The conversation
  // artifact may begin with inherited responses from earlier Runs.
  runModelCallCount: number;
  // Active Runs can contain a transient partial assistant response that has
  // not completed a model call. Until settlement, keep bubble ownership
  // deliberately neutral instead of assigning costs or retry indices.
  runActive?: boolean;
}) {
  // pendingRetryModelCall is set when the operator clicks the retry control
  // on a bubble. The inline modal collects an optional reason, then
  // fires onRetryFromModelCall(modelCall, reason) on confirm.
  const [pendingRetryModelCall, setPendingRetryModelCall] = useState<number | null>(null);

  let messages: AgentConversationMessage[] = [];
  try {
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) messages = parsed as AgentConversationMessage[];
  } catch {
    return (
      <div
        style={{
          padding: "10px 16px",
          borderBottom: "1px solid var(--border)",
          fontSize: 11,
          color: "var(--red)",
          fontFamily: "var(--font-mono)",
        }}
      >
        Could not parse Run model context artifact (invalid JSON).
      </div>
    );
  }
  if (messages.length === 0) return null;
  const visibleMessages = messages.filter(isVisibleConversationMessage);
  if (visibleMessages.length === 0) return null;

  // The source Run owns only the assistant-message suffix identified by its
  // authoritative count. Earlier assistant messages are prior-Run context:
  // they stay visible, but they have no local model-call number, cost, or
  // retry action on this Run.
  let assistantSeen = 0;
  for (const message of visibleMessages) {
    if (message.role === "assistant") assistantSeen++;
  }
  const validRunCount =
    Number.isInteger(runModelCallCount) &&
    runModelCallCount >= 0 &&
    runModelCallCount <= assistantSeen;
  const inheritedAssistantCount = validRunCount ? assistantSeen - runModelCallCount : assistantSeen;
  let assistantOrdinal = 0;
  const modelCallByIndex: number[] = visibleMessages.map((message) => {
    if (message.role !== "assistant") return 0;
    assistantOrdinal++;
    if (runActive) return 0;
    return assistantOrdinal > inheritedAssistantCount
      ? assistantOrdinal - inheritedAssistantCount
      : 0;
  });

  const costByModelCall = buildModelCallCostMap(steps);
  if (streamModelCallCosts) {
    for (const [modelCall, micros] of streamModelCallCosts) {
      if (!costByModelCall.has(modelCall) && micros > 0) {
        costByModelCall.set(modelCall, micros);
      }
    }
  }

  return (
    <>
      <div
        style={{
          padding: "12px 16px",
          borderBottom: "1px solid var(--border)",
          display: "flex",
          flexDirection: "column",
          gap: 8,
        }}
      >
        <div className="kicker" style={{ marginBottom: 4 }}>
          Run model context · {visibleMessages.length}{" "}
          {visibleMessages.length === 1 ? "entry" : "entries"}
        </div>
        {visibleMessages.map((m, i) => (
          <ConversationBubble
            key={i}
            message={m}
            modelCall={modelCallByIndex[i]}
            live={m.role === "assistant" && runActive}
            inherited={m.role === "assistant" && !runActive && modelCallByIndex[i] === 0}
            modelCallCostMicros={
              modelCallByIndex[i] > 0 ? costByModelCall.get(modelCallByIndex[i]) : undefined
            }
            canRetryFromModelCall={canRetryFromModelCall}
            busy={busy || pendingRetryModelCall !== null}
            onRetryFromModelCall={
              canRetryFromModelCall ? (modelCall) => setPendingRetryModelCall(modelCall) : undefined
            }
          />
        ))}
      </div>
      {pendingRetryModelCall !== null && (
        <RetryFromModelCallModal
          modelCall={pendingRetryModelCall}
          busy={busy}
          onConfirm={(reason) => {
            onRetryFromModelCall?.(pendingRetryModelCall, reason);
            setPendingRetryModelCall(null);
          }}
          onClose={() => setPendingRetryModelCall(null)}
        />
      )}
    </>
  );
}

export function isVisibleConversationMessage(message: AgentConversationMessage): boolean {
  return message.role === "user" || message.role === "assistant" || message.role === "tool";
}

// RetryFromModelCallModal collects an optional reason before submitting the
// retry-from-model-call request. The reason is stored in the run.resumed_from_event event
// and shown in the run timeline so operators can annotate why they
// branched from a particular model call.
function RetryFromModelCallModal({
  modelCall,
  busy,
  onConfirm,
  onClose,
}: {
  modelCall: number;
  busy: boolean;
  onConfirm: (reason: string) => void;
  onClose: () => void;
}) {
  const [reason, setReason] = useState("");
  return (
    <Modal
      title={`Retry from model call ${modelCall}`}
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
            {busy ? "Creating Run…" : "Retry in new Run"}
          </button>
        </div>
      }
    >
      <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
        <p style={{ margin: 0, fontSize: 13, color: "var(--t1)", lineHeight: 1.5 }}>
          A new Run will branch just before model call {modelCall} of this Run. Earlier conversation
          context and workspace state are preserved. To branch from an older call, select the Run
          that made it.
        </p>
        <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          <span
            style={{
              fontSize: 11,
              fontWeight: 500,
              color: "var(--t2)",
              textTransform: "uppercase",
              letterSpacing: "0.05em",
            }}
          >
            Reason <span style={{ fontWeight: 400, color: "var(--t3)" }}>(optional)</span>
          </span>
          <textarea
            autoFocus
            rows={2}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="Why are you branching from this model call?"
            style={{
              resize: "vertical",
              fontFamily: "var(--font-sans)",
              fontSize: 13,
              color: "var(--t0)",
              background: "var(--bg2)",
              border: "1px solid var(--border)",
              borderRadius: "var(--radius-sm)",
              padding: "6px 8px",
              lineHeight: 1.5,
              width: "100%",
              boxSizing: "border-box",
            }}
          />
        </label>
      </div>
    </Modal>
  );
}

// buildModelCallCostMap reads the authoritative model_call_index recorded on
// actual provider-call steps. `builtin.agent_loop_resume` is deliberately
// excluded: it marks approval continuation without making another model call.
export function buildModelCallCostMap(steps: TaskStepRecord[]): Map<number, number> {
  const sorted = [...steps].sort((a, b) => (a.index ?? 0) - (b.index ?? 0));
  const out = new Map<number, number>();
  for (const step of sorted) {
    if (step.kind !== "model" || step.tool_name !== "builtin.agent_loop_llm") continue;
    const runModelCall = stepModelCallIndex(step);
    if (runModelCall === null) continue;
    const summary = step.output_summary as Record<string, unknown> | undefined;
    if (!summary) continue;
    const raw = summary["cost_micros_usd"];
    if (typeof raw === "number" && raw > 0) {
      out.set(runModelCall, raw);
    }
  }
  return out;
}

function stepModelCallIndex(step: TaskStepRecord): number | null {
  const raw = step.input?.["model_call_index"];
  return typeof raw === "number" && Number.isInteger(raw) && raw >= 1 ? raw : null;
}

function ConversationBubble({
  message,
  modelCall,
  inherited = false,
  live = false,
  modelCallCostMicros,
  canRetryFromModelCall = false,
  busy = false,
  onRetryFromModelCall,
}: {
  message: AgentConversationMessage;
  modelCall?: number;
  inherited?: boolean;
  live?: boolean;
  modelCallCostMicros?: number;
  canRetryFromModelCall?: boolean;
  busy?: boolean;
  onRetryFromModelCall?: (modelCall: number) => void;
}) {
  if (message.role === "user") {
    return (
      <div style={{ display: "flex", justifyContent: "flex-end" }}>
        <div
          style={{
            maxWidth: "80%",
            padding: "8px 12px",
            background: "var(--teal-bg)",
            border: "1px solid var(--teal-border)",
            borderRadius: "var(--radius)",
            color: "var(--t0)",
            fontSize: 13,
            lineHeight: 1.5,
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
          }}
        >
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
        <div
          style={{
            maxWidth: "90%",
            padding: "6px 10px",
            background: "var(--bg2)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-sm)",
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--t1)",
          }}
        >
          <div className="kicker" style={{ marginBottom: 4 }}>
            tool result{callRef}
          </div>
          <pre
            style={{
              margin: 0,
              whiteSpace: "pre-wrap",
              wordBreak: "break-word",
              maxHeight: 200,
              overflowY: "auto",
              color: "var(--t1)",
            }}
          >
            {message.content || ""}
          </pre>
        </div>
      </div>
    );
  }
  // assistant — content + any tool calls
  const showRetry =
    !inherited && canRetryFromModelCall && !!modelCall && modelCall > 0 && !!onRetryFromModelCall;
  const showCost = typeof modelCallCostMicros === "number" && modelCallCostMicros > 0;
  return (
    <div
      style={{
        display: "flex",
        justifyContent: "flex-start",
        flexDirection: "column",
        gap: 6,
        alignItems: "stretch",
      }}
    >
      <div className="kicker" style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <span>
          {live ? "Live Run context" : inherited ? "Prior Run context" : `model call ${modelCall}`}
        </span>
        {showCost && (
          <span
            title={`LLM cost for model call ${modelCall}: ${formatMicrosUSD(modelCallCostMicros!)}`}
            style={{ color: "var(--t3)", fontFamily: "var(--font-mono)" }}
          >
            · {formatMicrosUSD(modelCallCostMicros!)}
          </span>
        )}
        {showRetry && (
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            disabled={busy}
            onClick={() => onRetryFromModelCall?.(modelCall!)}
            aria-label={`Retry from model call ${modelCall} of this Run`}
            title={`Create a new Run from model call ${modelCall} with the prior conversation preserved`}
            style={{ fontFamily: "var(--font-mono)", fontSize: 10, padding: "2px 6px" }}
          >
            ↻ retry this Run call
          </button>
        )}
      </div>
      {message.content && (
        <div
          style={{
            alignSelf: "flex-start",
            maxWidth: "80%",
            padding: "8px 12px",
            background: "var(--bg3)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius)",
            color: "var(--t0)",
            fontSize: 13,
            lineHeight: 1.5,
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
          }}
        >
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

function ToolCallChip({
  call,
}: {
  call: NonNullable<AgentConversationMessage["tool_calls"]>[number];
}) {
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
    <div
      style={{
        alignSelf: "flex-start",
        maxWidth: "90%",
        padding: "6px 10px",
        background: "var(--bg2)",
        border: "1px solid var(--teal-border)",
        borderRadius: "var(--radius-sm)",
        fontFamily: "var(--font-mono)",
        fontSize: 11,
        color: "var(--t1)",
      }}
    >
      <span style={{ color: "var(--teal)", fontWeight: 500 }}>
        → {call.function?.name || "(unknown)"}
      </span>
      {argsText && (
        <>
          <span style={{ color: "var(--t3)" }}> </span>
          <span
            title={argsText}
            style={{
              color: "var(--t2)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              maxWidth: "100%",
              display: "inline-block",
              verticalAlign: "bottom",
            }}
          >
            {argsText.length > 200 ? argsText.slice(0, 200) + "…" : argsText}
          </span>
        </>
      )}
    </div>
  );
}
