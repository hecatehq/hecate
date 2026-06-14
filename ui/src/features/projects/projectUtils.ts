export function splitIDs(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

export function splitRoleIDs(value: string): string[] {
  return splitIDs(value);
}

export function shortID(id: string): string {
  if (id.length <= 12) return id;
  return id.slice(0, 10) + "...";
}

export function firstNonEmpty(...values: Array<string | undefined | null>): string {
  for (const value of values) {
    const trimmed = value?.trim();
    if (trimmed) return trimmed;
  }
  return "";
}

export function projectNameFromPath(path: string): string {
  const trimmed = path.trim().replace(/[/\\]+$/, "");
  const segments = trimmed.split(/[/\\]/).filter(Boolean);
  return segments.at(-1) || "Untitled project";
}

export function isLinkableProjectLocator(value: string): boolean {
  return /^https?:\/\//i.test(value.trim());
}
