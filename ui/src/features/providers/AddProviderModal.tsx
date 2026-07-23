import { useEffect, useId, useRef, useState } from "react";
import {
  useEnsureProviderPresetsLoaded,
  useProvidersAndModels,
} from "../../app/state/providersAndModels";
import { useRuntime } from "../../app/state/runtime";
import { useSettings } from "../../app/state/settings";
import { useWiredProviderActions } from "../../app/state/coordinators/wired";
import { discoverLocalProviders } from "../../lib/api";
import { resolvedBaseURL } from "../../lib/provider-utils";
import { isRemoteRuntimeSession } from "../../lib/runtime-utils";
import type { LocalProviderDiscoveryRecord, ProviderPresetRecord } from "../../types/provider";
import { BrandAvatar, Icon, Icons, InlineError, Modal } from "../shared/ui";
import "./provider-mobile.css";

type Props = {
  open: boolean;
  onClose: () => void;
};

type AddFormState = {
  name: string;
  custom_name: string;
  account_id: string;
  base_url: string;
  api_key: string;
  kind: string;
  protocol: string;
};

export function AddProviderModal({ open, onClose }: Props) {
  const fieldIDPrefix = useId();
  useEnsureProviderPresetsLoaded(open);
  const settings = useSettings();
  const runtime = useRuntime();
  const providersAndModels = useProvidersAndModels();
  const providerActions = useWiredProviderActions();
  const providerPresets = providersAndModels.state.providerPresets;
  const settingsConfig = settings.state.config;
  const localProvidersAllowed = !isRemoteRuntimeSession(runtime.state.sessionInfo);
  const [step, setStep] = useState<"pick" | "form">("pick");
  const [pickTab, setPickTab] = useState<"cloud" | "local">("local");
  const [preset, setPreset] = useState<ProviderPresetRecord | null>(null);
  const [form, setForm] = useState<AddFormState>(emptyAddForm("cloud"));
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [localDiscovery, setLocalDiscovery] = useState<LocalProviderDiscoveryRecord[]>([]);
  const [localDiscoveryLoading, setLocalDiscoveryLoading] = useState(false);
  const [localDiscoveryError, setLocalDiscoveryError] = useState("");
  const nameInputRef = useRef<HTMLInputElement>(null);
  const urlInputRef = useRef<HTMLInputElement>(null);
  const apiKeyInputRef = useRef<HTMLInputElement>(null);
  const localDiscoveryRequestRef = useRef(0);

  useEffect(() => {
    localDiscoveryRequestRef.current++;
    if (!open) return;
    const nextTab = localProvidersAllowed ? "local" : "cloud";
    setStep("pick");
    setPickTab(nextTab);
    setPreset(null);
    setForm(emptyAddForm(nextTab));
    setError("");
    setLocalDiscovery([]);
    setLocalDiscoveryLoading(false);
    setLocalDiscoveryError("");
  }, [localProvidersAllowed, open]);

  useEffect(() => {
    if (!open || step !== "pick" || pickTab !== "local" || !localProvidersAllowed) return;
    void refreshLocalDiscovery();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [localProvidersAllowed, open, step, pickTab]);

  useEffect(() => {
    if (!open || step !== "form") return;
    const target =
      preset === null
        ? nameInputRef.current
        : form.kind === "local"
          ? urlInputRef.current
          : apiKeyInputRef.current;
    requestAnimationFrame(() => target?.focus());
    // Focus only when the operator enters the form; per-keystroke form updates
    // must not steal focus back to the preset's first editable field.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, step, preset?.id]);

  if (!open) return null;

  const localPresets = providerPresets.filter((p) => p.kind === "local");
  const cloudPresets = providerPresets.filter((p) => p.kind === "cloud");
  const configuredProviders = settingsConfig?.providers ?? [];

  function close() {
    onClose();
  }

  async function refreshLocalDiscovery() {
    if (!localProvidersAllowed) {
      setLocalDiscovery([]);
      setLocalDiscoveryLoading(false);
      setLocalDiscoveryError("");
      return;
    }
    const requestID = ++localDiscoveryRequestRef.current;
    setLocalDiscoveryLoading(true);
    setLocalDiscoveryError("");
    try {
      const response = await discoverLocalProviders();
      if (requestID !== localDiscoveryRequestRef.current) return;
      setLocalDiscovery(response.data ?? []);
    } catch (e) {
      if (requestID !== localDiscoveryRequestRef.current) return;
      setLocalDiscoveryError(e instanceof Error ? e.message : "Failed to discover local providers");
    } finally {
      if (requestID === localDiscoveryRequestRef.current) {
        setLocalDiscoveryLoading(false);
      }
    }
  }

  async function submitAdd() {
    setLoading(true);
    setError("");
    try {
      await providerActions.createProvider({
        name: form.name.trim(),
        preset_id: preset?.id,
        custom_name: form.custom_name.trim() || undefined,
        account_id: form.account_id.trim() || undefined,
        base_url: form.base_url.trim() || undefined,
        api_key: form.api_key.trim() || undefined,
        kind: form.kind,
        protocol: form.protocol,
      });
      close();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to add provider");
    } finally {
      setLoading(false);
    }
  }

  function pickPreset(nextPreset: ProviderPresetRecord) {
    const discovery = localDiscovery.find((item) => item.preset_id === nextPreset.id);
    setPreset(nextPreset);
    setForm({
      name: nextPreset.name,
      custom_name: "",
      account_id: nextPreset.id === "fireworks" ? "fireworks" : "",
      base_url: discovery?.base_url || nextPreset.base_url,
      api_key: "",
      kind: nextPreset.kind,
      protocol: nextPreset.protocol ?? "openai",
    });
    setStep("form");
  }

  function pickCustom(kind: "cloud" | "local") {
    setPreset(null);
    setForm(emptyAddForm(localProvidersAllowed ? kind : "cloud"));
    setStep("form");
  }

  function renderPickStep() {
    const effectivePickTab = localProvidersAllowed ? pickTab : "cloud";
    const presets = effectivePickTab === "cloud" ? cloudPresets : localPresets;
    const cardsPerRow = 3;
    const maxItemCount = localProvidersAllowed
      ? Math.max(cloudPresets.length, localPresets.length) + 1
      : cloudPresets.length + 1;
    const maxRows = Math.ceil(maxItemCount / cardsPerRow);
    const rowHeight = 78;
    const rowGap = 8;
    const gridMinHeight = maxRows * rowHeight + (maxRows - 1) * rowGap;

    return (
      <div
        className="add-provider-pick-step"
        style={{ display: "flex", flexDirection: "column", gap: 16 }}
      >
        <div
          className="add-provider-tabs"
          style={{
            display: "flex",
            gap: 4,
            borderBottom: "1px solid var(--border)",
            marginBottom: 4,
          }}
        >
          <TabButton
            id="cloud"
            label="Cloud"
            active={effectivePickTab === "cloud"}
            onClick={() => setPickTab("cloud")}
          />
          {localProvidersAllowed && (
            <TabButton
              id="local"
              label="Local"
              active={effectivePickTab === "local"}
              onClick={() => setPickTab("local")}
            />
          )}
          {localProvidersAllowed && effectivePickTab === "local" && (
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              onClick={() => void refreshLocalDiscovery()}
              disabled={localDiscoveryLoading}
              style={{ marginLeft: "auto", padding: "6px 8px", color: "var(--t2)" }}
            >
              {localDiscoveryLoading ? "Checking..." : "Check local"}
            </button>
          )}
        </div>
        {localProvidersAllowed && effectivePickTab === "local" && (
          <div
            style={{
              fontSize: 11,
              color: localDiscoveryError ? "var(--red)" : "var(--t3)",
              lineHeight: 1.45,
              marginTop: -8,
            }}
          >
            {localDiscoveryError ||
              "Checks command availability and probes each unique local endpoint once."}
          </div>
        )}
        <div
          className="add-provider-picker-grid"
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(3, minmax(0, 1fr))",
            gridAutoRows: `minmax(${rowHeight}px, auto)`,
            alignContent: "start",
            gap: rowGap,
            minHeight: gridMinHeight,
          }}
        >
          {presets.map((p) => (
            <PresetButton
              key={p.id}
              preset={p}
              discovery={
                p.kind === "local"
                  ? localDiscovery.find((item) => item.preset_id === p.id)
                  : undefined
              }
              onClick={() => pickPreset(p)}
            />
          ))}
          <CustomButton onClick={() => pickCustom(effectivePickTab)} />
        </div>
      </div>
    );
  }

  function renderFormStep() {
    const showURL = form.kind === "local" || (!preset && form.kind === "cloud");
    const showAPIKey = form.kind === "cloud";
    const currentBaseURL = resolvedBaseURL(
      preset?.id ?? "",
      preset
        ? {
            id: preset.id,
            name: preset.name,
            kind: preset.kind,
            protocol: preset.protocol,
            base_url: preset.base_url,
            credential_configured: false,
          }
        : undefined,
      providerPresets,
    );
    const effectiveBaseURL = (showURL ? form.base_url.trim() : currentBaseURL).trim();
    const baseURLTakenBy = effectiveBaseURL
      ? configuredProviders.find((p) => {
          const url = resolvedBaseURL(p.id, p, providerPresets);
          return url && url === effectiveBaseURL;
        })
      : undefined;
    const duplicateProvider = findDuplicateProviderID(
      configuredProviders,
      form.name,
      form.custom_name,
    );
    const duplicateMessage = duplicateProvider
      ? providerDuplicateMessage(duplicateProvider, form.name, form.custom_name, preset !== null)
      : "";
    const saveDisabled =
      loading || !form.name.trim() || Boolean(baseURLTakenBy) || Boolean(duplicateProvider);
    return (
      <div
        className="add-provider-form-step"
        style={{ display: "flex", flexDirection: "column", gap: 14 }}
      >
        <button
          type="button"
          className="btn btn-ghost btn-sm"
          onClick={() => setStep("pick")}
          disabled={loading}
          style={{
            alignSelf: "flex-start",
            padding: "2px 6px 2px 0",
            color: "var(--t2)",
            display: "flex",
            alignItems: "center",
            gap: 4,
          }}
        >
          <Icon d={Icons.chevL} size={11} />
          <span style={{ fontSize: 11 }}>All providers</span>
        </button>
        <div>
          <label
            className="kicker-lg"
            htmlFor={`${fieldIDPrefix}-name`}
            style={{ display: "block", marginBottom: 6 }}
          >
            Name
          </label>
          <input
            id={`${fieldIDPrefix}-name`}
            className="input"
            type="text"
            value={form.name}
            ref={nameInputRef}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            placeholder="My Provider"
            readOnly={preset !== null}
            disabled={preset !== null}
            title={
              preset !== null
                ? "Preset names are fixed — use Custom name below to disambiguate two instances of the same preset"
                : undefined
            }
          />
        </div>
        <div>
          <label
            className="kicker-lg"
            htmlFor={`${fieldIDPrefix}-custom-name`}
            style={{ display: "block", marginBottom: 6 }}
          >
            Custom name{" "}
            <span style={{ color: "var(--t3)", fontWeight: 400, textTransform: "none" }}>
              optional
            </span>
          </label>
          <input
            id={`${fieldIDPrefix}-custom-name`}
            className="input"
            type="text"
            value={form.custom_name}
            onChange={(e) => setForm((f) => ({ ...f, custom_name: e.target.value }))}
            placeholder="e.g. Prod, Dev, Staging"
            aria-invalid={Boolean(duplicateProvider)}
          />
          {duplicateMessage && (
            <div
              role="status"
              style={{ marginTop: 6, fontSize: 11, color: "var(--amber)", lineHeight: 1.4 }}
            >
              {duplicateMessage}
            </div>
          )}
        </div>
        {showURL && (
          <div>
            <label
              className="kicker-lg"
              htmlFor={`${fieldIDPrefix}-endpoint`}
              style={{ display: "block", marginBottom: 6 }}
            >
              Endpoint URL
            </label>
            <input
              id={`${fieldIDPrefix}-endpoint`}
              className="input"
              type="text"
              value={form.base_url}
              ref={urlInputRef}
              onChange={(e) => setForm((f) => ({ ...f, base_url: e.target.value }))}
              placeholder={currentBaseURL || "http://localhost:11434/v1"}
              style={{ fontFamily: "var(--font-mono)" }}
            />
            {baseURLTakenBy && (
              <div
                role="status"
                style={{ marginTop: 6, fontSize: 11, color: "var(--amber)", lineHeight: 1.4 }}
              >
                Endpoint already used by{" "}
                <span style={{ fontFamily: "var(--font-mono)" }}>
                  {baseURLTakenBy.name || baseURLTakenBy.id}
                </span>
                . Choose another URL to continue.
              </div>
            )}
          </div>
        )}
        {!showURL && baseURLTakenBy && (
          <div role="status" style={{ fontSize: 11, color: "var(--amber)", lineHeight: 1.4 }}>
            Endpoint <span style={{ fontFamily: "var(--font-mono)" }}>{effectiveBaseURL}</span> is
            already used by{" "}
            <span style={{ fontFamily: "var(--font-mono)" }}>
              {baseURLTakenBy.name || baseURLTakenBy.id}
            </span>
            . Choose another provider entry or remove the existing one first.
          </div>
        )}
        {preset?.id === "fireworks" && (
          <div>
            <label
              className="kicker-lg"
              htmlFor={`${fieldIDPrefix}-account-id`}
              style={{ display: "block", marginBottom: 6 }}
            >
              Fireworks account ID
            </label>
            <input
              id={`${fieldIDPrefix}-account-id`}
              className="input"
              type="text"
              value={form.account_id}
              onChange={(e) => setForm((f) => ({ ...f, account_id: e.target.value }))}
              placeholder="fireworks"
              style={{ fontFamily: "var(--font-mono)" }}
            />
            <div style={{ marginTop: 6, fontSize: 11, color: "var(--t3)", lineHeight: 1.4 }}>
              Keep <span style={{ fontFamily: "var(--font-mono)" }}>fireworks</span> for the public
              catalog, or use your Fireworks account ID for private and fine-tuned models.
            </div>
          </div>
        )}
        {showAPIKey && (
          <div>
            <label
              className="kicker-lg"
              htmlFor={`${fieldIDPrefix}-api-key`}
              style={{ display: "block", marginBottom: 6 }}
            >
              API Key
            </label>
            <input
              id={`${fieldIDPrefix}-api-key`}
              className="input"
              type="password"
              name="hecate-provider-api-key"
              autoComplete="new-password"
              autoCorrect="off"
              spellCheck={false}
              data-1p-ignore="true"
              data-lpignore="true"
              data-form-type="other"
              value={form.api_key}
              ref={apiKeyInputRef}
              onChange={(e) => setForm((f) => ({ ...f, api_key: e.target.value }))}
              placeholder="sk-…"
              style={{ fontFamily: "var(--font-mono)", letterSpacing: "0.1em" }}
            />
          </div>
        )}
        {error && <InlineError message={error} />}
        <button
          className="btn btn-primary btn-sm"
          style={{ justifyContent: "center", marginTop: 4 }}
          disabled={saveDisabled}
          onClick={() => void submitAdd()}
        >
          {loading ? "Adding..." : "Add provider"}
        </button>
      </div>
    );
  }

  return (
    <Modal
      title={step === "pick" ? "Add provider" : preset ? preset.name : "Custom provider"}
      onClose={close}
      footer={null}
      width={680}
    >
      <div className="add-provider-content">
        {step === "pick" ? renderPickStep() : renderFormStep()}
      </div>
    </Modal>
  );
}

function emptyAddForm(kind: "cloud" | "local"): AddFormState {
  return {
    name: "Custom",
    custom_name: "",
    account_id: "",
    base_url: "",
    api_key: "",
    kind,
    protocol: "openai",
  };
}

function providerSlug(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

function providerIDFor(name: string, customName: string): string {
  const idSource = [name.trim(), customName.trim()].filter(Boolean).join(" ");
  return providerSlug(idSource);
}

function findDuplicateProviderID(
  providers: Array<{ id: string; name: string; custom_name?: string }>,
  name: string,
  customName: string,
) {
  const id = providerIDFor(name, customName);
  if (!id) return undefined;
  return providers.find((provider) => provider.id === id);
}

function providerDisplayName(provider: { id: string; name: string; custom_name?: string }): string {
  return provider.custom_name
    ? `${provider.name} (${provider.custom_name})`
    : provider.name || provider.id;
}

function providerDuplicateMessage(
  provider: { id: string; name: string; custom_name?: string },
  name: string,
  customName: string,
  isPreset: boolean,
): string {
  const displayName = providerDisplayName(provider);
  if (customName.trim()) {
    return `Custom name is already used by ${displayName}. Choose another custom name to continue.`;
  }
  if (isPreset) {
    return `${name.trim()} is already configured. Add a custom name, like Dev or Local, to create another instance.`;
  }
  return `A provider named ${displayName} already exists. Add a custom name to create another instance.`;
}

function PresetButton({
  preset,
  discovery,
  onClick,
}: {
  preset: ProviderPresetRecord;
  discovery?: LocalProviderDiscoveryRecord;
  onClick: () => void;
}) {
  const status = localDiscoveryStatus(discovery);
  return (
    <button
      className="btn btn-ghost add-provider-picker-card"
      style={{
        minHeight: 60,
        height: "100%",
        display: "flex",
        alignItems: "center",
        gap: 10,
        textAlign: "left",
        padding: "10px 12px",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius)",
      }}
      onClick={onClick}
    >
      <BrandAvatar brand={preset.id} fallback={preset.name} size={28} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div
          className="add-provider-preset-heading"
          style={{ display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}
        >
          <div
            style={{
              fontSize: 13,
              fontWeight: 500,
              color: "var(--t0)",
              whiteSpace: "nowrap",
              overflow: "hidden",
              textOverflow: "ellipsis",
            }}
          >
            {preset.name}
          </div>
          {status && (
            <span
              title={status.title}
              style={{
                fontSize: 10,
                lineHeight: "16px",
                height: 16,
                borderRadius: 999,
                padding: "0 6px",
                whiteSpace: "nowrap",
                color: status.color,
                background: status.background,
                border: `1px solid ${status.border}`,
                flexShrink: 0,
              }}
            >
              {status.label}
            </span>
          )}
        </div>
        {preset.description && (
          <div
            style={{
              fontSize: 11,
              color: "var(--t3)",
              lineHeight: 1.35,
              marginTop: 2,
              display: "-webkit-box",
              WebkitLineClamp: 2,
              WebkitBoxOrient: "vertical",
              overflow: "hidden",
            }}
          >
            {preset.description}
          </div>
        )}
      </div>
    </button>
  );
}

function CustomButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      className="btn btn-ghost add-provider-picker-card"
      style={{
        minHeight: 60,
        height: "100%",
        display: "flex",
        alignItems: "center",
        gap: 10,
        textAlign: "left",
        padding: "10px 12px",
        border: "1px dashed var(--border)",
        borderRadius: "var(--radius)",
      }}
      onClick={onClick}
    >
      <div
        style={{
          width: 28,
          height: 28,
          borderRadius: "var(--radius-sm)",
          background: "var(--bg3)",
          border: "1px solid var(--border)",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          fontSize: 18,
          color: "var(--t3)",
          flexShrink: 0,
        }}
      >
        <Icon d={Icons.edit} size={13} />
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>Custom</div>
        <div
          style={{
            fontSize: 11,
            color: "var(--t3)",
            textTransform: "uppercase",
            letterSpacing: "0.04em",
          }}
        >
          openai-compatible
        </div>
      </div>
    </button>
  );
}

