import { useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";

import { chooseWorkspaceDirectory } from "../../lib/api";
import { projectDefaultWorkspaceFromRoots } from "../../lib/project-workspace";
import type { AgentPresetRecord } from "../../types/agent-preset";
import type { ModelRecord } from "../../types/model";
import type { ProjectRecord } from "../../types/project";
import type { ProviderPresetRecord } from "../../types/provider";
import {
  Icon,
  Icons,
  InlineError,
  ModelPicker,
  ProviderPicker,
  type ProviderOption,
} from "../shared/ui";
import {
  normalizeWorkspaceMode,
  projectDefaultsFormFromProject,
  projectDefaultsFormsEqual,
  projectRootFormKey,
  projectRootOptionLabel,
  projectRootSummary,
  rebaseProjectDefaultsForm,
  type ProjectDefaultsForm,
} from "./projectSettings";

export function ProjectSettingsPanel({
  agentPresets,
  agentPresetsError,
  error,
  models,
  pending,
  providerOptions,
  providerPresets,
  project,
  rootsPending,
  onClose,
  onDiscoverRoots,
  onOpenCreateWorktree,
  onSave,
}: {
  agentPresets: AgentPresetRecord[];
  agentPresetsError: string;
  error: string;
  models: ModelRecord[];
  pending: boolean;
  providerOptions: ProviderOption[];
  providerPresets: ProviderPresetRecord[];
  project: ProjectRecord;
  rootsPending: boolean;
  onClose: () => void;
  onDiscoverRoots: () => void | Promise<void>;
  onOpenCreateWorktree: () => void;
  onSave: (form: ProjectDefaultsForm) => void | Promise<void>;
}) {
  const [form, setForm] = useState<ProjectDefaultsForm>(() =>
    projectDefaultsFormFromProject(project),
  );
  const savedProjectIDRef = useRef(project.id);
  const savedFormRef = useRef(projectDefaultsFormFromProject(project));
  const titleRef = useRef<HTMLHeadingElement>(null);
  const [rootChooseError, setRootChooseError] = useState("");
  useEffect(() => {
    titleRef.current?.focus();
  }, []);
  useEffect(() => {
    const nextSavedForm = projectDefaultsFormFromProject(project);
    const previousSavedForm = savedFormRef.current;
    const projectChanged = savedProjectIDRef.current !== project.id;
    setForm((current) =>
      projectChanged
        ? nextSavedForm
        : rebaseProjectDefaultsForm(current, previousSavedForm, nextSavedForm),
    );
    savedProjectIDRef.current = project.id;
    savedFormRef.current = nextSavedForm;
  }, [project]);
  const scopedModels = useMemo(() => {
    if (!form.provider) return models;
    return models.filter((model) => model.metadata?.provider === form.provider);
  }, [form.provider, models]);
  const selectedPreset = useMemo(
    () => agentPresets.find((preset) => preset.id === form.defaultAgentPreset) ?? null,
    [agentPresets, form.defaultAgentPreset],
  );
  const savedForm = useMemo(() => projectDefaultsFormFromProject(project), [project]);
  const dirty = useMemo(() => !projectDefaultsFormsEqual(form, savedForm), [form, savedForm]);
  const workspaceMode = normalizeWorkspaceMode(form.workspaceMode);
  const knownWorkspaceMode = ["", "ephemeral", "persistent", "in_place"].includes(workspaceMode);

  function handleProviderChange(provider: string) {
    setForm((current) => {
      const nextModels = provider
        ? models.filter((model) => model.metadata?.provider === provider)
        : models;
      const modelStillValid =
        current.model &&
        nextModels.some(
          (model) =>
            model.id === current.model && (!provider || model.metadata?.provider === provider),
        );
      return {
        ...current,
        provider,
        model: modelStillValid ? current.model : "",
      };
    });
  }
  function handleDefaultRootChange(rootID: string) {
    setForm((current) => ({
      ...current,
      defaultRootID: rootID,
    }));
  }
  function handleRootActiveChange(rootIndex: number, active: boolean) {
    setForm((current) => ({
      ...current,
      roots: current.roots.map((root, index) => (index === rootIndex ? { ...root, active } : root)),
    }));
  }
  async function handleChooseRoot() {
    setRootChooseError("");
    try {
      const workspace = await chooseWorkspaceDirectory();
      const path = workspace.data.path.trim();
      if (!path) return;
      setForm((current) => {
        if (current.roots.some((root) => root.path.trim() === path)) return current;
        const nextRoot = {
          path,
          kind: "local",
          git_branch: workspace.data.branch || undefined,
          active: true,
        };
        return {
          ...current,
          defaultRootID:
            current.roots.length === 0 ? projectRootFormKey(nextRoot) : current.defaultRootID,
          roots: [...current.roots, nextRoot],
        };
      });
    } catch (err) {
      setRootChooseError(err instanceof Error ? err.message : "Failed to choose workspace folder.");
    }
  }
  const submitForm = () => onSave(form);

  const selectedFormRoot = form.roots.find(
    (root) => projectRootFormKey(root) === form.defaultRootID,
  );
  const workspace =
    selectedFormRoot?.path || projectDefaultWorkspaceFromRoots(form.roots, form.defaultRootID);
  const rootCount = form.roots.length;
  const isRootless = rootCount === 0;
  const rootlessWorktreeHelpID = "project-settings-rootless-worktree-help";

  return (
    <div
      style={{
        background: "var(--bg1)",
        display: "flex",
        flexDirection: "column",
        flex: 1,
        minHeight: 0,
        minWidth: 0,
      }}
    >
      <div
        style={{
          borderBottom: "1px solid var(--border)",
          padding: "14px 14px 12px",
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          gap: 12,
        }}
      >
        <div style={{ minWidth: 0 }}>
          <h1
            ref={titleRef}
            tabIndex={-1}
            style={{ fontSize: 14, fontWeight: 650, color: "var(--t0)", margin: 0 }}
          >
            Project settings
          </h1>
          <div
            style={{
              marginTop: 4,
              fontSize: 11,
              color: "var(--t3)",
              lineHeight: 1.45,
            }}
          >
            Choose defaults for future agent work and optional local files. Existing assignments
            keep the settings they started with.
          </div>
        </div>
        <button
          aria-label="Back to project"
          className="btn btn-ghost btn-sm"
          disabled={pending}
          title={pending ? "Wait for settings to finish saving" : "Back to project"}
          type="button"
          onClick={onClose}
        >
          <Icon d={Icons.chevL} size={13} />
          Back
        </button>
      </div>
      <div style={{ overflowY: "auto", padding: 14, display: "grid", gap: 14 }}>
        <form
          aria-busy={pending}
          onSubmit={(event) => {
            event.preventDefault();
            if (!pending && dirty) void submitForm();
          }}
          style={{ display: "grid", gap: 14 }}
        >
          {error && <InlineError message={error} />}
          {rootChooseError && <InlineError message={rootChooseError} />}
          {agentPresetsError && <InlineError message={agentPresetsError} />}
          <fieldset disabled={pending} style={settingsFieldsetStyle}>
            <ProjectSettingsSection title="Launch defaults">
              <div style={{ ...subtleTextStyle, marginBottom: 12 }}>
                Used for future Hecate Task and External Agent assignments. Human work does not need
                a provider or model.
              </div>
              <div style={{ display: "grid", gap: 12 }}>
                <div style={fieldStyle}>
                  <span style={fieldLabelStyle}>Provider and model</span>
                  <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
                    <ProviderPicker
                      value={form.provider}
                      onChange={handleProviderChange}
                      options={providerOptions}
                      emptyLabel={
                        providerOptions.length === 0 ? "no providers configured" : "select provider"
                      }
                    />
                    <ModelPicker
                      value={form.model}
                      onChange={(model) => setForm((current) => ({ ...current, model }))}
                      models={scopedModels}
                      presets={providerPresets}
                      includeAll
                      allLabel="inherit runtime default"
                      showProvider={!form.provider}
                    />
                  </div>
                </div>
                <div style={fieldStyle}>
                  <span style={fieldLabelStyle}>Agent behavior</span>
                  <select
                    aria-label="Default agent preset"
                    className="input"
                    value={form.defaultAgentPreset}
                    onChange={(event) =>
                      setForm((current) => ({
                        ...current,
                        defaultAgentPreset: event.target.value,
                      }))
                    }
                    style={{ fontFamily: "var(--font-mono)", fontSize: 12, minHeight: 36 }}
                  >
                    <option value="">Standard project assignment</option>
                    {agentPresets.map((preset) => (
                      <option key={preset.id} value={preset.id}>
                        {preset.name || preset.id}
                      </option>
                    ))}
                  </select>
                  <PresetPosturePreview preset={selectedPreset} />
                </div>
                <div style={fieldStyle}>
                  <span style={fieldLabelStyle}>Workspace behavior</span>
                  <div style={{ position: "relative", width: "100%" }}>
                    <select
                      aria-label="Workspace behavior"
                      className="input"
                      value={workspaceMode}
                      onChange={(event) =>
                        setForm((current) => ({ ...current, workspaceMode: event.target.value }))
                      }
                      style={{
                        appearance: "none",
                        cursor: "pointer",
                        fontFamily: "var(--font-mono)",
                        fontSize: 12,
                        minHeight: 36,
                        paddingRight: 34,
                      }}
                    >
                      <option value="">Isolated copy (recommended)</option>
                      <option value="ephemeral">Isolated copy (ephemeral setting)</option>
                      <option value="persistent">Isolated copy (persistent setting)</option>
                      <option value="in_place">Attached folder (writes directly)</option>
                      {!knownWorkspaceMode && (
                        <option value={workspaceMode}>Existing setting ({workspaceMode})</option>
                      )}
                    </select>
                    <span
                      aria-hidden="true"
                      style={{
                        alignItems: "center",
                        color: "var(--t2)",
                        display: "inline-flex",
                        height: "100%",
                        pointerEvents: "none",
                        position: "absolute",
                        right: 11,
                        top: 0,
                      }}
                    >
                      <Icon d={Icons.chevD} size={12} />
                    </span>
                  </div>
                  <div style={subtleTextStyle}>
                    Isolated modes currently use a fresh copy for each run. Writing directly to an
                    attached folder is always an explicit choice.
                  </div>
                </div>
              </div>
            </ProjectSettingsSection>
            <ProjectSettingsSection title="Local files">
              <div style={{ ...subtleTextStyle, marginBottom: 12 }}>
                Folders are optional. Attach one when this project needs documents, code, or local
                guidance.
              </div>
              <div style={{ display: "grid", gap: 12, marginBottom: 14 }}>
                <div style={fieldStyle}>
                  <span style={fieldLabelStyle}>Default folder</span>
                  <select
                    aria-label="Default folder"
                    className="input"
                    value={form.defaultRootID}
                    onChange={(event) => handleDefaultRootChange(event.target.value)}
                    style={{ fontFamily: "var(--font-mono)", fontSize: 12, minHeight: 36 }}
                  >
                    {form.roots.length === 0 && <option value="">No default folder</option>}
                    {form.roots.map((root) => (
                      <option key={root.id || root.path} value={projectRootFormKey(root)}>
                        {projectRootOptionLabel(root)}
                      </option>
                    ))}
                  </select>
                </div>
                <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
                  <button
                    className="btn btn-ghost btn-sm"
                    type="button"
                    onClick={() => void handleChooseRoot()}
                  >
                    Add folder
                  </button>
                  <button
                    className="btn btn-ghost btn-sm"
                    type="button"
                    aria-describedby={isRootless ? rootlessWorktreeHelpID : undefined}
                    disabled={isRootless}
                    onClick={onOpenCreateWorktree}
                    title={isRootless ? "Add a folder before creating a worktree" : undefined}
                  >
                    Create worktree
                  </button>
                  <button
                    className="btn btn-ghost btn-sm"
                    type="button"
                    aria-describedby={isRootless ? rootlessWorktreeHelpID : undefined}
                    disabled={rootsPending || isRootless}
                    onClick={() => void onDiscoverRoots()}
                    title={
                      isRootless
                        ? "Add a folder before finding worktrees"
                        : rootsPending
                          ? "Wait for worktree discovery to finish"
                          : undefined
                    }
                  >
                    {rootsPending ? "Finding…" : "Find worktrees"}
                  </button>
                  <span
                    id={isRootless ? rootlessWorktreeHelpID : undefined}
                    style={subtleTextStyle}
                  >
                    {isRootless
                      ? "No folders attached. Add a folder before creating or finding worktrees."
                      : `${rootCount} folder${rootCount === 1 ? "" : "s"}`}
                  </span>
                </div>
                {form.roots.length > 0 && (
                  <div style={{ display: "grid", gap: 8 }}>
                    {form.roots.map((root, rootIndex) => {
                      const rootID = root.id ?? root.path;
                      const isDefault = projectRootFormKey(root) === form.defaultRootID;
                      return (
                        <label
                          key={rootID}
                          style={{
                            border: "1px solid var(--border)",
                            borderRadius: 8,
                            display: "grid",
                            gap: 5,
                            padding: "9px 10px",
                          }}
                        >
                          <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
                            <input
                              aria-label={`Active project root ${root.path}`}
                              type="checkbox"
                              checked={Boolean(root.active)}
                              onChange={(event) =>
                                handleRootActiveChange(rootIndex, event.target.checked)
                              }
                            />
                            <span
                              style={{
                                color: "var(--t1)",
                                fontFamily: "var(--font-mono)",
                                fontSize: 11,
                                wordBreak: "break-all",
                              }}
                            >
                              {root.path}
                            </span>
                          </span>
                          <span style={{ ...subtleTextStyle, paddingLeft: 22 }}>
                            {projectRootSummary(root)}
                            {isDefault ? " · default" : ""}
                          </span>
                        </label>
                      );
                    })}
                  </div>
                )}
              </div>
              <div
                style={{
                  display: "grid",
                  gap: 5,
                  fontSize: 11,
                  color: "var(--t3)",
                  lineHeight: 1.45,
                }}
              >
                <div style={{ display: "flex", gap: 8, alignItems: "baseline" }}>
                  <span style={{ color: "var(--t3)", fontSize: 11, minWidth: 78 }}>
                    Current folder
                  </span>
                  <span
                    title={workspace}
                    style={{
                      color: "var(--t1)",
                      fontFamily: "var(--font-mono)",
                      fontSize: 11,
                      wordBreak: "break-all",
                    }}
                  >
                    {workspace || "No local files attached"}
                  </span>
                </div>
              </div>
            </ProjectSettingsSection>
            <div
              style={{
                background: "var(--bg1)",
                borderTop: "1px solid var(--border)",
                bottom: -14,
                margin: "0 -14px -14px",
                padding: 14,
                position: "sticky",
              }}
            >
              <button
                className="btn btn-primary"
                type="submit"
                disabled={pending || !dirty}
                style={{ width: "100%", justifyContent: "center" }}
              >
                {pending ? "Saving…" : "Save settings"}
              </button>
            </div>
          </fieldset>
        </form>
      </div>
    </div>
  );
}

function ProjectSettingsSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <h2 className="kicker" style={{ margin: "0 0 7px" }}>
        {title}
      </h2>
      {children}
    </section>
  );
}

