import type { ChatSessionRecord } from "../../types/chat";

// reconcileChatSession folds an incoming session snapshot onto the
// previous one while preserving object identity for the parts that did
// not change.
//
// Why this exists: the agent chat live stream publishes a *full*
// session snapshot per update (the backend deliberately keeps a
// snapshot protocol rather than forking a delta protocol). Each
// snapshot is freshly deserialized JSON, so every message object has a
// new identity even when its content is byte-for-byte identical to the
// last snapshot. Feeding that straight into React state forces every
// transcript row to re-render (and re-parse its markdown) on every
// streamed token batch — O(transcript) work at flush frequency.
//
// By reusing the previous message object whenever the incoming one is
// deep-equal, downstream memoized rows (keyed on message identity) skip
// re-rendering the messages that did not change. Only the message
// actually being streamed gets a new identity and re-renders.
//
// The comparison cost is O(transcript) per snapshot, but it is a cheap
// structural walk of small plain objects — far less than re-parsing
// markdown and rebuilding the React subtree for every row.
export function reconcileChatSession(
  prev: ChatSessionRecord | null,
  next: ChatSessionRecord,
): ChatSessionRecord {
  // Different session (operator switched) or no prior state: nothing to
  // reuse, take the snapshot as-is.
  if (!prev || prev.id !== next.id) {
    return next;
  }

  return {
    ...next,
    messages: reconcileMessages(prev.messages, next.messages),
    segments: reuseIfEqual(prev.segments, next.segments),
    agent_info: reuseIfEqual(prev.agent_info, next.agent_info),
    config_options: reuseIfEqual(prev.config_options, next.config_options),
    available_commands: reuseIfEqual(prev.available_commands, next.available_commands),
  };
}

// reconcileMessages returns an array where each element is the previous
// message object when an incoming message with the same id is
// deep-equal, otherwise the incoming object. Messages are matched by
// id; messages without an id (synthetic/optimistic placeholders) are
// never reused since their identity is not stable across snapshots.
function reconcileMessages(
  prev: ChatSessionRecord["messages"],
  next: ChatSessionRecord["messages"],
): ChatSessionRecord["messages"] {
  if (!next || next.length === 0) return next;
  if (!prev || prev.length === 0) return next;

  const previousByID = new Map<string, NonNullable<ChatSessionRecord["messages"]>[number]>();
  for (const message of prev) {
    if (message.id) previousByID.set(message.id, message);
  }

  return next.map((message) => {
    if (!message.id) return message;
    const previous = previousByID.get(message.id);
    return previous && deepEqual(previous, message) ? previous : message;
  });
}

// reuseIfEqual returns the previous value when it is deep-equal to the
// next, preserving its reference; otherwise returns next.
function reuseIfEqual<T>(prev: T | undefined, next: T | undefined): T | undefined {
  if (prev === next) return next;
  if (prev !== undefined && next !== undefined && deepEqual(prev, next)) {
    return prev;
  }
  return next;
}

// deepEqual is a structural equality check for the JSON-shaped data the
// chat session carries (plain objects, arrays, and primitives). It is
// intentionally narrow: chat records never contain Dates, Maps, Sets,
// functions, or class instances, so this does not handle them.
function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b) return false;
  if (a === null || b === null) return false;

  if (Array.isArray(a)) {
    if (!Array.isArray(b) || a.length !== b.length) return false;
    for (let i = 0; i < a.length; i += 1) {
      if (!deepEqual(a[i], b[i])) return false;
    }
    return true;
  }

  if (typeof a === "object") {
    if (typeof b !== "object" || b === null || Array.isArray(b)) return false;
    const aRecord = a as Record<string, unknown>;
    const bRecord = b as Record<string, unknown>;
    const aKeys = Object.keys(aRecord);
    const bKeys = Object.keys(bRecord);
    if (aKeys.length !== bKeys.length) return false;
    for (const key of aKeys) {
      if (!Object.prototype.hasOwnProperty.call(bRecord, key)) return false;
      if (!deepEqual(aRecord[key], bRecord[key])) return false;
    }
    return true;
  }

  // Primitives that are not === are not equal.
  return false;
}
