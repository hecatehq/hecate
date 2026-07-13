import type { CSSProperties } from "react";

export const presetRoleTitleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 13,
  fontWeight: 600,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

export const presetRoleSubtleTextStyle: CSSProperties = {
  color: "var(--t2)",
  fontSize: 12,
  lineHeight: 1.4,
};

export const presetRoleFieldStyle: CSSProperties = {
  display: "grid",
  gap: 6,
};

export const presetRoleFieldLabelStyle: CSSProperties = {
  color: "var(--t2)",
  fontFamily: "var(--font-mono)",
  fontSize: 11,
  textTransform: "uppercase",
};

export const presetRoleCheckboxLabelStyle: CSSProperties = {
  alignItems: "center",
  color: "var(--t1)",
  display: "inline-flex",
  fontSize: 12,
  gap: 6,
};
