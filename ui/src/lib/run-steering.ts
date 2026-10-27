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
  const key = `hecate.runSteering.${sha256Hex(JSON.stringify([sessionID, taskID, runID, content]))}`;
  const existing = storage.getItem(key);
  if (existing) {
    if (!/^[\x21-\x7e]{1,128}$/.test(existing)) {
      throw new Error(
        "Saved guidance delivery state is unreadable. Review the run before retrying.",
      );
    }
    return { key, id: existing };
  }
  let unresolved = 0;
  for (let index = 0; index < storage.length; index++) {
    if (storage.key(index)?.startsWith("hecate.runSteering.")) unresolved++;
  }
  if (unresolved >= 64) {
    throw new Error(
      "Too many guidance deliveries need review. Check their run activity before sending more.",
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
