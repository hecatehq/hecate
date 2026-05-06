import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  applyTaskRunPatch, cancelTaskRun, createTask, deleteTask, getModels, getProviderPresets, getProviders,
  getTaskApprovals, getTaskRunArtifacts, getTaskRunEvents,
  getTaskRuns, getTaskRunSteps, getTasks, resolveTaskApproval, revertTaskRunPatch,
  retryTaskRun, retryTaskRunFromTurn, resumeTaskRun, resumeTaskRunRaisingCeiling,
  startTask, streamTaskRun,
} from "../../lib/api";
import type {
  ModelRecord,
  ProviderPresetRecord,
  ProviderRecord,
  TaskApprovalRecord, TaskArtifactRecord, TaskRecord, TaskRunRecord, TaskStepRecord,
  TaskRunEventRecord, TaskActivityRecord,
} from "../../types/runtime";
import { TaskList } from "./TaskList";
import { TaskDetail } from "./TaskDetail";
import { NewTaskSlideOver, type CreateTaskPayload } from "./NewTaskSlideOver";

type StreamState = "idle" | "connecting" | "live" | "closed" | "error";
type TaskFocusRequest = { taskID: string; runID?: string; nonce: number };

function readStoredAgentWorkspace(): string {
  if (typeof window === "undefined") return "";
  try {
    return window.localStorage.getItem("hecate.agentWorkspace")?.trim() ?? "";
  } catch {
    return "";
  }
}

export function streamTurnCostKey(turnIndex: number | undefined): number | null {
  if (typeof turnIndex !== "number" || !Number.isFinite(turnIndex) || turnIndex < 0) {
    return null;
  }
  return Math.trunc(turnIndex) + 1;
}

