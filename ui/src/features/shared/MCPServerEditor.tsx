import {
  createMCPServerFormEntry,
  type MCPApprovalPolicy,
  type MCPServerFormEntry,
  type MCPServerTransport,
} from "../../lib/mcp-server-form";

import { Icon, Icons } from "./ui";

type Props = {
  entries: MCPServerFormEntry[];
  onChange: (entries: MCPServerFormEntry[]) => void;
  label?: string;
  description?: string;
  addButtonLabel?: string;
  includeApprovalPolicy?: boolean;
  compact?: boolean;
};

export function MCPServerEditor({
  entries,
  onChange,
  label = "MCP servers",
  description,
  addButtonLabel = "Add MCP server",
  includeApprovalPolicy = true,
  compact = false,
}: Props) {
  function updateMCPServer(index: number, patch: Partial<MCPServerFormEntry>) {
    onChange(entries.map((entry, i) => (i === index ? { ...entry, ...patch } : entry)));
  }

  function addMCPServer() {
    onChange([...entries, createMCPServerFormEntry()]);
  }

  function removeMCPServer(index: number) {
    onChange(entries.filter((_, i) => i !== index));
  }

  return (
    <div>
      {label && (
        <label
          style={{
            fontSize: 11,
            color: "var(--t2)",
            display: "block",
            marginBottom: 4,
            fontFamily: "var(--font-mono)",
          }}
        >
          {label}
          {description && <span style={{ color: "var(--t3)" }}> {description}</span>}
        </label>
      )}
      <div style={{ display: "flex", flexDirection: "column", gap: compact ? 6 : 8 }}>
        {entries.map((entry, i) => (
          <div
            key={i}
            style={{
              border: "1px solid var(--border)",
              borderRadius: "var(--radius-sm)",
              padding: compact ? 7 : 8,
              background: "var(--bg0)",
              display: "flex",
              flexDirection: "column",
              gap: 6,
            }}
          >
            <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
              <input
                className="input"
                aria-label={`MCP server ${i + 1} name`}
                placeholder="name (e.g. filesystem)"
                value={entry.name}
                onChange={(e) => updateMCPServer(i, { name: e.target.value })}
                style={{ flex: 1 }}
              />
              <button
                className="btn btn-ghost btn-sm"
                style={{ padding: "3px 6px" }}
                onClick={() => removeMCPServer(i)}
                title="Remove this server"
                aria-label={`Remove MCP server ${entry.name || i + 1}`}
                type="button"
              >
                <Icon d={Icons.x} size={12} />
              </button>
            </div>
            <PillToggle
              ariaLabel={`Server ${i + 1} transport`}
              options={[
                { value: "stdio", label: "stdio" },
                { value: "http", label: "HTTP" },
              ]}
              value={entry.transport}
              onChange={(value) => updateMCPServer(i, { transport: value as MCPServerTransport })}
            />
            {entry.transport === "stdio" && (
              <>
                <input
                  className="input"
                  aria-label={`MCP server ${i + 1} command`}
                  placeholder="command (e.g. npx)"
                  value={entry.command}
                  onChange={(e) => updateMCPServer(i, { command: e.target.value })}
                />
                <input
                  className="input"
                  aria-label={`MCP server ${i + 1} args`}
                  placeholder="args (space-separated, e.g. -y @modelcontextprotocol/server-filesystem /workspace)"
                  value={entry.argsRaw}
                  onChange={(e) => updateMCPServer(i, { argsRaw: e.target.value })}
                />
                <textarea
                  className="input"
                  aria-label={`MCP server ${i + 1} environment`}
                  placeholder="env (KEY=VALUE per line; $VAR_NAME refers to gateway env)"
                  rows={compact ? 1 : 2}
                  style={{ resize: "vertical" }}
                  value={entry.envRaw}
                  onChange={(e) => updateMCPServer(i, { envRaw: e.target.value })}
                />
              </>
            )}
            {entry.transport === "http" && (
              <>
                <input
                  className="input"
                  aria-label={`MCP server ${i + 1} URL`}
                  placeholder="url (e.g. https://api.example.com/mcp)"
                  value={entry.url}
                  onChange={(e) => updateMCPServer(i, { url: e.target.value })}
                />
                <textarea
                  className="input"
                  aria-label={`MCP server ${i + 1} headers`}
                  placeholder="headers (KEY=VALUE per line, e.g. Authorization=Bearer $GITHUB_TOKEN)"
                  rows={compact ? 1 : 2}
                  style={{ resize: "vertical" }}
                  value={entry.headersRaw}
                  onChange={(e) => updateMCPServer(i, { headersRaw: e.target.value })}
                />
              </>
            )}
            {includeApprovalPolicy && (
              <div style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 2 }}>
                <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>
                  APPROVAL
                </span>
                <PillToggle
                  ariaLabel={`Server ${i + 1} approval policy`}
                  options={[
                    { value: "auto", label: "auto" },
                    { value: "require_approval", label: "require approval" },
                    { value: "block", label: "block" },
                  ]}
                  value={entry.approvalPolicy}
                  onChange={(value) =>
                    updateMCPServer(i, { approvalPolicy: value as MCPApprovalPolicy })
                  }
                />
              </div>
            )}
          </div>
        ))}
        <button
          className="btn btn-ghost btn-sm"
          style={{ alignSelf: "flex-start" }}
          onClick={addMCPServer}
          type="button"
        >
          {addButtonLabel}
        </button>
      </div>
    </div>
  );
}

function PillToggle<T extends string>({
  ariaLabel,
  options,
  value,
  onChange,
}: {
  ariaLabel: string;
  options: Array<{ value: T; label: string }>;
  value: T;
  onChange: (next: T) => void;
}) {
  return (
    <div
      role="group"
      aria-label={ariaLabel}
      style={{
        display: "inline-flex",
        gap: 4,
        background: "var(--bg2)",
        borderRadius: "var(--radius)",
        padding: 3,
        border: "1px solid var(--border)",
      }}
    >
      {options.map((option) => {
        const selected = value === option.value;
        return (
          <button
            key={option.value}
            type="button"
            aria-pressed={selected}
            onClick={() => onChange(option.value)}
            style={{
              border: 0,
              borderRadius: "var(--radius-sm)",
              padding: "3px 8px",
              background: selected ? "var(--teal)" : "transparent",
              color: selected ? "var(--bg0)" : "var(--t2)",
              fontSize: 10,
              fontFamily: "var(--font-mono)",
              cursor: "pointer",
            }}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );
}
