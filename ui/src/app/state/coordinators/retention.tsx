// Retention coordinator: bridges the retention slice's typed run
// result to the global notice banner. The slice owns the state
// machine + the API call; this coordinator wires success / failure
// to the cross-cutting notice toast so the slice stays free of UI
// banner concerns.

import { useRetention } from "../retention";
import type { NoticeState } from "../settings";

export type UseRetentionActionsParams = {
  setNotice: (next: NoticeState | null) => void;
};

export function useRetentionActions(params: UseRetentionActionsParams) {
  const retention = useRetention();

  async function runRetention() {
    params.setNotice(null);
    const result = await retention.actions.runRetention();
    params.setNotice(result.ok
      ? { kind: "success", message: "Retention run completed." }
      : { kind: "error", message: "Failed to run retention." });
  }

  return { runRetention };
}
