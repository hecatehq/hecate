const DEFAULT_ACCEPTED_TTL_MS = 120_000;
const DEFAULT_AMBIGUOUS_TTL_MS = 15_000;

export function createConnectionStartState({
  now = Date.now,
  acceptedTtlMs = DEFAULT_ACCEPTED_TTL_MS,
  ambiguousTtlMs = DEFAULT_AMBIGUOUS_TTL_MS,
} = {}) {
  const deadlines = new Map();

  function remember(connectionId, ttlMs) {
    if (typeof connectionId !== "string" || !connectionId) return;
    deadlines.set(connectionId, now() + ttlMs);
  }

  function reconcile(connection) {
    const connectionId = connection?.id;
    const deadline = deadlines.get(connectionId);
    if (deadline === undefined) return false;

    const authoritativeTransition =
      connection?.reachable === true ||
      connection?.status === "starting" ||
      connection?.can_start !== true;
    if (authoritativeTransition || now() >= deadline) {
      deadlines.delete(connectionId);
      return false;
    }
    return true;
  }

  return {
    begin(connectionId) {
      remember(connectionId, acceptedTtlMs);
    },
    accepted(connectionId) {
      remember(connectionId, acceptedTtlMs);
    },
    ambiguous(connectionId) {
      remember(connectionId, ambiguousTtlMs);
    },
    reconcile,
    reset() {
      deadlines.clear();
    },
  };
}
