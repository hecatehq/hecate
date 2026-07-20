import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  applyTaskRunPatch,
  cancelTaskRun,
  createTask,
  deleteTaskSchedule,
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
  getTaskScheduleOccurrences,
  getTaskSchedules,
  resolveTaskApproval,
  revertTaskRunPatch,
  retryTaskRun,
  retryTaskRunFromModelCall,
  resumeTaskRun,
  resumeTaskRunRaisingCeiling,
  startTask,
  streamTaskRun,
  upsertTaskSchedule,
  type UpsertTaskSchedulePayload,
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
  TaskScheduleOccurrenceRecord,
  TaskScheduleRecord,
  TaskStepRecord,
} from "../../types/task";
import { TaskList, type TaskListFilter } from "./TaskList";
import { TaskDetail } from "./TaskDetail";
import { NewTaskSlideOver, type CreateTaskPayload } from "./NewTaskSlideOver";
import type { ScheduleHistoryState, ScheduleLoadState } from "./TaskScheduleControl";
import { ProjectScopePanel } from "../projects/ProjectScopePanel";
import { formatProjectDeleteSummary } from "../projects/projectDisplay";
import { EntityDetailPane, MasterDetailWorkspace } from "../shared/EntityWorkspace";
import { ConfirmModal } from "../shared/ui";

type StreamState = "idle" | "connecting" | "live" | "closed" | "error";
type TaskFocusRequest = { taskID: string; runID?: string; nonce: number };
type TaskSelection = { taskID: string; runID: string };

