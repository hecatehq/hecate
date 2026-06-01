// Coordinator action override context. Production never sets a value
// here — coordinator hooks call into the real implementations. Test
// scaffolding provides a partial override so view tests can keep
// asserting on action calls (`expect(setProviderAPIKey).toHaveBeenCalledWith(...)`)
// without exercising the real API + slice mutation chain.
//
// Each coordinator hook checks this context and returns the override
// bag merged over its real return value. The override is shallow per
// coordinator: missing fields fall through to the real implementation.

import { createContext, useContext, type ReactNode } from "react";

// The coordinator and slice action types are intentionally opaque
// here. Each coordinator (or slice) hook reads `overrides?.<name>`
// and merges the partial over its own typed return value via
// `applyOverride`, so the override pass-through is type-checked at
// the call site without forcing this module to import slice or
// coordinator types — that would re-introduce a cycle via the
// slice ↔ overrides ↔ coordinator import chain.
export type CoordinatorActionsOverride = Record<string, unknown>;

export type CoordinatorOverrides = {
  chat?: CoordinatorActionsOverride;
  dashboard?: CoordinatorActionsOverride;
  providers?: CoordinatorActionsOverride;
  policy?: CoordinatorActionsOverride;
  agentAdapters?: CoordinatorActionsOverride;
  retention?: CoordinatorActionsOverride;
  settings?: CoordinatorActionsOverride;
  // Slice-action overrides — needed because views call slice actions
  // directly (setMessage, setModel, removeQueuedChatMessage, …) and
  // existing tests already stub these. The slice provider's reducer
  // still runs against the seeded state; the override just intercepts
  // the action dispatch.
  runtimeSlice?: CoordinatorActionsOverride;
  usageSlice?: CoordinatorActionsOverride;
  providersAndModelsSlice?: CoordinatorActionsOverride;
  projectsSlice?: CoordinatorActionsOverride;
  chatSlice?: CoordinatorActionsOverride;
  approvalsSlice?: CoordinatorActionsOverride;
  retentionSlice?: CoordinatorActionsOverride;
  settingsSlice?: CoordinatorActionsOverride;
  // Derived-value overrides. The retired facade exposed these as
  // precomputed values; per-view tests still set them directly and
  // expect those values to win regardless of what the derivation
  // hooks would compute from slice state. Production never sets.
  derivedChatTarget?: "agent" | "external_agent";
  derivedNewChatAgentID?: string;
};

export const CoordinatorOverridesContext = createContext<CoordinatorOverrides | null>(null);

export function CoordinatorOverridesProvider({
  value,
  children,
}: {
  value: CoordinatorOverrides | null;
  children: ReactNode;
}) {
  return (
    <CoordinatorOverridesContext.Provider value={value}>
      {children}
    </CoordinatorOverridesContext.Provider>
  );
}

export function useCoordinatorOverrides(): CoordinatorOverrides | null {
  return useContext(CoordinatorOverridesContext);
}

// Helpers each coordinator hook calls to merge its real return value
// with any test-provided override. Coordinators that return plain
// objects with stable shapes — every coordinator in `coordinators/`.
export function applyOverride<T extends object>(
  real: T,
  override: CoordinatorActionsOverride | undefined,
): T {
  if (!override) return real;
  return { ...real, ...(override as Partial<T>) };
}
