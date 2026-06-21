import { useEffect, useState } from "react";
import type { ResolveChatApprovalPayload } from "../../lib/api";
import {
  agentApprovalScopeDescription,
  agentApprovalScopeLabel,
  agentApprovalToolKindLabel,
  agentApprovalToolLabel,
} from "../../lib/agent-approval-labels";
import type { ChatApprovalOption, ChatApprovalRecord } from "../../types/chat";
import { Icon, Icons, Modal } from "../shared/ui";

// AgentApprovalModal renders the operator decision UI for a single
// pending approval. Self-contained: opens with just the (sessionID,
// approvalID) pair, fetches the full row on mount, and surfaces
// resolve / cancel actions.
//
// Design notes:
//   - Full row is fetched on open (not held in the pending map) so the
//     banner stays cheap. While the fetch is in flight we show a
//     spinner; on failure we close.
//   - The broad agent tool-kind scope requires an explicit
//     "Confirm" step before sending the resolve to avoid one-click
//     blanket-allow mistakes.
//   - There's no diff preview surface today — the `agentApprovalItem`
//     wire shape doesn't carry a diff body. The collapse-by-default
//     behavior described in the design will be wired up once the
//     backend surfaces it; for now we render the ACP options + scope
//     selector + optional note.
type Props = {
  sessionID: string;
  approvalID: string;
  onClose: () => void;
  fetchApproval: (sessionID: string, approvalID: string) => Promise<ChatApprovalRecord | null>;
  onResolve: (
    sessionID: string,
    approvalID: string,
    payload: ResolveChatApprovalPayload,
  ) => Promise<boolean>;
  onCancel: (sessionID: string, approvalID: string) => Promise<boolean>;
};