function TabButton({
  id: _id,
  label,
  active,
  onClick,
}: {
  id: "cloud" | "local";
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className="btn btn-ghost btn-sm"
      onClick={onClick}
      style={{
        padding: "6px 12px",
        borderRadius: 0,
        borderBottom: `2px solid ${active ? "var(--teal)" : "transparent"}`,
        color: active ? "var(--t0)" : "var(--t2)",
        fontWeight: active ? 500 : 400,
      }}
    >
      {label}
    </button>
  );
}

function localDiscoveryStatus(item: LocalProviderDiscoveryRecord | undefined): {
  label: string;
  title: string;
  color: string;
  background: string;
  border: string;
} | null {
  if (!item) return null;
  if (item.http_available) {
    const models = item.model_count
      ? ` · ${item.model_count} model${item.model_count === 1 ? "" : "s"}`
      : "";
    return {
      label: "Running",
      title: `HTTP probe passed at ${item.probe_url}${models}`,
      color: "var(--green)",
      background: "var(--green-bg)",
      border: "var(--green-border)",
    };
  }
  if (item.command_available) {
    return {
      label: "Installed",
      title: `${item.command || "Command"} found${item.command_path ? ` at ${item.command_path}` : ""}; local HTTP endpoint is not running`,
      color: "var(--amber)",
      background: "var(--amber-bg)",
      border: "var(--amber-border)",
    };
  }
  return {
    label: "Not detected",
    title:
      item.error || `No ${item.command || "provider"} command on PATH and no local HTTP response`,
    color: "var(--t3)",
    background: "var(--bg3)",
    border: "var(--border)",
  };
}
