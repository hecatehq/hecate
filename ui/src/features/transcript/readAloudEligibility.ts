const BLOCKED_READ_ALOUD_STATUSES = new Set([
  "queued",
  "running",
  "in_progress",
  "awaiting_approval",
  "pending",
  "failed",
  "cancelled",
]);

export function readAloudStatusIsBlocked(status: string | undefined): boolean {
  return BLOCKED_READ_ALOUD_STATUSES.has(status?.trim().toLowerCase() ?? "");
}
