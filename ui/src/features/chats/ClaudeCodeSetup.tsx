import type {
  AgentAdapterHealthRecord,
  AgentAdapterRecord,
  AgentAdapterSetupCommandStatus,
} from "../../types/agent-adapter";
import { claudeCodeSetupTokenCommand } from "../../lib/claude-code-setup";
import { Icon, Icons } from "../shared/ui";

export { claudeCodeSetupTokenCommand } from "../../lib/claude-code-setup";

export type ClaudeCodePreflightState = {
  title: string;
  body: string;
  detail?: string;
  blockSend: boolean;
  tone: "amber" | "red";
  adapterInstalled: boolean;
  tokenStatus: "not_saved" | "saved_needs_check" | "invalid";
  cliSignedIn: boolean;
};

export function AgentSetupHints({
  adapters,
  selectedID,
}: {
  adapters: AgentAdapterRecord[];
  selectedID?: string;
}) {
  const ordered = adapters.slice().sort((a, b) => {
    if (a.id === selectedID) return -1;
    if (b.id === selectedID) return 1;
    if (a.available !== b.available) return a.available ? 1 : -1;
    return a.name.localeCompare(b.name);
  });

  if (ordered.length === 0) {
    return (
      <div
        style={{
          margin: "14px auto 0",
          maxWidth: 520,
          borderTop: "1px solid var(--border)",
          paddingTop: 12,
          fontSize: 12,
          color: "var(--t2)",
          lineHeight: 1.5,
        }}
      >
        No agent adapters are registered by this Hecate build.
      </div>
    );
  }

  return (
    <div
      style={{
        margin: "16px auto 0",
        maxWidth: 620,
        textAlign: "left",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius)",
        background: "var(--bg2)",
        overflow: "hidden",
      }}
    >
      {ordered.map((adapter, index) => {
        const hint = agentSetupHint(adapter);
        return (
          <div
            key={adapter.id}
            style={{
              padding: "10px 12px",
              borderTop: index === 0 ? 0 : "1px solid var(--border)",
              display: "grid",
              gridTemplateColumns: "minmax(120px, 0.7fr) minmax(0, 1.3fr)",
              gap: 10,
              alignItems: "start",
            }}
          >
            <ExternalAgentSetupSummary adapter={adapter} hint={hint} />
          </div>
        );
      })}
    </div>
  );
}

