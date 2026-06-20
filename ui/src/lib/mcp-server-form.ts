export type MCPServerTransport = "stdio" | "http";
export type MCPApprovalPolicy = "auto" | "require_approval" | "block";

export type MCPServerFormEntry = {
  name: string;
  transport: MCPServerTransport;
  command: string;
  argsRaw: string;
  envRaw: string;
  url: string;
  headersRaw: string;
  approvalPolicy: MCPApprovalPolicy;
};

export type MCPServerPayload = {
  name: string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  approval_policy?: MCPApprovalPolicy;
};

export function createMCPServerFormEntry(): MCPServerFormEntry {
  return {
    name: "",
    transport: "stdio",
    command: "",
    argsRaw: "",
    envRaw: "",
    url: "",
    headersRaw: "",
    approvalPolicy: "auto",
  };
}

export function mcpServerFormEntryFromPayload(server: MCPServerPayload): MCPServerFormEntry {
  return {
    name: server.name ?? "",
    transport: server.url ? "http" : "stdio",
    command: server.command ?? "",
    argsRaw: (server.args ?? []).join(" "),
    envRaw: formatKeyValueLines(server.env),
    url: server.url ?? "",
    headersRaw: formatKeyValueLines(server.headers),
    approvalPolicy: normalizeApprovalPolicy(server.approval_policy),
  };
}

export function mcpServerFormEntriesFromPayload(
  servers: MCPServerPayload[] | undefined,
): MCPServerFormEntry[] {
  return (servers ?? []).map(mcpServerFormEntryFromPayload);
}

export function mcpServerFormEntriesToPayload(
  entries: MCPServerFormEntry[],
  options: { includeApprovalPolicy?: boolean } = {},
): MCPServerPayload[] {
  const includeApprovalPolicy = options.includeApprovalPolicy ?? true;
  return entries
    .filter((entry) => {
      return entry.name.trim() !== "" || entry.command.trim() !== "" || entry.url.trim() !== "";
    })
    .map((entry) => {
      const base: MCPServerPayload = { name: entry.name.trim() };
      if (entry.transport === "stdio") {
        base.command = entry.command.trim();
        if (entry.argsRaw.trim() !== "") base.args = entry.argsRaw.trim().split(/\s+/);
        const env = parseKeyValueLines(entry.envRaw);
        if (env) base.env = env;
      } else {
        base.url = entry.url.trim();
        const headers = parseKeyValueLines(entry.headersRaw);
        if (headers) base.headers = headers;
      }
      if (includeApprovalPolicy && entry.approvalPolicy && entry.approvalPolicy !== "auto") {
        base.approval_policy = entry.approvalPolicy;
      }
      return base;
    });
}

export function parseKeyValueLines(raw: string): Record<string, string> | undefined {
  const trimmed = raw.trim();
  if (trimmed === "") return undefined;
  const out: Record<string, string> = {};
  for (const line of trimmed.split(/\r?\n/)) {
    const idx = line.indexOf("=");
    if (idx <= 0) continue;
    const key = line.slice(0, idx).trim();
    const value = line.slice(idx + 1);
    if (key === "") continue;
    out[key] = value;
  }
  return Object.keys(out).length === 0 ? undefined : out;
}

function formatKeyValueLines(values: Record<string, string> | undefined): string {
  if (!values) return "";
  return Object.entries(values)
    .map(([key, value]) => `${key}=${value}`)
    .join("\n");
}

function normalizeApprovalPolicy(value: MCPServerPayload["approval_policy"]): MCPApprovalPolicy {
  switch (value) {
    case "require_approval":
    case "block":
      return value;
    default:
      return "auto";
  }
}
