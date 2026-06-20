export type ProjectVisibilityDetailOptions = {
  shownCount: number;
  totalCount: number;
  itemLabelSingular: string;
  itemLabelPlural: string;
  hiddenLabelSingular: string;
  hiddenLabelPlural: string;
  serverOmittedCount?: number;
};

export function projectVisibilityDetail({
  shownCount,
  totalCount,
  itemLabelSingular,
  itemLabelPlural,
  hiddenLabelSingular,
  hiddenLabelPlural,
  serverOmittedCount = 0,
}: ProjectVisibilityDetailOptions): string {
  const shown = Math.max(0, shownCount);
  const total = Math.max(shown, totalCount);
  const hidden = Math.max(0, total - shown);
  if (hidden === 0) return "";
  const totalLabel = total === 1 ? itemLabelSingular : itemLabelPlural;
  const hiddenLabel = hidden === 1 ? hiddenLabelSingular : hiddenLabelPlural;
  const hiddenVerb = hidden === 1 ? "is" : "are";
  const capped = Math.max(0, serverOmittedCount);
  const capDetail = capped > 0 ? ` (${capped} capped by the server)` : "";
  return `Showing ${shown} of ${total} ${totalLabel}; ${hidden} lower-priority ${hiddenLabel} ${hiddenVerb} hidden${capDetail}.`;
}
