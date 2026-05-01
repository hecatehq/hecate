import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import type { ConfiguredAPIKeyRecord, ConfiguredPolicyRuleRecord } from "../../types/runtime";
import type { PolicyRuleUpsertPayload } from "../../lib/api";
import { getSemanticCacheStatus, listSemanticCacheEntries, getMCPCacheStats } from "../../lib/api";
import type { SemanticCacheStatusResponse, SemanticCacheEntriesResponse, MCPCacheStatsResponse } from "../../types/runtime";
import { Badge, ChipInput, ConfirmModal, CopyBtn, Icon, Icons, InlineError, SlideOver } from "../shared/ui";
import { PricebookTab } from "./PricebookTab";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
};

// TABS is the candidate set. The visible subset is computed at render
// time from session.multiTenant — single-tenant deployments hide the
// tenant + key management surfaces (those endpoints stay live; the
// tabs are simply UI noise when there's only one tenant). Balances
// and Usage have moved to the Costs workspace.
const TABS = ["pricebook", "policy", "retention", "semantic", "mcp", "tenants", "keys"] as const;
type Tab = (typeof TABS)[number];
const TAB_LABELS: Record<Tab, string> = {
  keys: "Keys",
  tenants: "Tenants",
  pricebook: "Pricing",
  policy: "Policy",
  retention: "Retention",
  semantic: "Semantic Cache",
  mcp: "MCP Cache",
};

// Tabs that only appear in multi-tenant deployments. Single-tenant
// (default) hides them — the operator has no use for tenant/key CRUD
// when there's only one tenant.
const MULTI_TENANT_TABS: ReadonlySet<Tab> = new Set(["tenants", "keys"]);

const TAB_STORAGE_KEY = "hecate.adminTab";

export function AdminView({ state, actions }: Props) {
  const visibleTabs = useMemo<readonly Tab[]>(() => {
    const multi = state.session.multiTenant;
    return TABS.filter(t => multi || !MULTI_TENANT_TABS.has(t));
  }, [state.session.multiTenant]);

  // Persist the admin sub-tab so refreshing while on (say) Pricebook
  // returns the operator to Pricebook. If the saved id has fallen out
  // of the visible set (e.g. stored "usage" from a pre-Costs build, or
  // "tenants" while multi-tenant is off), default to the first visible
  // tab instead of rendering an empty body.
  const [tab, setTabRaw] = useState<Tab>(() => {
    const saved = localStorage.getItem(TAB_STORAGE_KEY);
    if (saved && (visibleTabs as readonly string[]).includes(saved)) return saved as Tab;
    return visibleTabs[0];
  });
  const setTab = (next: Tab) => {
    localStorage.setItem(TAB_STORAGE_KEY, next);
    setTabRaw(next);
  };

  // If the visible set changes (e.g. multi-tenant flips off mid-session)
  // and the active tab vanishes, snap to the first visible tab.
  if (!(visibleTabs as readonly string[]).includes(tab)) {
    const fallback = visibleTabs[0];
    if (fallback && fallback !== tab) {
      // Defer to next render to avoid a setState-in-render warning.
      queueMicrotask(() => setTab(fallback));
    }
  }

  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", overflow: "hidden" }}>
      {/* Admin bearer token — only meaningful in multi-tenant mode where
          operators issue tenant API keys against the admin bearer. In
          single-user mode the bootstrap handshake has already populated
          authToken in localStorage and the operator never types it
          again, so the row is noise. */}
      {state.session.multiTenant && <AdminToken state={state} actions={actions} />}

      {/* Tab bar */}
      <div style={{ display: "flex", gap: 2, padding: "0 16px", borderBottom: "1px solid var(--border)", flexShrink: 0 }}>
        {visibleTabs.map(t => (
          <button key={t} type="button"
            onClick={() => setTab(t)}
            style={{
              padding: "7px 12px",
              fontSize: 12,
              fontFamily: "var(--font-mono)",
              background: "none",
              border: "none",
              borderBottom: tab === t ? "2px solid var(--teal)" : "2px solid transparent",
              color: tab === t ? "var(--teal)" : "var(--t2)",
              cursor: "pointer",
              marginBottom: -1,
              textTransform: "uppercase",
              letterSpacing: "0.04em",
            }}>
            {TAB_LABELS[t]}
          </button>
        ))}
      </div>

      {/* Tab content */}
      <div style={{ flex: 1, overflowY: "auto", padding: 16 }}>
        {tab === "keys"         && <KeysTab state={state} actions={actions} />}
        {tab === "tenants"      && <TenantsTab state={state} actions={actions} />}
        {tab === "policy"       && <PolicyTab state={state} actions={actions} />}
        {tab === "pricebook"    && <PricebookTab state={state} actions={actions} />}
        {tab === "retention"    && <RetentionTab state={state} actions={actions} />}
        {tab === "semantic"     && <SemanticCacheTab authToken={state.authToken} />}
        {tab === "mcp"          && <MCPCacheTab authToken={state.authToken} />}
      </div>
    </div>
  );
}

function SectionHeader({
  title,
  description,
  meta,
  actions,
}: {
  title: string;
  description?: string;
  meta?: string;
  actions?: ReactNode;
}) {
  return (
    <div style={{ display: "flex", alignItems: "flex-start", gap: 12, marginBottom: 12 }}>
      <div style={{ minWidth: 0 }}>
        <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: description ? 3 : 0 }}>{title}</div>
        {description && (
          <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>{description}</div>
        )}
      </div>
      {meta && (
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)", whiteSpace: "nowrap" }}>{meta}</span>
      )}
      {actions && <div style={{ marginLeft: "auto", display: "flex", gap: 8, alignItems: "center" }}>{actions}</div>}
    </div>
  );
}

// ─── Admin bearer token ───────────────────────────────────────────────────────

