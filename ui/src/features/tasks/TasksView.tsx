import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  applyTaskRunPatch,
  cancelTaskRun,
  createTask,
  deleteTask,
  getModels,
  getProviders,
  getTask,
  getTaskApprovals,
  getTaskRunArtifacts,
  getTaskRunContext,
  getTaskRunEvents,
  getTaskRuns,
  getTaskRunSteps,
  getTasks,
  resolveTaskApproval,
  revertTaskRunPatch,
  retryTaskRun,
  retryTaskRunFromModelCall,
  resumeTaskRun,
  resumeTaskRunRaisingCeiling,
  startTask,
  streamTaskRun,
} from "../../lib/api";
import {
  useEnsureProviderPresetsLoaded,
  useProvidersAndModels,
} from "../../app/state/providersAndModels";
import { useChat } from "../../app/state/chat";
import { useProjects } from "../../app/state/projects";
import { projectDefaultWorkspace } from "../../lib/project-workspace";
import type { ModelRecord } from "../../types/model";
import type { ProviderRecord } from "../../types/provider";
import type {
  TaskActivityRecord,
  TaskApprovalRecord,
  TaskArtifactRecord,
  TaskRecord,
  TaskRunEventRecord,
  TaskRunRecord,
  TaskStepRecord,
} from "../../types/task";
import { TaskList } from "./TaskList";
import { TaskDetail } from "./TaskDetail";
import { NewTaskSlideOver, type CreateTaskPayload } from "./NewTaskSlideOver";
import { ProjectScopePanel } from "../projects/ProjectScopePanel";
import { formatProjectDeleteSummary } from "../projects/projectDisplay";

type StreamState = "idle" | "connecting" | "live" | "closed" | "error";
type TaskFocusRequest = { taskID: string; runID?: string; nonce: number };
type TaskSelection = { taskID: string; runID: string };

export function streamModelCallCostKey(modelCallIndex: number | undefined): number | null {
  if (
    typeof modelCallIndex !== "number" ||
    !Number.isFinite(modelCallIndex) ||
    modelCallIndex < 1
  ) {
    return null;
  }
  return Math.trunc(modelCallIndex);
}

function TaskStartState({
  loading,
  notice,
  onNewTask,
}: {
  loading: boolean;
  notice: { tone: "success" | "error"; message: string } | null;
  onNewTask: () => void;
}) {
  return (
    <div
      style={{
        flex: 1,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: 24,
      }}
    >
      <div style={{ maxWidth: 460, textAlign: "center" }}>
        <div style={{ fontSize: 15, color: "var(--t0)", fontWeight: 600 }}>
          {loading ? "Loading tasks…" : "Start a task"}
        </div>
        <div style={{ fontSize: 12, color: "var(--t3)", lineHeight: 1.6, marginTop: 8 }}>
          {loading
            ? "Checking the task runtime for recent work."
            : "Tasks are durable units of work. Each start, continuation, retry, or resume creates a Run. Create standalone shell, Git, file, or agent-loop work here, or inspect work started from Chats and Projects."}
        </div>
        {!loading && notice?.tone === "error" && (
          <div className="page-banner page-banner--error" role="alert" style={{ marginTop: 16 }}>
            {notice.message}
          </div>
        )}
        {!loading && (
          <button
            className="btn btn-primary"
            type="button"
            onClick={onNewTask}
            style={{ marginTop: 18, justifyContent: "center" }}
          >
            New task
          </button>
        )}
      </div>
    </div>
  );
}

