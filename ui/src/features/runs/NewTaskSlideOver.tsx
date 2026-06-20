import { useEffect, useMemo, useState } from "react";
import type { ModelRecord } from "../../types/model";
import type { ProviderPresetRecord, ProviderRecord } from "../../types/provider";
import {
  Icon,
  Icons,
  ModelPicker,
  ProviderPicker,
  SlideOver,
  type ProviderOption,
} from "../shared/ui";
import { MCPServerEditor } from "../shared/MCPServerEditor";
import {
  mcpServerFormEntriesToPayload,
  type MCPApprovalPolicy,
  type MCPServerFormEntry,
} from "../../lib/mcp-server-form";

export type ExecutionKind = "shell" | "git" | "file" | "agent_loop";

const KIND_LABELS: Record<ExecutionKind, string> = {
  shell: "Shell",
  git: "Git",
  file: "File",
  agent_loop: "Agent loop",
};

function KindTab({
  kind,
  selected,
  onClick,
}: {
  kind: ExecutionKind;
  selected: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={selected}
      onClick={onClick}
      style={{
        padding: "5px 12px",
        fontSize: 11,
        fontFamily: "var(--font-mono)",
        background: selected ? "var(--teal)" : "transparent",
        color: selected ? "var(--bg0)" : "var(--t2)",
        border: "none",
        borderRadius: "var(--radius-sm)",
        cursor: "pointer",
        transition: "background 0.1s, color 0.1s",
        fontWeight: selected ? 600 : 400,
      }}
    >
      {KIND_LABELS[kind]}
    </button>
  );
}

export type CreateTaskPayload = {
  prompt: string;
  execution_kind: ExecutionKind;
  shell_command?: string;
  git_command?: string;
  file_path?: string;
  file_content?: string;
  file_operation?: string;
  working_directory?: string;
  requested_model?: string;
  requested_provider?: string;
  workspace_mode?: string;
  // Per-task agent_loop system prompt — narrowest layer (after
  // global / tenant / workspace CLAUDE.md|AGENTS.md).
  system_prompt?: string;
  // Per-task cost ceiling in micro-USD. The agent loop sums LLM
  // spend across turns and fails the run on overage. 0 / unset =
  // no ceiling.
  budget_micros_usd?: number;
  // External MCP servers the agent_loop run should bring up. Each
  // entry becomes either one stdio subprocess (command/args/env) or
  // one HTTP/SSE client (url/headers); its tools are exposed to the
  // LLM as `mcp__<name>__<tool>`. approval_policy gates how those
  // tool calls dispatch — see the orchestrator-side contract on
  // pkg/types.MCPApproval* constants. Empty array / unset = no
  // external tools (built-ins only).
  mcp_servers?: Array<{
    name: string;
    command?: string;
    args?: string[];
    env?: Record<string, string>;
    url?: string;
    headers?: Record<string, string>;
    approval_policy?: MCPApprovalPolicy;
  }>;
};

type Props = {
  open: boolean;
  models: ModelRecord[];
  // Provider catalog from /hecate/v1/providers/status (status + health) plus the
  // /hecate/v1/providers/presets list (display names). Both optional; when
  // unset the provider picker isn't rendered and the model picker
  // shows raw provider ids in its per-row suffix.
  providers?: ProviderRecord[];
  providerPresets?: ProviderPresetRecord[];
  defaultWorkspace?: string;
  busyAction: string;
  errorMessage?: string;
  onClose: () => void;
  onCreate: (payload: CreateTaskPayload) => void;
};