function AdminToken({ state, actions }: Props) {
  const [visible, setVisible] = useState(false);

  return (
    <div className="card" style={{ margin: "12px 16px 0", padding: "10px 14px", flexShrink: 0 }}>
      <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
        <span style={{ fontSize: 12, fontWeight: 500, color: "var(--t1)", whiteSpace: "nowrap" }}>Admin token</span>
        <Badge status={state.authToken ? "healthy" : "down"} label={state.authToken ? "active" : "not set"} />
        <div style={{ flex: 1, background: "var(--bg0)", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", padding: "5px 10px", fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {visible ? (state.authToken || "not set") : "••••••••••••••••••••••••••••••••••••••••••••"}
        </div>
        <button className="btn btn-sm" onClick={() => setVisible(v => !v)}>{visible ? "Hide" : "Reveal"}</button>
        <button className="btn btn-sm" onClick={() => void actions.rotateAPIKey()}>
          <Icon d={Icons.refresh} size={13} /> Rotate
        </button>
      </div>
      <div style={{ fontSize: 10, color: "var(--t3)", marginTop: 4, fontFamily: "var(--font-mono)" }}>
        GATEWAY_ADMIN_TOKEN — required for control-plane operations
      </div>
    </div>
  );
}

// ─── Keys tab ────────────────────────────────────────────────────────────────

function KeysTab({ state, actions }: Props) {
  const [filterTenant, setFilterTenant] = useState("all");
  const [newKeyOpen, setNewKeyOpen] = useState(false);
  const [rotateOpen, setRotateOpen] = useState(false);
  const [createdKeyToken, setCreatedKeyToken] = useState<string | null>(null);
  const [createKeyError, setCreateKeyError] = useState("");

  const apiKeys = state.adminConfig?.api_keys ?? [];
  const tenants = state.adminConfig?.tenants ?? [];
  const tenantNames = [...new Set(apiKeys.map(k => k.tenant).filter(Boolean))] as string[];

  const filteredKeys = filterTenant === "all" ? apiKeys : apiKeys.filter(k => k.tenant === filterTenant);
  const grouped = tenantNames.map(t => ({ tenant: t, keys: filteredKeys.filter(k => k.tenant === t) })).filter(g => g.keys.length > 0);
  const ungrouped = filteredKeys.filter(k => !k.tenant);

  function generateSecret(): string {
    const bytes = new Uint8Array(24);
    crypto.getRandomValues(bytes);
    return "hct_sk_" + Array.from(bytes).map(b => b.toString(16).padStart(2, "0")).join("");
  }

  function openNewKey() {
    setNewKeyOpen(true);
    setCreatedKeyToken(null);
    setCreateKeyError("");
    actions.setAPIKeyFormName("");
    actions.setAPIKeyFormTenant("");
    actions.setAPIKeyFormRole("tenant");
    actions.setAPIKeyFormSecret(generateSecret());
    // Reset scoping fields too — leaving stale chips from a previous
    // open would silently scope a new key to an unrelated key's
    // permissions.
    actions.setAPIKeyFormProviders([]);
    actions.setAPIKeyFormModels([]);
  }

  // Autocomplete sources for the chip inputs in the New key form.
  const providerOptions = (state.providerPresets ?? []).map(p => ({ id: p.id, label: p.name }));
  const modelOptions = state.models.map(m => ({ id: m.id, label: m.id }));

  async function handleCreateKey() {
    if (!state.apiKeyFormName.trim() || !state.apiKeyFormSecret.trim()) return;
    const secret = state.apiKeyFormSecret;
    setCreateKeyError("");
    try {
      await actions.upsertAPIKey();
      setCreatedKeyToken(secret);
    } catch (err) {
      setCreateKeyError(err instanceof Error ? err.message : "Failed to create key.");
    }
  }

  async function handleRotateKey() {
    if (!state.rotateAPIKeyID.trim()) return;
    await actions.rotateAPIKey();
    setRotateOpen(false);
  }

  return (
    <>
      <SectionHeader
        title="API keys"
        description="Issue and rotate control-plane credentials."
        meta={`${apiKeys.length} total`}
        actions={
          <>
            {tenantNames.length > 0 && (
              <select className="select" value={filterTenant} onChange={e => setFilterTenant(e.target.value)}>
                <option value="all">All tenants</option>
                {tenantNames.map(t => <option key={t} value={t}>{t}</option>)}
              </select>
            )}
            <button className="btn btn-sm" onClick={() => setRotateOpen(true)}>
              <Icon d={Icons.refresh} size={13} /> Rotate key
            </button>
            <button className="btn btn-primary btn-sm" onClick={openNewKey}>
              <Icon d={Icons.plus} size={13} /> New key
            </button>
          </>
        }
      />

      {/* Keys grouped by tenant */}
      <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
        {grouped.map(group => (
          <div key={group.tenant}>
            <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--teal)", fontWeight: 500 }}>{group.tenant}</span>
              <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>{group.keys.length} key{group.keys.length !== 1 ? "s" : ""}</span>
            </div>
            <KeyTable keys={group.keys} onDelete={id => void actions.deleteAPIKey(id)} onToggle={(id, enabled) => void actions.setAPIKeyEnabled(id, enabled)} />
          </div>
        ))}
        {ungrouped.length > 0 && (
          <div>
            <div style={{ marginBottom: 6 }}>
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--t2)" }}>no tenant</span>
            </div>
            <KeyTable keys={ungrouped} onDelete={id => void actions.deleteAPIKey(id)} onToggle={(id, enabled) => void actions.setAPIKeyEnabled(id, enabled)} />
          </div>
        )}
        {apiKeys.length === 0 && (
          <div className="card" style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}>
            No API keys. Create one above.
          </div>
        )}
      </div>

      {/* New key slide-over */}
      {newKeyOpen && (
        <SlideOver title="New API key" onClose={() => setNewKeyOpen(false)}
          footer={
            <>
              {createKeyError && <div style={{ marginBottom: 8 }}><InlineError message={createKeyError} /></div>}
              <div style={{ display: "flex", gap: 8 }}>
                {!createdKeyToken ? (
                  <>
                    <button className="btn btn-primary" style={{ flex: 1, justifyContent: "center" }}
                      disabled={!state.apiKeyFormName.trim() || !state.apiKeyFormSecret.trim()}
                      onClick={() => void handleCreateKey()}>
                      <Icon d={Icons.plus} size={14} /> Create key
                    </button>
                    <button className="btn" onClick={() => setNewKeyOpen(false)}>Cancel</button>
                  </>
                ) : (
                  <button className="btn btn-primary" style={{ flex: 1, justifyContent: "center" }} onClick={() => setNewKeyOpen(false)}>Done</button>
                )}
              </div>
            </>
          }>
          {createdKeyToken ? (
            <div style={{ padding: "20px 0" }}>
              <div style={{ textAlign: "center", marginBottom: 20 }}>
                <div style={{ width: 40, height: 40, borderRadius: "50%", background: "var(--green-bg)", border: "1px solid var(--green-border)", display: "flex", alignItems: "center", justifyContent: "center", margin: "0 auto 10px" }}>
                  <Icon d={Icons.check} size={20} />
                </div>
                <div style={{ fontSize: 14, fontWeight: 500, color: "var(--t0)" }}>Key created</div>
                <div style={{ fontSize: 12, color: "var(--red)", marginTop: 4 }}>Copy this now — it won't be shown again.</div>
              </div>
              <div style={{ background: "var(--bg0)", border: "1px solid var(--teal-border)", borderRadius: "var(--radius-sm)", padding: "10px 12px", fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--teal)", wordBreak: "break-all", marginBottom: 10 }}>
                {createdKeyToken}
              </div>
              <CopyBtn text={createdKeyToken} />
            </div>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
              <Field label="KEY NAME">
                <input className="input" placeholder="e.g. eng-team-ci" value={state.apiKeyFormName}
                  onChange={e => actions.setAPIKeyFormName(e.target.value)} />
              </Field>
              <Field label="TENANT">
                <select className="select" style={{ width: "100%", padding: "7px 10px" }} value={state.apiKeyFormTenant}
                  onChange={e => actions.setAPIKeyFormTenant(e.target.value)}>
                  <option value="">— none —</option>
                  {tenants.map(t => <option key={t.id} value={t.name}>{t.name}</option>)}
                </select>
              </Field>
              <Field label="ROLE">
                <select className="select" style={{ width: "100%", padding: "7px 10px" }} value={state.apiKeyFormRole}
                  onChange={e => actions.setAPIKeyFormRole(e.target.value)}>
                  <option value="tenant">tenant</option>
                  <option value="gateway">gateway</option>
                  <option value="admin">admin</option>
                  <option value="readonly">readonly</option>
                </select>
              </Field>
              <div>
                <div style={{ display: "flex", alignItems: "center", marginBottom: 4 }}>
                  <label style={{ fontSize: 11, color: "var(--t2)", fontFamily: "var(--font-mono)", flex: 1 }}>SECRET</label>
                  <button className="btn btn-ghost btn-sm" style={{ fontSize: 10, padding: "2px 6px" }}
                    onClick={() => actions.setAPIKeyFormSecret(generateSecret())}>Regenerate</button>
                </div>
                <input className="input" type="text" value={state.apiKeyFormSecret}
                  onChange={e => actions.setAPIKeyFormSecret(e.target.value)}
                  style={{ fontFamily: "var(--font-mono)", fontSize: 11 }} />
                <div style={{ fontSize: 10, color: "var(--t3)", marginTop: 3 }}>Auto-generated. You can replace with your own value.</div>
              </div>
              <Field label="ALLOWED PROVIDERS (blank = all)">
                <ChipInput
                  values={state.apiKeyFormProviders}
                  onChange={actions.setAPIKeyFormProviders}
                  options={providerOptions}
                  placeholder="all providers"
                  ariaLabel="Allowed providers for this key"
                />
              </Field>
              <Field label="ALLOWED MODELS (blank = all)">
                <ChipInput
                  values={state.apiKeyFormModels}
                  onChange={actions.setAPIKeyFormModels}
                  options={modelOptions}
                  placeholder="all models"
                  ariaLabel="Allowed models for this key"
                />
              </Field>
            </div>
          )}
        </SlideOver>
      )}

      {/* Rotate key slide-over */}
      {rotateOpen && (
        <SlideOver title="Rotate API key" onClose={() => setRotateOpen(false)}
          footer={
            <div style={{ display: "flex", gap: 8 }}>
              <button className="btn btn-primary" style={{ flex: 1, justifyContent: "center" }}
                disabled={!state.rotateAPIKeyID.trim()}
                onClick={() => void handleRotateKey()}>
                <Icon d={Icons.refresh} size={14} /> Rotate
              </button>
              <button className="btn" onClick={() => setRotateOpen(false)}>Cancel</button>
            </div>
          }>
          <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
            <Field label="KEY ID">
              <select className="select" style={{ width: "100%", padding: "7px 10px" }}
                value={state.rotateAPIKeyID}
                onChange={e => actions.setRotateAPIKeyID(e.target.value)}>
                <option value="">— select key —</option>
                {apiKeys.map(k => <option key={k.id} value={k.id}>{k.name} ({k.id})</option>)}
              </select>
            </Field>
            <Field label="NEW SECRET (optional — leave blank to auto-generate)">
              <input className="input" type="text" value={state.rotateAPIKeySecret}
                onChange={e => actions.setRotateAPIKeySecret(e.target.value)}
                placeholder="leave blank to auto-generate"
                style={{ fontFamily: "var(--font-mono)", fontSize: 11 }} />
            </Field>
            <div style={{ fontSize: 11, color: "var(--amber)", fontFamily: "var(--font-mono)" }}>
              The old secret will be invalidated immediately.
            </div>
          </div>
        </SlideOver>
      )}
    </>
  );
}

