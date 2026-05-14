// ChatViewPrototype — feature/ux-2 visual prototype for the Chats workspace.
//
// Static markup, no API wiring, no real state beyond what's needed to make
// pickers and panels visible. The point is to communicate the layout shape:
//
//   ┌──────────────┬──────────────────────────────────┬─────────────────┐
//   │  Sessions    │  Active conversation             │  Context        │
//   │  (sidebar)   │  ────────────────────────────    │  (collapsible)  │
//   │              │  transcript                      │                 │
//   │              │  ────────────────────────────    │                 │
//   │              │  composer                        │                 │
//   └──────────────┴──────────────────────────────────┴─────────────────┘
//
// Key design moves vs the current ChatView:
//   - One transcript area, no nested setup cards. Agent/model/workspace pickers
//     move into the composer toolbar and the right context panel.
//   - Context panel collapses by default. Operators expose it when they want to
//     change agent, model, workspace, or system prompt — it's not a permanent
//     two-column header.
//   - Empty state leads with three explicit options (Hecate Agent / Claude Code
//     / Codex), each a one-click start, instead of a multi-section setup card.
//   - Session list groups by time bucket (Today / Yesterday / Earlier) so
//     long histories stay scannable without operators searching for "where did
//     today's chat go."
//   - Composer toolbar pins the model + agent + workspace pickers inline so the
//     operator can change context without leaving the composer.

import { useState } from "react";

type SessionRow = {
  id: string;
  title: string;
  agent: "hecate" | "claude_code" | "codex" | "cursor";
  preview: string;
  time: string; // human-readable bucket label
  unread?: boolean;
};

const DEMO_SESSIONS: { bucket: string; sessions: SessionRow[] }[] = [
  {
    bucket: "Today",
    sessions: [
      { id: "s1", title: "Refactor router pipeline", agent: "hecate", preview: "Let's look at the executor handoff…", time: "2:18 PM", unread: true },
      { id: "s2", title: "Why is alpha.28 stuck?", agent: "claude_code", preview: "Check the publish-updater-website job", time: "11:04 AM" },
    ],
  },
  {
    bucket: "Yesterday",
    sessions: [
      { id: "s3", title: "Polish Observability waterfall", agent: "hecate", preview: "Sub-ms parsing + outline parents", time: "5:42 PM" },
      { id: "s4", title: "ACP session config options", agent: "codex", preview: "PrepareSession before first prompt", time: "10:17 AM" },
    ],
  },
  {
    bucket: "Earlier this week",
    sessions: [
      { id: "s5", title: "Claude Code auth UX", agent: "hecate", preview: "Replace jargon + add deep-link button", time: "Mon" },
      { id: "s6", title: "Bridge release for alpha.28", agent: "claude_code", preview: "Manual --prerelease=false --latest flip", time: "Sun" },
    ],
  },
];

const AGENT_LETTER: Record<SessionRow["agent"], string> = {
  hecate: "H",
  claude_code: "C",
  codex: "C",
  cursor: "C",
};

const AGENT_TINT: Record<SessionRow["agent"], string> = {
  hecate: "var(--teal)",
  claude_code: "oklch(0.72 0.14 35)", // claude orange
  codex: "oklch(0.74 0.10 260)", // codex blue
  cursor: "oklch(0.72 0.12 320)", // cursor magenta
};

// Accept the same prop shape as the real ChatView so AppShell can swap the
// import verbatim. All props are ignored — the prototype renders static demo
// data only.
export function ChatViewPrototype(_props: Record<string, unknown>) {
  void _props;
  const [selectedID, setSelectedID] = useState("s1");
  const [contextOpen, setContextOpen] = useState(false);

  return (
    <div style={{
      height: "100%",
      display: "grid",
      gridTemplateColumns: contextOpen ? "260px 1fr 320px" : "260px 1fr 32px",
      overflow: "hidden",
      transition: "grid-template-columns 200ms ease",
    }}>
      <SessionsSidebar selectedID={selectedID} onSelect={setSelectedID} />
      <ConversationPane onToggleContext={() => setContextOpen(v => !v)} />
      <ContextPanel open={contextOpen} onToggle={() => setContextOpen(v => !v)} />
    </div>
  );
}

