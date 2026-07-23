import { ApiError } from "../../lib/api";

const externalAgentObserverRetryBaseMS = 250;
const externalAgentObserverRetryMaxMS = 2_000;
const externalAgentObserverNonRetryableHTTPStatuses = new Set([401, 403, 404]);

export function externalAgentObserverRetryDelayMS(attempt: number): number {
  const exponent = Math.min(Math.max(attempt, 0), 4);
  return Math.min(
    externalAgentObserverRetryBaseMS * 2 ** exponent,
    externalAgentObserverRetryMaxMS,
  );
}

export function externalAgentObserverFailureIsNonRetryable(error: unknown): error is ApiError {
  return (
    error instanceof ApiError && externalAgentObserverNonRetryableHTTPStatuses.has(error.status)
  );
}

export function externalAgentObserverFailureIsOrphanedTurn(error: unknown): error is ApiError {
  return (
    error instanceof ApiError && error.status === 409 && error.code === "chat.session_not_running"
  );
}

export function waitForExternalAgentObserverRetry(
  signal: AbortSignal,
  delayMS: number,
): Promise<boolean> {
  if (signal.aborted) return Promise.resolve(false);
  return new Promise((resolve) => {
    const timeout = window.setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve(true);
    }, delayMS);
    const onAbort = () => {
      window.clearTimeout(timeout);
      resolve(false);
    };
    signal.addEventListener("abort", onAbort, { once: true });
    if (signal.aborted) onAbort();
  });
}