function KeyTable({ keys, onDelete, onToggle }: {
  keys: ConfiguredAPIKeyRecord[];
  onDelete: (id: string) => void;
  onToggle: (id: string, enabled: boolean) => void;
}) {
  return (
    <div className="card" style={{ overflow: "hidden" }}>
      <table className="table">
        <thead>
          <tr><th>Name</th><th>Preview</th><th>Role</th><th>Status</th><th>Created</th><th></th></tr>
        </thead>
        <tbody>
          {keys.map(k => (
            <tr key={k.id}>
              <td className="mono" style={{ color: "var(--t0)", fontWeight: 500 }}>{k.name}</td>
              <td>
                <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: 140 }}>
                    {k.key_preview || "••••••••"}
                  </span>
                  {k.key_preview && <CopyBtn text={k.key_preview} />}
                </div>
              </td>
              <td>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, background: "var(--bg3)", padding: "2px 6px", borderRadius: 3, border: "1px solid var(--border)", color: "var(--t1)" }}>
                  {k.role}
                </span>
              </td>
              <td><Badge status={k.enabled ? "enabled" : "disabled"} /></td>
              <td className="mono" style={{ color: "var(--t3)" }}>{k.created_at ? new Date(k.created_at).toLocaleDateString() : "—"}</td>
              <td>
                <div style={{ display: "flex", gap: 4 }}>
                  <button className="btn btn-ghost btn-sm" style={{ padding: "3px 6px" }}
                    onClick={() => onToggle(k.id, !k.enabled)} title={k.enabled ? "Disable" : "Enable"}>
                    <Icon d={k.enabled ? Icons.eye : Icons.check} size={12} />
                  </button>
                  <button className="btn btn-ghost btn-sm" style={{ color: "var(--red)", padding: "3px 6px" }}
                    onClick={() => onDelete(k.id)}>
                    <Icon d={Icons.trash} size={13} />
                  </button>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ─── Tenants tab ─────────────────────────────────────────────────────────────

function TenantsTab({ state, actions }: Props) {
  const [newOpen, setNewOpen] = useState(false);
  const [createError, setCreateError] = useState("");

  const tenants = state.adminConfig?.tenants ?? [];

  // Autocomplete sources for the chip inputs in the New tenant form:
  // every cloud / local provider preset (so operators can scope by id
  // even when the gateway hasn't yet seen the provider's models) and
  // every discovered model.
  const providerOptions = (state.providerPresets ?? []).map(p => ({ id: p.id, label: p.name }));
  const modelOptions = state.models.map(m => ({ id: m.id, label: m.id }));

  async function handleCreate() {
    if (!state.tenantFormName.trim()) return;
    setCreateError("");
    try {
      await actions.upsertTenant();
      setNewOpen(false);
      actions.setTenantFormName("");
      actions.setTenantFormID("");
      actions.setTenantFormProviders([]);
      actions.setTenantFormModels([]);
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : "Failed to create tenant.");
    }
  }

  return (
    <>
      <SectionHeader
        title="Tenants"
        description="Scope access and model/provider allowances."
        meta={`${tenants.length} total`}
        actions={
          <button className="btn btn-primary btn-sm" onClick={() => {
            setNewOpen(true);
            setCreateError("");
            actions.setTenantFormName("");
            actions.setTenantFormID("");
            actions.setTenantFormProviders([]);
            actions.setTenantFormModels([]);
          }}>
            <Icon d={Icons.plus} size={13} /> New tenant
          </button>
        }
      />

      {tenants.length > 0 ? (
        <div className="card" style={{ overflow: "hidden" }}>
          <table className="table">
            <thead>
              <tr><th>Name</th><th>ID</th><th>Status</th><th>Allowed providers</th><th>Allowed models</th><th></th></tr>
            </thead>
            <tbody>
              {tenants.map(t => (
                <tr key={t.id}>
                  <td style={{ color: "var(--t0)", fontWeight: 500 }}>{t.name}</td>
                  <td className="mono" style={{ color: "var(--t2)" }}>{t.id}</td>
                  <td>
                    <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                      <Badge status={t.enabled ? "enabled" : "disabled"} />
                      <button className="btn btn-ghost btn-sm" style={{ padding: "2px 5px", fontSize: 10 }}
                        onClick={() => void actions.setTenantEnabled(t.id, !t.enabled)}>
                        {t.enabled ? "Disable" : "Enable"}
                      </button>
                    </div>
                  </td>
                  <td className="mono" style={{ color: "var(--t2)" }}>{t.allowed_providers?.join(", ") || "all"}</td>
                  <td className="mono" style={{ color: "var(--t2)" }}>{t.allowed_models?.join(", ") || "all"}</td>
                  <td>
                    <button className="btn btn-ghost btn-sm" style={{ color: "var(--red)", padding: "3px 6px" }}
                      onClick={() => void actions.deleteTenant(t.id)}>
                      <Icon d={Icons.trash} size={13} />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="card" style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}>
          No tenants. Create one above.
        </div>
      )}

      {newOpen && (
        <SlideOver title="New tenant" onClose={() => setNewOpen(false)}
          footer={
            <>
              {createError && <div style={{ marginBottom: 8 }}><InlineError message={createError} /></div>}
              <div style={{ display: "flex", gap: 8 }}>
                <button className="btn btn-primary" style={{ flex: 1, justifyContent: "center" }}
                  disabled={!state.tenantFormName.trim()}
                  onClick={() => void handleCreate()}>
                  <Icon d={Icons.plus} size={14} /> Create tenant
                </button>
                <button className="btn" onClick={() => setNewOpen(false)}>Cancel</button>
              </div>
            </>
          }>
          <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
            <Field label="NAME">
              <input className="input" placeholder="e.g. engineering" value={state.tenantFormName}
                onChange={e => actions.setTenantFormName(e.target.value)} />
            </Field>
            <Field label="ID (optional — auto-generated if blank)">
              <input className="input" placeholder="e.g. engineering" value={state.tenantFormID}
                onChange={e => actions.setTenantFormID(e.target.value)}
                style={{ fontFamily: "var(--font-mono)" }} />
            </Field>
            <Field label="ALLOWED PROVIDERS (blank = all)">
              <ChipInput
                values={state.tenantFormProviders}
                onChange={actions.setTenantFormProviders}
                options={providerOptions}
                placeholder="all providers"
                ariaLabel="Allowed providers"
              />
            </Field>
            <Field label="ALLOWED MODELS (blank = all)">
              <ChipInput
                values={state.tenantFormModels}
                onChange={actions.setTenantFormModels}
                options={modelOptions}
                placeholder="all models"
                ariaLabel="Allowed models"
              />
            </Field>
            <Field label="AGENT SYSTEM PROMPT (optional, applies to agent_loop tasks)">
              <textarea
                className="input"
                placeholder="Tenant-level directives stacked under the global default, e.g. 'You operate in a financial-services context — always --dry-run before destructive ops.'"
                rows={3}
                style={{ resize: "vertical" }}
                value={state.tenantFormSystemPrompt}
                onChange={e => actions.setTenantFormSystemPrompt(e.target.value)}
              />
            </Field>
          </div>
        </SlideOver>
      )}
    </>
  );
}

// ─── Policy tab ───────────────────────────────────────────────────────────────

// Policy rules are evaluated in array order; each rule either denies
// the request (with an optional reason surfaced in the 403 body) or
// rewrites the requested model to a target. Match conditions are
// AND'd within a rule; an empty list / zero-valued threshold matches
// anything. The full evaluation logic lives in internal/policy/.
//
// PolicyTab is a thin layer over the existing /admin/control-plane
// CRUD endpoints — the UI's job is to make the rule shape obvious
// without forcing operators to memorize the wire field names.

const PROVIDER_KIND_OPTIONS = [
  { id: "cloud", label: "Cloud" },
  { id: "local", label: "Local" },
];

const ROLE_OPTIONS = [
  { id: "admin", label: "Admin" },
  { id: "tenant", label: "Tenant" },
  { id: "anonymous", label: "Anonymous" },
];

// Well-known route reasons the gateway emits — lets operators pick
// from suggestions instead of guessing the exact wire string. We pass
// freeText=true on the chip input so unknown reasons (e.g. from a
// future gateway version) can still be matched as-is.
const ROUTE_REASON_OPTIONS = [
  { id: "requested_model", label: "requested_model" },
  { id: "default_model", label: "default_model" },
  { id: "fallback", label: "fallback" },
  { id: "failover", label: "failover" },
  { id: "tenant_default", label: "tenant_default" },
];

type PolicyFormState = {
  id: string;
  action: "deny" | "rewrite_model";
  reason: string;
  roles: string[];
  tenants: string[];
  providers: string[];
  provider_kinds: string[];
  models: string[];
  route_reasons: string[];
  min_prompt_tokens: string; // string for input control; parsed on save
  min_estimated_cost_usd: string;
  rewrite_model_to: string;
};

const EMPTY_FORM: PolicyFormState = {
  id: "",
  action: "deny",
  reason: "",
  roles: [],
  tenants: [],
  providers: [],
  provider_kinds: [],
  models: [],
  route_reasons: [],
  min_prompt_tokens: "",
  min_estimated_cost_usd: "",
  rewrite_model_to: "",
};

function ruleToForm(rule: ConfiguredPolicyRuleRecord): PolicyFormState {
  return {
    id: rule.id,
    action: rule.action === "rewrite_model" ? "rewrite_model" : "deny",
    reason: rule.reason ?? "",
    roles: rule.roles ?? [],
    tenants: rule.tenants ?? [],
    providers: rule.providers ?? [],
    provider_kinds: rule.provider_kinds ?? [],
    models: rule.models ?? [],
    route_reasons: rule.route_reasons ?? [],
    min_prompt_tokens: rule.min_prompt_tokens ? String(rule.min_prompt_tokens) : "",
    // Wire stores micros USD; the form shows dollars for legibility.
    min_estimated_cost_usd: rule.min_estimated_cost_micros_usd
      ? (rule.min_estimated_cost_micros_usd / 1_000_000).toString()
      : "",
    rewrite_model_to: rule.rewrite_model_to ?? "",
  };
}

function formToPayload(form: PolicyFormState): PolicyRuleUpsertPayload {
  // Drop empty optionals so the gateway sees the same shape it would
  // get from a config-file rule. JSON.stringify will omit undefined.
  const payload: PolicyRuleUpsertPayload = {
    id: form.id.trim(),
    action: form.action,
  };
  if (form.reason.trim()) payload.reason = form.reason.trim();
  if (form.roles.length) payload.roles = form.roles;
  if (form.tenants.length) payload.tenants = form.tenants;
  if (form.providers.length) payload.providers = form.providers;
  if (form.provider_kinds.length) payload.provider_kinds = form.provider_kinds;
  if (form.models.length) payload.models = form.models;
  if (form.route_reasons.length) payload.route_reasons = form.route_reasons;
  const minTokens = parseInt(form.min_prompt_tokens, 10);
  if (Number.isFinite(minTokens) && minTokens > 0) payload.min_prompt_tokens = minTokens;
  const minDollars = parseFloat(form.min_estimated_cost_usd);
  if (Number.isFinite(minDollars) && minDollars > 0) {
    payload.min_estimated_cost_micros_usd = Math.round(minDollars * 1_000_000);
  }
  if (form.action === "rewrite_model" && form.rewrite_model_to.trim()) {
    payload.rewrite_model_to = form.rewrite_model_to.trim();
  }
  return payload;
}

// describeMatches renders a compact summary of a rule's match
// conditions for the table — "any" when nothing's set, otherwise a
// dot-separated list of the populated dimensions. Keeps the table
// scannable without dumping every field per row.
function describeMatches(rule: ConfiguredPolicyRuleRecord): string {
  const parts: string[] = [];
  if (rule.roles?.length) parts.push(`role: ${rule.roles.join(", ")}`);
  if (rule.tenants?.length) parts.push(`tenant: ${rule.tenants.join(", ")}`);
  if (rule.providers?.length) parts.push(`provider: ${rule.providers.join(", ")}`);
  if (rule.provider_kinds?.length) parts.push(`kind: ${rule.provider_kinds.join(", ")}`);
  if (rule.models?.length) parts.push(`model: ${rule.models.join(", ")}`);
  if (rule.route_reasons?.length) parts.push(`reason: ${rule.route_reasons.join(", ")}`);
  if (rule.min_prompt_tokens) parts.push(`≥${rule.min_prompt_tokens} prompt tokens`);
  if (rule.min_estimated_cost_micros_usd) {
    parts.push(`≥$${(rule.min_estimated_cost_micros_usd / 1_000_000).toFixed(4)}`);
  }
  return parts.length ? parts.join(" · ") : "any";
}

function PolicyTab({ state, actions }: Props) {
  const [editing, setEditing] = useState<PolicyFormState | null>(null);
  const [deleteCandidate, setDeleteCandidate] = useState<string | null>(null);
  const [formError, setFormError] = useState("");

  const rules: ConfiguredPolicyRuleRecord[] = state.adminConfig?.policy_rules ?? [];
  const tenantOptions = (state.adminConfig?.tenants ?? []).map(t => ({ id: t.id, label: t.name }));
  const providerOptions = (state.providerPresets ?? []).map(p => ({ id: p.id, label: p.name }));
  const modelOptions = state.models.map(m => ({ id: m.id, label: m.id }));

  function openNew() {
    setEditing({ ...EMPTY_FORM });
    setFormError("");
  }

  function openEdit(rule: ConfiguredPolicyRuleRecord) {
    setEditing(ruleToForm(rule));
    setFormError("");
  }

  async function handleSave() {
    if (!editing) return;
    if (!editing.id.trim()) {
      setFormError("Rule ID is required.");
      return;
    }
    if (editing.action === "rewrite_model" && !editing.rewrite_model_to.trim()) {
      setFormError("Rewrite-model rules need a target model.");
      return;
    }
    setFormError("");
    try {
      await actions.upsertPolicyRule(formToPayload(editing));
      setEditing(null);
    } catch (err) {
      setFormError(err instanceof Error ? err.message : "Failed to save rule.");
    }
  }

  async function handleDelete() {
    if (!deleteCandidate) return;
    await actions.deletePolicyRule(deleteCandidate);
    setDeleteCandidate(null);
  }

  return (
    <>
      <SectionHeader
        title="Policy"
        description="Deny or rewrite requests before they route."
        meta={`${rules.length} total`}
        actions={
          <button className="btn btn-primary btn-sm" onClick={openNew}>
            <Icon d={Icons.plus} size={13} /> New rule
          </button>
        }
      />

      {rules.length > 0 ? (
        <div className="card" style={{ overflow: "hidden" }}>
          <table className="table">
            <thead>
              <tr>
                <th>ID</th>
                <th>Action</th>
                <th>Matches</th>
                <th>Effect</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {rules.map(rule => (
                <tr key={rule.id} style={{ cursor: "pointer" }} onClick={() => openEdit(rule)}>
                  <td className="mono" style={{ color: "var(--t0)", fontWeight: 500 }}>{rule.id}</td>
                  <td>
                    {rule.action === "rewrite_model" ? (
                      <span className="badge badge-amber">rewrite</span>
                    ) : (
                      <span className="badge badge-red">deny</span>
                    )}
                  </td>
                  <td className="mono" style={{ color: "var(--t2)", fontSize: 11 }}>{describeMatches(rule)}</td>
                  <td className="mono" style={{ color: "var(--t1)", fontSize: 11 }}>
                    {rule.action === "rewrite_model"
                      ? <>→ <span style={{ color: "var(--t0)" }}>{rule.rewrite_model_to}</span></>
                      : (rule.reason || <span style={{ color: "var(--t3)" }}>no reason</span>)}
                  </td>
                  <td onClick={e => e.stopPropagation()}>
                    <button className="btn btn-ghost btn-sm" style={{ color: "var(--red)", padding: "3px 6px" }}
                      aria-label={`Delete rule ${rule.id}`}
                      onClick={() => setDeleteCandidate(rule.id)}>
                      <Icon d={Icons.trash} size={13} />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="card" style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}>
          No policy rules. Click &quot;New rule&quot; to add a deny or model-rewrite rule.
        </div>
      )}

      {editing && (
        <SlideOver
          title={state.adminConfig?.policy_rules?.find(r => r.id === editing.id) ? "Edit policy rule" : "New policy rule"}
          onClose={() => setEditing(null)}
          footer={
            <>
              {formError && <div style={{ marginBottom: 8 }}><InlineError message={formError} /></div>}
              <div style={{ display: "flex", gap: 8 }}>
                <button className="btn btn-primary" style={{ flex: 1, justifyContent: "center" }}
                  disabled={!editing.id.trim() || (editing.action === "rewrite_model" && !editing.rewrite_model_to.trim())}
                  onClick={() => void handleSave()}>
                  <Icon d={Icons.check} size={14} /> Save rule
                </button>
                <button className="btn" onClick={() => setEditing(null)}>Cancel</button>
              </div>
            </>
          }>
          <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
            <Field label="RULE ID">
              <input className="input" placeholder="e.g. deny-cloud-for-team-a" value={editing.id}
                onChange={e => setEditing(s => s && ({ ...s, id: e.target.value }))}
                style={{ fontFamily: "var(--font-mono)" }} />
            </Field>
            <Field label="ACTION">
              <div style={{ display: "flex", gap: 8 }}>
                {(["deny", "rewrite_model"] as const).map(a => (
                  <label key={a} style={{
                    display: "flex", alignItems: "center", gap: 6,
                    padding: "6px 10px", borderRadius: "var(--radius-sm)",
                    border: `1px solid ${editing.action === a ? "var(--teal)" : "var(--border)"}`,
                    color: editing.action === a ? "var(--teal)" : "var(--t1)",
                    fontSize: 12, fontFamily: "var(--font-mono)", cursor: "pointer", flex: 1,
                  }}>
                    <input type="radio" checked={editing.action === a} onChange={() => setEditing(s => s && ({ ...s, action: a }))}
                      style={{ accentColor: "var(--teal)" }} />
                    {a}
                  </label>
                ))}
              </div>
            </Field>
            {editing.action === "deny" ? (
              <Field label="REASON (shown in the 403 response body)">
                <input className="input" placeholder="e.g. team-a is local-only"
                  value={editing.reason}
                  onChange={e => setEditing(s => s && ({ ...s, reason: e.target.value }))} />
              </Field>
            ) : (
              <Field label="REWRITE TO MODEL">
                <input className="input" placeholder="e.g. gpt-4o-mini"
                  value={editing.rewrite_model_to}
                  onChange={e => setEditing(s => s && ({ ...s, rewrite_model_to: e.target.value }))}
                  style={{ fontFamily: "var(--font-mono)" }}
                  list="policy-model-suggestions" />
                <datalist id="policy-model-suggestions">
                  {modelOptions.map(o => <option key={o.id} value={o.id} />)}
                </datalist>
              </Field>
            )}

            <div style={{ borderTop: "1px solid var(--border)", paddingTop: 12, marginTop: 4 }}>
              <div className="kicker-lg" style={{ color: "var(--t2)", marginBottom: 8 }}>
                Match conditions (any-blank = match anything)
              </div>
              <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
                <Field label="ROLES">
                  <ChipInput
                    values={editing.roles}
                    onChange={v => setEditing(s => s && ({ ...s, roles: v }))}
                    options={ROLE_OPTIONS}
                    placeholder="any role"
                    ariaLabel="Roles"
                  />
                </Field>
                <Field label="TENANTS">
                  <ChipInput
                    values={editing.tenants}
                    onChange={v => setEditing(s => s && ({ ...s, tenants: v }))}
                    options={tenantOptions}
                    placeholder="any tenant"
                    ariaLabel="Tenants"
                  />
                </Field>
                <Field label="PROVIDERS">
                  <ChipInput
                    values={editing.providers}
                    onChange={v => setEditing(s => s && ({ ...s, providers: v }))}
                    options={providerOptions}
                    placeholder="any provider"
                    ariaLabel="Providers"
                  />
                </Field>
                <Field label="PROVIDER KINDS">
                  <ChipInput
                    values={editing.provider_kinds}
                    onChange={v => setEditing(s => s && ({ ...s, provider_kinds: v }))}
                    options={PROVIDER_KIND_OPTIONS}
                    placeholder="any kind"
                    ariaLabel="Provider kinds"
                  />
                </Field>
                <Field label="MODELS">
                  <ChipInput
                    values={editing.models}
                    onChange={v => setEditing(s => s && ({ ...s, models: v }))}
                    options={modelOptions}
                    placeholder="any model"
                    ariaLabel="Models"
                  />
                </Field>
                <Field label="ROUTE REASONS">
                  <ChipInput
                    values={editing.route_reasons}
                    onChange={v => setEditing(s => s && ({ ...s, route_reasons: v }))}
                    options={ROUTE_REASON_OPTIONS}
                    freeText
                    placeholder="any reason"
                    ariaLabel="Route reasons"
                  />
                </Field>
                <div style={{ display: "flex", gap: 12 }}>
                  <div style={{ flex: 1 }}>
                    <Field label="MIN PROMPT TOKENS">
                      <input className="input" type="number" placeholder="0 = any"
                        value={editing.min_prompt_tokens}
                        onChange={e => setEditing(s => s && ({ ...s, min_prompt_tokens: e.target.value }))} />
                    </Field>
                  </div>
                  <div style={{ flex: 1 }}>
                    <Field label="MIN COST (USD)">
                      <input className="input" type="number" step="0.0001" placeholder="0 = any"
                        value={editing.min_estimated_cost_usd}
                        onChange={e => setEditing(s => s && ({ ...s, min_estimated_cost_usd: e.target.value }))} />
                    </Field>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </SlideOver>
      )}

      {deleteCandidate && (
        <ConfirmModal
          title="Delete policy rule"
          message={
            <>
              Delete rule <code style={{ fontFamily: "var(--font-mono)", color: "var(--t0)" }}>{deleteCandidate}</code>?
              The change is immediate — any in-flight request matching this rule will lose the policy effect.
            </>
          }
          confirmLabel="Delete rule"
          danger
          onClose={() => setDeleteCandidate(null)}
          onConfirm={() => void handleDelete()}
        />
      )}
    </>
  );
}

// ─── Retention tab ────────────────────────────────────────────────────────────

const KNOWN_SUBSYSTEMS = [
  "trace_snapshots",
  "budget_events",
  "audit_events",
  "exact_cache",
  "semantic_cache",
] as const;

function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

function RetentionTab({ state, actions }: Props) {
  const runs = state.retentionRuns ?? [];
  const lastRun = state.retentionLastRun;
  const lastRunResults = lastRun?.results ?? [];

  // Parse CSV state into a local Set for chip toggles
  const selectedSet = new Set(
    state.retentionSubsystems
      .split(",")
      .map(s => s.trim())
      .filter(s => KNOWN_SUBSYSTEMS.includes(s as typeof KNOWN_SUBSYSTEMS[number]))
  );

  function toggleSubsystem(name: string) {
    const next = new Set(selectedSet);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    actions.setRetentionSubsystems([...next].join(","));
  }

  const totalDeleted = lastRunResults.filter(r => !r.skipped).reduce((n, r) => n + (r.deleted ?? 0), 0);
  const maxDeleted = Math.max(1, ...lastRunResults.map(r => r.deleted ?? 0));

  return (
    <>
      <SectionHeader
        title="Retention"
        description="Prune stored traces, budgets, audit events, and cache data."
        meta={`${runs.length} run${runs.length === 1 ? "" : "s"}`}
      />

      {/* Controls */}
      <div className="card" style={{ padding: "14px 16px", marginBottom: 16 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 12 }}>
          <span style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>Subsystems to prune</span>
          <span style={{ fontSize: 11, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>
            {selectedSet.size === 0 ? "all" : `${selectedSet.size} selected`}
          </span>
          <button className="btn btn-primary btn-sm" style={{ marginLeft: "auto" }}
            disabled={state.retentionLoading}
            onClick={() => void actions.runRetention()}>
            <Icon d={Icons.refresh} size={13} /> {state.retentionLoading ? "Running…" : "Run now"}
          </button>
        </div>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
          {KNOWN_SUBSYSTEMS.map(name => {
            const active = selectedSet.has(name);
            return (
              <button key={name} type="button" onClick={() => toggleSubsystem(name)}
                style={{
                  padding: "4px 10px",
                  fontFamily: "var(--font-mono)",
                  fontSize: 11,
                  borderRadius: "var(--radius-sm)",
                  border: `1px solid ${active ? "var(--teal-border)" : "var(--border)"}`,
                  background: active ? "var(--teal-bg)" : "var(--bg3)",
                  color: active ? "var(--teal)" : "var(--t2)",
                  cursor: "pointer",
                  transition: "background 0.1s, color 0.1s, border-color 0.1s",
                }}>
                {name}
              </button>
            );
          })}
        </div>
        <div style={{ fontSize: 10, color: "var(--t3)", marginTop: 8 }}>
          No selection = prune all subsystems
        </div>
        {state.retentionError && <div style={{ marginTop: 8 }}><InlineError message={state.retentionError} /></div>}
      </div>

      {/* Last run summary */}
      {lastRun && (
        <div className="card" style={{ padding: "14px 16px", marginBottom: 16 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12 }}>
            <span style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>Last run</span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
              {relativeTime(lastRun.finished_at)}
            </span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>·</span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)" }}>{lastRun.trigger}</span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>·</span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: totalDeleted > 0 ? "var(--teal)" : "var(--t3)" }}>
              {totalDeleted} deleted
            </span>
          </div>
          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
            {lastRunResults.map(r => (
              <div key={r.name} style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: r.skipped ? "var(--t3)" : "var(--t1)", width: 140, flexShrink: 0 }}>
                  {r.name}
                </span>
                {r.skipped ? (
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", fontStyle: "italic" }}>skipped</span>
                ) : r.error ? (
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--red)" }}>{r.error}</span>
                ) : (
                  <>
                    <div style={{ flex: 1, height: 4, background: "var(--bg3)", borderRadius: 2, overflow: "hidden" }}>
                      <div style={{
                        height: "100%",
                        width: `${Math.round((r.deleted / maxDeleted) * 100)}%`,
                        background: r.deleted > 0 ? "var(--teal)" : "var(--bg3)",
                        borderRadius: 2,
                      }} />
                    </div>
                    <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: r.deleted > 0 ? "var(--teal)" : "var(--t3)", width: 48, textAlign: "right", flexShrink: 0 }}>
                      {r.deleted} del
                    </span>
                  </>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* History */}
      <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: 8 }}>History</div>
      {runs.length > 0 ? (
        <div className="card" style={{ overflow: "hidden" }}>
          {runs.slice(0, 20).map((r, i) => {
            const del = r.results?.filter(s => !s.skipped).reduce((n, s) => n + (s.deleted ?? 0), 0) ?? 0;
            const errored = r.results?.some(s => s.error);
            return (
              <div key={i} style={{ display: "flex", alignItems: "center", gap: 10, padding: "8px 14px", borderBottom: i < Math.min(runs.length, 20) - 1 ? "1px solid var(--border)" : "none" }}>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)", width: 70, flexShrink: 0 }}>{r.trigger}</span>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>{relativeTime(r.finished_at)}</span>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: del > 0 ? "var(--teal)" : "var(--t3)", marginLeft: "auto" }}>
                  {del} deleted
                </span>
                {errored && <Badge status="down" label="error" />}
              </div>
            );
          })}
        </div>
      ) : (
        <div className="card" style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}>
          No retention runs yet.
        </div>
      )}
    </>
  );
}

// ─── Semantic Cache Tab ───────────────────────────────────────────────────────

const PAGE_SIZE = 50;

function SemanticCacheTab({ authToken }: { authToken: string }) {
  const [status, setStatus] = useState<SemanticCacheStatusResponse["data"] | null>(null);
  const [entries, setEntries] = useState<SemanticCacheEntriesResponse["data"]>([]);
  const [loadingStatus, setLoadingStatus] = useState(true);
  const [loadingEntries, setLoadingEntries] = useState(true);
  const [offset, setOffset] = useState(0);
  const [hasMore, setHasMore] = useState(false);
  const [errorMsg, setErrorMsg] = useState("");

  const fetchStatus = useCallback(async () => {
    setLoadingStatus(true);
    try {
      const res = await getSemanticCacheStatus(authToken || undefined);
      setStatus(res.data);
    } catch (e) {
      setErrorMsg(e instanceof Error ? e.message : "Failed to load status");
    } finally {
      setLoadingStatus(false);
    }
  }, [authToken]);

  const fetchEntries = useCallback(async (off: number) => {
    setLoadingEntries(true);
    try {
      const res = await listSemanticCacheEntries({ limit: PAGE_SIZE + 1, offset: off }, authToken || undefined);
      const page = res.data.slice(0, PAGE_SIZE);
      setEntries(page);
      setHasMore(res.data.length > PAGE_SIZE);
      setOffset(off);
    } catch (e) {
      setErrorMsg(e instanceof Error ? e.message : "Failed to load entries");
    } finally {
      setLoadingEntries(false);
    }
  }, [authToken]);

  useEffect(() => {
    void fetchStatus();
    void fetchEntries(0);
  }, [fetchStatus, fetchEntries]);

  const configured = status?.configured ?? false;

  return (
    <>
      <SectionHeader
        title="Semantic Cache"
        description="Vector-similarity cache for chat completions. Serves previously cached responses for semantically equivalent queries."
        meta={loadingStatus ? "…" : configured ? `${status?.entries ?? 0} entries` : "not configured"}
      />

      {errorMsg && (
        <div style={{ color: "var(--red)", fontSize: 12, marginBottom: 12 }}>{errorMsg}</div>
      )}

      {/* Status card */}
      <div className="card" style={{ padding: "14px 16px", marginBottom: 16 }}>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(160px, 1fr))", gap: "12px 20px" }}>
          <StatCell label="configured" value={loadingStatus ? "…" : configured ? "yes" : "no"} />
          <StatCell label="enabled" value={loadingStatus ? "…" : (status?.enabled ? "yes" : "no")} />
          <StatCell label="backend" value={loadingStatus ? "…" : (status?.backend || "—")} />
          <StatCell label="entries" value={loadingStatus ? "…" : String(status?.entries ?? 0)} />
          <StatCell label="max entries" value={loadingStatus ? "…" : String(status?.max_entries ?? 0)} />
          <StatCell label="min similarity" value={loadingStatus ? "…" : String(status?.min_similarity ?? 0)} />
          <StatCell label="default TTL" value={loadingStatus ? "…" : formatTTL(status?.default_ttl_sec ?? 0)} />
          <StatCell label="max text chars" value={loadingStatus ? "…" : String(status?.max_text_chars ?? 0)} />
        </div>
      </div>

      {/* Entries table */}
      <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: 8, display: "flex", alignItems: "center", gap: 10 }}>
        <span>Entries</span>
        <button className="btn btn-secondary btn-sm" onClick={() => { void fetchStatus(); void fetchEntries(offset); }} disabled={loadingEntries}>
          <Icon d={Icons.refresh} size={12} /> Refresh
        </button>
      </div>

      {!configured ? (
        <div className="card" style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}>
          Semantic cache is not configured. Set <code>GATEWAY_SEMANTIC_CACHE_ENABLED=true</code> to enable it.
        </div>
      ) : entries.length === 0 && !loadingEntries ? (
        <div className="card" style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}>
          No cached entries.
        </div>
      ) : (
        <div className="card" style={{ overflow: "hidden" }}>
          <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 12 }}>
            <thead>
              <tr style={{ borderBottom: "1px solid var(--border)", background: "var(--bg2)" }}>
                <th style={{ padding: "6px 12px", textAlign: "left", fontFamily: "var(--font-mono)", fontWeight: 500, color: "var(--t2)", fontSize: 11 }}>Namespace</th>
                <th style={{ padding: "6px 12px", textAlign: "left", fontFamily: "var(--font-mono)", fontWeight: 500, color: "var(--t2)", fontSize: 11 }}>Text snippet</th>
                <th style={{ padding: "6px 12px", textAlign: "left", fontFamily: "var(--font-mono)", fontWeight: 500, color: "var(--t2)", fontSize: 11 }}>Stored</th>
                <th style={{ padding: "6px 12px", textAlign: "left", fontFamily: "var(--font-mono)", fontWeight: 500, color: "var(--t2)", fontSize: 11 }}>Expires</th>
              </tr>
            </thead>
            <tbody>
              {loadingEntries ? (
                <tr><td colSpan={4} style={{ padding: "20px", textAlign: "center", color: "var(--t3)" }}>Loading…</td></tr>
              ) : entries.map((entry, i) => (
                <tr key={i} style={{ borderBottom: i < entries.length - 1 ? "1px solid var(--border)" : "none" }}>
                  <td style={{ padding: "7px 12px", fontFamily: "var(--font-mono)", color: "var(--t2)", fontSize: 11, whiteSpace: "nowrap" }}>
                    {formatNamespace(entry.namespace)}
                  </td>
                  <td style={{ padding: "7px 12px", color: "var(--t1)", maxWidth: 400, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {entry.text_snippet}
                  </td>
                  <td style={{ padding: "7px 12px", fontFamily: "var(--font-mono)", color: "var(--t3)", fontSize: 11, whiteSpace: "nowrap" }}>
                    {entry.stored_at ? relativeTime(entry.stored_at) : "—"}
                  </td>
                  <td style={{ padding: "7px 12px", fontFamily: "var(--font-mono)", color: "var(--t3)", fontSize: 11, whiteSpace: "nowrap" }}>
                    {entry.expires_at ? relativeTime(entry.expires_at) : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

          {(offset > 0 || hasMore) && (
            <div style={{ display: "flex", gap: 8, padding: "8px 12px", borderTop: "1px solid var(--border)", justifyContent: "flex-end" }}>
              <button className="btn btn-secondary btn-sm" disabled={offset === 0 || loadingEntries}
                onClick={() => void fetchEntries(Math.max(0, offset - PAGE_SIZE))}>
                ← Prev
              </button>
              <span style={{ fontSize: 11, color: "var(--t3)", alignSelf: "center" }}>
                {offset + 1}–{offset + entries.length}
              </span>
              <button className="btn btn-secondary btn-sm" disabled={!hasMore || loadingEntries}
                onClick={() => void fetchEntries(offset + PAGE_SIZE)}>
                Next →
              </button>
            </div>
          )}
        </div>
      )}
    </>
  );
}

function StatCell({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div style={{ fontSize: 10, fontFamily: "var(--font-mono)", color: "var(--t3)", textTransform: "uppercase", letterSpacing: "0.05em", marginBottom: 2 }}>{label}</div>
      <div style={{ fontSize: 13, color: "var(--t0)", fontFamily: "var(--font-mono)" }}>{value}</div>
    </div>
  );
}

function formatTTL(seconds: number): string {
  if (seconds <= 0) return "—";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}

function formatNamespace(ns: string): string {
  // namespace format: "model:foo|provider:bar|tenant:baz" — render just the key values
  return ns.split("|").map(part => {
    const idx = part.indexOf(":");
    return idx >= 0 ? part.slice(idx + 1) : part;
  }).join(" · ");
}

// ─── MCP Cache Tab ────────────────────────────────────────────────────────────

function MCPCacheTab({ authToken }: { authToken: string }) {
  const [data, setData] = useState<MCPCacheStatsResponse["data"] | null>(null);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState("");

  const fetch = useCallback(async () => {
    setLoading(true);
    setErrorMsg("");
    try {
      const res = await getMCPCacheStats(authToken || undefined);
      setData(res.data);
    } catch (e) {
      setErrorMsg(e instanceof Error ? e.message : "Failed to load MCP cache stats");
    } finally {
      setLoading(false);
    }
  }, [authToken]);

  useEffect(() => { void fetch(); }, [fetch]);

  const configured = data?.configured ?? false;

  return (
    <>
      <SectionHeader
        title="MCP Client Cache"
        description="Shared MCP subprocess cache. Amortises spawn cost across runs with the same upstream server config."
        meta={loading ? "…" : configured ? `${data?.entries ?? 0} entries` : "not configured"}
      />

      {errorMsg && (
        <div style={{ color: "var(--red)", fontSize: 12, marginBottom: 12 }}>{errorMsg}</div>
      )}

      <div className="card" style={{ padding: "14px 16px", marginBottom: 16 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12 }}>
          <span style={{ fontSize: 12, fontWeight: 500, color: "var(--t0)" }}>Status</span>
          <button className="btn btn-secondary btn-sm" style={{ marginLeft: "auto" }} onClick={() => void fetch()} disabled={loading}>
            <Icon d={Icons.refresh} size={12} /> Refresh
          </button>
        </div>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(140px, 1fr))", gap: "12px 20px" }}>
          <StatCell label="configured" value={loading ? "…" : configured ? "yes" : "no"} />
          <StatCell label="entries"    value={loading ? "…" : String(data?.entries ?? 0)} />
          <StatCell label="in use"     value={loading ? "…" : String(data?.in_use ?? 0)} />
          <StatCell label="idle"       value={loading ? "…" : String(data?.idle ?? 0)} />
          <StatCell label="checked"    value={loading ? "…" : data?.checked_at ? relativeTime(data.checked_at) : "—"} />
        </div>
      </div>

      {!configured && !loading && (
        <div className="card" style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}>
          MCP client cache is not configured. It is enabled automatically when MCP servers are used with shared subprocess pooling.
        </div>
      )}
    </>
  );
}

// ─── Shared helpers ───────────────────────────────────────────────────────────

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label style={{ fontSize: 11, color: "var(--t2)", display: "block", marginBottom: 4, fontFamily: "var(--font-mono)" }}>{label}</label>
      {children}
    </div>
  );
}