// ─── Sessions sidebar ─────────────────────────────────────────────────────────

function SessionsSidebar({ selectedID, onSelect }: { selectedID: string; onSelect: (id: string) => void }) {
  const [query, setQuery] = useState("");
  return (
    <aside style={{
      borderRight: "1px solid var(--border)",
      background: "var(--bg1)",
      display: "flex",
      flexDirection: "column",
      minWidth: 0,
    }}>
      <div style={{ padding: "12px 12px 8px", borderBottom: "1px solid var(--border)" }}>
        <button
          type="button"
          style={{
            width: "100%",
            padding: "8px 10px",
            background: "var(--teal)",
            color: "oklch(0.12 0.02 185)",
            border: "none",
            borderRadius: "var(--radius-sm)",
            fontWeight: 600,
            fontSize: 12,
            cursor: "pointer",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            gap: 6,
          }}>
          <span style={{ fontSize: 14, lineHeight: 1 }}>＋</span> New chat
        </button>
        <input
          value={query}
          onChange={e => setQuery(e.target.value)}
          placeholder="Search chats"
          style={{
            marginTop: 8,
            width: "100%",
            padding: "6px 9px",
            background: "var(--bg2)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-sm)",
            color: "var(--t0)",
            fontSize: 12,
            outline: "none",
            boxSizing: "border-box",
          }}
        />
      </div>
      <div style={{ flex: 1, overflowY: "auto", padding: "8px 0" }}>
        {DEMO_SESSIONS.map(group => (
          <div key={group.bucket} style={{ marginBottom: 4 }}>
            <div style={{
              padding: "4px 14px",
              fontFamily: "var(--font-mono)",
              fontSize: 9,
              letterSpacing: "0.12em",
              textTransform: "uppercase",
              color: "var(--t3)",
            }}>
              {group.bucket}
            </div>
            {group.sessions.map(s => (
              <button
                key={s.id}
                type="button"
                onClick={() => onSelect(s.id)}
                style={{
                  width: "100%",
                  textAlign: "left",
                  background: selectedID === s.id ? "var(--teal-bg)" : "transparent",
                  borderLeft: selectedID === s.id ? "2px solid var(--teal)" : "2px solid transparent",
                  padding: "6px 12px 7px",
                  cursor: "pointer",
                  display: "flex",
                  gap: 9,
                  alignItems: "flex-start",
                  border: "none",
                  borderBottom: "1px solid transparent",
                  fontFamily: "inherit",
                }}>
                <AgentDot agent={s.agent} size={20} />
                <div style={{ minWidth: 0, flex: 1 }}>
                  <div style={{
                    display: "flex",
                    gap: 6,
                    alignItems: "baseline",
                    marginBottom: 2,
                  }}>
                    <span style={{
                      color: "var(--t0)",
                      fontSize: 12,
                      fontWeight: s.unread ? 600 : 500,
                      flex: 1,
                      whiteSpace: "nowrap",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                    }}>
                      {s.title}
                    </span>
                    <span style={{ color: "var(--t3)", fontSize: 10, fontFamily: "var(--font-mono)" }}>
                      {s.time}
                    </span>
                  </div>
                  <div style={{
                    color: "var(--t2)",
                    fontSize: 11,
                    lineHeight: 1.3,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}>
                    {s.preview}
                  </div>
                </div>
              </button>
            ))}
          </div>
        ))}
      </div>
    </aside>
  );
}

function AgentDot({ agent, size }: { agent: SessionRow["agent"]; size: number }) {
  return (
    <div style={{
      width: size,
      height: size,
      borderRadius: "var(--radius-sm)",
      background: "var(--bg3)",
      border: `1px solid ${AGENT_TINT[agent]}`,
      color: AGENT_TINT[agent],
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      fontSize: Math.round(size * 0.5),
      fontWeight: 600,
      fontFamily: "var(--font-mono)",
      flexShrink: 0,
    }}>
      {AGENT_LETTER[agent]}
    </div>
  );
}

// ─── Conversation pane ────────────────────────────────────────────────────────

function ConversationPane({ onToggleContext }: { onToggleContext: () => void }) {
  return (
    <main style={{
      display: "flex",
      flexDirection: "column",
      minWidth: 0,
      overflow: "hidden",
      background: "var(--bg0)",
    }}>
      <ConversationHeader onToggleContext={onToggleContext} />
      <Transcript />
      <Composer />
    </main>
  );
}

function ConversationHeader({ onToggleContext }: { onToggleContext: () => void }) {
  return (
    <header style={{
      padding: "10px 16px",
      borderBottom: "1px solid var(--border)",
      display: "flex",
      alignItems: "center",
      gap: 10,
      background: "var(--bg1)",
    }}>
      <AgentDot agent="hecate" size={22} />
      <div style={{ minWidth: 0, flex: 1 }}>
        <div style={{ color: "var(--t0)", fontSize: 13, fontWeight: 500 }}>
          Refactor router pipeline
        </div>
        <div style={{ color: "var(--t2)", fontSize: 11, fontFamily: "var(--font-mono)", marginTop: 1 }}>
          Hecate Agent · ministral-3:latest · /Users/chicoxyzzy/dev/hecate
        </div>
      </div>
      <button type="button" title="Rename" style={iconBtn}>
        <span style={{ fontSize: 13 }}>✏</span>
      </button>
      <button type="button" title="Archive" style={iconBtn}>
        <span style={{ fontSize: 13 }}>📁</span>
      </button>
      <button type="button" onClick={onToggleContext} title="Toggle context panel" style={{ ...iconBtn, color: "var(--teal)" }}>
        <span style={{ fontSize: 13 }}>⚙</span>
      </button>
    </header>
  );
}

const iconBtn: React.CSSProperties = {
  width: 28,
  height: 28,
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  background: "transparent",
  border: "1px solid transparent",
  borderRadius: "var(--radius-sm)",
  color: "var(--t2)",
  cursor: "pointer",
};

function Transcript() {
  return (
    <div style={{ flex: 1, overflowY: "auto", padding: "16px 0" }}>
      <div style={{ maxWidth: 760, margin: "0 auto", padding: "0 16px", display: "flex", flexDirection: "column", gap: 16 }}>
        <Message role="user" time="2:18 PM" content="The router's executor handoff looks tangled. Where does the failover decision actually happen?" />
        <Message
          role="assistant"
          model="hecate-agent"
          time="2:18 PM"
          content={`Failover decision lives in two places:

1. **internal/router/router.go — \`Fallbacks()\`** builds the ordered candidate list (initial selection + fallback chain). This is pure routing logic; no execution.
2. **internal/gateway/executor.go — main loop around line 80** iterates that list. For each candidate it emits \`router.candidate.considered\`, runs preflight, then either calls the provider or moves to the next candidate. The actual failover-triggered event lands here.

The split is intentional: routing logic doesn't know about preflight failures or upstream errors. The executor reconciles routing intent with runtime reality.`}
          tokens={{ in: 248, out: 156, cost: "$0.0008" }}
        />
        <Message role="user" time="2:21 PM" content="Show me the failover-triggered event payload." />
        <SystemActivity label="Reading file" detail="internal/gateway/executor.go (132 lines)" />
      </div>
    </div>
  );
}

function Message({ role, model, time, content, tokens }: {
  role: "user" | "assistant";
  model?: string;
  time: string;
  content: string;
  tokens?: { in: number; out: number; cost: string };
}) {
  const isUser = role === "user";
  return (
    <div style={{ display: "flex", gap: 10, alignItems: "flex-start" }}>
      <div style={{
        width: 26,
        height: 26,
        borderRadius: "var(--radius-sm)",
        background: isUser ? "var(--bg3)" : "var(--teal-bg)",
        border: `1px solid ${isUser ? "var(--border)" : "var(--teal-border)"}`,
        color: isUser ? "var(--t1)" : "var(--teal)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        fontSize: 11,
        fontWeight: 600,
        fontFamily: "var(--font-mono)",
        flexShrink: 0,
        marginTop: 1,
      }}>
        {isUser ? "U" : "H"}
      </div>
      <div style={{ minWidth: 0, flex: 1 }}>
        <div style={{ display: "flex", gap: 8, alignItems: "baseline", marginBottom: 5 }}>
          <span style={{ color: isUser ? "var(--t1)" : "var(--teal)", fontSize: 11, fontFamily: "var(--font-mono)" }}>
            {isUser ? "You" : (model || "assistant")}
          </span>
          <span style={{ color: "var(--t3)", fontSize: 10, fontFamily: "var(--font-mono)" }}>{time}</span>
          {tokens && (
            <span style={{ color: "var(--t3)", fontSize: 10, fontFamily: "var(--font-mono)" }}>
              {tokens.in}↑ {tokens.out}↓ · {tokens.cost}
            </span>
          )}
        </div>
        <div style={{
          color: "var(--t0)",
          fontSize: 14,
          lineHeight: 1.6,
          whiteSpace: "pre-wrap",
        }}>
          {content}
        </div>
      </div>
    </div>
  );
}

function SystemActivity({ label, detail }: { label: string; detail: string }) {
  return (
    <div style={{
      display: "flex",
      gap: 9,
      alignItems: "center",
      paddingLeft: 36,
      color: "var(--t2)",
      fontSize: 11,
      fontFamily: "var(--font-mono)",
    }}>
      <span style={{
        width: 6,
        height: 6,
        borderRadius: 999,
        background: "var(--teal)",
        opacity: 0.7,
        flexShrink: 0,
      }} />
      <span style={{ color: "var(--t1)" }}>{label}</span>
      <span>{detail}</span>
    </div>
  );
}

function Composer() {
  const [text, setText] = useState("");
  return (
    <div style={{
      borderTop: "1px solid var(--border)",
      padding: "10px 16px 12px",
      background: "var(--bg1)",
    }}>
      <div style={{ maxWidth: 760, margin: "0 auto" }}>
        <div style={{
          border: "1px solid var(--border2)",
          borderRadius: "var(--radius)",
          background: "var(--bg2)",
          overflow: "hidden",
        }}>
          <textarea
            value={text}
            onChange={e => setText(e.target.value)}
            placeholder="Message Hecate Agent…"
            rows={2}
            style={{
              width: "100%",
              padding: "10px 12px",
              background: "transparent",
              border: "none",
              color: "var(--t0)",
              fontSize: 14,
              fontFamily: "inherit",
              resize: "none",
              outline: "none",
              boxSizing: "border-box",
            }}
          />
          <div style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            padding: "6px 8px 7px",
            borderTop: "1px solid var(--border)",
          }}>
            <PickerChip label="Agent" value="Hecate Agent" tint="var(--teal)" />
            <PickerChip label="Model" value="ministral-3:latest" />
            <PickerChip label="Workspace" value="hecate/" />
            <span style={{ flex: 1 }} />
            <span style={{ color: "var(--t3)", fontSize: 10, fontFamily: "var(--font-mono)" }}>
              ⌘↩ to send
            </span>
            <button
              type="button"
              disabled={text.trim().length === 0}
              style={{
                padding: "5px 14px",
                background: text.trim() ? "var(--teal)" : "var(--bg3)",
                color: text.trim() ? "oklch(0.12 0.02 185)" : "var(--t3)",
                border: "none",
                borderRadius: "var(--radius-sm)",
                fontSize: 12,
                fontWeight: 600,
                cursor: text.trim() ? "pointer" : "not-allowed",
              }}>
              Send
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function PickerChip({ label, value, tint }: { label: string; value: string; tint?: string }) {
  return (
    <button
      type="button"
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 6,
        padding: "3px 8px",
        background: "var(--bg3)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        color: "var(--t1)",
        fontSize: 11,
        fontFamily: "var(--font-mono)",
        cursor: "pointer",
      }}>
      <span style={{ color: "var(--t3)" }}>{label}</span>
      <span style={{ color: tint || "var(--t0)" }}>{value}</span>
      <span style={{ color: "var(--t3)", fontSize: 9 }}>▾</span>
    </button>
  );
}

