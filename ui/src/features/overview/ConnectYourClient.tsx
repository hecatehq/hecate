import { useMemo, useState } from "react";

import { CodeBlock } from "../shared/ui";

// ConnectYourClient renders copy-paste recipes for pointing common clients
// (Codex / Claude Code / curl) at this gateway. The token shown is whichever
// one the operator pasted into TokenGate — fine for kicking the tires, but
// the inline note nudges them toward minting a dedicated API key once they
// move past the demo stage. The component is intentionally state-light: no
// fetches, no subscriptions, just rendering snippets from props.

type Props = {
  gatewayURL: string;
  token: string;
};

type Tab = "openai" | "anthropic" | "curl";

const TABS: { id: Tab; label: string }[] = [
  { id: "openai", label: "OpenAI / Codex" },
  { id: "anthropic", label: "Anthropic / Claude Code" },
  { id: "curl", label: "curl" },
];

export function ConnectYourClient({ gatewayURL, token }: Props) {
  const [active, setActive] = useState<Tab>("openai");
  const [open, setOpen] = useState(true);

  // The displayed token is whatever the UI session has — the admin token
  // pasted into TokenGate. We mask it with bullets when the operator clicks
  // "hide" so screenshots / shoulder-surfers don't leak the real value.
  const [tokenVisible, setTokenVisible] = useState(false);

  const snippets = useMemo(() => {
    const displayedToken = token || "<paste-your-token>";
    return {
      openai: [
        `export OPENAI_BASE_URL="${gatewayURL}/v1"`,
        `export OPENAI_API_KEY="${displayedToken}"`,
      ].join("\n"),
      anthropic: [
        `export ANTHROPIC_BASE_URL="${gatewayURL}"`,
        `export ANTHROPIC_API_KEY="${displayedToken}"`,
      ].join("\n"),
      curl: [
        `curl -sS "${gatewayURL}/v1/chat/completions" \\`,
        `  -H "Content-Type: application/json" \\`,
        `  -H "Authorization: Bearer ${displayedToken}" \\`,
        `  -d '{`,
        `    "model": "gpt-4o-mini",`,
        `    "messages": [{"role": "user", "content": "hello"}]`,
        `  }'`,
      ].join("\n"),
    };
  }, [gatewayURL, token]);

  // When the token is hidden, swap the displayed snippets in for ones that
  // mask the secret. We don't generate two snippet objects in one place to
  // avoid duplicating the layout — the substitution is one targeted swap.
  const masked = useMemo(() => {
    if (tokenVisible || !token) return snippets;
    const stars = "•".repeat(Math.min(Math.max(token.length, 8), 32));
    return {
      openai: snippets.openai.replace(token, stars),
      anthropic: snippets.anthropic.replace(token, stars),
      curl: snippets.curl.replace(token, stars),
    };
  }, [snippets, token, tokenVisible]);

  return (
    <div className="card" style={{ padding: 0 }}>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        style={{
          width: "100%",
          padding: "12px 14px",
          background: "transparent",
          border: "none",
          textAlign: "left",
          cursor: "pointer",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          color: "var(--t1)",
          fontSize: 12,
          fontFamily: "var(--font-mono)",
          letterSpacing: "0.06em",
          textTransform: "uppercase",
        }}
        aria-expanded={open}
      >
        <span>Connect a client</span>
        <span style={{ color: "var(--t3)" }}>{open ? "−" : "+"}</span>
      </button>

      {open && (
        <div style={{ padding: "0 14px 14px", display: "flex", flexDirection: "column", gap: 10 }}>
          <p style={{ margin: 0, color: "var(--t2)", fontSize: 12, lineHeight: 1.5 }}>
            Point any OpenAI- or Anthropic-compatible client at this gateway. The snippets use your
            admin token; for production, mint a dedicated API key on the Access tab and substitute
            it for the token below.
          </p>

          <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
            <div style={{ display: "flex", gap: 4 }}>
              {TABS.map((tab) => (
                <button
                  key={tab.id}
                  type="button"
                  onClick={() => setActive(tab.id)}
                  className={`btn btn-sm${active === tab.id ? " btn-primary" : " btn-ghost"}`}
                  style={{ fontSize: 11 }}
                >
                  {tab.label}
                </button>
              ))}
            </div>

            {token && (
              <button
                type="button"
                onClick={() => setTokenVisible((v) => !v)}
                className="btn btn-ghost btn-sm"
                style={{ marginLeft: "auto", fontSize: 11 }}
                aria-pressed={tokenVisible}
              >
                {tokenVisible ? "hide token" : "show token"}
              </button>
            )}
          </div>

          <CodeBlock code={masked[active]} lang={active === "curl" ? "bash" : "bash"} />
        </div>
      )}
    </div>
  );
}