export function NewTaskSlideOver({
  open,
  models,
  providers = [],
  providerPresets = [],
  defaultWorkspace = "",
  busyAction,
  errorMessage,
  onClose,
  onCreate,
}: Props) {
  const normalizedDefaultWorkspace = defaultWorkspace.trim();
  const [taskKind, setTaskKind] = useState<ExecutionKind>("shell");
  const [taskPrompt, setTaskPrompt] = useState("");
  const [taskCommand, setTaskCommand] = useState("");
  const [taskGitCommand, setTaskGitCommand] = useState("");
  const [taskWorkingDir, setTaskWorkingDir] = useState(normalizedDefaultWorkspace);
  const [lastDefaultWorkspace, setLastDefaultWorkspace] = useState(normalizedDefaultWorkspace);
  const [taskFilePath, setTaskFilePath] = useState("");
  const [taskFileContent, setTaskFileContent] = useState("");
  const [taskFileOp, setTaskFileOp] = useState("write");
  const [taskModel, setTaskModel] = useState("");
  // Provider filter — "auto" means "any provider" (the request-router
  // picks based on the selected model). Selecting a specific provider
  // narrows the model dropdown to that provider's catalog. Mirrors
  // the chat surface's ProviderFilter pattern but kept local since
  // the new-task panel is a one-shot form, not a persisted setting.
  const [taskProvider, setTaskProvider] = useState("auto");
  // In-place mode: run inside the source directory rather than an
  // isolated clone. Toggling this on tells the gateway to use
  // working_directory as the sandbox root, so writes hit the real
  // repo. Off (default) gives the safer isolated-clone behavior.
  const [taskInPlace, setTaskInPlace] = useState(false);

  // Provider options for the picker — mirrors the chat header's
  // provider list: filter to healthy providers, attach kind +
  // configured flags from the preset catalog so the dropdown shows
  // a key indicator on cloud rows. Memoized so the picker doesn't
  // re-derive on every keystroke in the form fields.
  const providerOptions = useMemo<ProviderOption[]>(() => {
    return providers
      .filter((p) => p.healthy && p.name)
      .map((p) => {
        const preset = providerPresets.find((pp) => pp.id === p.name);
        return {
          id: p.name,
          name: preset?.name || p.name,
          healthy: p.healthy,
          kind: preset?.kind ?? p.kind,
          // Status from /hecate/v1/providers/status carries the credential state
          // implicitly: healthy + available means configured.
          configured: undefined,
        };
      });
  }, [providers, providerPresets]);

  // Models scoped to the selected provider. "auto" means show all.
  // The ModelPicker still type-filters within whatever slice we hand
  // it, so this doesn't fight the picker's internal filter.
  const scopedModels = useMemo(() => {
    if (taskProvider === "auto") return models;
    return models.filter((m) => m.metadata?.provider === taskProvider);
  }, [models, taskProvider]);
  const effectiveTaskModel = useMemo(() => {
    if (taskModel) return taskModel;
    return defaultModelID(scopedModels);
  }, [scopedModels, taskModel]);

  // When the operator switches provider, clear the model selection if
  // it's no longer in the scoped list. Without this the trigger
  // button would still display the previously selected model id even
  // though the dropdown wouldn't include it — confusing on submit
  // because the request would carry a model that doesn't belong to
  // the chosen provider.
  function handleProviderChange(next: string) {
    setTaskProvider(next);
    if (next !== "auto" && taskModel) {
      const stillValid = models.some((m) => m.id === taskModel && m.metadata?.provider === next);
      if (!stillValid) setTaskModel("");
    }
  }

  // Models known not to support tool-calling. Surfaced as
  // non-blocking warnings on the picker rows when the operator is
  // creating an agent_loop task (other execution kinds don't use
  // tools). Conservative list — substring match in lowercase, only
  // patterns where we're confident the model lacks tools. False
  // positives are worse than false negatives here: a wrongly-flagged
  // model is still selectable, but a wrongly-unflagged model just
  // produces the friendlier runtime error we already ship.
  const noToolsWarnings = useMemo<Map<string, string>>(() => {
    if (taskKind !== "agent_loop") return new Map();
    const noToolsHint =
      "Likely doesn't support tool-calling — agent_loop runs will fail. Try qwen2.5-coder, gpt-4o-mini, or claude-sonnet.";
    const patterns: RegExp[] = [
      /^smollm/i, // Ollama smollm / smollm2 (any size) — chat-only
      /^tinyllama/i, // Ollama tinyllama
      /^gemma:2b/i, // small Gemma — no native tool support
      /^phi:?[12](\.|:|$)/i, // phi:1, phi:2 (phi3+ does support tools)
      /^llama2/i, // base llama2 — no native function calling
      /embed/i, // embeddings models (nomic-embed-text, text-embedding-ada-002, etc.)
      /^all-minilm/i, // sentence-transformers
    ];
    const out = new Map<string, string>();
    for (const m of models) {
      if (patterns.some((p) => p.test(m.id))) out.set(m.id, noToolsHint);
    }
    return out;
  }, [models, taskKind]);
  // Per-task system prompt — only meaningful for agent_loop kind.
  // Empty value falls back to the tenant / workspace / global layers.
  const [taskSystemPrompt, setTaskSystemPrompt] = useState("");
  // Per-task cost ceiling. UI takes a USD float for ergonomics
  // ("$2.50") and converts to micro-USD (the wire shape) on submit.
  // Empty / 0 = no ceiling.
  const [taskBudgetUSD, setTaskBudgetUSD] = useState("");
  // External MCP servers — only meaningful for agent_loop. Edited
  // as raw text rows in the form; parsed into the API shape on
  // submit. Empty array = no external MCP host (built-in tools only).
  const [mcpServers, setMcpServers] = useState<MCPServerFormEntry[]>([]);

  useEffect(() => {
    if (!open) return;
    setTaskWorkingDir((current) => {
      const currentTrimmed = current.trim();
      if (currentTrimmed === "" || currentTrimmed === lastDefaultWorkspace) {
        return normalizedDefaultWorkspace;
      }
      return current;
    });
    setLastDefaultWorkspace(normalizedDefaultWorkspace);
  }, [lastDefaultWorkspace, normalizedDefaultWorkspace, open]);

  function formIsValid(): boolean {
    if (taskKind === "shell") return taskCommand.trim() !== "";
    if (taskKind === "git") return taskGitCommand.trim() !== "";
    if (taskKind === "file") return taskFilePath.trim() !== "";
    if (taskKind === "agent_loop") return taskPrompt.trim() !== "" && effectiveTaskModel !== "";
    return false;
  }

  function submit() {
    const command =
      taskKind === "shell" ? taskCommand.trim() : taskKind === "git" ? taskGitCommand.trim() : "";
    const filePath = taskKind === "file" ? taskFilePath.trim() : "";
    if (taskKind === "shell" && !command) return;
    if (taskKind === "git" && !command) return;
    if (taskKind === "file" && !filePath) return;
    // MCP servers: only attach for agent_loop kind. Drop blank rows
    // (operator added then abandoned), parse args on whitespace,
    // parse env / headers as KEY=VALUE per non-empty line. The
    // gateway re-validates names/commands/urls and returns 400 with
    // a concrete diagnostic if any row is malformed; this layer
    // just drops obviously-empty rows so the user doesn't get a
    // "name required at index 3" for a row they thought they cleared.
    //
    // Per-row, only the active transport's fields are emitted —
    // mixing command + url is a hard error at the gateway, so we
    // never send both even if the operator's stale state for the
    // inactive side is still in memory.
    const mcpPayload = taskKind === "agent_loop" ? mcpServerFormEntriesToPayload(mcpServers) : [];

    onCreate({
      prompt:
        taskPrompt.trim() ||
        (taskKind === "shell" ? command : taskKind === "git" ? `git ${command}` : filePath),
      execution_kind: taskKind,
      ...(taskKind === "shell" ? { shell_command: command } : {}),
      ...(taskKind === "git" ? { git_command: command } : {}),
      ...(taskKind === "file"
        ? { file_path: filePath, file_content: taskFileContent, file_operation: taskFileOp }
        : {}),
      ...(taskWorkingDir.trim() ? { working_directory: taskWorkingDir.trim() } : {}),
      ...(effectiveTaskModel ? { requested_model: effectiveTaskModel } : {}),
      ...(taskProvider !== "auto" ? { requested_provider: taskProvider } : {}),
      ...(taskInPlace ? { workspace_mode: "in_place" } : {}),
      ...(taskKind === "agent_loop" && taskSystemPrompt.trim()
        ? { system_prompt: taskSystemPrompt.trim() }
        : {}),
      ...(taskKind === "agent_loop" && parseFloat(taskBudgetUSD) > 0
        ? { budget_micros_usd: Math.round(parseFloat(taskBudgetUSD) * 1_000_000) }
        : {}),
      ...(mcpPayload.length > 0 ? { mcp_servers: mcpPayload } : {}),
    });
    setTaskPrompt("");
    setTaskCommand("");
    setTaskGitCommand("");
    setTaskWorkingDir(normalizedDefaultWorkspace);
    setTaskFilePath("");
    setTaskFileContent("");
    setTaskFileOp("write");
    setTaskSystemPrompt("");
    setTaskBudgetUSD("");
    setTaskProvider("auto");
    setTaskModel("");
    setTaskInPlace(false);
    setMcpServers([]);
  }

  if (!open) return null;

  return (
    <SlideOver
      title="New task"
      onClose={onClose}
      width={480}
      footer={
        <div style={{ display: "flex", gap: 8 }}>
          <button
            className="btn btn-primary"
            style={{ flex: 1, justifyContent: "center" }}
            disabled={!formIsValid() || busyAction === "create"}
            onClick={submit}
            type="button"
          >
            <Icon d={Icons.send} size={14} /> {busyAction === "create" ? "Creating…" : "Queue task"}
          </button>
          <button className="btn" onClick={onClose} type="button">
            Cancel
          </button>
        </div>
      }
    >
      <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
        <div>
          <label
            style={{
              fontSize: 11,
              color: "var(--t2)",
              display: "block",
              marginBottom: 6,
              fontFamily: "var(--font-mono)",
            }}
          >
            EXECUTION KIND
          </label>
          <div
            style={{
              display: "flex",
              gap: 4,
              background: "var(--bg2)",
              borderRadius: "var(--radius)",
              padding: 3,
              border: "1px solid var(--border)",
            }}
          >
            {(["shell", "git", "file", "agent_loop"] as ExecutionKind[]).map((k) => (
              <KindTab key={k} kind={k} selected={taskKind === k} onClick={() => setTaskKind(k)} />
            ))}
          </div>
        </div>

        {taskKind === "shell" && (
          <div>
            <label
              style={{
                fontSize: 11,
                color: "var(--t2)",
                display: "block",
                marginBottom: 4,
                fontFamily: "var(--font-mono)",
              }}
            >
              SHELL COMMAND <span style={{ color: "var(--red)" }}>*</span>
            </label>
            <div
              style={{
                display: "flex",
                alignItems: "center",
                background: "var(--bg0)",
                border: "1px solid var(--border)",
                borderRadius: "var(--radius-sm)",
                padding: "0 10px",
              }}
            >
              <span
                style={{
                  fontFamily: "var(--font-mono)",
                  fontSize: 12,
                  color: "var(--t3)",
                  marginRight: 6,
                }}
              >
                $
              </span>
              <input
                className="input"
                aria-label="Shell command"
                style={{ border: "none", background: "transparent", padding: "7px 0", flex: 1 }}
                placeholder="ls -la / echo hello"
                value={taskCommand}
                onChange={(e) => setTaskCommand(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && formIsValid() && submit()}
              />
            </div>
            <div
              style={{
                fontSize: 10,
                color: "var(--amber)",
                fontFamily: "var(--font-mono)",
                marginTop: 4,
              }}
            >
              Shell execution requires approval before running.
            </div>
          </div>
        )}

        {taskKind === "git" && (
          <div>
            <label
              style={{
                fontSize: 11,
                color: "var(--t2)",
                display: "block",
                marginBottom: 4,
                fontFamily: "var(--font-mono)",
              }}
            >
              GIT COMMAND <span style={{ color: "var(--red)" }}>*</span>
            </label>
            <div
              style={{
                display: "flex",
                alignItems: "center",
                background: "var(--bg0)",
                border: "1px solid var(--border)",
                borderRadius: "var(--radius-sm)",
                padding: "0 10px",
              }}
            >
              <span
                style={{
                  fontFamily: "var(--font-mono)",
                  fontSize: 12,
                  color: "var(--t3)",
                  marginRight: 6,
                }}
              >
                git
              </span>
              <input
                className="input"
                aria-label="Git command"
                style={{ border: "none", background: "transparent", padding: "7px 0", flex: 1 }}
                placeholder="status / log --oneline -5"
                value={taskGitCommand}
                onChange={(e) => setTaskGitCommand(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && formIsValid() && submit()}
              />
            </div>
          </div>
        )}

        {taskKind === "file" && (
          <>
            <div>
              <label
                style={{
                  fontSize: 11,
                  color: "var(--t2)",
                  display: "block",
                  marginBottom: 4,
                  fontFamily: "var(--font-mono)",
                }}
              >
                OPERATION
              </label>
              <div style={{ display: "flex", gap: 8 }}>
                {["write", "append"].map((op) => (
                  <label
                    key={op}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 6,
                      fontSize: 12,
                      color: taskFileOp === op ? "var(--t0)" : "var(--t2)",
                      cursor: "pointer",
                    }}
                  >
                    <input
                      type="radio"
                      aria-label={`File operation: ${op}`}
                      checked={taskFileOp === op}
                      onChange={() => setTaskFileOp(op)}
                      style={{ accentColor: "var(--teal)" }}
                    />
                    {op}
                  </label>
                ))}
              </div>
            </div>
            <div>
              <label
                style={{
                  fontSize: 11,
                  color: "var(--t2)",
                  display: "block",
                  marginBottom: 4,
                  fontFamily: "var(--font-mono)",
                }}
              >
                FILE PATH <span style={{ color: "var(--red)" }}>*</span>
              </label>
              <input
                className="input"
                aria-label="File path"
                placeholder="/path/to/file.txt"
                value={taskFilePath}
                onChange={(e) => setTaskFilePath(e.target.value)}
              />
            </div>
            <div>
              <label
                style={{
                  fontSize: 11,
                  color: "var(--t2)",
                  display: "block",
                  marginBottom: 4,
                  fontFamily: "var(--font-mono)",
                }}
              >
                CONTENT
              </label>
              <textarea
                className="input"
                aria-label="File content"
                placeholder="File content…"
                rows={4}
                style={{ resize: "vertical" }}
                value={taskFileContent}
                onChange={(e) => setTaskFileContent(e.target.value)}
              />
            </div>
          </>
        )}

        {taskKind === "agent_loop" && (
          <div>
            <label
              style={{
                fontSize: 11,
                color: "var(--t2)",
                display: "block",
                marginBottom: 4,
                fontFamily: "var(--font-mono)",
              }}
            >
              PROMPT <span style={{ color: "var(--red)" }}>*</span>
            </label>
            <textarea
              className="input"
              aria-label="Agent loop prompt"
              placeholder="Describe the task…"
              rows={4}
              style={{ resize: "vertical" }}
              value={taskPrompt}
              onChange={(e) => setTaskPrompt(e.target.value)}
            />
          </div>
        )}

        {(taskKind === "shell" || taskKind === "git" || taskKind === "agent_loop") && (
          <div>
            <label
              style={{
                fontSize: 11,
                color: "var(--t2)",
                display: "block",
                marginBottom: 4,
                fontFamily: "var(--font-mono)",
              }}
            >
              WORKSPACE
            </label>
            <input
              className="input"
              aria-label="Workspace path"
              placeholder="/Users/alice/dev/project"
              value={taskWorkingDir}
              onChange={(e) => setTaskWorkingDir(e.target.value)}
            />
            {/* In-place toggle: skip the temp-dir clone and run
                  directly in the source path. Default off (safer
                  isolated clone). When on, the path entered above
                  must be an absolute, existing directory or the run
                  will fail before starting with a clear error. */}
            <label
              style={{
                display: "flex",
                alignItems: "center",
                gap: 6,
                marginTop: 8,
                fontSize: 12,
                color: taskInPlace ? "var(--t0)" : "var(--t2)",
                cursor: "pointer",
              }}
            >
              <input
                type="checkbox"
                checked={taskInPlace}
                onChange={(e) => setTaskInPlace(e.target.checked)}
                style={{ accentColor: "var(--teal)" }}
              />
              Run directly in this workspace
            </label>
            {/* Workspace preview — always visible so the operator
                  knows up-front where writes will land. The
                  isolated-clone path uses ${TMPDIR}/hecate-workspaces/
                  {task_id}/{run_id}; we render the pattern rather
                  than a concrete path because task/run ids don't
                  exist until create-time. The in-place case
                  reflects the actual entered path so the operator
                  can sanity-check before submitting. */}
            <WorkspacePreview workingDir={taskWorkingDir} inPlace={taskInPlace} />
          </div>
        )}

        {taskKind !== "agent_loop" && (
          <div>
            <label
              style={{
                fontSize: 11,
                color: "var(--t2)",
                display: "block",
                marginBottom: 4,
                fontFamily: "var(--font-mono)",
              }}
            >
              DESCRIPTION <span style={{ color: "var(--t3)" }}>(optional)</span>
            </label>
            <input
              className="input"
              aria-label="Task description"
              placeholder="Human-readable description…"
              value={taskPrompt}
              onChange={(e) => setTaskPrompt(e.target.value)}
            />
          </div>
        )}

        {taskKind === "agent_loop" && (
          <div>
            <label
              style={{
                fontSize: 11,
                color: "var(--t2)",
                display: "block",
                marginBottom: 4,
                fontFamily: "var(--font-mono)",
              }}
            >
              SYSTEM PROMPT <span style={{ color: "var(--t3)" }}>(optional, narrowest layer)</span>
            </label>
            <textarea
              className="input"
              aria-label="System prompt"
              placeholder="Per-task agent directives. Stacks under global / tenant / workspace CLAUDE.md|AGENTS.md."
              rows={3}
              style={{ resize: "vertical" }}
              value={taskSystemPrompt}
              onChange={(e) => setTaskSystemPrompt(e.target.value)}
            />
          </div>
        )}

        {taskKind === "agent_loop" && (
          <div>
            <label
              style={{
                fontSize: 11,
                color: "var(--t2)",
                display: "block",
                marginBottom: 4,
                fontFamily: "var(--font-mono)",
              }}
            >
              COST CEILING (USD){" "}
              <span style={{ color: "var(--t3)" }}>(optional, fails the run on overage)</span>
            </label>
            <input
              className="input"
              aria-label="Cost ceiling in USD"
              type="number"
              step="0.01"
              min="0"
              placeholder="2.50"
              value={taskBudgetUSD}
              onChange={(e) => setTaskBudgetUSD(e.target.value)}
            />
          </div>
        )}

        {taskKind === "agent_loop" && (
          <MCPServerEditor
            entries={mcpServers}
            onChange={setMcpServers}
            label="MCP SERVERS"
            description="(optional, exposes external tools as mcp__<name>__<tool>)"
          />
        )}

        <div>
          <label
            style={{
              fontSize: 11,
              color: "var(--t2)",
              display: "block",
              marginBottom: 4,
              fontFamily: "var(--font-mono)",
            }}
          >
            PROVIDER & MODEL
          </label>
          <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
            <ProviderPicker
              value={taskProvider}
              onChange={handleProviderChange}
              options={providerOptions}
              includeAuto
              autoLabel="Any provider"
            />
            <ModelPicker
              value={effectiveTaskModel}
              onChange={setTaskModel}
              models={scopedModels}
              presets={providerPresets}
              runtimeProviders={providers}
              // Hide the per-row provider suffix when a specific
              // provider is already pinned — every row would carry
              // the same suffix.
              showProvider={taskProvider === "auto"}
              // Non-blocking ⚠ marker on models that probably
              // can't tool-call (agent_loop only). The runtime
              // error message we ship is friendly, but flagging
              // up-front saves a wasted run.
              modelWarnings={noToolsWarnings}
            />
          </div>
        </div>

        {errorMessage && (
          <div style={{ fontSize: 12, color: "var(--red)", fontFamily: "var(--font-mono)" }}>
            {errorMessage}
          </div>
        )}
      </div>
    </SlideOver>
  );
}