export function TasksView({
  focusRequest,
  onOpenAgentChat,
}: {
  focusRequest?: TaskFocusRequest | null;
  onOpenAgentChat?: (sessionID: string) => void;
} = {}) {
  const [loading, setLoading] = useState(true);
  const [tasks, setTasks] = useState<TaskRecord[]>([]);
  const [selectedTaskID, setSelectedTaskID] = useState("");
  const [runs, setRuns] = useState<TaskRunRecord[]>([]);
  const [selectedRunID, setSelectedRunID] = useState("");
  const [approvals, setApprovals] = useState<TaskApprovalRecord[]>([]);
  const [steps, setSteps] = useState<TaskStepRecord[]>([]);
  const [artifacts, setArtifacts] = useState<TaskArtifactRecord[]>([]);
  const [activity, setActivity] = useState<TaskActivityRecord[]>([]);
  const [runEvents, setRunEvents] = useState<TaskRunEventRecord[]>([]);
  // Streamed per-turn costs, keyed by turn number. Populated as
  // `turn.completed` events arrive on the SSE stream. Acts as a
  // fallback for the model-step output_summary path: when the step's
  // cost isn't recorded yet (or older runs that pre-date the cost
  // field), the conversation viewer reads from this map instead.
  const [streamTurnCosts, setStreamTurnCosts] = useState<Map<number, number>>(new Map());
  const [streamState, setStreamState] = useState<StreamState>("idle");
  const [busyAction, setBusyAction] = useState("");
  const [notice, setNotice] = useState<{ tone: "success" | "error"; message: string } | null>(null);
  const [newTaskOpen, setNewTaskOpen] = useState(false);
  const [defaultTaskWorkspace, setDefaultTaskWorkspace] = useState(readStoredAgentWorkspace);
  const [availableModels, setAvailableModels] = useState<ModelRecord[]>([]);
  // Provider catalog feeds the new-task slideover's provider picker
  // and the model picker's per-row "(provider name)" suffix. Loaded
  // once on mount alongside models — the catalog rarely changes
  // mid-session, so the simple one-shot fetch is enough; control-plane
  // changes (enabling/disabling a provider) take effect after the
  // operator opens a new tab or refreshes.
  const [availableProviders, setAvailableProviders] = useState<ProviderRecord[]>([]);
  const [providerPresets, setProviderPresets] = useState<ProviderPresetRecord[]>([]);

  const streamCursorRef = useRef(0);

  const selectedTask = useMemo(() => tasks.find(t => t.id === selectedTaskID) ?? null, [tasks, selectedTaskID]);
  const selectedRun = useMemo(() => runs.find(r => r.id === selectedRunID) ?? null, [runs, selectedRunID]);

  const resetRunDetail = useCallback(() => {
    setSteps([]);
    setArtifacts([]);
    setActivity([]);
    setRunEvents([]);
    setStreamTurnCosts(new Map());
    streamCursorRef.current = 0;
  }, []);

  const loadRunDetail = useCallback(async (taskID: string, runID: string) => {
    if (!taskID || !runID) { resetRunDetail(); return; }
    const [stepsRes, artifactsRes, eventsRes] = await Promise.all([
      getTaskRunSteps(taskID, runID),
      getTaskRunArtifacts(taskID, runID),
      getTaskRunEvents(taskID, runID, 0),
    ]);
    setSteps(stepsRes.data ?? []);
    setArtifacts(artifactsRes.data ?? []);
    setRunEvents((eventsRes.data ?? []).slice().sort((left, right) => left.sequence - right.sequence));
  }, [resetRunDetail]);

  const loadTaskDetail = useCallback(async (taskID: string, preferredRunID = "") => {
    if (!taskID) return;
    const [runsRes, approvalsRes] = await Promise.all([
      getTaskRuns(taskID),
      getTaskApprovals(taskID),
    ]);
    const nextRuns = runsRes.data ?? [];
    setRuns(nextRuns);
    setApprovals(approvalsRes.data ?? []);
    const nextRunID = (preferredRunID && nextRuns.some(r => r.id === preferredRunID) ? preferredRunID : "") || nextRuns[0]?.id || "";
    setSelectedRunID(nextRunID);
    if (nextRunID) await loadRunDetail(taskID, nextRunID);
    else resetRunDetail();
  }, [loadRunDetail, resetRunDetail]);

  const loadTasks = useCallback(async (preferredTaskID = "", preferredRunID = "") => {
    // single-user: always authenticated
    setLoading(true);
    try {
      const res = await getTasks(30);
      const nextTasks = res.data ?? [];
      setTasks(nextTasks);
      const nextTaskID = (preferredTaskID && nextTasks.some(t => t.id === preferredTaskID) ? preferredTaskID : "") || nextTasks[0]?.id || "";
      setSelectedTaskID(nextTaskID);
      if (nextTaskID) await loadTaskDetail(nextTaskID, preferredRunID);
    } catch { /* silently ignore */ }
    finally { setLoading(false); }
  }, [loadTaskDetail]);

  useEffect(() => { void loadTasks(); }, [loadTasks]);

  useEffect(() => {
    if (!focusRequest?.taskID) return;
    void loadTasks(focusRequest.taskID, focusRequest.runID);
  }, [focusRequest, loadTasks]);

  useEffect(() => {
    // Models + providers + presets feed the new-task slideover's
    // model and provider pickers. Load all three in parallel; on
    // failure each falls back to its empty default rather than
    // blocking the whole page — a missing provider catalog just
    // means the picker shows raw provider ids instead of pretty
    // names, and a missing model list shows "no models match".
    getModels().then(res => setAvailableModels(res.data ?? [])).catch(() => {});
    getProviders().then(res => setAvailableProviders(res.data ?? [])).catch(() => {});
    getProviderPresets().then(res => setProviderPresets(res.data ?? [])).catch(() => {});
  }, []);

  useEffect(() => {
    if (!selectedTaskID || !selectedRunID) {
      setStreamState(selectedRunID ? "closed" : "idle");
      return;
    }
    const controller = new AbortController();
    setStreamState("connecting");

    void streamTaskRun(
      selectedTaskID, selectedRunID,
      ({ payload }) => {
        setStreamState("live");
        streamCursorRef.current = payload.data.sequence ?? streamCursorRef.current;
        setRuns(cur => {
          const others = cur.filter(r => r.id !== payload.data.run.id);
          return [payload.data.run, ...others];
        });
        setSteps(payload.data.steps ?? []);
        setArtifacts(payload.data.artifacts ?? []);
        setActivity(payload.data.activity ?? []);
        // Capture per-turn cost when the snapshot was driven by an
        // `turn.completed` event. Dedup by turn number — the
        // SSE may replay the same event on reconnect, and we don't
        // want a duplicate to wipe the entry. A `0` cost keeps the
        // entry (legitimate free tier / cached turn).
        const turnCostKey = streamTurnCostKey(payload.data.turn?.turn_index);
        if (payload.data.turn && turnCostKey !== null) {
          setStreamTurnCosts(prev => {
            const next = new Map(prev);
            next.set(turnCostKey, payload.data.turn!.cost_micros_usd ?? 0);
            return next;
          });
        }
        // Approvals ride along in every snapshot now (server-side
        // change in TaskRunStreamEventData). Treat the SSE as the
        // source of truth so the banner stays in sync — without
        // this, an approval created mid-stream wouldn't surface
        // until a manual refresh, and a server-resolved one would
        // linger in the UI for the same reason. We only overwrite
        // when the payload actually carries the field, so older
        // gateways that don't include it don't blank the banner.
        if (payload.data.approvals !== undefined) {
          setApprovals(payload.data.approvals);
        }
        const eventType = payload.data.event_type;
        if (eventType && payload.data.sequence > 0) {
          setRunEvents(cur => {
            if (cur.some(event => event.sequence === payload.data.sequence)) {
              return cur;
            }
            return [
              ...cur,
              {
                schema_version: "1",
                event_id: `stream-${payload.data.sequence}`,
                task_id: selectedTaskID,
                run_id: selectedRunID,
                sequence: payload.data.sequence,
                occurred_at: new Date().toISOString(),
                type: eventType,
                data: {},
              },
            ];
          });
        }
        setTasks(cur => cur.map(t => t.id === selectedTaskID ? { ...t, status: payload.data.run.status } : t));
      },
      streamCursorRef.current,
      controller.signal,
    ).then(() => {
      if (!controller.signal.aborted) {
        setStreamState("closed");
        void loadTaskDetail(selectedTaskID, selectedRunID);
      }
    }).catch((err) => {
      if (!controller.signal.aborted) {
        setStreamState("error");
        console.error(err);
      }
    });

    return () => controller.abort();
  }, [loadTaskDetail, selectedRunID, selectedTaskID]);

  async function handleSelectTask(taskID: string) {
    setSelectedTaskID(taskID);
    resetRunDetail();
    setNotice(null);
    try {
      await loadTaskDetail(taskID);
    } catch (err) {
      // 404 here means the cached task ID is stale (gateway restarted
      // with memory backend, tenant change, etc.). Drop the dead row
      // from the visible list so subsequent clicks don't repeat the
      // 404, and surface a concrete notice. Other errors fall through
      // silently — the run pane already renders an error from the
      // SSE state if one occurs.
      if (err instanceof ApiError && err.status === 404) {
        setNotice({ tone: "error", message: "That task no longer exists. Refreshing." });
        setTasks(cur => cur.filter(t => t.id !== taskID));
        if (selectedTaskID === taskID) {
          setSelectedTaskID("");
          resetRunDetail();
        }
        void loadTasks();
      }
    }
  }

  async function handleSelectRun(runID: string) {
    if (!selectedTaskID || runID === selectedRunID) return;
    setSelectedRunID(runID);
    streamCursorRef.current = 0;
    setNotice(null);
    try { await loadRunDetail(selectedTaskID, runID); } catch { /* ignore */ }
  }

  async function handleResolveApproval(approval: TaskApprovalRecord, decision: "approve" | "reject") {
    if (!selectedTaskID) return;
    setBusyAction(decision);
    try {
      await resolveTaskApproval(selectedTaskID, approval.id, { decision });
      setNotice({ tone: "success", message: decision === "approve" ? "Approved." : "Denied." });
      await loadTaskDetail(selectedTaskID, approval.run_id);
    } catch (err) {
      setNotice({ tone: "error", message: err instanceof Error ? err.message : "failed" });
    } finally { setBusyAction(""); }
  }

  async function handleCancelRun() {
    if (!selectedTaskID || !selectedRunID) return;
    setBusyAction("cancel");
    try {
      await cancelTaskRun(selectedTaskID, selectedRunID);
      await loadTaskDetail(selectedTaskID, selectedRunID);
    } catch { /* ignore */ }
    finally { setBusyAction(""); }
  }

  async function handleRetryRun() {
    if (!selectedTaskID || !selectedRunID) return;
    setBusyAction("retry");
    try {
      const res = await retryTaskRun(selectedTaskID, selectedRunID);
      await loadTasks(selectedTaskID, res.data.id);
    } catch { /* ignore */ }
    finally { setBusyAction(""); }
  }

  async function handleResumeRun() {
    if (!selectedTaskID || !selectedRunID) return;
    setBusyAction("resume");
    try {
      const res = await resumeTaskRun(selectedTaskID, selectedRunID);
      await loadTasks(selectedTaskID, res.data.id);
    } catch { /* ignore */ }
    finally { setBusyAction(""); }
  }

  // Raise the per-task cost ceiling and resume in one click. Only
  // exposed in the run header when the prior run failed with
  // otel_status_message=cost_ceiling_exceeded. The gateway persists
  // the new ceiling before queueing the resumed run, so the agent
  // loop sees the raised value on its first turn. Surfaces server
  // validation (e.g. "ceiling cannot be lower") as a notice rather
  // than failing silently.
  async function handleResumeRaisingCeiling(budgetMicrosUSD: number) {
    if (!selectedTaskID || !selectedRunID) return;
    setBusyAction("resume-raise");
    try {
      const res = await resumeTaskRunRaisingCeiling(selectedTaskID, selectedRunID, budgetMicrosUSD);
      setNotice({ tone: "success", message: `Ceiling raised to $${(budgetMicrosUSD / 1_000_000).toFixed(3)} and resumed (run #${res.data.number}).` });
      await loadTasks(selectedTaskID, res.data.id);
    } catch (err) {
      setNotice({ tone: "error", message: err instanceof Error ? err.message : "raise & resume failed" });
    } finally { setBusyAction(""); }
  }

  async function handleApplyPatch(artifactID: string) {
    if (!selectedTaskID || !selectedRunID) return;
    setBusyAction("apply-patch:" + artifactID);
    try {
      await applyTaskRunPatch(selectedTaskID, selectedRunID, artifactID);
      setNotice({ tone: "success", message: "Patch applied." });
      await loadRunDetail(selectedTaskID, selectedRunID);
    } catch (err) {
      setNotice({ tone: "error", message: err instanceof Error ? err.message : "patch apply failed" });
    } finally { setBusyAction(""); }
  }

  async function handleRevertPatch(artifactID: string) {
    if (!selectedTaskID || !selectedRunID) return;
    setBusyAction("revert-patch:" + artifactID);
    try {
      await revertTaskRunPatch(selectedTaskID, selectedRunID, artifactID);
      setNotice({ tone: "success", message: "Patch reverted." });
      await loadRunDetail(selectedTaskID, selectedRunID);
    } catch (err) {
      setNotice({ tone: "error", message: err instanceof Error ? err.message : "patch revert failed" });
    } finally { setBusyAction(""); }
  }

  // Retry-from-turn-N: re-issue the LLM call at turn N with the prior
  // conversation preserved. Server-side validation rejects out-of-range
  // turns and non-agent_loop runs with a 4xx — we surface the message
  // in the run-level notice so the operator can correct and try again
  // rather than silently failing.
  async function handleRetryFromTurn(turn: number, reason: string) {
    if (!selectedTaskID || !selectedRunID) return;
    setBusyAction("retry-from-turn");
    try {
      const res = await retryTaskRunFromTurn(selectedTaskID, selectedRunID, turn, reason || undefined);
      const reasonSuffix = reason ? ` — ${reason}` : "";
      setNotice({ tone: "success", message: `Retrying from turn ${turn}${reasonSuffix} (run #${res.data.number}).` });
      await loadTasks(selectedTaskID, res.data.id);
    } catch (err) {
      setNotice({ tone: "error", message: err instanceof Error ? err.message : "retry-from-turn failed" });
    } finally { setBusyAction(""); }
  }

  async function handleDeleteTask(taskID: string) {
    setBusyAction("delete:" + taskID);
    try {
      await deleteTask(taskID);
      const nextTasks = tasks.filter(t => t.id !== taskID);
      setTasks(nextTasks);
      if (selectedTaskID === taskID) {
        const next = nextTasks[0]?.id ?? "";
        setSelectedTaskID(next);
        if (next) await loadTaskDetail(next);
        else resetRunDetail();
      }
    } catch (err) {
      setNotice({ tone: "error", message: err instanceof Error ? err.message : "delete failed" });
    } finally { setBusyAction(""); }
  }

  async function handleCreateTask(payload: CreateTaskPayload) {
    setBusyAction("create");
    try {
      const created = await createTask(payload);
      const started = await startTask(created.data.id);
      setNewTaskOpen(false);
      await loadTasks(created.data.id, started.data.id);
    } catch (err) {
      setNotice({ tone: "error", message: err instanceof Error ? err.message : "failed to create task" });
    } finally { setBusyAction(""); }
  }

  return (
    <div style={{ display: "flex", height: "100%", overflow: "hidden", position: "relative" }}>
      <TaskList
        tasks={tasks}
        selectedTaskID={selectedTaskID}
        loading={loading}
        busyAction={busyAction}
        onSelect={(id) => void handleSelectTask(id)}
        onDelete={(id) => void handleDeleteTask(id)}
        onNewTask={() => {
          setDefaultTaskWorkspace(readStoredAgentWorkspace());
          setNewTaskOpen(true);
        }}
        onRefresh={() => void loadTasks(selectedTaskID, selectedRunID)}
      />

      {selectedTask ? (
        <TaskDetail
          task={selectedTask}
          run={selectedRun}
          runs={runs}
          selectedRunID={selectedRunID}
          steps={steps}
          artifacts={artifacts}
          activity={activity}
          events={runEvents}
          approvals={approvals}
          streamTurnCosts={streamTurnCosts}
          streamState={streamState}
          busyAction={busyAction}
          notice={notice}
          onSelectRun={(id) => void handleSelectRun(id)}
          onOpenAgentChat={onOpenAgentChat}
          onResolveApproval={(approval, decision) => void handleResolveApproval(approval, decision)}
          onCancelRun={() => void handleCancelRun()}
          onRetryRun={() => void handleRetryRun()}
          onResumeRun={() => void handleResumeRun()}
          onRetryFromTurn={(turn, reason) => void handleRetryFromTurn(turn, reason)}
          onResumeRaisingCeiling={(budgetMicrosUSD) => void handleResumeRaisingCeiling(budgetMicrosUSD)}
          onApplyPatch={(artifactID) => void handleApplyPatch(artifactID)}
          onRevertPatch={(artifactID) => void handleRevertPatch(artifactID)}
        />
      ) : (
        <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center" }}>
          <div style={{ textAlign: "center", color: "var(--t3)", fontSize: 12 }}>
            {loading ? "Loading…" : "Select a task to inspect."}
          </div>
        </div>
      )}

      <NewTaskSlideOver
        open={newTaskOpen}
        models={availableModels}
        providers={availableProviders}
        providerPresets={providerPresets}
        defaultWorkspace={defaultTaskWorkspace}
        busyAction={busyAction}
        errorMessage={notice?.tone === "error" ? notice.message : undefined}
        onClose={() => setNewTaskOpen(false)}
        onCreate={(payload) => void handleCreateTask(payload)}
      />

      <style>{`
        @keyframes pulse { 0%,100%{opacity:1} 50%{opacity:0.4} }
        @keyframes blink  { 0%,100%{opacity:1} 50%{opacity:0} }
      `}</style>
    </div>
  );
}
