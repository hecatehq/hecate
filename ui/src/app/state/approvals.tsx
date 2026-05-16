// Approvals slice: pending agent-chat approval banners + grant
// list. Owns four state fields plus an internal mutation-version
// ref that protects the catch-up GET against races with the SSE
// stream and optimistic local actions.
//
// State map invariant: `pendingBySessionID` is replaced (never
// mutated in place) on update; React reference equality is the
// re-render trigger. Empty arrays are deleted from the map so
// `.get(sessionID)?.length` reads as "no pending banner" without
// a separate empty-vs-missing branch.
//
// Race protection: each mutator (upsert, remove) bumps a per-
// session version counter in a ref. `refetchPending` snapshots
// the version before its request and compares after; a mismatch
// means a live SSE update or optimistic local action landed
// during the catch-up, so we ignore the stale GET rather than
// clobbering newer state.
//
// Cross-slice concerns: action requests can fail; the slice
// returns discriminated Results so callers route success / error
// to the global `notice` banner without the slice importing it.

import { createContext, useCallback, useContext, useMemo, useReducer, useRef, type ReactNode } from "react";

import {
  type AgentChatGrantFilter,
  cancelAgentChatApproval as cancelAgentChatApprovalRequest,
  deleteAgentChatGrant as deleteAgentChatGrantRequest,
  getAgentChatApproval as getAgentChatApprovalRequest,
  listAgentChatApprovals as listAgentChatApprovalsRequest,
  listAgentChatGrants as listAgentChatGrantsRequest,
  type ResolveAgentChatApprovalPayload,
  resolveAgentChatApproval as resolveAgentChatApprovalRequest,
} from "../../lib/api";
import { approvalRecordToPending } from "../runtimeConsoleChatHelpers";
import type {
  AgentChatApprovalRecord,
  AgentChatGrantRecord,
  PendingAgentApproval,
} from "../../types/runtime";

export type ApprovalsState = {
  pendingBySessionID: Map<string, PendingAgentApproval[]>;
  grants: AgentChatGrantRecord[];
  grantsLoading: boolean;
  grantsError: string;
};

export type ApprovalActionResult = { ok: true } | { ok: false; error: string };
export type GetApprovalResult =
  | { ok: true; record: AgentChatApprovalRecord }
  | { ok: false; error: string };

export type ApprovalsActions = {
  // Mutation API — called from the SSE stream handler + the
  // session-select catch-up. Synchronous; no cross-cut.
  setPendingForSession: (sessionID: string, rows: PendingAgentApproval[]) => void;
  upsertPending: (event: PendingAgentApproval) => void;
  removePending: (sessionID: string, approvalID: string) => void;
  refetchPending: (sessionID: string) => Promise<void>;
  // Request API — return Results so callers can wire notice/UI
  // feedback without the slice importing them.
  getApproval: (sessionID: string, approvalID: string) => Promise<GetApprovalResult>;
  resolveApproval: (
    sessionID: string,
    approvalID: string,
    decision: ResolveAgentChatApprovalPayload,
  ) => Promise<ApprovalActionResult>;
  cancelApproval: (sessionID: string, approvalID: string) => Promise<ApprovalActionResult>;
  // Grants — error state lives inside the slice (grantsError)
  // because Connections renders it inline; no Result needed for
  // loadGrants.
  loadGrants: (filter?: AgentChatGrantFilter) => Promise<void>;
  deleteGrant: (grantID: string) => Promise<ApprovalActionResult>;
};

type ApprovalsContextValue = {
  state: ApprovalsState;
  actions: ApprovalsActions;
};

type Action =
  | { type: "pending/setForSession"; sessionID: string; rows: PendingAgentApproval[] }
  | { type: "pending/upsert"; event: PendingAgentApproval }
  | { type: "pending/remove"; sessionID: string; approvalID: string }
  | { type: "grants/loadStart" }
  | { type: "grants/loaded"; rows: AgentChatGrantRecord[] }
  | { type: "grants/loadFailed"; error: string }
  | { type: "grants/removed"; grantID: string };

const initialState: ApprovalsState = {
  pendingBySessionID: new Map(),
  grants: [],
  grantsLoading: false,
  grantsError: "",
};

function reducer(state: ApprovalsState, action: Action): ApprovalsState {
  switch (action.type) {
    case "pending/setForSession": {
      const next = new Map(state.pendingBySessionID);
      if (action.rows.length === 0) {
        next.delete(action.sessionID);
      } else {
        next.set(action.sessionID, action.rows);
      }
      return { ...state, pendingBySessionID: next };
    }
    case "pending/upsert": {
      const next = new Map(state.pendingBySessionID);
      const existing = next.get(action.event.session_id) ?? [];
      const filtered = existing.filter((row) => row.approval_id !== action.event.approval_id);
      filtered.push(action.event);
      next.set(action.event.session_id, filtered);
      return { ...state, pendingBySessionID: next };
    }
    case "pending/remove": {
      const existing = state.pendingBySessionID.get(action.sessionID);
      if (!existing) return state;
      const filtered = existing.filter((row) => row.approval_id !== action.approvalID);
      if (filtered.length === existing.length) return state;
      const next = new Map(state.pendingBySessionID);
      if (filtered.length === 0) {
        next.delete(action.sessionID);
      } else {
        next.set(action.sessionID, filtered);
      }
      return { ...state, pendingBySessionID: next };
    }
    case "grants/loadStart":
      return { ...state, grantsLoading: true, grantsError: "" };
    case "grants/loaded":
      return { ...state, grants: action.rows, grantsLoading: false };
    case "grants/loadFailed":
      return { ...state, grantsLoading: false, grantsError: action.error };
    case "grants/removed":
      return {
        ...state,
        grants: state.grants.filter((grant) => grant.id !== action.grantID),
      };
  }
}

