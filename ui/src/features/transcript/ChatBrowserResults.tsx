import { useEffect, useState } from "react";
import { ApiError, getTaskRunArtifact } from "../../lib/api";
import type { ChatActivityRecord } from "../../types/chat";
import type { TaskArtifactRecord } from "../../types/task";
import {
  BrowserEvidenceBody,
  BrowserEvidencePanel,
  isBrowserEvidenceKind,
  type BrowserEvidenceKind,
} from "../shared/BrowserEvidence";

export type BrowserRunReference = { taskID: string; runID: string };

export function isBrowserArtifact(activity: ChatActivityRecord): boolean {
  return activity.type === "artifact" && isBrowserEvidenceKind(activity.kind);
}

export function ChatBrowserResults({
  run,
  activities,
}: {
  run: BrowserRunReference;
  activities: ChatActivityRecord[];
}) {
  const seen = new Set<string>();
  const reports = activities.filter((activity) => {
    if (!isBrowserArtifact(activity)) return false;
    const identity = activity.artifact_id || activity.id;
    if (identity && seen.has(identity)) return false;
    if (identity) seen.add(identity);
    return true;
  });
  return reports.map((activity, index) => (
    <ChatBrowserResult
      key={JSON.stringify([
        run.taskID,
        run.runID,
        activity.artifact_id || activity.id || index,
        activity.kind,
        activity.step_id,
      ])}
      run={run}
      activity={activity}
      activities={activities}
    />
  ));
}

type ReportState =
  | { status: "idle" | "loading" | "error" | "missing" | "invalid" }
  | { status: "loaded"; artifact: TaskArtifactRecord };

function ChatBrowserResult({
  run,
  activity,
  activities,
}: {
  run: BrowserRunReference;
  activity: ChatActivityRecord;
  activities: ChatActivityRecord[];
}) {
  const [open, setOpen] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<ReportState>({ status: "idle" });
  const kind = activity.kind as BrowserEvidenceKind;
  const artifactID = activity.artifact_id;
  const stepID =
    activity.step_id || (state.status === "loaded" ? state.artifact.step_id : undefined);
  const step = stepID
    ? activities.find((item) => item.type === "tool_call" && item.step_id === stepID)
    : undefined;
  const loaded = state.status === "loaded";

  useEffect(() => {
    if (!open || loaded || !artifactID || !run.taskID || !run.runID) return;
    const controller = new AbortController();
    let current = true;
    setState({ status: "loading" });
    getTaskRunArtifact(run.taskID, run.runID, artifactID, controller.signal)
      .then(({ data }) => {
        if (!current) return;
        if (
          data.id !== artifactID ||
          data.task_id !== run.taskID ||
          data.run_id !== run.runID ||
          data.kind !== kind ||
          data.mime_type !== "text/plain" ||
          data.storage_kind !== "inline" ||
          (data.content_text !== undefined && typeof data.content_text !== "string") ||
          (activity.step_id && data.step_id !== activity.step_id)
        ) {
          setState({ status: "invalid" });
          return;
        }
        setState({ status: "loaded", artifact: data });
      })
      .catch((error: unknown) => {
        if (!current) return;
        setState({
          status: error instanceof ApiError && error.status === 404 ? "missing" : "error",
        });
      });
    return () => {
      current = false;
      controller.abort();
    };
  }, [open, loaded, attempt, artifactID, run.taskID, run.runID, kind, activity.step_id]);

  const missingReference = !artifactID || !run.taskID || !run.runID;
  return (
    <BrowserEvidencePanel
      kind={kind}
      title={activity.title}
      stepStatus={step?.status}
      open={open}
      onOpenChange={setOpen}
    >
      {open && (
        <>
          {missingReference ? (
            <p role="status">This browser result has no retained report reference.</p>
          ) : state.status === "loaded" ? (
            <BrowserEvidenceBody artifact={state.artifact} />
          ) : (
            <p role="status">
              {state.status === "missing"
                ? "This browser report is no longer available. It may have been removed by retention or task deletion."
                : state.status === "invalid"
                  ? "The retained report does not match this browser result. Open Task details to inspect it."
                  : state.status === "error"
                    ? "Could not load the browser report. Try loading it again. This does not rerun the browser."
                    : "Loading browser report…"}
            </p>
          )}
          {!missingReference && (
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              disabled={state.status === "loading" || state.status === "idle"}
              onClick={() => {
                setState({ status: "idle" });
                setAttempt((value) => value + 1);
              }}
            >
              Reload report
            </button>
          )}
        </>
      )}
    </BrowserEvidencePanel>
  );
}