function ExternalAgentSetupSummary({
  adapter,
  hint,
}: {
  adapter: AgentAdapterRecord;
  hint: ReturnType<typeof agentSetupHint>;
}) {
  return (
    <>
      <div style={{ minWidth: 0 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
          <span
            style={{
              color: adapter.available ? "var(--green)" : "var(--red)",
              display: "inline-flex",
              flexShrink: 0,
            }}
          >
            <Icon d={adapter.available ? Icons.check : Icons.x} size={11} />
          </span>
          <span
            style={{
              fontSize: 12,
              fontWeight: 600,
              color: "var(--t1)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {adapter.name}
          </span>
        </div>
        <div
          style={{
            marginTop: 3,
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: "var(--t3)",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {hint.label}
        </div>
      </div>
      <div style={{ minWidth: 0, fontSize: 11, color: "var(--t2)", lineHeight: 1.45 }}>
        <div style={{ color: adapter.available ? "var(--green)" : "var(--t1)" }}>
          {adapter.available ? agentReadyLabel(adapter) : hint.action}
        </div>
        {!adapter.available && hint.commands.length > 0 && (
          <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 7 }}>
            {hint.commands.map((command) => (
              <CopyCommandLink
                key={command.command}
                command={command.command}
                label={command.label}
              />
            ))}
          </div>
        )}
        {!adapter.available && adapter.error && (
          <div
            style={{
              marginTop: 6,
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              color: "var(--t3)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {adapter.error}
          </div>
        )}
        {hint.note && <div style={{ marginTop: 5, color: "var(--t3)" }}>{hint.note}</div>}
        {adapter.docs_url && (
          <a
            href={adapter.docs_url}
            target="_blank"
            rel="noreferrer"
            style={{
              display: "inline-flex",
              marginTop: 5,
              color: "var(--teal)",
              textDecoration: "none",
            }}
          >
            setup docs
          </a>
        )}
      </div>
    </>
  );
}

function CopyCommandLink({ command, label }: { command: string; label?: string }) {
  async function copyCommand() {
    try {
      await navigator.clipboard?.writeText(command);
    } catch {
      // Clipboard access can be unavailable in tests or locked-down
      // webviews. The command remains visible and selectable.
    }
  }
  return (
    <button
      type="button"
      onClick={() => void copyCommand()}
      title={`Copy ${command}`}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 5,
        border: 0,
        background: "transparent",
        color: "var(--teal)",
        padding: 0,
        cursor: "pointer",
        fontFamily: "var(--font-mono)",
        fontSize: 11,
      }}
    >
      <span>{label || command}</span>
      <Icon d={Icons.copy} size={11} />
    </button>
  );
}

export function claudeCodePreflightState(
  adapter: AgentAdapterRecord | undefined,
  health: AgentAdapterHealthRecord | null,
): ClaudeCodePreflightState | null {
  if (!adapter || adapter.id !== "claude_code") return null;
  const hasSavedCredential = Boolean(adapter.credential_configured);
  const cliSignedIn = adapter.auth_status === "ok";
  const adapterInstalled = adapter.available && health?.status !== "not_installed";
  const tokenVerified = hasSavedCredential && health?.status === "ready";
  const tokenStatus: ClaudeCodePreflightState["tokenStatus"] = hasSavedCredential
    ? health?.status === "auth_required" || health?.status === "error"
      ? "invalid"
      : "saved_needs_check"
    : "not_saved";
  if (tokenVerified) {
    return null;
  }
  const base = { adapterInstalled, tokenStatus, cliSignedIn };

  if (!adapter.available || health?.status === "not_installed") {
    return {
      title: "Set up Claude Code before sending",
      body: "Install Claude Code, then paste the one-time token printed in Terminal by `claude setup-token`.",
      detail:
        health?.hint ||
        adapter.error ||
        "Hecate can manage the ACP launcher, but the Claude Code CLI still needs to be installed and signed in.",
      blockSend: true,
      tone: "amber",
      ...base,
    };
  }

  if (
    health?.status === "auth_required" ||
    (adapter.auth_status === "unauthenticated" && hasSavedCredential)
  ) {
    return {
      title: "Claude Code needs sign-in",
      body: "Hecate has a Claude Code token saved, but the adapter still reports authentication required. Generate a fresh token and save it here.",
      detail: health?.hint || adapter.auth_error,
      blockSend: true,
      tone: "amber",
      ...base,
    };
  }

  if (adapter.auth_status === "billing") {
    return {
      title: "Claude Code billing needs attention",
      body: "Claude Code reported a billing or subscription problem. Test the adapter, then fix the Claude account before sending.",
      detail: adapter.auth_error || health?.hint || health?.error,
      blockSend: true,
      tone: "red",
      ...base,
    };
  }

  if (!hasSavedCredential) {
    return {
      title: "Set up Claude Code token before sending",
      body: cliSignedIn
        ? "Claude Code is signed in for normal CLI use, but Hecate needs its own adapter token before it can start Claude ACP sessions."
        : "Claude Code needs an adapter-visible token before Hecate can start Claude ACP sessions.",
      detail: adapter.auth_error || health?.hint || health?.error,
      blockSend: true,
      tone: "amber",
      ...base,
    };
  }

  return {
    title: "Check Claude Code setup before sending",
    body: "Hecate has a Claude Code token saved, but it has not been verified by the adapter yet. Check auth or save a fresh token before sending.",
    detail: adapter.auth_error || health?.hint || health?.error,
    blockSend: true,
    tone: "amber",
    ...base,
  };
}

function ClaudeCodeSetupFacts({ state }: { state: ClaudeCodePreflightState }) {
  const tokenLabel =
    state.tokenStatus === "not_saved"
      ? "token not saved"
      : state.tokenStatus === "invalid"
        ? "token invalid"
        : "token saved, needs check";
  const tokenColor = state.tokenStatus === "invalid" ? "var(--danger)" : "var(--amber)";
  return (
    <div
      style={{
        display: "flex",
        flexWrap: "wrap",
        gap: 8,
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        marginTop: 5,
      }}
    >
      <span style={{ color: state.adapterInstalled ? "var(--teal)" : "var(--t3)" }}>
        adapter {state.adapterInstalled ? "installed" : "not installed"}
      </span>
      <span style={{ color: tokenColor }}>{tokenLabel}</span>
      {state.cliSignedIn && <span style={{ color: "var(--t3)" }}>CLI signed in</span>}
    </div>
  );
}

export function ClaudeCodePreflightCard({
  state,
  loading,
  onCopyInstall,
  onCopySetup,
  onOpenSetup,
  onTest,
}: {
  state: ClaudeCodePreflightState;
  loading: boolean;
  onCopyInstall: () => void;
  onCopySetup: () => void;
  onOpenSetup: () => void;
  onTest: () => void;
}) {
  const color = state.tone === "red" ? "var(--danger)" : "var(--amber)";
  const border = state.tone === "red" ? "rgba(239, 68, 68, 0.32)" : "rgba(245, 191, 79, 0.35)";
  const background = state.tone === "red" ? "rgba(239, 68, 68, 0.08)" : "rgba(245, 191, 79, 0.08)";

  return (
    <div
      data-testid="claude-code-preflight"
      style={{
        maxWidth: 820,
        margin: "0 auto 8px",
        border: `1px solid ${border}`,
        borderRadius: "var(--radius-sm)",
        background,
        padding: "9px 10px",
        display: "grid",
        gap: 8,
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          gap: 12,
        }}
      >
        <div style={{ minWidth: 0 }}>
          <div style={{ color, fontWeight: 700, fontSize: 12 }}>{state.title}</div>
          <div style={{ color: "var(--t1)", fontSize: 12, lineHeight: 1.45, marginTop: 3 }}>
            {state.body}
          </div>
          {state.detail && (
            <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.4, marginTop: 4 }}>
              {state.detail}
            </div>
          )}
          <ClaudeCodeSetupFacts state={state} />
        </div>
        <button
          type="button"
          className="btn btn-primary btn-sm"
          onClick={onOpenSetup}
          style={{ flexShrink: 0 }}
        >
          Open setup
        </button>
      </div>
      <div style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: 6 }}>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onCopyInstall}>
          Copy check command
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onCopySetup}>
          Copy setup command
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onTest} disabled={loading}>
          {loading ? "Checking auth..." : "Check auth"}
        </button>
      </div>
    </div>
  );
}