// ─── Right context panel ──────────────────────────────────────────────────────

function ContextPanel({ open, onToggle }: { open: boolean; onToggle: () => void }) {
  if (!open) {
    return (
      <div style={{
        borderLeft: "1px solid var(--border)",
        background: "var(--bg1)",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        paddingTop: 12,
      }}>
        <button onClick={onToggle} title="Open context panel" style={{
          ...iconBtn,
          color: "var(--t2)",
          transform: "rotate(180deg)",
        }}>
          <span style={{ fontSize: 11 }}>◀</span>
        </button>
      </div>
    );
  }
  return (
    <aside style={{
      borderLeft: "1px solid var(--border)",
      background: "var(--bg1)",
      display: "flex",
      flexDirection: "column",
      overflow: "hidden",
    }}>
      <div style={{
        padding: "12px 14px",
        borderBottom: "1px solid var(--border)",
        display: "flex",
        alignItems: "center",
        gap: 8,
      }}>
        <span style={{ color: "var(--t0)", fontSize: 12, fontWeight: 500, flex: 1 }}>Context</span>
        <button onClick={onToggle} title="Close" style={iconBtn}>
          <span style={{ fontSize: 11 }}>▶</span>
        </button>
      </div>
      <div style={{ flex: 1, overflowY: "auto", padding: 14, display: "flex", flexDirection: "column", gap: 18 }}>
        <ContextSection title="Agent">
          <Field label="Type" value="Hecate Agent · tools on" />
          <Field label="Workspace" value="/Users/chicoxyzzy/dev/hecate" mono />
          <Field label="System prompt" value="3 lines · edit" actionable />
        </ContextSection>
        <ContextSection title="Model">
          <Field label="Provider" value="Ollama (local)" />
          <Field label="Model" value="ministral-3:latest" mono />
          <Field label="Capabilities" value="tools · streaming" />
        </ContextSection>
        <ContextSection title="This session">
          <Field label="Messages" value="2" />
          <Field label="Tokens used" value="248↑ 156↓" mono />
          <Field label="Cost" value="$0.0008" mono />
          <Field label="Last activity" value="2:21 PM" />
        </ContextSection>
        <ContextSection title="Actions">
          <PanelButton>Edit system prompt</PanelButton>
          <PanelButton>Switch agent</PanelButton>
          <PanelButton>Open trace</PanelButton>
          <PanelButton tone="red">Archive chat</PanelButton>
        </ContextSection>
      </div>
    </aside>
  );
}

function ContextSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <div style={{
        color: "var(--t3)",
        fontSize: 9,
        letterSpacing: "0.12em",
        textTransform: "uppercase",
        fontFamily: "var(--font-mono)",
        marginBottom: 7,
      }}>
        {title}
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
        {children}
      </div>
    </section>
  );
}

function Field({ label, value, mono, actionable }: { label: string; value: string; mono?: boolean; actionable?: boolean }) {
  return (
    <div style={{ display: "flex", gap: 8, alignItems: "baseline" }}>
      <span style={{ color: "var(--t3)", fontSize: 11, minWidth: 86 }}>{label}</span>
      <span style={{
        color: actionable ? "var(--teal)" : "var(--t0)",
        fontSize: 11,
        fontFamily: mono ? "var(--font-mono)" : "inherit",
        flex: 1,
        cursor: actionable ? "pointer" : "default",
        textDecoration: actionable ? "underline" : "none",
        textDecorationColor: actionable ? "rgba(70, 200, 200, 0.4)" : undefined,
        wordBreak: "break-all",
      }}>
        {value}
      </span>
    </div>
  );
}

function PanelButton({ children, tone }: { children: React.ReactNode; tone?: "red" }) {
  const fg = tone === "red" ? "var(--red)" : "var(--t1)";
  return (
    <button type="button" style={{
      padding: "6px 10px",
      background: "var(--bg2)",
      border: "1px solid var(--border)",
      borderRadius: "var(--radius-sm)",
      color: fg,
      fontSize: 11,
      cursor: "pointer",
      textAlign: "left",
      fontFamily: "inherit",
    }}>
      {children}
    </button>
  );
}
