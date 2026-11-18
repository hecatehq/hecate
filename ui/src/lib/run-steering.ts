import { sha256Hex } from "./sha256";

// Only a content fingerprint and random request id are retained here. Guidance
// text stays in the existing composer draft until the server acknowledges it.
export function reserveRunSteeringRequest(
  storage: Storage,
  sessionID: string,
  taskID: string,
  runID: string,
  content: string,
): { key: string; id: string } {
  const prefix = `hecate.runSteering.${sha256Hex(JSON.stringify([sessionID, taskID, runID]))}.`;
  const fingerprint = sha256Hex(JSON.stringify([sessionID, taskID, runID, content]));
  const key = `${prefix}${fingerprint}`;
  const legacyKey = `hecate.runSteering.${fingerprint}`;
  const existing = storage.getItem(key);
  const legacy = storage.getItem(legacyKey);
  const saved = existing ?? legacy;
  if (saved !== null) {
    if (
      !/^[\x21-\x7e]{1,128}$/.test(saved) ||
      (existing !== null && legacy !== null && existing !== legacy)
    ) {
      throw new Error(
        "Saved guidance delivery state is unreadable. Review the run before retrying.",
      );
    }
    if (existing === null) {
      storage.setItem(key, saved);
      if (storage.getItem(key) !== saved) {
        throw new Error("Could not save guidance delivery state. Nothing was sent.");
      }
    }
    // Migrate only this exact payload, keeping its identity through failure.
    // Old runs must not exhaust another run's retry bookkeeping allowance.
    if (legacy !== null) storage.removeItem(legacyKey);
    return { key, id: saved };
  }
  let unresolved = 0;
  for (let index = 0; index < storage.length; index++) {
    if (storage.key(index)?.startsWith(prefix)) unresolved++;
  }
  if (unresolved >= 64) {
    throw new Error(
      "This run has too many guidance deliveries needing review. Retry the original text to check delivery, or stop this run and send a follow-up.",
    );
  }
  const id = `guidance-${Date.now()}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`;
  storage.setItem(key, id);
  if (storage.getItem(key) !== id) {
    throw new Error("Could not save guidance delivery state. Nothing was sent.");
  }
  return { key, id };
}

export function settleRunSteeringRequest(storage: Storage, request: { key: string; id: string }) {
  if (storage.getItem(request.key) === request.id) storage.removeItem(request.key);
}