export function TasksView({
  focusRequest,
  focusIntent,
  onFocusRequestHandled,
  onOpenChat,
  onOpenTrace,
  onSelectionChange,
}: {
  focusRequest?: TaskFocusRequest | null;
  focusIntent?: TaskFocusRequest | null;
  onFocusRequestHandled?: (nonce: number) => void;
  onOpenChat?: (sessionID: string, messageID?: string) => void;
  onOpenTrace?: (requestID: string) => void;
  onSelectionChange?: (
    taskID: string | null,
    runID: string | null,
    mode?: "push" | "replace",
  ) => void;
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
  // Streamed per-model-call costs, keyed by model-call number. Populated as
  // `model.call.completed` events arrive on the SSE stream. Acts as a
  // fallback for the model-step output_summary path: when the step's
  // cost isn't recorded yet (or older runs that pre-date the cost
  // field), the conversation viewer reads from this map instead.
  const [streamModelCallCosts, setStreamModelCallCosts] = useState<Map<number, number>>(new Map());
  const [streamState, setStreamState] = useState<StreamState>("idle");
  const [busyAction, setBusyAction] = useState("");
  const [notice, setNotice] = useState<{ tone: "success" | "error"; message: string } | null>(null);
  const [newTaskOpen, setNewTaskOpen] = useState(false);
  const [availableModels, setAvailableModels] = useState<ModelRecord[]>([]);
  // Provider catalog feeds the new-task slideover's provider picker
  // and the model picker's per-row "(provider name)" suffix. Loaded
  // once on mount alongside models — the catalog rarely changes
  // mid-session, so the simple one-shot fetch is enough; settings
  // changes (enabling/disabling a provider) take effect after the
  // operator opens a new tab or refreshes.
  const [availableProviders, setAvailableProviders] = useState<ProviderRecord[]>([]);
  useEnsureProviderPresetsLoaded();
  const providerPresets = useProvidersAndModels().state.providerPresets;
  const chat = useChat();
  const projects = useProjects();
  const selectProject = projects.actions.selectProject;
  const activeProjectID = projects.activeProjectID;
  const defaultTaskWorkspace = projectDefaultWorkspace(projects.activeProject);

  const streamCursorRef = useRef(0);
  const taskLoadGenerationRef = useRef(0);
  const selectedTaskRunRef = useRef<TaskSelection>({ taskID: "", runID: "" });

  const selectedTask = useMemo(
    () => tasks.find((t) => t.id === selectedTaskID) ?? null,
    [tasks, selectedTaskID],
  );
  const selectedRun = useMemo(
    () => runs.find((r) => r.id === selectedRunID) ?? null,
    [runs, selectedRunID],
  );
  const resetRunDetail = useCallback(() => {
    setSteps([]);
    setArtifacts([]);
    setActivity([]);
    setRunEvents([]);
    setStreamModelCallCosts(new Map());
    streamCursorRef.current = 0;
  }, []);

  const selectTaskRun = useCallback(
    (taskID: string, runID: string) => {
      const previous = selectedTaskRunRef.current;
      if (previous.taskID !== taskID || previous.runID !== runID) {
        resetRunDetail();
      }
      selectedTaskRunRef.current = { taskID, runID };
      setSelectedTaskID(taskID);
      setSelectedRunID(runID);
    },
    [resetRunDetail],
  );

  const loadRunDetail = useCallback(
    async (taskID: string, runID: string, generation?: number): Promise<boolean> => {
      const loadGeneration = generation ?? ++taskLoadGenerationRef.current;
      if (!taskID || !runID) {
        if (taskLoadGenerationRef.current === loadGeneration) resetRunDetail();
        return taskLoadGenerationRef.current === loadGeneration;
      }
      const [stepsRes, artifactsRes, eventsRes] = await Promise.all([
        getTaskRunSteps(taskID, runID),
        getTaskRunArtifacts(taskID, runID),
        getTaskRunEvents(taskID, runID, 0),
      ]);
      if (taskLoadGenerationRef.current !== loadGeneration) return false;
      setSteps(stepsRes.data ?? []);
      setArtifacts(artifactsRes.data ?? []);
      setRunEvents(
        (eventsRes.data ?? []).slice().sort((left, right) => left.sequence - right.sequence),
      );
      return true;
    },
    [resetRunDetail],
  );

  const loadTaskDetail = useCallback(
    async (taskID: string, preferredRunID = "", generation?: number): Promise<string | null> => {
      const loadGeneration = generation ?? ++taskLoadGenerationRef.current;
      if (!taskID) return "";
      const [runsRes, approvalsRes] = await Promise.all([
        getTaskRuns(taskID),
        getTaskApprovals(taskID),
      ]);
      if (taskLoadGenerationRef.current !== loadGeneration) return null;
      const nextRuns = runsRes.data ?? [];
      setRuns(nextRuns);
      setApprovals(approvalsRes.data ?? []);
      const nextRunID =
        (preferredRunID && nextRuns.some((r) => r.id === preferredRunID) ? preferredRunID : "") ||
        nextRuns[0]?.id ||
        "";
      selectTaskRun(taskID, nextRunID);
      if (nextRunID) {
        const loaded = await loadRunDetail(taskID, nextRunID, loadGeneration);
        if (!loaded) return null;
      } else resetRunDetail();
      return nextRunID;
    },
    [loadRunDetail, resetRunDetail, selectTaskRun],
  );

  const loadTasks = useCallback(
    async (
      preferredTaskID = "",
      preferredRunID = "",
      projectID: string | null = activeProjectID,
      addressedTask?: TaskRecord,
    ): Promise<TaskSelection | null> => {
      const loadGeneration = ++taskLoadGenerationRef.current;
      // single-user: always authenticated
      setLoading(true);
      try {
        const res = await getTasks(30, projectID);
        if (taskLoadGenerationRef.current !== loadGeneration) return null;
        const listedTasks = res.data ?? [];
        const nextTasks =
          addressedTask && !listedTasks.some((task) => task.id === addressedTask.id)
            ? [addressedTask, ...listedTasks]
            : listedTasks;
        setTasks(nextTasks);
        const currentTaskID = selectedTaskRunRef.current.taskID;
        const preservedTaskID =
          currentTaskID && nextTasks.some((t) => t.id === currentTaskID) ? currentTaskID : "";
        const nextTaskID =
          (preferredTaskID && nextTasks.some((t) => t.id === preferredTaskID)
            ? preferredTaskID
            : "") ||
          preservedTaskID ||
          nextTasks[0]?.id ||
          "";
        if (selectedTaskRunRef.current.taskID !== nextTaskID) {
          selectTaskRun(nextTaskID, "");
        }
        if (nextTaskID) {
          const nextRunID = await loadTaskDetail(nextTaskID, preferredRunID, loadGeneration);
          if (nextRunID === null) return null;
          return { taskID: nextTaskID, runID: nextRunID };
        }
        setRuns([]);
        setApprovals([]);
        selectTaskRun("", "");
        return { taskID: "", runID: "" };
      } catch {
        /* silently ignore */
        return null;
      } finally {
        if (taskLoadGenerationRef.current === loadGeneration) setLoading(false);
      }
    },
    [activeProjectID, loadTaskDetail, selectTaskRun],
  );

  useEffect(() => {
    if (focusRequest?.taskID) return;
    let cancelled = false;
    void loadTasks().then((selection) => {
      if (cancelled || !selection) return;
      onSelectionChange?.(selection.taskID || null, selection.runID || null, "replace");
    });
    return () => {
      cancelled = true;
    };
  }, [activeProjectID, focusRequest?.taskID, loadTasks, onSelectionChange]);

  useEffect(() => {
    if (!focusRequest?.taskID) return;
    let cancelled = false;
    taskLoadGenerationRef.current += 1;
    setLoading(true);
    setNotice(null);
    void (async () => {
      try {
        const addressedTask = (await getTask(focusRequest.taskID)).data;
        if (cancelled) return;
        const addressedProjectID = addressedTask.project_id ?? "";
        if (addressedProjectID !== projects.activeProjectID) {
          await selectProject(addressedProjectID);
        }
        if (cancelled) return;
        const selection = await loadTasks(
          focusRequest.taskID,
          focusRequest.runID,
          addressedProjectID,
          addressedTask,
        );
        if (cancelled || !selection) return;
        if (
          selection.taskID === focusRequest.taskID &&
          selection.runID === (focusRequest.runID ?? "")
        ) {
          return;
        }
        onSelectionChange?.(selection.taskID || null, selection.runID || null, "replace");
      } catch (error) {
        if (cancelled) return;
        if (!(error instanceof ApiError) || error.status !== 404) {
          selectTaskRun("", "");
          setLoading(false);
          setNotice({
            tone: "error",
            message: error instanceof Error ? error.message : "Failed to load the requested task.",
          });
          return;
        }
        const selection = await loadTasks("", "", projects.activeProjectID);
        if (cancelled || !selection) return;
        onSelectionChange?.(selection.taskID || null, selection.runID || null, "replace");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [
    focusRequest,
    loadTasks,
    onSelectionChange,
    projects.activeProjectID,
    selectProject,
    selectTaskRun,
  ]);

  useEffect(() => {
    // Models + providers + presets feed the new-task slideover's
    // model and provider pickers. Load all three in parallel; on
    // failure each falls back to its empty default rather than
    // blocking the whole page — a missing provider catalog just
    // means the picker shows raw provider ids instead of pretty
    // names, and a missing model list shows "no models match".
    getModels()
      .then((res) => setAvailableModels(res.data ?? []))
      .catch(() => {});
    getProviders()
      .then((res) => setAvailableProviders(res.data ?? []))
      .catch(() => {});
  }, []);

  useEffect(() => {
    if (!selectedTaskID || !selectedRunID) {
      setStreamState(selectedRunID ? "closed" : "idle");
      return;
    }
    const controller = new AbortController();
    setStreamState("connecting");

    void streamTaskRun(
      selectedTaskID,
      selectedRunID,
      ({ payload }) => {
        const currentSelection = selectedTaskRunRef.current;
        if (
          currentSelection.taskID !== selectedTaskID ||
          currentSelection.runID !== selectedRunID
        ) {
          return;
        }
        setStreamState("live");
        streamCursorRef.current = payload.data.sequence ?? streamCursorRef.current;
        setRuns((cur) => {
          const others = cur.filter((r) => r.id !== payload.data.run.id);
          return [payload.data.run, ...others];
        });
        setSteps(payload.data.steps ?? []);
        setArtifacts(payload.data.artifacts ?? []);
        setActivity(payload.data.activity ?? []);
        // Capture per-model-call cost when the snapshot was driven by a
        // `model.call.completed` event. Dedup by model-call number — the
        // SSE may replay the same event on reconnect, and we don't
        // want a duplicate to wipe the entry. A `0` cost keeps the
        // entry (legitimate free tier / cached model call).
        const modelCallCostKey = streamModelCallCostKey(payload.data.model_call?.model_call_index);
        if (payload.data.model_call && modelCallCostKey !== null) {
          setStreamModelCallCosts((prev) => {
            const next = new Map(prev);
            next.set(modelCallCostKey, payload.data.model_call!.cost_micros_usd ?? 0);
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
          setRunEvents((cur) => {
            if (cur.some((event) => event.sequence === payload.data.sequence)) {
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
        const streamedRun = payload.data.run;
        setTasks((cur) =>
          cur.map((task) => {
            if (task.id !== selectedTaskID || task.latest_run_id !== streamedRun.id) return task;
            return {
              ...task,
              status: streamedRun.status,
              latest_model: streamedRun.model,
              latest_provider: streamedRun.provider,
              latest_run_step_count: streamedRun.step_count ?? 0,
              latest_run_artifact_count: streamedRun.artifact_count ?? 0,
              last_error: streamedRun.last_error,
            };
          }),
        );
      },
      streamCursorRef.current,
      controller.signal,
    )
      .then(() => {
        if (!controller.signal.aborted) {
          setStreamState("closed");
          void loadTaskDetail(selectedTaskID, selectedRunID);
        }
      })
      .catch((err) => {
        if (!controller.signal.aborted) {
          setStreamState("error");
          console.error(err);
        }
      });

    return () => controller.abort();
  }, [loadTaskDetail, selectedRunID, selectedTaskID]);

  async function handleSelectTask(taskID: string) {
    selectTaskRun(taskID, "");
    setNotice(null);
    try {
      const runID = await loadTaskDetail(taskID);
      if (runID === null) return;
      onSelectionChange?.(taskID, runID || null);
    } catch (err) {
      // 404 here means the cached task ID is stale (gateway restarted
      // with memory backend, tenant change, etc.). Drop the dead row
      // from the visible list so subsequent clicks don't repeat the
      // 404, and surface a concrete notice. Other errors fall through
      // silently — the run pane already renders an error from the
      // SSE state if one occurs.
      if (err instanceof ApiError && err.status === 404) {
        setNotice({ tone: "error", message: "That task no longer exists. Refreshing." });
        setTasks((cur) => cur.filter((t) => t.id !== taskID));
        if (selectedTaskID === taskID) {
          selectTaskRun("", "");
        }
        const selection = await loadTasks();
        if (selection) {
          onSelectionChange?.(selection.taskID || null, selection.runID || null, "replace");
        }
      }
    }
  }

  async function handleSelectRun(runID: string) {
    if (!selectedTaskID || runID === selectedRunID) return;
    selectTaskRun(selectedTaskID, runID);
    onSelectionChange?.(selectedTaskID, runID);
    setNotice(null);
    try {
      await loadRunDetail(selectedTaskID, runID);
    } catch {
      /* ignore */
    }
  }

  async function handleResolveApproval(
    approval: TaskApprovalRecord,
    decision: "approve" | "reject",
  ) {
    if (!selectedTaskID) return;
    setBusyAction(decision);
    try {
      await resolveTaskApproval(selectedTaskID, approval.id, { decision });
      setNotice({ tone: "success", message: decision === "approve" ? "Approved." : "Denied." });
      await loadTaskDetail(selectedTaskID, approval.run_id);
    } catch (err) {
      setNotice({ tone: "error", message: err instanceof Error ? err.message : "failed" });
    } finally {
      setBusyAction("");
    }
  }

  async function handleCancelRun() {
    if (!selectedTaskID || !selectedRunID) return;
    setBusyAction("cancel");
    try {
      await cancelTaskRun(selectedTaskID, selectedRunID);
      await loadTaskDetail(selectedTaskID, selectedRunID);
    } catch {
      /* ignore */
    } finally {
      setBusyAction("");
    }
  }

  async function handleRetryRun() {
    if (!selectedTaskID || !selectedRunID) return;
    setBusyAction("retry");
    try {
      const res = await retryTaskRun(selectedTaskID, selectedRunID);
      await loadTasks(selectedTaskID, res.data.id);
      onSelectionChange?.(selectedTaskID, res.data.id);
    } catch {
      /* ignore */
    } finally {
      setBusyAction("");
    }
  }

  async function handleResumeRun() {
    if (!selectedTaskID || !selectedRunID) return;
    setBusyAction("resume");
    try {
      const res = await resumeTaskRun(selectedTaskID, selectedRunID);
      await loadTasks(selectedTaskID, res.data.id);
      onSelectionChange?.(selectedTaskID, res.data.id);
    } catch {
      /* ignore */
    } finally {
      setBusyAction("");
    }
  }

  // Raise the per-task cost ceiling and resume in one click. Only
  // exposed in the run header when the prior run failed with
  // otel_status_message=cost_ceiling_exceeded. The gateway persists
  // the new ceiling before queueing the resumed run, so the agent
  // loop sees the raised value on its first model call. Surfaces server
  // validation (e.g. "ceiling cannot be lower") as a notice rather
  // than failing silently.
  async function handleResumeRaisingCeiling(budgetMicrosUSD: number) {
    if (!selectedTaskID || !selectedRunID) return;
    setBusyAction("resume-raise");
    try {
      const res = await resumeTaskRunRaisingCeiling(selectedTaskID, selectedRunID, budgetMicrosUSD);
      setNotice({
        tone: "success",
        message: `Ceiling raised to $${(budgetMicrosUSD / 1_000_000).toFixed(3)} and resumed in Run #${res.data.number}.`,
      });
      await loadTasks(selectedTaskID, res.data.id);
      onSelectionChange?.(selectedTaskID, res.data.id);
    } catch (err) {
      setNotice({
        tone: "error",
        message: err instanceof Error ? err.message : "raise & resume failed",
      });
    } finally {
      setBusyAction("");
    }
  }

  async function handleApplyPatch(artifactID: string) {
    if (!selectedTaskID || !selectedRunID) return;
    setBusyAction("apply-patch:" + artifactID);
    try {
      await applyTaskRunPatch(selectedTaskID, selectedRunID, artifactID);
      setNotice({ tone: "success", message: "Patch applied." });
      await loadRunDetail(selectedTaskID, selectedRunID);
    } catch (err) {
      setNotice({
        tone: "error",
        message: err instanceof Error ? err.message : "patch apply failed",
      });
    } finally {
      setBusyAction("");
    }
  }

  async function handleRevertPatch(artifactID: string) {
    if (!selectedTaskID || !selectedRunID) return;
    setBusyAction("revert-patch:" + artifactID);
    try {
      await revertTaskRunPatch(selectedTaskID, selectedRunID, artifactID);
      setNotice({ tone: "success", message: "Patch reverted." });
      await loadRunDetail(selectedTaskID, selectedRunID);
    } catch (err) {
      setNotice({
        tone: "error",
        message: err instanceof Error ? err.message : "patch revert failed",
      });
    } finally {
      setBusyAction("");
    }
  }

  // Retry-from-model-call-N: re-issue the LLM call at model call N with the prior
  // conversation preserved. Server-side validation rejects out-of-range
  // model calls and non-agent_loop runs with a 4xx — we surface the message
  // in the run-level notice so the operator can correct and try again
  // rather than silently failing.
  async function handleRetryFromModelCall(modelCallIndex: number, reason: string) {
    if (!selectedTaskID || !selectedRunID) return;
    setBusyAction("retry-from-model-call");
    try {
      const res = await retryTaskRunFromModelCall(
        selectedTaskID,
        selectedRunID,
        modelCallIndex,
        reason || undefined,
      );
      const reasonSuffix = reason ? ` — ${reason}` : "";
      setNotice({
        tone: "success",
        message: `Retrying from source Run model call ${modelCallIndex}${reasonSuffix} (Run #${res.data.number}).`,
      });
      await loadTasks(selectedTaskID, res.data.id);
      onSelectionChange?.(selectedTaskID, res.data.id);
    } catch (err) {
      setNotice({
        tone: "error",
        message: err instanceof Error ? err.message : "retry-from-model-call failed",
      });
    } finally {
      setBusyAction("");
    }
  }

  async function handleDeleteTask(taskID: string) {
    setBusyAction("delete:" + taskID);
    try {
      await deleteTask(taskID);
      const nextTasks = tasks.filter((t) => t.id !== taskID);
      setTasks(nextTasks);
      if (selectedTaskID === taskID) {
        const next = nextTasks[0]?.id ?? "";
        selectTaskRun(next, "");
        const nextRunID = next ? await loadTaskDetail(next) : "";
        if (nextRunID === null) return;
        onSelectionChange?.(next || null, nextRunID || null, "replace");
      }
    } catch (err) {
      setNotice({ tone: "error", message: err instanceof Error ? err.message : "delete failed" });
    } finally {
      setBusyAction("");
    }
  }

  async function handleCreateTask(payload: CreateTaskPayload) {
    setBusyAction("create");
    try {
      const created = await createTask({
        ...payload,
        project_id: activeProjectID || undefined,
      });
      const started = await startTask(created.data.id);
      setNewTaskOpen(false);
      await loadTasks(created.data.id, started.data.id, activeProjectID);
      onSelectionChange?.(created.data.id, started.data.id);
    } catch (err) {
      setNotice({
        tone: "error",
        message: err instanceof Error ? err.message : "failed to create task",
      });
    } finally {
      setBusyAction("");
    }
  }

  return (
    <div style={{ display: "flex", height: "100%", overflow: "hidden", position: "relative" }}>
      <TaskList
        tasks={tasks}
        selectedTaskID={selectedTaskID}
        loading={loading}
        busyAction={busyAction}
        projectScope={
          <ProjectScopePanel
            noProjectDetail="Unprojected tasks only."
            emptyHint="Add a folder when you want task defaults and workspace context."
            canChangeProjectScope={() => {
              const reason = chat.actions.chatOwnershipMutationBlockReason();
              if (!reason) return true;
              setNotice({ tone: "error", message: reason });
              return false;
            }}
            beginProjectDelete={() => {
              const token = chat.actions.beginChatOwnershipMutation();
              if (token !== null) return token;
              setNotice({
                tone: "error",
                message:
                  chat.actions.chatOwnershipMutationBlockReason() ||
                  "Wait for the current chat ownership change to finish.",
              });
              return null;
            }}
            finishProjectDelete={chat.actions.finishChatOwnershipMutation}
            deleteMessage={(project) => (
              <>
                Delete <strong>{project.name}</strong>? Existing tasks stay available, but this
                project context and its defaults are removed.
              </>
            )}
            onProjectSelected={(projectID) => {
              void loadTasks("", "", projectID).then((selection) => {
                if (!selection) return;
                onSelectionChange?.(selection.taskID || null, selection.runID || null, "replace");
              });
            }}
            onProjectDeleted={(projectID, result) => {
              const browserQueueCleared = chat.actions.fenceDeletedChatProject(projectID);
              setNotice({
                tone: browserQueueCleared ? "success" : "error",
                message: browserQueueCleared
                  ? formatProjectDeleteSummary(result)
                  : `${formatProjectDeleteSummary(result)} Hecate could not clear every browser-local queued prompt for this project. Clear this site's browser data before closing or reloading.`,
              });
            }}
          />
        }
        onSelect={(id) => void handleSelectTask(id)}
        onDelete={(id) => void handleDeleteTask(id)}
        onNewTask={() => {
          setNewTaskOpen(true);
        }}
      />

      {selectedTask ? (
        <TaskDetail
          task={selectedTask}
          focusRequestNonce={
            focusIntent?.taskID === selectedTaskID &&
            (!focusIntent.runID || focusIntent.runID === selectedRunID)
              ? focusIntent.nonce
              : undefined
          }
          run={selectedRun}
          runs={runs}
          selectedRunID={selectedRunID}
          steps={steps}
          artifacts={artifacts}
          activity={activity}
          events={runEvents}
          approvals={approvals}
          streamModelCallCosts={streamModelCallCosts}
          streamState={streamState}
          busyAction={busyAction}
          notice={notice}
          onSelectRun={(id) => void handleSelectRun(id)}
          onOpenChat={onOpenChat}
          onFocusRequestHandled={onFocusRequestHandled}
          onResolveApproval={(approval, decision) => void handleResolveApproval(approval, decision)}
          onCancelRun={() => void handleCancelRun()}
          onRetryRun={() => void handleRetryRun()}
          onResumeRun={() => void handleResumeRun()}
          onRefresh={() => void loadTasks(selectedTaskID, selectedRunID)}
          onRetryFromModelCall={(modelCall, reason) =>
            void handleRetryFromModelCall(modelCall, reason)
          }
          onResumeRaisingCeiling={(budgetMicrosUSD) =>
            void handleResumeRaisingCeiling(budgetMicrosUSD)
          }
          onApplyPatch={(artifactID) => void handleApplyPatch(artifactID)}
          onRevertPatch={(artifactID) => void handleRevertPatch(artifactID)}
          loadContext={
            selectedTaskID && selectedRunID
              ? async () => (await getTaskRunContext(selectedTaskID, selectedRunID)).data
              : null
          }
          onOpenTrace={onOpenTrace}
        />
      ) : (
        <TaskStartState
          loading={loading}
          notice={notice}
          onNewTask={() => {
            setNewTaskOpen(true);
          }}
        />
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
