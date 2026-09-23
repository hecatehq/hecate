import { useCallback, useEffect, useState } from "react";

import {
  createAgentPreset,
  deleteAgentPreset,
  getAgentPresets,
  updateAgentPreset,
} from "../../lib/api";
import type { AgentPresetRecord } from "../../types/agent-preset";
import type { BrowserEvidenceRuntimeReadiness } from "../../types/provider";
import { AgentPresetsModal } from "../projects/AgentPresetsModal";
import {
  presetCreatePayloadFromForm,
  presetUpdatePayloadFromForm,
  type AgentPresetForm,
} from "../projects/projectPresetsRoles";
import { Icon, Icons, InlineError } from "../shared/ui";
import { SettingsSectionHeader as SectionHeader } from "./SettingsSectionHeader";

export function WorkPoliciesSettings({
  browserEvidenceReadiness,
}: {
  browserEvidenceReadiness?: BrowserEvidenceRuntimeReadiness;
}) {
  const [presets, setPresets] = useState<AgentPresetRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [modalOpen, setModalOpen] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const response = await getAgentPresets();
      setPresets(response.data ?? []);
    } catch (loadError) {
      setError(errorMessage(loadError, "Failed to load work policies."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function create(form: AgentPresetForm): Promise<AgentPresetRecord | undefined> {
    setPending(true);
    setError("");
    try {
      const response = await createAgentPreset(presetCreatePayloadFromForm(form));
      setPresets((current) => [response.data, ...current]);
      return response.data;
    } catch (createError) {
      setError(errorMessage(createError, "Failed to create work policy."));
      return undefined;
    } finally {
      setPending(false);
    }
  }

  async function update(
    presetID: string,
    form: AgentPresetForm,
  ): Promise<AgentPresetRecord | undefined> {
    setPending(true);
    setError("");
    try {
      const response = await updateAgentPreset(presetID, presetUpdatePayloadFromForm(form));
      setPresets((current) =>
        current.map((preset) => (preset.id === response.data.id ? response.data : preset)),
      );
      return response.data;
    } catch (updateError) {
      setError(errorMessage(updateError, "Failed to update work policy."));
      return undefined;
    } finally {
      setPending(false);
    }
  }

  async function remove(preset: AgentPresetRecord): Promise<boolean> {
    setPending(true);
    setError("");
    try {
      await deleteAgentPreset(preset.id);
      setPresets((current) => current.filter((candidate) => candidate.id !== preset.id));
      return true;
    } catch (deleteError) {
      setError(errorMessage(deleteError, "Failed to delete work policy."));
      return false;
    } finally {
      setPending(false);
    }
  }

  return (
    <section style={{ marginBottom: 20 }}>
      <SectionHeader
        title="Work policies"
        description="Reusable instructions, routing preferences, and permission boundaries for native Tasks and project work."
        meta={loading ? "loading" : `${presets.length} saved`}
        actions={
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            disabled={loading}
            onClick={() => setModalOpen(true)}
          >
            <Icon d={Icons.settings} size={13} /> Manage policies
          </button>
        }
      />
      {error && !modalOpen && <InlineError message={error} />}
      <div
        className="card"
        style={{ padding: "14px 16px", color: "var(--t2)", fontSize: 12, lineHeight: 1.5 }}
      >
        A Task snapshots its selected policy when it is created. Later policy edits do not alter
        queued, scheduled, retried, or resumed work.
      </div>
      {modalOpen && (
        <AgentPresetsModal
          browserEvidenceReadiness={browserEvidenceReadiness}
          error={error}
          pending={pending}
          presets={presets}
          onClose={() => {
            if (!pending) setModalOpen(false);
          }}
          onCreate={create}
          onDelete={remove}
          onUpdate={update}
        />
      )}
    </section>
  );
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message.trim() ? error.message : fallback;
}
