// ModelPicker: one picker shared by every surface that needs to pick
// a model — the chat view, the new-task slideover, and any future
// caller. Earlier the chat had its own richer copy with search +
// disabled-provider rendering, while shared/ui exported a simpler
// grouped one; the two fell out of sync (e.g. cost ceiling work
// hadn't propagated to the new-task picker). Consolidating means
// every surface gets the same affordances: type-to-filter, sort
// disabled providers to the bottom, key-icon for unconfigured cloud
// creds, optional per-row provider suffix.
//
// All extension points are optional — callers that don't care about
// disabled-provider rendering or the provider suffix get the same
// look as the old simple picker minus the section headers.

import { useEffect, useRef, useState } from "react";
import type { KeyboardEvent } from "react";

import type { ModelRecord } from "../../types/model";
import type { ProviderPresetRecord } from "../../types/provider";
import { Icon, Icons } from "./Icons";
import { focusDropdownItem, focusInitialDropdownItem } from "./dropdownKeyboard";
import { useFloatingDropdownStyle } from "./useFloatingDropdownStyle";
import { useFloatingMenu } from "./useFloatingMenu";

export function ModelPicker({
  value,
  onChange,
  models,
  presets,
  disabledProviders,
  modelWarnings,
  showProvider = true,
  triggerWidth,
  includeAll = false,
  allValue = "",
  allLabel = "All models",
  variant = "header",
}: {
  value: string;
  onChange: (v: string) => void;
  models: ModelRecord[];
  // Maps provider id → display name. Used to render the per-row
  // provider suffix as a friendly name (e.g. "openai" → "OpenAI").
  // Without it the picker falls back to the raw provider id.
  presets?: ProviderPresetRecord[];
  // Provider ids whose models render disabled (greyed, not clickable,
  // with a key indicator). Map value is the tooltip explaining why
  // (e.g. "Add an API key for X in Connections"). Pass an
  // empty/omitted map to disable.
  disabledProviders?: Map<string, string>;
  // Per-model non-blocking warnings keyed by model id. The model
  // stays selectable, but a small ⚠ icon renders next to its row
  // with the value as a tooltip. Used by the new-task panel to
  // flag models known to lack tool-calling support (e.g.
  // smollm2:135m, embeddings models) — operators can still pick
  // them if they know what they're doing, but the visual cue
  // saves a confused round-trip when the agent loop fails with
  // "model does not support tool-calling".
  modelWarnings?: Map<string, string>;
  // Render the per-row "(provider name)" suffix. Set false when the
  // outer provider filter is already pinned to a single provider —
  // every row would carry the same suffix, which is just noise.
  showProvider?: boolean;
  // Pin the trigger to a fixed width so it aligns with siblings
  // (chat header pairs the model picker with the provider picker).
  // Defaults to the historical chat width of 220px; pass `undefined`
  // to let the button size to its content.
  triggerWidth?: number | undefined;
  // Optional sentinel used by filter surfaces such as Observability.
  // Chat/task creation leave this off because they require a concrete
  // model selection; trace filters can start from "All models".
  includeAll?: boolean;
  allValue?: string;
  allLabel?: string;
  variant?: "header" | "composer";
}) {
  const [filter, setFilter] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  const {
    open,
    setOpen,
    toggle,
    wrapRef: ref,
    triggerRef,
    menuRef,
  } = useFloatingMenu<HTMLDivElement, HTMLButtonElement>({
    // onCloseRef inside useFloatingMenu absorbs closure-identity
    // churn, so passing a fresh () => setFilter("") each render
    // doesn't re-bind the document listener — no useCallback needed.
    onClose: () => setFilter(""),
  });
  // Right-anchored: the menu is 300px wide and the trigger is at the
  // right side of its row in the chat header, so left-anchoring would
  // push it off-screen on narrow viewports.
  const floatingStyle = useFloatingDropdownStyle(
    triggerRef,
    open,
    "right",
    variant === "composer" ? "up" : "down",
  );

  useEffect(() => {
    if (open) setTimeout(() => inputRef.current?.focus(), 0);
  }, [open]);

  const providerName = (id: string) => presets?.find((p) => p.id === id)?.name || id;
  const matchedFilter = filter
    ? models.filter((m) => m.id.toLowerCase().includes(filter.toLowerCase()))
    : models;
  // Sort usable models above disabled ones — within each bucket the
  // source order is preserved (provider-grouped, alphabetical-ish).
  // Stable partition via two passes avoids accidentally reordering
  // rows whose disabled state is the same.
  const filtered = (() => {
    if (!disabledProviders || disabledProviders.size === 0) return matchedFilter;
    const usable: ModelRecord[] = [];
    const disabled: ModelRecord[] = [];
    for (const m of matchedFilter) {
      const provider = m.metadata?.provider;
      if (provider && disabledProviders.has(provider)) disabled.push(m);
      else usable.push(m);
    }
    return [...usable, ...disabled];
  })();
  // Disable the picker when there are no models to show. This handles the
  // "selected provider has no discovered models" case (e.g. Ollama or
  // LM Studio with the runtime not running) — opening a dropdown only to
  // see an empty list is worse than a clearly-disabled affordance. The
  // outer caller already passes a provider-scoped `models` array, so this
  // check covers both "no providers configured" and "scoped provider has
  // no models" without extra plumbing.
  const isEmpty = models.length === 0 && !includeAll;
  const label =
    includeAll && value === allValue
      ? allLabel
      : isEmpty
        ? "no models available"
        : value || "Pick a model";
  const buttonWidth = triggerWidth === undefined ? undefined : triggerWidth;
  const disabledTitle = isEmpty
    ? "No discovered models for this provider. Configure credentials or start the local runtime."
    : label;

  function closeMenu() {
    setOpen(false);
    triggerRef.current?.focus();
  }

  function selectModel(model: ModelRecord) {
    const provider = model.metadata?.provider;
    if (provider && disabledProviders?.has(provider)) return;
    onChange(model.id);
    setOpen(false);
  }

  function onInputKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      closeMenu();
      return;
    }
    if (event.key === "ArrowDown") {
      event.preventDefault();
      event.stopPropagation();
      focusInitialDropdownItem(menuRef.current);
      return;
    }
    if (event.key === "Enter") {
      if (includeAll && filter.trim() === "") {
        event.preventDefault();
        event.stopPropagation();
        onChange(allValue);
        closeMenu();
        return;
      }
      const firstEnabled = filtered.find((model) => {
        const provider = model.metadata?.provider;
        return !provider || !disabledProviders?.has(provider);
      });
      if (!firstEnabled) return;
      event.preventDefault();
      event.stopPropagation();
      selectModel(firstEnabled);
    }
  }

  function onMenuKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      closeMenu();
      return;
    }
    if (
      event.key === "ArrowDown" ||
      event.key === "ArrowUp" ||
      event.key === "Home" ||
      event.key === "End"
    ) {
      event.preventDefault();
      focusDropdownItem(menuRef.current, event.key);
    }
  }

  return (
    <div className="dropdown-wrap" ref={ref}>
      <button
        ref={triggerRef}
        type="button"
        aria-label={`Model picker: ${label}`}
        aria-expanded={open}
        aria-haspopup="listbox"
        className="btn btn-ghost btn-sm"
        onClick={() => {
          if (!isEmpty) toggle();
        }}
        disabled={isEmpty}
        title={disabledTitle}
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          gap: 5,
          color: isEmpty ? "var(--t3)" : "var(--t1)",
          width: buttonWidth,
          cursor: isEmpty ? "not-allowed" : undefined,
          opacity: isEmpty ? 0.6 : undefined,
          ...(variant === "composer"
            ? {
                background: "var(--bg1)",
                borderColor: "var(--border)",
                borderRadius: "var(--radius-sm)",
                minHeight: 34,
                padding: "5px 10px",
              }
            : {}),
        }}
      >
        {variant !== "composer" && <Icon d={Icons.model} size={13} />}
        <span
          style={{
            flex: 1,
            minWidth: 0,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            textAlign: "left",
          }}
        >
          {label}
        </span>
        <Icon d={Icons.chevD} size={11} />
      </button>
      {open && floatingStyle && (
        <div
          ref={menuRef}
          className="dropdown-menu dropdown-menu-floating"
          onKeyDown={onMenuKeyDown}
          style={{ ...floatingStyle, minWidth: 300 }}
        >
          <div style={{ padding: "6px 8px", borderBottom: "1px solid var(--border)" }}>
            <input
              ref={inputRef}
              className="input"
              style={{ fontSize: 12, padding: "4px 8px", fontFamily: "var(--font-mono)" }}
              placeholder="Filter models…"
              aria-label="Filter models"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              onClick={(e) => e.stopPropagation()}
              onKeyDown={onInputKeyDown}
            />
          </div>
          <div role="listbox" style={{ maxHeight: 300, overflowY: "auto", overflowX: "hidden" }}>
            {includeAll && (
              <>
                <button
                  type="button"
                  data-dropdown-item
                  data-selected={value === allValue ? "true" : undefined}
                  className={`dropdown-item ${value === allValue ? "selected" : ""}`}
                  aria-selected={value === allValue}
                  role="option"
                  onClick={() => {
                    onChange(allValue);
                    closeMenu();
                  }}
                >
                  <span
                    style={{
                      flex: 1,
                      fontFamily: "var(--font-mono)",
                      fontSize: 12,
                      textAlign: "left",
                    }}
                  >
                    {allLabel}
                  </span>
                </button>
                {filtered.length > 0 && <div className="dropdown-divider" />}
              </>
            )}
            {!includeAll && filter.trim() === "" && (
              <>
                <button
                  type="button"
                  data-dropdown-item
                  data-selected={value === "" ? "true" : undefined}
                  className={`dropdown-item ${value === "" ? "selected" : ""}`}
                  aria-selected={value === ""}
                  role="option"
                  onClick={() => {
                    onChange("");
                    closeMenu();
                  }}
                >
                  <span
                    style={{
                      flex: 1,
                      fontFamily: "var(--font-mono)",
                      fontSize: 12,
                      textAlign: "left",
                    }}
                  >
                    Pick a model
                  </span>
                </button>
                {filtered.length > 0 && <div className="dropdown-divider" />}
              </>
            )}
            {filtered.length === 0 && (!includeAll || filter.trim()) && (
              <div style={{ padding: "10px 12px", fontSize: 12, color: "var(--t3)" }}>
                No models match
              </div>
            )}
            {filtered.map((m) => {
              const provider = m.metadata?.provider;
              const reason = provider ? disabledProviders?.get(provider) : undefined;
              const disabled = !!reason;
              const warning = !disabled ? modelWarnings?.get(m.id) : undefined;
              // Title combines warning (if any) with the disabled
              // reason. We skip the warning when the row is already
              // disabled — the disabled tooltip is the more
              // important signal.
              const rowTitle = disabled ? reason : warning;
              return (
                <button
                  key={m.id}
                  type="button"
                  data-dropdown-item
                  data-selected={m.id === value ? "true" : undefined}
                  className={`dropdown-item ${m.id === value ? "selected" : ""}`}
                  aria-disabled={disabled || undefined}
                  aria-selected={m.id === value}
                  role="option"
                  title={rowTitle}
                  style={disabled ? { cursor: "not-allowed" } : undefined}
                  onClick={() => selectModel(m)}
                >
                  {/* Only the model id dims when disabled. Provider
                      name keeps its t3 color so the right column reads
                      consistently across enabled + disabled rows. */}
                  <span
                    style={{
                      flex: 1,
                      fontFamily: "var(--font-mono)",
                      fontSize: 12,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                      opacity: disabled ? 0.5 : 1,
                    }}
                  >
                    {m.id}
                  </span>
                  {showProvider && provider && (
                    <span
                      style={{
                        fontSize: 10,
                        color: "var(--t3)",
                        fontFamily: "var(--font-mono)",
                        flexShrink: 0,
                        marginLeft: 6,
                      }}
                    >
                      {providerName(provider)}
                    </span>
                  )}
                  {/* Reserve a fixed slot whether or not a key/warning
                      icon renders — keeps the right edge aligned
                      across rows so the model-id and provider-name
                      columns stay coherent. Disabled (red key) wins
                      over warning (amber ⚠) when both could fire. */}
                  <span
                    style={{
                      display: "inline-flex",
                      flexShrink: 0,
                      marginLeft: 6,
                      width: 11,
                      justifyContent: "center",
                    }}
                  >
                    {disabled ? (
                      <span
                        aria-label="credentials missing"
                        style={{ color: "var(--red)", display: "inline-flex" }}
                      >
                        <Icon d={Icons.keys} size={11} />
                      </span>
                    ) : warning ? (
                      <span
                        aria-label={warning}
                        style={{ color: "var(--amber)", display: "inline-flex" }}
                      >
                        <Icon d={Icons.warning} size={11} />
                      </span>
                    ) : null}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