export function taskMatchesFilter(
  task: TaskRecord,
  filter: TaskListFilter,
  schedulesByTaskID: ReadonlyMap<string, TaskScheduleRecord>,
): boolean {
  switch (filter) {
    case "attention":
      return (
        (task.pending_approval_count ?? 0) > 0 ||
        task.status === "awaiting_approval" ||
        task.status === "failed"
      );
    case "scheduled":
      return schedulesByTaskID.has(task.id);
    case "chat":
      return task.origin_kind === "chat";
    default:
      return true;
  }
}

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
  loadError,
  notice,
  onNewTask,
  onRetry,
}: {
  loading: boolean;
  loadError: string;
  notice: { tone: "success" | "error"; message: string } | null;
  onNewTask: () => void;
  onRetry: () => void;
}) {
  const unavailable = Boolean(loadError);
  return (
    <EntityDetailPane
      style={{
        alignItems: "center",
        justifyContent: "center",
        padding: 24,
      }}
    >
      <div style={{ maxWidth: 460, textAlign: "center" }}>
        <div style={{ fontSize: 15, color: "var(--t0)", fontWeight: 600 }}>
          {unavailable ? "Tasks unavailable" : loading ? "Loading tasks…" : "Start a task"}
        </div>
        <div style={{ fontSize: 12, color: "var(--t3)", lineHeight: 1.6, marginTop: 8 }}>
          {unavailable
            ? "Hecate could not confirm the current Task list. Retry before creating work so existing Tasks are not mistaken for an empty workspace."
            : loading
              ? "Checking the task runtime for recent work."
              : "Tasks are durable units of work. Each start, continuation, retry, or resume creates a Run. Create standalone shell, Git, file, or agent-loop work here, or inspect work started from Chats and Projects."}
        </div>
        {unavailable && (
          <div
            className="page-banner page-banner--error"
            role="alert"
            aria-live="assertive"
            style={{ marginTop: 16 }}
          >
            {loadError}
          </div>
        )}
        {!unavailable && !loading && notice?.tone === "error" && (
          <div
            className="page-banner page-banner--error"
            role="alert"
            aria-live="assertive"
            style={{ marginTop: 16 }}
          >
            {notice.message}
          </div>
        )}
        {unavailable ? (
          <button
            className="btn btn-primary"
            type="button"
            onClick={onRetry}
            style={{ marginTop: 18, justifyContent: "center" }}
          >
            Retry task load
          </button>
        ) : !loading ? (
          <button
            className="btn btn-primary"
            type="button"
            onClick={onNewTask}
            style={{ marginTop: 18, justifyContent: "center" }}
          >
            New task
          </button>
        ) : null}
      </div>
    </EntityDetailPane>
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
  const [taskListLoadError, setTaskListLoadError] = useState("");
  const [taskDetailLoadError, setTaskDetailLoadError] = useState("");
  const [tasks, setTasks] = useState<TaskRecord[]>([]);
  const [taskFilter, setTaskFilter] = useState<TaskListFilter>("all");
  const [schedules, setSchedules] = useState<TaskScheduleRecord[]>([]);
  const [scheduleLoadState, setScheduleLoadState] = useState<ScheduleLoadState>("loading");
  const [scheduleLoadError, setScheduleLoadError] = useState("");
  const [scheduleOccurrences, setScheduleOccurrences] = useState<TaskScheduleOccurrenceRecord[]>(
    [],
  );
  const [scheduleHistoryState, setScheduleHistoryState] = useState<ScheduleHistoryState>("idle");
  const [scheduleHistoryError, setScheduleHistoryError] = useState("");
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
  const [deletingTaskID, setDeletingTaskID] = useState("");
  const [notice, setNotice] = useState<{ tone: "success" | "error"; message: string } | null>(null);
  const [newTaskOpen, setNewTaskOpen] = useState(false);
  const [pendingDeleteTaskID, setPendingDeleteTaskID] = useState("");
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
  const scheduleMutationGenerationRef = useRef(0);
  const scheduleHistoryGenerationRef = useRef(0);
  const selectedTaskRunRef = useRef<TaskSelection>({ taskID: "", runID: "" });
  const taskIndexHeadingRef = useRef<HTMLHeadingElement>(null);

  const selectedTask = useMemo(
    () => tasks.find((t) => t.id === selectedTaskID) ?? null,
    [tasks, selectedTaskID],
  );
  const selectedRun = useMemo(
    () => runs.find((r) => r.id === selectedRunID) ?? null,
    [runs, selectedRunID],
  );
  const schedulesByTaskID = useMemo(
    () => new Map(schedules.map((schedule) => [schedule.task_id, schedule])),
    [schedules],
  );
  const selectedSchedule = selectedTaskID ? (schedulesByTaskID.get(selectedTaskID) ?? null) : null;
  const pendingDeleteTask = pendingDeleteTaskID
    ? (tasks.find((task) => task.id === pendingDeleteTaskID) ?? null)
    : null;
  const effectiveTaskFilter =
    scheduleLoadState !== "loaded" && taskFilter === "scheduled" ? "all" : taskFilter;
  const filteredTasks = useMemo(
    () => tasks.filter((task) => taskMatchesFilter(task, effectiveTaskFilter, schedulesByTaskID)),
    [effectiveTaskFilter, tasks, schedulesByTaskID],
  );

  useEffect(() => {
    if (scheduleLoadState === "loaded") return;
    setTaskFilter((current) => (current === "scheduled" ? "all" : current));
  }, [scheduleLoadState]);
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
      if (previous.taskID !== taskID) {
        setRuns([]);
        setApprovals([]);
      }
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
      const nextRunID =
        (preferredRunID && nextRuns.some((r) => r.id === preferredRunID) ? preferredRunID : "") ||
        nextRuns[0]?.id ||
        "";
      selectTaskRun(taskID, nextRunID);
      setRuns(nextRuns);
      setApprovals(approvalsRes.data ?? []);
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
      setScheduleLoadState("loading");
      setScheduleLoadError("");
      setTaskDetailLoadError("");
      let taskListLoaded = false;
      try {
        const res = await getTasks(30, projectID);
        if (taskLoadGenerationRef.current !== loadGeneration) return null;
        taskListLoaded = true;
        setTaskListLoadError("");
        const listedTasks = res.data ?? [];
        const nextTasks =
          addressedTask && !listedTasks.some((task) => task.id === addressedTask.id)
            ? [addressedTask, ...listedTasks]
            : listedTasks;
        setTasks(nextTasks);
        const visibleTaskIDs = nextTasks.map((task) => task.id);
        const visibleTaskIDSet = new Set(visibleTaskIDs);
        setSchedules((current) =>
          current.filter((schedule) => visibleTaskIDSet.has(schedule.task_id)),
        );
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
        const scheduleRequest =
          visibleTaskIDs.length > 0
            ? getTaskSchedules(visibleTaskIDs)
            : Promise.resolve({ object: "task_schedules", data: [] as TaskScheduleRecord[] });
        if (visibleTaskIDs.length === 0) setScheduleLoadState("loaded");
        const detailRequest = nextTaskID
          ? loadTaskDetail(nextTaskID, preferredRunID, loadGeneration)
          : Promise.resolve("");
        if (!nextTaskID) {
          setRuns([]);
          setApprovals([]);
          selectTaskRun("", "");
        }
        const [scheduleResult, detailResult] = await Promise.allSettled([
          scheduleRequest,
          detailRequest,
        ]);
        if (taskLoadGenerationRef.current !== loadGeneration) return null;
        if (scheduleResult.status === "fulfilled") {
          setSchedules(scheduleResult.value.data ?? []);
          setScheduleLoadState("loaded");
          setScheduleLoadError("");
        } else {
          setScheduleLoadState("error");
          setScheduleLoadError(
            scheduleResult.reason instanceof Error
              ? scheduleResult.reason.message
              : "unknown error",
          );
        }
        if (detailResult.status === "rejected") {
          setTaskDetailLoadError(
            detailResult.reason instanceof Error ? detailResult.reason.message : "unknown error",
          );
          return { taskID: nextTaskID, runID: "" };
        }
        if (detailResult.value === null) return null;
        setTaskDetailLoadError("");
        return { taskID: nextTaskID, runID: detailResult.value };
      } catch (error) {
        if (taskLoadGenerationRef.current !== loadGeneration) return null;
        const message = error instanceof Error ? error.message : "unknown error";
        if (!taskListLoaded) {
          setTaskListLoadError(message);
          setScheduleLoadState("error");
          setScheduleLoadError("Tasks must load before their Schedules can be refreshed");
        } else {
          setTaskDetailLoadError(message);
        }
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
    const historyGeneration = ++scheduleHistoryGenerationRef.current;
    const taskID = selectedTaskID;
    if (!selectedTaskID || !selectedSchedule) {
      setScheduleOccurrences([]);
      setScheduleHistoryState("idle");
      setScheduleHistoryError("");
      return;
    }
    setScheduleOccurrences([]);
    setScheduleHistoryState("loading");
    setScheduleHistoryError("");
    void getTaskScheduleOccurrences(selectedTaskID, 20)
      .then((response) => {
        if (
          scheduleHistoryGenerationRef.current === historyGeneration &&
          selectedTaskRunRef.current.taskID === taskID
        ) {
          setScheduleOccurrences(response.data ?? []);
          setScheduleHistoryState("loaded");
          setScheduleHistoryError("");
        }
      })
      .catch((error) => {
        if (
          scheduleHistoryGenerationRef.current === historyGeneration &&
          selectedTaskRunRef.current.taskID === taskID
        ) {
          setScheduleHistoryState("error");
          setScheduleHistoryError(error instanceof Error ? error.message : "unknown error");
        }
      });
  }, [selectedSchedule, selectedTaskID]);

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
    setTaskDetailLoadError("");
    try {
      const runID = await loadTaskDetail(taskID);
      if (runID === null) return;
      setTaskDetailLoadError("");
      onSelectionChange?.(taskID, runID || null);
    } catch (err) {
      // 404 here means the cached task ID is stale (gateway restarted
      // with memory backend, tenant change, etc.). Drop the dead row
      // from the visible list so subsequent clicks don't repeat the
      // 404, and surface a concrete notice. Other errors fall through
      // in the selected Task pane without turning it into a Task-list
      // failure.
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
      } else if (selectedTaskRunRef.current.taskID === taskID) {
        setTaskDetailLoadError(err instanceof Error ? err.message : "unknown error");
      }
    }
  }

  async function handleSelectRun(runID: string) {
    if (!selectedTaskID || runID === selectedRunID) return;
    selectTaskRun(selectedTaskID, runID);
    onSelectionChange?.(selectedTaskID, runID);
    setNotice(null);
    setTaskDetailLoadError("");
    try {
      await loadRunDetail(selectedTaskID, runID);
      setTaskDetailLoadError("");
    } catch (error) {
      if (
        selectedTaskRunRef.current.taskID === selectedTaskID &&
        selectedTaskRunRef.current.runID === runID
      ) {
        setTaskDetailLoadError(error instanceof Error ? error.message : "unknown error");
      }
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

  async function handleDeleteTask(taskID: string): Promise<boolean> {
    setDeletingTaskID(taskID);
    setBusyAction("delete:" + taskID);
    try {
      await deleteTask(taskID);
      const nextTasks = tasks.filter((t) => t.id !== taskID);
      setTasks(nextTasks);
      setSchedules((current) => current.filter((schedule) => schedule.task_id !== taskID));
      if (selectedTaskID === taskID) {
        const next = nextTasks[0]?.id ?? "";
        selectTaskRun(next, "");
        let nextRunID = "";
        if (next) {
          try {
            nextRunID = (await loadTaskDetail(next)) ?? "";
            setTaskDetailLoadError("");
          } catch (error) {
            setRuns([]);
            setApprovals([]);
            setTaskDetailLoadError(
              error instanceof Error ? error.message : "The next Task could not be loaded.",
            );
          }
        } else {
          setRuns([]);
          setApprovals([]);
          setTaskDetailLoadError("");
        }
        onSelectionChange?.(next || null, nextRunID || null, "replace");
      }
      return true;
    } catch (err) {
      setNotice({ tone: "error", message: err instanceof Error ? err.message : "delete failed" });
      return false;
    } finally {
      setBusyAction((current) => (current === `delete:${taskID}` ? "" : current));
      setDeletingTaskID((current) => (current === taskID ? "" : current));
    }
  }

  async function handleStartRun() {
    const taskID = selectedTaskRunRef.current.taskID;
    if (!taskID) return;
    setBusyAction("start");
    try {
      const started = await startTask(taskID);
      const mergeStartedTaskRow = () => {
        setTasks((current) =>
          current.map((task) =>
            task.id === taskID && !task.latest_run_id
              ? {
                  ...task,
                  status: started.data.status,
                  latest_run_id: started.data.id,
                  latest_model: started.data.model,
                  latest_provider: started.data.provider,
                  latest_run_step_count: started.data.step_count ?? 0,
                  latest_run_artifact_count: started.data.artifact_count ?? 0,
                  last_error: started.data.last_error,
                  started_at: started.data.started_at,
                  finished_at: started.data.finished_at,
                }
              : task,
          ),
        );
      };
      mergeStartedTaskRow();
      if (selectedTaskRunRef.current.taskID !== taskID) return;
      const ensureStartedRunIsVisible = () => {
        selectTaskRun(taskID, started.data.id);
        mergeStartedTaskRow();
        setRuns((current) =>
          current.some((run) => run.id === started.data.id) ? current : [started.data, ...current],
        );
      };
      setApprovals([]);
      ensureStartedRunIsVisible();
      onSelectionChange?.(taskID, started.data.id);
      const selection = await loadTasks(taskID, started.data.id, activeProjectID);
      if (
        !selection ||
        selectedTaskRunRef.current.taskID !== taskID ||
        selection.runID !== started.data.id
      ) {
        mergeStartedTaskRow();
        if (selectedTaskRunRef.current.taskID === taskID) {
          ensureStartedRunIsVisible();
          setNotice({
            tone: "error",
            message:
              `First Run started, but Tasks could not refresh. Run #${started.data.number} ` +
              "is shown from the successful start response; use Refresh task to reconcile.",
          });
        }
        return;
      }
      ensureStartedRunIsVisible();
      setNotice({ tone: "success", message: "First Run started." });
    } catch (error) {
      if (selectedTaskRunRef.current.taskID === taskID) {
        setNotice({
          tone: "error",
          message: error instanceof Error ? error.message : "Could not start the first Run.",
        });
      }
    } finally {
      setBusyAction("");
    }
  }

  async function handleSaveSchedule(payload: UpsertTaskSchedulePayload): Promise<boolean> {
    const taskID = selectedTaskRunRef.current.taskID;
    if (!taskID) throw new Error("Select a task before saving a schedule.");
    const mutationGeneration = ++scheduleMutationGenerationRef.current;
    setBusyAction("schedule-save");
    try {
      const response = await upsertTaskSchedule(taskID, payload);
      if (scheduleMutationGenerationRef.current !== mutationGeneration) return false;
      setSchedules((current) => [
        response.data,
        ...current.filter((schedule) => schedule.task_id !== taskID),
      ]);
      const selectionIsCurrent = selectedTaskRunRef.current.taskID === taskID;
      if (selectionIsCurrent) {
        setNotice({
          tone: "success",
          message: payload.enabled ? "Schedule saved." : "Schedule paused.",
        });
      }
      return selectionIsCurrent;
    } catch (error) {
      if (
        scheduleMutationGenerationRef.current === mutationGeneration &&
        selectedTaskRunRef.current.taskID === taskID
      ) {
        setNotice({
          tone: "error",
          message: error instanceof Error ? error.message : "Could not save the schedule.",
        });
      }
      throw error;
    } finally {
      if (scheduleMutationGenerationRef.current === mutationGeneration) setBusyAction("");
    }
  }

  async function handleDeleteSchedule(): Promise<boolean> {
    const taskID = selectedTaskRunRef.current.taskID;
    if (!taskID) throw new Error("Select a task before removing a schedule.");
    const mutationGeneration = ++scheduleMutationGenerationRef.current;
    setBusyAction("schedule-delete");
    try {
      await deleteTaskSchedule(taskID);
      if (scheduleMutationGenerationRef.current !== mutationGeneration) return false;
      setSchedules((current) => current.filter((schedule) => schedule.task_id !== taskID));
      const selectionIsCurrent = selectedTaskRunRef.current.taskID === taskID;
      if (selectionIsCurrent) {
        setScheduleOccurrences([]);
        setScheduleHistoryState("idle");
        setScheduleHistoryError("");
        setNotice({ tone: "success", message: "Schedule removed." });
      }
      return selectionIsCurrent;
    } catch (error) {
      if (
        scheduleMutationGenerationRef.current === mutationGeneration &&
        selectedTaskRunRef.current.taskID === taskID
      ) {
        setNotice({
          tone: "error",
          message: error instanceof Error ? error.message : "Could not remove the schedule.",
        });
      }
      throw error;
    } finally {
      if (scheduleMutationGenerationRef.current === mutationGeneration) setBusyAction("");
    }
  }

  async function handleCreateTask(payload: CreateTaskPayload): Promise<boolean> {
    setBusyAction("create");
    setNotice(null);
    const { start_immediately: startImmediately, ...taskPayload } = payload;
    let createdTask: TaskRecord;
    try {
      const created = await createTask({
        ...taskPayload,
        project_id: activeProjectID || undefined,
      });
      createdTask = created.data;
    } catch (error) {
      setNotice({
        tone: "error",
        message: `Task could not be created: ${
          error instanceof Error ? error.message : "unknown error"
        }`,
      });
      setBusyAction("");
      return false;
    }

    const showCreatedTaskLocally = (startedRun: TaskRunRecord | null) => {
      const fallbackTask: TaskRecord = startedRun
        ? {
            ...createdTask,
            status: startedRun.status,
            latest_run_id: startedRun.id,
            latest_model: startedRun.model,
            latest_provider: startedRun.provider,
            latest_run_step_count: startedRun.step_count ?? 0,
            latest_run_artifact_count: startedRun.artifact_count ?? 0,
            last_error: startedRun.last_error,
            started_at: startedRun.started_at,
            finished_at: startedRun.finished_at,
          }
        : {
            ...createdTask,
            latest_run_id: undefined,
            latest_model: undefined,
            latest_provider: undefined,
            latest_run_step_count: undefined,
            latest_run_artifact_count: undefined,
          };
      selectTaskRun(createdTask.id, startedRun?.id ?? "");
      setTasks((current) => [
        fallbackTask,
        ...current.filter((task) => task.id !== createdTask.id),
      ]);
      setRuns(startedRun ? [startedRun] : []);
      setApprovals([]);
    };

    try {
      let started: Awaited<ReturnType<typeof startTask>> | null = null;
      if (startImmediately) {
        try {
          started = await startTask(createdTask.id);
        } catch (error) {
          setNewTaskOpen(false);
          const selection = await loadTasks(createdTask.id, "", activeProjectID, createdTask);
          if (!selection) showCreatedTaskLocally(null);
          onSelectionChange?.(createdTask.id, null);
          setNotice({
            tone: "error",
            message: `Task created, but its first Run could not start: ${
              error instanceof Error ? error.message : "unknown error"
            }. Use Start first Run to retry.`,
          });
          return true;
        }
      }
      setNewTaskOpen(false);
      const runID = started?.data.id ?? "";
      const selection = await loadTasks(createdTask.id, runID, activeProjectID, createdTask);
      if (!selection) showCreatedTaskLocally(started?.data ?? null);
      onSelectionChange?.(createdTask.id, runID || null);
      if (!startImmediately) {
        setNotice({
          tone: "success",
          message: "Task created without a Run. Add a schedule or start it when you are ready.",
        });
      }
      return true;
    } finally {
      setBusyAction("");
    }
  }

  return (
    <MasterDetailWorkspace>
      <TaskList
        tasks={filteredTasks}
        selectedTaskID={selectedTaskID}
        loading={loading}
        busyAction={busyAction}
        deletingTaskID={deletingTaskID}
        filter={effectiveTaskFilter}
        onFilterChange={setTaskFilter}
        schedulesByTaskID={schedulesByTaskID}
        scheduleLoadState={scheduleLoadState}
        scheduleLoadError={scheduleLoadError}
        indexHeadingRef={taskIndexHeadingRef}
        newTaskDisabled={Boolean(taskListLoadError)}
        newTaskDisabledReason="Retry the Task list before creating a Task."
        emptyMessage={
          taskListLoadError
            ? "Tasks could not be loaded."
            : tasks.length === 0
              ? undefined
              : "No tasks match this filter."
        }
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
            projectScopeChangeBlockReason={chat.actions.chatOwnershipMutationBlockReason}
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
        onDelete={setPendingDeleteTaskID}
        onNewTask={() => {
          if (taskListLoadError) return;
          setNotice(null);
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
          onStartRun={() => void handleStartRun()}
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
          schedule={selectedSchedule}
          scheduleOccurrences={scheduleOccurrences}
          scheduleLoadState={scheduleLoadState}
          scheduleLoadError={scheduleLoadError}
          scheduleHistoryState={scheduleHistoryState}
          scheduleHistoryError={scheduleHistoryError}
          taskListLoadError={taskListLoadError}
          taskDetailLoadError={taskDetailLoadError}
          onSaveSchedule={handleSaveSchedule}
          onDeleteSchedule={handleDeleteSchedule}
        />
      ) : (
        <TaskStartState
          loading={loading}
          loadError={taskListLoadError}
          notice={notice}
          onNewTask={() => {
            if (taskListLoadError) return;
            setNotice(null);
            setNewTaskOpen(true);
          }}
          onRetry={() => void loadTasks("", "", activeProjectID)}
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
        onCreate={handleCreateTask}
      />

      {pendingDeleteTask && (
        <ConfirmModal
          danger
          title="Delete task"
          confirmLabel="Delete task"
          message={
            <>
              Delete{" "}
              <strong>
                {pendingDeleteTask.title || pendingDeleteTask.prompt || "Untitled task"}
              </strong>
              ? This permanently removes the Task, all of its Runs, its Schedule, and occurrence
              history. This cannot be undone.
            </>
          }
          pending={deletingTaskID === pendingDeleteTask.id}
          returnFocusRef={taskIndexHeadingRef}
          onClose={() => {
            if (deletingTaskID !== pendingDeleteTask.id) setPendingDeleteTaskID("");
          }}
          onConfirm={async () => {
            if (await handleDeleteTask(pendingDeleteTask.id)) setPendingDeleteTaskID("");
          }}
        />
      )}

      <style>{`
        @keyframes pulse { 0%,100%{opacity:1} 50%{opacity:0.4} }
        @keyframes blink  { 0%,100%{opacity:1} 50%{opacity:0} }
      `}</style>
    </MasterDetailWorkspace>
  );
}