export function ClaudeCodeSetupEmptyPanel(props: {
  state: ClaudeCodePreflightState;
  loading: boolean;
  cliStatus?: AgentAdapterSetupCommandStatus;
  tokenDraft: string;
  tokenSaving: boolean;
  onTokenDraftChange: (value: string) => void;
  onSaveToken: () => void;
  onTest: () => void;
}) {
  const commandStatus = props.cliStatus;
  const claudeInstalled = Boolean(commandStatus?.available);
  const tokenCommand = claudeCodeSetupTokenCommand(commandStatus);
  const canSave = props.tokenDraft.trim().length > 0 && !props.tokenSaving;
  return (
    <div
      style={{
        margin: "18px auto 0",
        maxWidth: 620,
        textAlign: "left",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius)",
        background: "var(--bg2)",
        overflow: "hidden",
      }}
      data-testid="claude-code-preflight"
    >
      <ClaudeCodeSetupRow
        label="Install"
        title="Prepare Claude Code"
        detail="Hecate can run Claude Code through npx; a global `claude` install also works."
        command="npx -y @anthropic-ai/claude-code --version"
        done={claudeInstalled}
        statusText={
          claudeInstalled
            ? `Available${commandStatus?.command ? ` via ${commandStatus.command}` : ""}`
            : undefined
        }
      />
      <ClaudeCodeSetupRow
        label="Auth"
        title="Create an adapter token"
        detail="Copy this command into Terminal. After you click Authorize in the browser, return to Terminal and copy the token printed there. It is shown only once."
        command={tokenCommand}
      />
      <div
        style={{
          padding: "10px 12px",
          borderTop: "1px solid var(--border)",
          display: "grid",
          gridTemplateColumns: "minmax(120px, 0.7fr) minmax(0, 1.3fr)",
          gap: 10,
          alignItems: "start",
        }}
      >
        <div style={{ minWidth: 0 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
            <span style={{ color: "var(--amber)", display: "inline-flex", flexShrink: 0 }}>
              <Icon d={Icons.keys} size={11} />
            </span>
            <span style={{ fontSize: 12, fontWeight: 600, color: "var(--t1)" }}>Save token</span>
          </div>
          <div
            style={{
              marginTop: 3,
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              color: "var(--t3)",
            }}
          >
            CLAUDE_CODE_OAUTH_TOKEN
          </div>
        </div>
        <div style={{ minWidth: 0, fontSize: 11, color: "var(--t2)", lineHeight: 1.45 }}>
          <div>
            Paste the token from Terminal here. If you closed Terminal or missed the token, run the
            token command again to generate a fresh one.
          </div>
          <div style={{ marginTop: 4, color: "var(--t3)" }}>
            Save validates the token with Claude Code before Hecate stores it.
          </div>
          {props.state.detail && (
            <div style={{ marginTop: 4, color: "var(--t3)" }}>{props.state.detail}</div>
          )}
          <ClaudeCodeSetupFacts state={props.state} />
          <input
            aria-label="Claude Code OAuth token"
            value={props.tokenDraft}
            onChange={(event) => props.onTokenDraftChange(event.currentTarget.value)}
            placeholder="Paste token from claude setup-token"
            type="password"
            spellCheck={false}
            autoComplete="off"
            style={{
              width: "100%",
              marginTop: 8,
              border: "1px solid var(--border)",
              borderRadius: "var(--radius-sm)",
              background: "var(--bg1)",
              color: "var(--t1)",
              padding: "7px 8px",
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              outline: "none",
            }}
          />
          <div style={{ display: "flex", gap: 6, flexWrap: "wrap", marginTop: 8 }}>
            <button
              type="button"
              className="btn btn-primary btn-sm"
              onClick={props.onSaveToken}
              disabled={!canSave}
            >
              {props.tokenSaving ? "Checking auth..." : "Save"}
            </button>
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              onClick={props.onTest}
              disabled={props.loading}
            >
              {props.loading ? "Checking auth..." : "Check auth"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function ClaudeCodeSetupRow({
  label,
  title,
  detail,
  command,
  done = false,
  statusText,
}: {
  label: string;
  title: string;
  detail: string;
  command: string;
  done?: boolean;
  statusText?: string;
}) {
  async function copyCommand() {
    try {
      await navigator.clipboard?.writeText(command);
    } catch {
      // Clipboard access can be unavailable in tests or locked-down
      // webviews. The command is still visible and selectable.
    }
  }

  return (
    <div
      style={{
        padding: "10px 12px",
        borderTop: label === "Install" ? 0 : "1px solid var(--border)",
        display: "grid",
        gridTemplateColumns: "minmax(120px, 0.7fr) minmax(0, 1.3fr)",
        gap: 10,
        alignItems: "start",
      }}
    >
      <div style={{ minWidth: 0 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
          <span
            style={{
              color: done ? "var(--green)" : "var(--teal)",
              display: "inline-flex",
              flexShrink: 0,
            }}
          >
            <Icon d={done ? Icons.check : Icons.terminal} size={11} />
          </span>
          <span style={{ fontSize: 12, fontWeight: 600, color: "var(--t1)" }}>{title}</span>
        </div>
        <div
          style={{ marginTop: 3, fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}
        >
          {label}
        </div>
      </div>
      <div style={{ minWidth: 0, fontSize: 11, color: "var(--t2)", lineHeight: 1.45 }}>
        <div>{detail}</div>
        {done ? (
          <div style={{ marginTop: 7 }}>
            <code style={{ fontFamily: "var(--font-mono)", color: "var(--t2)" }}>{command}</code>
            {statusText && <div style={{ marginTop: 4, color: "var(--green)" }}>{statusText}</div>}
          </div>
        ) : (
          <button
            type="button"
            onClick={() => void copyCommand()}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 6,
              marginTop: 8,
              padding: 0,
              border: 0,
              background: "transparent",
              color: "var(--teal)",
              cursor: "pointer",
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              maxWidth: "100%",
              overflow: "hidden",
              textOverflow: "ellipsis",
            }}
            title={`Copy ${command}`}
          >
            <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{command}</span>
            <Icon d={Icons.copy} size={12} />
          </button>
        )}
      </div>
    </div>
  );
}

function agentSetupHint(adapter: AgentAdapterRecord): {
  label: string;
  action: string;
  commands: Array<{ label: string; command: string }>;
  note?: string;
} {
  switch (adapter.id) {
    case "codex":
      return {
        label: "Codex",
        action: "Install Codex CLI, then sign in with Codex.",
        commands: [
          { label: "Install", command: "npm install -g @openai/codex" },
          { label: "Auth", command: "codex login" },
        ],
      };
    case "claude_code":
      return {
        label: "Claude Code",
        action: "Install Claude Code, then create and save an adapter token.",
        commands: [
          { label: "Install", command: "npm install -g @anthropic-ai/claude-code" },
          { label: "Token", command: "claude setup-token" },
        ],
        note: "The Claude Code adapter token is separate from normal CLI login.",
      };
    case "cursor_agent":
      return {
        label: "Cursor",
        action: "Install Cursor with Agent support, then sign in from Cursor.",
        commands: [],
        note: "Cursor Agent is installed with the Cursor application, not npm.",
      };
    default:
      return {
        label: adapter.command || adapter.id,
        action: "Install the adapter command and test it in Settings.",
        commands: adapter.command
          ? [{ label: "Check", command: `${adapter.command} --version` }]
          : [],
      };
  }
}

function agentReadyLabel(adapter: AgentAdapterRecord): string {
  if (adapter.auth_status && adapter.auth_status !== "ok") {
    return adapter.auth_error || `Auth status: ${adapter.auth_status}`;
  }
  if (adapter.version) return `Ready · ${adapter.version}`;
  return "Ready";
}
