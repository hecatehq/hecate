import { useEffect, useId, useRef, useState } from "react";
import { getWorkspaceSkills } from "../../lib/api";
import type { WorkspaceSkillInfo, WorkspaceSkillSelection } from "../../types/workspace-skill";

export function WorkspaceSkillPicker({
  workspace,
  value,
  onChange,
  disabled = false,
}: {
  workspace: string;
  value: WorkspaceSkillSelection[];
  onChange: (skills: WorkspaceSkillSelection[]) => void;
  disabled?: boolean;
}) {
  const helpID = useId();
  const [catalog, setCatalog] = useState<WorkspaceSkillInfo[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);

  async function inspect() {
    controller.current?.abort();
    const request = new AbortController();
    controller.current = request;
    setLoading(true);
    setError("");
    try {
      const response = await getWorkspaceSkills(workspace, request.signal);
      if (request.signal.aborted) return;
      setCatalog(response.data);
      onChange([]);
    } catch {
      if (!request.signal.aborted)
        setError(
          "Skills could not be inspected. Check the workspace and try again. Local runtime access is required.",
        );
    } finally {
      if (!request.signal.aborted) setLoading(false);
    }
  }

  return (
    <fieldset
      disabled={disabled}
      style={{ border: 0, padding: 0, margin: "8px 0", minWidth: 0 }}
      aria-describedby={helpID}
    >
      <legend>
        Workspace skills <span className="muted">(optional)</span>
      </legend>
      <p id={helpID} className="field-hint">
        Select up to four instruction files. Their current versions are frozen at creation and sent
        to the selected model. Scripts and extra permissions are not enabled.
      </p>
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        disabled={!workspace.trim() || loading}
        onClick={() => void inspect()}
      >
        {loading ? "Inspecting skills…" : catalog ? "Refresh skills" : "Choose skills"}
      </button>
      {!workspace.trim() && <p className="field-hint">Choose a workspace first.</p>}
      {loading && <span role="status">Inspecting workspace skills…</span>}
      {error && <p role="alert">{error}</p>}
      {catalog?.length === 0 && (
        <p role="status">No supported SKILL.md files found in this workspace.</p>
      )}
      {catalog && catalog.length > 0 && (
        <ul
          style={{
            listStyle: "none",
            padding: "2px",
            margin: "8px 0",
            maxHeight: 200,
            overflowY: "auto",
          }}
        >
          {catalog.map((skill) => {
            const checked = value.some(
              (selected) => selected.path === skill.path && selected.sha256 === skill.sha256,
            );
            return (
              <li key={skill.path}>
                <label
                  style={{ display: "flex", gap: 8, alignItems: "start", overflowWrap: "anywhere" }}
                >
                  <input
                    type="checkbox"
                    checked={checked}
                    disabled={loading || (!checked && value.length >= 4)}
                    onChange={() =>
                      onChange(
                        checked
                          ? value.filter((selected) => selected.path !== skill.path)
                          : [...value, { path: skill.path, sha256: skill.sha256 }],
                      )
                    }
                  />
                  <span>
                    {skill.name}
                    <span style={{ display: "block", fontSize: 11, color: "var(--t2)" }}>
                      {skill.path} · {skill.bytes} bytes · {skill.sha256.slice(0, 8)}
                    </span>
                  </span>
                </label>
              </li>
            );
          })}
        </ul>
      )}
      {value.length > 0 && (
        <p role="status" className="field-hint">
          {value.length} skills selected. Creating this chat or task shares their instructions with
          its model provider.
        </p>
      )}
    </fieldset>
  );
}
