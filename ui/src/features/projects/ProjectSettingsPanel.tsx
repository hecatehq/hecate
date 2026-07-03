import { useEffect, useMemo, useState, type CSSProperties, type ReactNode } from "react";

import { chooseWorkspaceDirectory } from "../../lib/api";
import { projectDefaultWorkspaceFromRoots } from "../../lib/project-workspace";
import type { AgentProfileRecord } from "../../types/agent-profile";
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
  projectRootOptionLabel,
  projectRootSummary,
  type ProjectDefaultsForm,
} from "./projectSettings";

export function ProjectSettingsPanel({
  agentProfiles,
  agentProfilesError,
  error,
  models,
  pending,
  providerOptions,
  providerPresets,
  project,
  rootsPending,
  onDiscoverRoots,
  onOpenCreateWorktree,
  onSave,
}: {
  agentProfiles: AgentProfileRecord[];
  agentProfilesError: string;
  error: string;
  models: ModelRecord[];
  pending: boolean;
  providerOptions: ProviderOption[];
  providerPresets: ProviderPresetRecord[];
  project: ProjectRecord;
  rootsPending: boolean;
  onDiscoverRoots: () => void | Promise<void>;
  onOpenCreateWorktree: () => void;
  onSave: (form: ProjectDefaultsForm) => void | Promise<void>;
}) {
  const [form, setForm] = useState<ProjectDefaultsForm>(() =>
    projectDefaultsFormFromProject(project),
  );
  const [rootChooseError, setRootChooseError] = useState("");
  useEffect(() => {
    setForm(projectDefaultsFormFromProject(project));
  }, [project]);
  const scopedModels = useMemo(() => {
    if (!form.provider) return models;
    return models.filter((model) => model.metadata?.provider === form.provider);
  }, [form.provider, models]);
  const selectedProfile = useMemo(
    () => agentProfiles.find((profile) => profile.id === form.defaultAgentProfile) ?? null,
    [agentProfiles, form.defaultAgentProfile],
  );

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
      roots: current.roots.map((root) => (root.id === rootID ? { ...root, active: true } : root)),
    }));
  }
  function handleRootActiveChange(rootID: string, active: boolean) {
    setForm((current) => ({
      ...current,
      defaultRootID: !active && current.defaultRootID === rootID ? "" : current.defaultRootID,
      roots: current.roots.map((root) => (root.id === rootID ? { ...root, active } : root)),
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
          defaultRootID: current.defaultRootID,
          roots: [...current.roots, nextRoot],
        };
      });
    } catch (err) {
      setRootChooseError(err instanceof Error ? err.message : "Failed to choose workspace folder.");
    }
  }
  const submitForm = () => onSave(form);

  const workspace = projectDefaultWorkspaceFromRoots(form.roots, form.defaultRootID);
  const rootCount = form.roots.length;

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
          <div style={{ fontSize: 12, fontWeight: 650, color: "var(--t0)" }}>Project settings</div>
          <div
            style={{
              marginTop: 4,
              fontSize: 11,
              color: "var(--t3)",
              lineHeight: 1.45,
            }}
          >
            Controls defaults for future native project assignments. Existing task runs keep the
            settings they started with.
          </div>
        </div>
      </div>
      <div style={{ overflowY: "auto", padding: 14, display: "grid", gap: 14 }}>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void submitForm();
          }}
          style={{ display: "grid", gap: 14 }}
        >
          {error && <InlineError message={error} />}
          {rootChooseError && <InlineError message={rootChooseError} />}
          {agentProfilesError && <InlineError message={agentProfilesError} />}
          <ProjectSettingsSection title="Assignment defaults">
            <div style={{ ...subtleTextStyle, marginBottom: 12 }}>
              Native Hecate assignments copy these defaults when creating the backing task.
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
                <span style={fieldLabelStyle}>Agent preset</span>
                <select
                  aria-label="Default agent preset"
                  className="input"
                  value={form.defaultAgentProfile}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      defaultAgentProfile: event.target.value,
                    }))
                  }
                  style={{ fontFamily: "var(--font-mono)", fontSize: 12, minHeight: 36 }}
                >
                  <option value="">built-in project_assignment</option>
                  {agentProfiles.map((profile) => (
                    <option key={profile.id} value={profile.id}>
                      {profile.name || profile.id} ({profile.id})
                    </option>
                  ))}
                </select>
                <ProfilePosturePreview profile={selectedProfile} />
              </div>
              <div style={fieldStyle}>
                <span style={fieldLabelStyle}>Workspace mode</span>
                <div style={{ position: "relative", width: "100%" }}>
                  <select
                    aria-label="Workspace mode"
                    className="input"
                    value={normalizeWorkspaceMode(form.workspaceMode)}
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
                    <option value="in_place">in_place</option>
                    <option value="persistent">persistent</option>
                    <option value="ephemeral">ephemeral</option>
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
              </div>
              <button
                className="btn btn-primary"
                type="submit"
                disabled={pending}
                style={{ width: "100%", justifyContent: "center" }}
              >
                {pending ? "Saving…" : "Save defaults"}
              </button>
            </div>
          </ProjectSettingsSection>
          <ProjectSettingsSection title="Project context">
            <div style={{ ...subtleTextStyle, marginBottom: 12 }}>
              Project roots are optional local folders or checkouts. Use them when this project
              needs local files, code, or workspace guidance.
            </div>
            <div style={{ display: "grid", gap: 12, marginBottom: 14 }}>
              <div style={fieldStyle}>
                <span style={fieldLabelStyle}>Default root</span>
                <select
                  aria-label="Default project root"
                  className="input"
                  value={form.defaultRootID}
                  onChange={(event) => handleDefaultRootChange(event.target.value)}
                  style={{ fontFamily: "var(--font-mono)", fontSize: 12, minHeight: 36 }}
                >
                  <option value="">no default root</option>
                  {form.roots.map((root) => (
                    <option key={root.id || root.path} value={root.id ?? ""}>
                      {projectRootOptionLabel(root)}
                    </option>
                  ))}
                </select>
              </div>
              <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
                <button
                  className="btn btn-primary btn-sm"
                  type="button"
                  onClick={() => void handleChooseRoot()}
                >
                  Add folder
                </button>
                <button
                  className="btn btn-ghost btn-sm"
                  type="button"
                  disabled={rootCount === 0}
                  onClick={onOpenCreateWorktree}
                >
                  Create worktree
                </button>
                <button
                  className="btn btn-ghost btn-sm"
                  type="button"
                  disabled={rootsPending || rootCount === 0}
                  onClick={() => void onDiscoverRoots()}
                >
                  {rootsPending ? "Discovering…" : "Discover worktrees"}
                </button>
                <span style={subtleTextStyle}>
                  {rootCount === 0
                    ? "No roots configured."
                    : `${rootCount} root${rootCount === 1 ? "" : "s"}`}
                </span>
              </div>
              {form.roots.length > 0 && (
                <div style={{ display: "grid", gap: 8 }}>
                  {form.roots.map((root) => {
                    const rootID = root.id ?? root.path;
                    const isDefault = form.defaultRootID !== "" && root.id === form.defaultRootID;
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
                              handleRootActiveChange(root.id ?? "", event.target.checked)
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
                <span style={{ color: "var(--t3)", fontSize: 11, minWidth: 78 }}>Workspace</span>
                <span
                  title={workspace}
                  style={{
                    color: "var(--t1)",
                    fontFamily: "var(--font-mono)",
                    fontSize: 11,
                    wordBreak: "break-all",
                  }}
                >
                  {workspace || "No default root"}
                </span>
              </div>
            </div>
          </ProjectSettingsSection>
        </form>
      </div>
    </div>
  );
}

function ProjectSettingsSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <div className="kicker" style={{ marginBottom: 7 }}>
        {title}
      </div>
      {children}
    </section>
  );
}

function ProfilePosturePreview({ profile }: { profile: AgentProfileRecord | null }) {
  if (!profile) {
    return (
      <div style={{ ...subtleTextStyle, marginTop: 4 }}>
        Uses the built-in project_assignment posture until a saved preset is selected.
      </div>
    );
  }
  const details = [
    profile.surface,
    profile.execution_profile ? `runtime ${profile.execution_profile}` : "",
    profile.provider_hint || profile.model_hint
      ? `hints ${[profile.provider_hint, profile.model_hint].filter(Boolean).join("/")}`
      : "",
    `tools ${profile.tools_enabled ? "on" : "off"}`,
    `writes ${profile.writes_allowed ? "on" : "off"}`,
    `network ${profile.network_allowed ? "on" : "off"}`,
    `approval ${profile.approval_policy}`,
    `memory ${profile.project_memory_policy}`,
    `sources ${profile.context_source_policy}`,
  ].filter(Boolean);
  return <div style={{ ...subtleTextStyle, marginTop: 4 }}>{details.join(" · ")}</div>;
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