function PresetPosturePreview({ preset }: { preset: AgentPresetRecord | null }) {
  if (!preset) {
    return (
      <div style={{ ...subtleTextStyle, marginTop: 4 }}>
        Uses Hecate's standard project-assignment behavior.
      </div>
    );
  }
  const details = [
    preset.surface,
    preset.execution_profile ? `runtime ${preset.execution_profile}` : "",
    preset.provider_hint || preset.model_hint
      ? `hints ${[preset.provider_hint, preset.model_hint].filter(Boolean).join("/")}`
      : "",
    `tools ${preset.tools_enabled ? "on" : "off"}`,
    `writes ${preset.writes_allowed ? "on" : "off"}`,
    `network ${preset.network_allowed ? "on" : "off"}`,
    `approval ${preset.approval_policy}`,
    `memory ${preset.project_memory_policy}`,
    `sources ${preset.context_source_policy}`,
  ].filter(Boolean);
  return (
    <details className="project-support-details" style={{ ...runtimeDetailsStyle, marginTop: 4 }}>
      <summary>Runtime details</summary>
      <div style={{ ...subtleTextStyle, marginTop: 8 }}>{details.join(" · ")}</div>
    </details>
  );
}

const subtleTextStyle: CSSProperties = {
  color: "var(--t3)",
  fontSize: 12,
  lineHeight: 1.4,
};

const fieldStyle: CSSProperties = {
  display: "grid",
  gap: 6,
};

const fieldLabelStyle: CSSProperties = {
  color: "var(--t2)",
  fontFamily: "var(--font-mono)",
  fontSize: 11,
  textTransform: "uppercase",
};

const settingsFieldsetStyle: CSSProperties = {
  border: 0,
  display: "grid",
  gap: 14,
  margin: 0,
  minWidth: 0,
  padding: 0,
};

const runtimeDetailsStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  color: "var(--t2)",
  fontSize: 12,
  paddingTop: 7,
};
