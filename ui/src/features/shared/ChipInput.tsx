// ChipInput is the multi-select picker every settings form uses for
// list-of-ids fields (provider lists, model allowlists, etc.). It
// replaces the old comma-separated text input —
// no more typo-and-pray, every chip is a value the gateway recognizes.
//
// Three modes:
//   1. options-only (default) — chips can only come from the
//      autocomplete suggestion list. Useful when the wire convention
//      requires existing entities (tenants, providers, models).
//   2. options + freeText — same as above, but a value typed and
//      Enter'd that isn't in options gets added as a chip too.
//      Used for route_reasons where the gateway emits well-known
//      strings but operators may want to match new ones.
//   3. freeText-only (no options) — pure tag input. Falls back to a
//      placeholder hint when empty.
//
// Keyboard contract:
//   - Type to filter suggestions; ArrowDown/ArrowUp to navigate
//   - Enter to commit the highlighted suggestion (or freeText input)
//   - Backspace on an empty input removes the last chip
//   - Click a chip's × to remove it
//   - Esc to close the suggestion dropdown

import { useId, useRef, useState } from "react";

import { useFloatingMenu } from "./useFloatingMenu";

export type ChipOption = { id: string; label: string };

export function ChipInput({
  values,
  onChange,
  options,
  freeText = false,
  placeholder = "",
  ariaLabel,
  disabled = false,
}: {
  values: string[];
  onChange: (next: string[]) => void;
  options?: ChipOption[];
  freeText?: boolean;
  placeholder?: string;
  ariaLabel?: string;
  disabled?: boolean;
}) {
  const [draft, setDraft] = useState("");
  const [highlight, setHighlight] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listboxID = useId();
  // portalSelector: null because the suggestion list is rendered
  // inline inside wrapRef rather than in a portal-style fixed-
  // position container — the default ".dropdown-menu-floating"
  // exemption would be a no-op here.
  const { open, setOpen, wrapRef } = useFloatingMenu<HTMLDivElement>({
    portalSelector: null,
  });

  // Suggestions = options not already chipped, filtered by draft.
  const suggestions = (options ?? [])
    .filter(o => !values.includes(o.id))
    .filter(o => {
      if (!draft.trim()) return true;
      const q = draft.toLowerCase();
      return o.id.toLowerCase().includes(q) || o.label.toLowerCase().includes(q);
    });

  const labelById = new Map((options ?? []).map(o => [o.id, o.label]));
  const displayLabel = (id: string) => labelById.get(id) ?? id;

  function commit(id: string) {
    if (!id) return;
    if (values.includes(id)) return;
    onChange([...values, id]);
    setDraft("");
    setHighlight(0);
  }

  function remove(id: string) {
    onChange(values.filter(v => v !== id));
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setOpen(true);
      setHighlight(h => Math.min(h + 1, suggestions.length - 1));
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setHighlight(h => Math.max(h - 1, 0));
      return;
    }
    if (e.key === "Enter") {
      e.preventDefault();
      const picked = suggestions[highlight];
      if (picked) {
        commit(picked.id);
      } else if (freeText && draft.trim()) {
        commit(draft.trim());
      }
      return;
    }
    if (e.key === "Backspace" && draft === "" && values.length > 0) {
      e.preventDefault();
      remove(values[values.length - 1]);
      return;
    }
    if (e.key === "Escape") {
      setOpen(false);
    }
  }

  // The wrapper looks like a regular .input but contains the chip
  // row + a transparent text field. Visual borrow from the existing
  // input style so it slots into Field labels without bespoke CSS.
  return (
    <div className="dropdown-wrap" ref={wrapRef}>
      <div
        className="input"
        style={{
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          gap: 4,
          padding: "4px 6px",
          minHeight: 32,
          cursor: disabled ? "not-allowed" : "text",
          opacity: disabled ? 0.6 : 1,
        }}
        onClick={() => { if (!disabled) inputRef.current?.focus(); }}>
        {values.map(id => (
          <span
            key={id}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 4,
              padding: "2px 6px",
              borderRadius: "var(--radius-sm)",
              background: "var(--bg3)",
              border: "1px solid var(--border)",
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              color: "var(--t0)",
            }}>
            {displayLabel(id)}
            {!disabled && (
              <button
                type="button"
                aria-label={`Remove ${displayLabel(id)}`}
                onClick={e => { e.stopPropagation(); remove(id); }}
                style={{
                  background: "none",
                  border: "none",
                  color: "var(--t2)",
                  cursor: "pointer",
                  padding: 0,
                  fontSize: 12,
                  lineHeight: 1,
                  display: "inline-flex",
                  alignItems: "center",
                }}>
                ×
              </button>
            )}
          </span>
        ))}
        <input
          ref={inputRef}
          type="text"
          aria-label={ariaLabel}
          aria-autocomplete={options?.length ? "list" : undefined}
          aria-controls={open ? listboxID : undefined}
          aria-expanded={open}
          aria-activedescendant={open && suggestions[highlight] ? `${listboxID}-${suggestions[highlight].id}` : undefined}
          role="combobox"
          value={draft}
          disabled={disabled}
          placeholder={values.length === 0 ? placeholder : ""}
          onChange={e => { setDraft(e.target.value); setOpen(true); setHighlight(0); }}
          onFocus={() => setOpen(true)}
          onKeyDown={onKeyDown}
          style={{
            flex: 1,
            minWidth: 80,
            border: "none",
            outline: "none",
            background: "transparent",
            color: "var(--t0)",
            fontFamily: "var(--font-sans)",
            fontSize: 13,
            padding: "2px 4px",
          }}
        />
      </div>
      {open && (suggestions.length > 0 || (freeText && draft.trim())) && (
        <div id={listboxID} role="listbox" className="dropdown-menu" style={{ minWidth: 200, maxHeight: 220, overflowY: "auto" }}>
          {suggestions.map((s, i) => (
            <div
              key={s.id}
              id={`${listboxID}-${s.id}`}
              role="option"
              aria-selected={i === highlight}
              className={`dropdown-item ${i === highlight ? "selected" : ""}`}
              // Hover to highlight matches the existing ModelPicker behavior.
              onMouseDown={e => { e.preventDefault(); commit(s.id); }}
              onMouseEnter={() => setHighlight(i)}>
              <span style={{ flex: 1, fontFamily: "var(--font-mono)", fontSize: 12 }}>{s.label}</span>
              {s.label !== s.id && (
                <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>{s.id}</span>
              )}
            </div>
          ))}
          {freeText && draft.trim() && !suggestions.find(s => s.id === draft.trim()) && (
            <div
              role="option"
              aria-selected={suggestions.length === highlight}
              className="dropdown-item"
              style={{ fontStyle: "italic", color: "var(--t2)" }}
              onMouseDown={e => { e.preventDefault(); commit(draft.trim()); }}>
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 12 }}>add &quot;{draft.trim()}&quot;</span>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
