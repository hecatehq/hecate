import type { CSSProperties } from "react";

export const profileRoleTitleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 13,
  fontWeight: 600,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

export const profileRoleSubtleTextStyle: CSSProperties = {
  color: "var(--t3)",
  fontSize: 12,
  lineHeight: 1.4,
};

export const profileRoleFieldStyle: CSSProperties = {
  display: "grid",
  gap: 6,
};

export const profileRoleFieldLabelStyle: CSSProperties = {
  color: "var(--t2)",
  fontFamily: "var(--font-mono)",
  fontSize: 11,
  textTransform: "uppercase",
};

export const profileRoleCheckboxLabelStyle: CSSProperties = {
  alignItems: "center",
  color: "var(--t1)",
  display: "inline-flex",
  fontSize: 12,
  gap: 6,
};