export function AgentApprovalModal({
  sessionID,
  approvalID,
  onClose,
  fetchApproval,
  onResolve,
  onCancel,
}: Props) {
  const [row, setRow] = useState<ChatApprovalRecord | null>(null);
  const [error, setError] = useState<string>("");
  const [decision, setDecision] = useState<"allow" | "deny">("allow");
  const [scope, setScope] = useState<string>("");
  const [selectedOption, setSelectedOption] = useState<string>("");
  const [note, setNote] = useState<string>("");
  const [busy, setBusy] = useState(false);
  const [confirmingBroadScope, setConfirmingBroadScope] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const result = await fetchApproval(sessionID, approvalID);
      if (cancelled) return;
      if (!result) {
        setError("Could not load this approval. It may have been resolved already.");
        return;
      }
      setRow(result);
      // Sensible defaults: first scope choice, first ACP option that
      // matches the current decision. Never carry an allow option into
      // a deny decision; the backend treats selected_option as the
      // exact ACP response to send back to the adapter.
      const firstScope = result.scope_choices?.[0] ?? "once";
      setScope(firstScope);
      setSelectedOption(defaultOptionForDecision(result.acp_options, "allow"));
    })();
    return () => {
      cancelled = true;
    };
  }, [sessionID, approvalID, fetchApproval]);

  const isBroadScope = scope === "adapter_tool";
  const buttonLabel = decision === "allow" ? "Allow" : "Deny";
  const submitLabel =
    isBroadScope && !confirmingBroadScope ? `${buttonLabel} (broad scope)` : buttonLabel;
  const scopeDescription = agentApprovalScopeDescription(scope, row?.tool_kind);

  async function handleSubmit() {
    if (!row || busy) return;
    if (isBroadScope && !confirmingBroadScope) {
      // First click on broad-scope arms confirmation; the next click
      // actually fires the resolve. Operator can change their mind by
      // selecting another scope in the meantime — we reset the arming
      // state on scope change too.
      setConfirmingBroadScope(true);
      return;
    }
    setBusy(true);
    const ok = await onResolve(sessionID, approvalID, {
      decision: decision === "allow" ? "approve" : "deny",
      scope,
      selected_option: selectedOption || undefined,
      note: note.trim() || undefined,
    });
    setBusy(false);
    if (ok) onClose();
  }

  async function handleCancelApproval() {
    if (!row || busy) return;
    setBusy(true);
    const ok = await onCancel(sessionID, approvalID);
    setBusy(false);
    if (ok) onClose();
  }

  // Reset broad-scope arming whenever the scope choice changes — the
  // confirm step only fires for broad scope, so any other selection
  // should clear the prompt.
  useEffect(() => {
    if (!isBroadScope && confirmingBroadScope) {
      setConfirmingBroadScope(false);
    }
  }, [isBroadScope, confirmingBroadScope]);

  return (
    <Modal
      title="Agent approval"
      onClose={onClose}
      footer={
        <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            disabled={busy}
            onClick={() => void handleCancelApproval()}
            data-testid="agent-approval-modal-cancel"
          >
            Cancel approval
          </button>
          <div style={{ marginLeft: "auto", display: "flex", gap: 8 }}>
            <button
              type="button"
              className={decision === "deny" ? "btn btn-danger btn-sm" : "btn btn-primary btn-sm"}
              disabled={busy || !row}
              onClick={() => void handleSubmit()}
              data-testid="agent-approval-modal-submit"
            >
              {decision === "allow" ? (
                <Icon d={Icons.approve} size={13} />
              ) : (
                <Icon d={Icons.deny} size={13} />
              )}{" "}
              {busy ? "Sending…" : submitLabel}
            </button>
          </div>
        </div>
      }
    >
      {error && (
        <div
          style={{
            padding: "10px 12px",
            border: "1px solid var(--red-border)",
            borderRadius: "var(--radius-sm)",
            background: "var(--red-bg)",
            color: "var(--red)",
            fontSize: 12,
          }}
        >
          {error}
        </div>
      )}

      {!row && !error && (
        <div
          style={{ padding: 24, textAlign: "center", color: "var(--t3)", fontSize: 12 }}
          data-testid="agent-approval-modal-loading"
        >
          Loading approval…
        </div>
      )}

      {row && (
        <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
          {/* Identity row: who's asking, what for. */}
          <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            <span
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 11,
                color: "var(--t3)",
                textTransform: "uppercase",
                letterSpacing: "0.04em",
              }}
            >
              {row.adapter_id}
              {row.workspace ? ` · ${row.workspace}` : ""}
            </span>
            <span style={{ fontSize: 13, color: "var(--t0)", fontWeight: 500 }}>
              {agentApprovalToolLabel(row)}
            </span>
          </div>

          {/* Decision toggle. */}
          <div>
            <label
              style={{
                fontSize: 11,
                color: "var(--t2)",
                textTransform: "uppercase",
                letterSpacing: "0.04em",
                marginBottom: 6,
                display: "block",
              }}
            >
              Decision
            </label>
            <p
              style={{
                margin: "0 0 6px",
                color: "var(--t3)",
                fontSize: 11,
                lineHeight: 1.4,
              }}
            >
              Deny sends the selected reject option to the agent. Cancel returns ACP Cancelled
              without selecting an agent option.
            </p>
            <div
              style={{
                display: "flex",
                border: "1px solid var(--border)",
                borderRadius: "var(--radius-sm)",
                overflow: "hidden",
              }}
            >
              {(["allow", "deny"] as const).map((kind) => (
                <button
                  key={kind}
                  type="button"
                  onClick={() => {
                    setDecision(kind);
                    setSelectedOption(defaultOptionForDecision(row.acp_options, kind));
                  }}
                  data-testid={`agent-approval-modal-decision-${kind}`}
                  style={{
                    flex: 1,
                    padding: "8px 12px",
                    background:
                      decision === kind
                        ? kind === "allow"
                          ? "var(--teal-bg)"
                          : "var(--red-bg)"
                        : "transparent",
                    color:
                      decision === kind
                        ? kind === "allow"
                          ? "var(--teal)"
                          : "var(--red)"
                        : "var(--t2)",
                    border: "none",
                    fontSize: 12,
                    cursor: "pointer",
                    fontFamily: "var(--font-mono)",
                    textTransform: "uppercase",
                    letterSpacing: "0.04em",
                  }}
                >
                  {kind}
                </button>
              ))}
            </div>
          </div>

          {/* ACP options. The agent offers one or more concrete options
              the operator can pick from (e.g. "approve_for_session" /
              "approve_once"). We send back option_id so the agent
              receives the exact choice it surfaced. */}
          {row.acp_options.length > 0 && (
            <div>
              <label
                style={{
                  fontSize: 11,
                  color: "var(--t2)",
                  textTransform: "uppercase",
                  letterSpacing: "0.04em",
                  marginBottom: 6,
                  display: "block",
                }}
              >
                Agent option
              </label>
              <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                {row.acp_options.map((opt) => {
                  const matchesDecision = optionMatchesDecision(opt, decision);
                  return (
                    <label
                      key={opt.option_id}
                      data-testid={`agent-approval-modal-option-${opt.option_id}`}
                      style={{
                        display: "flex",
                        alignItems: "center",
                        gap: 8,
                        padding: "6px 10px",
                        border: `1px solid ${selectedOption === opt.option_id ? "var(--teal-border)" : "var(--border)"}`,
                        background:
                          selectedOption === opt.option_id ? "var(--teal-bg)" : "var(--bg3)",
                        borderRadius: "var(--radius-sm)",
                        cursor: matchesDecision ? "pointer" : "not-allowed",
                        opacity: matchesDecision ? 1 : 0.5,
                      }}
                    >
                      <input
                        type="radio"
                        name="acp-option"
                        value={opt.option_id}
                        checked={selectedOption === opt.option_id}
                        disabled={!matchesDecision}
                        onChange={() => {
                          if (matchesDecision) setSelectedOption(opt.option_id);
                        }}
                      />
                      <span style={{ fontSize: 12, color: "var(--t0)", flex: 1 }}>{opt.name}</span>
                      <span
                        style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}
                      >
                        {approvalOptionKindLabel(opt.kind)}
                      </span>
                    </label>
                  );
                })}
              </div>
            </div>
          )}

          {/* Scope selector. Backend offers scope_choices on the row;
              we render only those (an empty list means scope isn't
              meaningful for this tool). */}
          {row.scope_choices && row.scope_choices.length > 0 && (
            <div>
              <label
                style={{
                  fontSize: 11,
                  color: "var(--t2)",
                  textTransform: "uppercase",
                  letterSpacing: "0.04em",
                  marginBottom: 6,
                  display: "block",
                }}
              >
                Apply to
              </label>
              <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                {row.scope_choices.map((s) => (
                  <button
                    key={s}
                    type="button"
                    onClick={() => setScope(s)}
                    title={agentApprovalScopeDescription(s, row.tool_kind)}
                    data-testid={`agent-approval-modal-scope-${s}`}
                    style={{
                      padding: "4px 10px",
                      fontFamily: "var(--font-mono)",
                      fontSize: 11,
                      borderRadius: "var(--radius-sm)",
                      border: `1px solid ${scope === s ? "var(--teal-border)" : "var(--border)"}`,
                      background: scope === s ? "var(--teal-bg)" : "var(--bg3)",
                      color: scope === s ? "var(--teal)" : "var(--t2)",
                      cursor: "pointer",
                    }}
                  >
                    {agentApprovalScopeLabel(s, row.tool_kind)}
                  </button>
                ))}
              </div>
              {scopeDescription && (
                <div
                  data-testid="agent-approval-modal-scope-description"
                  style={{
                    marginTop: 8,
                    color: "var(--t3)",
                    fontSize: 11,
                    lineHeight: 1.45,
                  }}
                >
                  {scopeDescription}
                </div>
              )}
              {isBroadScope && (
                <div
                  data-testid="agent-approval-modal-broad-warning"
                  style={{
                    marginTop: 8,
                    padding: "8px 10px",
                    border: "1px solid var(--amber-border)",
                    background: "var(--amber-bg)",
                    color: "var(--amber)",
                    borderRadius: "var(--radius-sm)",
                    fontSize: 11,
                  }}
                >
                  <Icon d={Icons.warning} size={12} /> <strong>Broad scope:</strong> this creates a
                  durable grant for future {agentApprovalToolKindLabel(row.tool_kind)} requests from
                  this agent. Click {buttonLabel} once to arm, then again to confirm.
                </div>
              )}
            </div>
          )}

          {/* Optional note for the audit log. */}
          <div>
            <label
              style={{
                fontSize: 11,
                color: "var(--t2)",
                textTransform: "uppercase",
                letterSpacing: "0.04em",
                marginBottom: 6,
                display: "block",
              }}
            >
              Note (optional)
            </label>
            <textarea
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="Why this decision? Saved on the approval record."
              data-testid="agent-approval-modal-note"
              style={{
                width: "100%",
                background: "var(--bg3)",
                border: "1px solid var(--border)",
                borderRadius: "var(--radius-sm)",
                color: "var(--t0)",
                fontSize: 12,
                fontFamily: "var(--font-sans)",
                padding: "6px 10px",
                resize: "vertical",
                minHeight: 60,
                outline: "none",
                lineHeight: 1.5,
              }}
            />
          </div>
        </div>
      )}
    </Modal>
  );
}

function approvalOptionKindLabel(kind: string): string {
  switch (kind) {
    case "allow_once":
      return "allow once";
    case "allow_always":
      return "allow always";
    case "reject_once":
      return "reject once";
    case "reject_always":
      return "reject always";
    default:
      return kind.replaceAll("_", " ");
  }
}

function defaultOptionForDecision(
  options: ChatApprovalOption[],
  decision: "allow" | "deny",
): string {
  return options.find((opt) => optionMatchesDecision(opt, decision))?.option_id ?? "";
}

function optionMatchesDecision(option: ChatApprovalOption, decision: "allow" | "deny"): boolean {
  const wanted =
    decision === "allow"
      ? new Set(["allow_once", "allow_always"])
      : new Set(["reject_once", "reject_always"]);
  return wanted.has(option.kind);
}