function defaultModelID(models: ModelRecord[]): string {
  return models.find((m) => m.metadata?.default)?.id || "";
}

// WorkspacePreview tells the operator where writes will land on
// task creation. Isolated-clone mode (default) renders the path
// pattern; in-place mode renders the resolved source path with an
// amber warning that writes will mutate the source. Validation
// here is intentionally light — the gateway rejects malformed
// in-place paths at run-create time with a concrete error; we
// only flag the obvious missing-path case so the operator
// notices before submitting.
function WorkspacePreview({ workingDir, inPlace }: { workingDir: string; inPlace: boolean }) {
  const trimmed = workingDir.trim();
  const isAbs = trimmed.startsWith("/") || /^[A-Za-z]:\\/.test(trimmed);
  if (inPlace) {
    if (!trimmed) {
      return (
        <div
          style={{
            fontSize: 10,
            color: "var(--red)",
            fontFamily: "var(--font-mono)",
            marginTop: 4,
          }}
        >
          ⚠ Direct workspace mode needs an absolute workspace path — the run will fail without it.
        </div>
      );
    }
    if (!isAbs) {
      return (
        <div
          style={{
            fontSize: 10,
            color: "var(--red)",
            fontFamily: "var(--font-mono)",
            marginTop: 4,
          }}
        >
          ⚠ In-place mode needs an absolute path — relative paths are rejected.
        </div>
      );
    }
    return (
      <div
        style={{
          fontSize: 10,
          color: "var(--amber)",
          fontFamily: "var(--font-mono)",
          marginTop: 4,
        }}
      >
        Workspace: <span style={{ color: "var(--t1)" }}>{trimmed}</span> · writes land here directly
      </div>
    );
  }
  // Isolated-clone (default).
  return (
    <div style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)", marginTop: 4 }}>
      Workspace: isolated clone at{" "}
      <span style={{ color: "var(--t2)" }}>{"${TMPDIR}/hecate-workspaces/<task_id>/<run_id>"}</span>
      {trimmed && (
        <>
          {" "}
          · cloned from <span style={{ color: "var(--t2)" }}>{trimmed}</span>
        </>
      )}
    </div>
  );
}