const ApprovalsContext = createContext<ApprovalsContextValue | null>(null);

export function ApprovalsProvider({ children }: { children: ReactNode }) {
  const [state, dispatch] = useReducer(reducer, initialState);
  // Per-session mutation version. Lives outside the reducer
  // because every live mutation needs to bump it synchronously
  // before refetchPending captures the "before" snapshot; folding
  // it into a useReducer action would mean a separate render
  // pass between the bump and the read.
  const versionBySessionID = useRef<Map<string, number>>(new Map());

  const bumpVersion = useCallback((sessionID: string) => {
    const current = versionBySessionID.current.get(sessionID) ?? 0;
    versionBySessionID.current.set(sessionID, current + 1);
  }, []);

  const setPendingForSession = useCallback((sessionID: string, rows: PendingAgentApproval[]) => {
    dispatch({ type: "pending/setForSession", sessionID, rows });
  }, []);

  const upsertPending = useCallback((event: PendingAgentApproval) => {
    bumpVersion(event.session_id);
    dispatch({ type: "pending/upsert", event });
  }, [bumpVersion]);

  const removePending = useCallback((sessionID: string, approvalID: string) => {
    bumpVersion(sessionID);
    dispatch({ type: "pending/remove", sessionID, approvalID });
  }, [bumpVersion]);

  const refetchPending = useCallback(async (sessionID: string) => {
    if (!sessionID) return;
    const startedAtVersion = versionBySessionID.current.get(sessionID) ?? 0;
    try {
      const result = await listAgentChatApprovalsRequest(sessionID, "pending");
      const currentVersion = versionBySessionID.current.get(sessionID) ?? 0;
      if (currentVersion !== startedAtVersion) {
        // A live SSE update or optimistic local action landed
        // during this catch-up. Ignore the stale GET rather than
        // clearing a newer pending approval or re-adding one
        // that was just resolved.
        return;
      }
      const rows = (result.data ?? []).map(approvalRecordToPending);
      dispatch({ type: "pending/setForSession", sessionID, rows });
    } catch {
      // Banner is best-effort; failure here just means the
      // operator doesn't see the catch-up state until the next
      // reconnect.
    }
  }, []);

  const getApproval = useCallback(async (
    sessionID: string,
    approvalID: string,
  ): Promise<GetApprovalResult> => {
    try {
      const payload = await getAgentChatApprovalRequest(sessionID, approvalID);
      return { ok: true, record: payload.data };
    } catch (error) {
      return { ok: false, error: error instanceof Error ? error.message : "Failed to load approval." };
    }
  }, []);

  const resolveApproval = useCallback(async (
    sessionID: string,
    approvalID: string,
    decision: ResolveAgentChatApprovalPayload,
  ): Promise<ApprovalActionResult> => {
    try {
      await resolveAgentChatApprovalRequest(sessionID, approvalID, decision);
      // Optimistic removal: the SSE `approval.resolved` event will
      // also fire and remove the row, but updating immediately
      // keeps the banner snappy when the operator closes the modal.
      bumpVersion(sessionID);
      dispatch({ type: "pending/remove", sessionID, approvalID });
      return { ok: true };
    } catch (error) {
      return { ok: false, error: error instanceof Error ? error.message : "Failed to resolve approval." };
    }
  }, [bumpVersion]);

  const cancelApproval = useCallback(async (
    sessionID: string,
    approvalID: string,
  ): Promise<ApprovalActionResult> => {
    try {
      await cancelAgentChatApprovalRequest(sessionID, approvalID);
      bumpVersion(sessionID);
      dispatch({ type: "pending/remove", sessionID, approvalID });
      return { ok: true };
    } catch (error) {
      return { ok: false, error: error instanceof Error ? error.message : "Failed to cancel approval." };
    }
  }, [bumpVersion]);

  const loadGrants = useCallback(async (filter: AgentChatGrantFilter = {}): Promise<void> => {
    dispatch({ type: "grants/loadStart" });
    try {
      const payload = await listAgentChatGrantsRequest(filter);
      dispatch({ type: "grants/loaded", rows: payload.data ?? [] });
    } catch (error) {
      const msg = error instanceof Error ? error.message : "failed to load grants";
      dispatch({ type: "grants/loadFailed", error: msg });
    }
  }, []);

  const deleteGrant = useCallback(async (grantID: string): Promise<ApprovalActionResult> => {
    try {
      await deleteAgentChatGrantRequest(grantID);
      dispatch({ type: "grants/removed", grantID });
      return { ok: true };
    } catch (error) {
      return { ok: false, error: error instanceof Error ? error.message : "Failed to revoke grant." };
    }
  }, []);

  const actions = useMemo<ApprovalsActions>(() => ({
    setPendingForSession,
    upsertPending,
    removePending,
    refetchPending,
    getApproval,
    resolveApproval,
    cancelApproval,
    loadGrants,
    deleteGrant,
  }), [
    setPendingForSession,
    upsertPending,
    removePending,
    refetchPending,
    getApproval,
    resolveApproval,
    cancelApproval,
    loadGrants,
    deleteGrant,
  ]);

  const value = useMemo(() => ({ state, actions }), [state, actions]);

  return <ApprovalsContext.Provider value={value}>{children}</ApprovalsContext.Provider>;
}

export function useApprovals(): ApprovalsContextValue {
  const ctx = useContext(ApprovalsContext);
  if (!ctx) {
    throw new Error("useApprovals must be used inside an <ApprovalsProvider>");
  }
  return ctx;
}
