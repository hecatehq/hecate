import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { UpsertTaskSchedulePayload } from "../../lib/api";
import type { TaskScheduleOccurrenceRecord, TaskScheduleRecord } from "../../types/task";
import { Modal } from "../shared/ui";

export type ScheduleOperation = "save" | "delete" | null;
export type ScheduleLoadState = "loading" | "loaded" | "error";
export type ScheduleHistoryState = "idle" | "loading" | "loaded" | "error";

type Props = {
  schedule: TaskScheduleRecord | null;
  occurrences: TaskScheduleOccurrenceRecord[];
  availability?: ScheduleLoadState;
  availabilityError?: string;
  historyState?: ScheduleHistoryState;
  historyError?: string;
  operation: ScheduleOperation;
  disabled?: boolean;
  onSave: (payload: UpsertTaskSchedulePayload) => Promise<boolean | void>;
  onDelete: () => Promise<boolean | void>;
};

type ScheduleField = "runAt" | "cron" | "timezone";
type ScheduleFieldErrors = Partial<Record<ScheduleField, string>>;

export function browserTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

function pad(value: number): string {
  return String(value).padStart(2, "0");
}

export function toLocalDateTimeInput(value?: string): string {
  const date = value ? new Date(value) : new Date(Date.now() + 60 * 60 * 1000);
  if (Number.isNaN(date.getTime())) return "";
  if (!value) {
    date.setMinutes(0, 0, 0);
    if (date.getTime() <= Date.now()) date.setHours(date.getHours() + 1);
  }
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export function formatScheduleDateTime(value: string, timezone: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const normalizedTimezone = timezone.trim() || browserTimezone();
  try {
    const formatted = new Intl.DateTimeFormat(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      timeZone: normalizedTimezone,
    }).format(date);
    return `${formatted} · ${normalizedTimezone}`;
  } catch {
    return `${date.toLocaleString()} · ${normalizedTimezone}`;
  }
}

export function scheduleIsCompletedOneOff(schedule: TaskScheduleRecord): boolean {
  if (schedule.kind !== "once" || schedule.enabled || schedule.next_run_at || !schedule.run_at) {
    return false;
  }
  // Claiming a one-off atomically clears next_run_at, disables the schedule,
  // and stamps updated_at at or after its nominal fire time. A manually paused
  // future one-off keeps an earlier updated_at, so it remains distinguishable.
  const scheduledFor = Date.parse(schedule.run_at);
  const lastUpdated = Date.parse(schedule.updated_at);
  return (
    Number.isFinite(scheduledFor) && Number.isFinite(lastUpdated) && lastUpdated >= scheduledFor
  );
}

export function scheduleSummary(schedule: TaskScheduleRecord): string {
  if (scheduleIsCompletedOneOff(schedule)) {
    return schedule.run_at
      ? `Completed · ${formatScheduleDateTime(schedule.run_at, schedule.timezone)}`
      : "Completed";
  }
  if (!schedule.enabled) return "Schedule paused";
  if (schedule.kind === "once") {
    return schedule.run_at
      ? `Once · ${formatScheduleDateTime(schedule.run_at, schedule.timezone)}`
      : "Once";
  }
  return `${schedule.cron_expression || "Cron"} · ${schedule.timezone}`;
}

export function scheduleVisibleStatus(schedule: TaskScheduleRecord): string {
  if (scheduleIsCompletedOneOff(schedule)) return "Completed schedule";
  if (!schedule.enabled) return "Schedule paused";
  if (schedule.next_run_at) {
    return `Next · ${formatScheduleDateTime(schedule.next_run_at, schedule.timezone)}`;
  }
  return "Scheduled";
}

function availableTimezones(): string[] {
  const intl = Intl as typeof Intl & {
    supportedValuesOf?: (key: "timeZone") => string[];
  };
  const zones = intl.supportedValuesOf?.("timeZone") ?? [];
  return zones.includes("UTC") ? zones : ["UTC", ...zones];
}

function occurrenceLabel(occurrence: TaskScheduleOccurrenceRecord): string {
  switch (occurrence.status) {
    case "started":
      return occurrence.run_id ? `Started Run ${occurrence.run_id}` : "Started a Run";
    case "skipped":
      return "Skipped because another Run was active";
    case "failed":
      return occurrence.error || "Could not start the Run";
    default:
      return "Claimed by the scheduler";
  }
}

function errorField(message: string, kind: "once" | "cron"): ScheduleField | null {
  const normalized = message.toLowerCase();
  if (normalized.includes("timezone") || normalized.includes("time zone")) return "timezone";
  if (normalized.includes("cron")) return "cron";
  if (
    normalized.includes("run_at") ||
    normalized.includes("run at") ||
    normalized.includes("future date") ||
    normalized.includes("future time")
  ) {
    return "runAt";
  }
  return kind === "cron" && normalized.includes("expression") ? "cron" : null;
}

function describedBy(helpID: string, errorID: string, hasError: boolean): string {
  return hasError ? `${helpID} ${errorID}` : helpID;
}

export function TaskScheduleControl({
  schedule,
  occurrences,
  availability = "loaded",
  availabilityError = "",
  historyState = schedule ? "loaded" : "idle",
  historyError = "",
  operation,
  disabled = false,
  onSave,
  onDelete,
}: Props) {
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<"once" | "cron">("once");
  const [enabled, setEnabled] = useState(true);
  const [timezone, setTimezone] = useState(browserTimezone());
  const [runAt, setRunAt] = useState(toLocalDateTimeInput());
  const [cronExpression, setCronExpression] = useState("0 9 * * 1-5");
  const [formError, setFormError] = useState("");
  const [fieldErrors, setFieldErrors] = useState<ScheduleFieldErrors>({});
  const [confirmDelete, setConfirmDelete] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const confirmDeleteRef = useRef<HTMLButtonElement>(null);
  const deleteWarningRef = useRef<HTMLDivElement>(null);
  const wasDeletePendingRef = useRef(operation === "delete");
  const runAtRef = useRef<HTMLInputElement>(null);
  const cronRef = useRef<HTMLInputElement>(null);
  const timezoneRef = useRef<HTMLInputElement>(null);
  const timezoneOptions = useMemo(availableTimezones, []);
  const busy = operation !== null;
  const scheduleUnavailable = availability !== "loaded";
  const formDisabled = disabled || busy || scheduleUnavailable;
  const initialFocusRef = kind === "once" ? runAtRef : cronRef;

  useEffect(() => {
    if (!open) return;
    setKind(schedule?.kind ?? "once");
    setEnabled(schedule?.enabled ?? true);
    setTimezone(schedule?.kind === "cron" ? schedule.timezone : browserTimezone());
    setRunAt(toLocalDateTimeInput(schedule?.run_at));
    setCronExpression(schedule?.cron_expression || "0 9 * * 1-5");
    setFormError("");
    setFieldErrors({});
    setConfirmDelete(false);
  }, [open, schedule]);

  useLayoutEffect(() => {
    const deletePending = operation === "delete";
    const wasDeletePending = wasDeletePendingRef.current;
    wasDeletePendingRef.current = deletePending;
    if (!wasDeletePending && deletePending) {
      deleteWarningRef.current?.focus({ preventScroll: true });
      return;
    }
    if (!wasDeletePending || deletePending) return;

    const confirmButton = confirmDeleteRef.current;
    if (confirmButton && !confirmButton.disabled) {
      confirmButton.focus({ preventScroll: true });
    }
  }, [operation]);

  function clearFieldError(field: ScheduleField) {
    setFieldErrors((current) => {
      if (!current[field]) return current;
      const next = { ...current };
      delete next[field];
      return next;
    });
  }

  function showFieldError(field: ScheduleField, message: string) {
    setFormError("");
    setFieldErrors({ [field]: message });
    const fieldRef = field === "runAt" ? runAtRef : field === "cron" ? cronRef : timezoneRef;
    fieldRef.current?.focus();
  }

  async function submit() {
    if (formDisabled) return;
    const normalizedTimezone = timezone.trim();
    if (!normalizedTimezone) {
      showFieldError("timezone", "Choose an IANA timezone.");
      return;
    }
    const payload: UpsertTaskSchedulePayload = {
      kind,
      timezone: normalizedTimezone,
      enabled,
    };
    if (kind === "once") {
      const parsedRunAt = new Date(runAt);
      if (!runAt || Number.isNaN(parsedRunAt.getTime()) || parsedRunAt.getTime() <= Date.now()) {
        showFieldError("runAt", "Choose a future date and time.");
        return;
      }
      payload.run_at = parsedRunAt.toISOString();
    } else {
      const normalizedCron = cronExpression.trim().replace(/\s+/g, " ");
      if (normalizedCron.split(" ").length !== 5) {
        showFieldError("cron", "Enter a five-field cron expression, such as 0 9 * * 1-5.");
        return;
      }
      payload.cron_expression = normalizedCron;
    }
    setFormError("");
    setFieldErrors({});
    try {
      const shouldClose = await onSave(payload);
      if (shouldClose !== false) setOpen(false);
    } catch (error) {
      const message = error instanceof Error ? error.message : "Could not save the schedule.";
      const field = errorField(message, kind);
      if (field) showFieldError(field, message);
      else setFormError(message);
    }
  }

  async function remove() {
    if (formDisabled) return;
    if (!confirmDelete) {
      setConfirmDelete(true);
      return;
    }
    setFormError("");
    setFieldErrors({});
    try {
      const shouldClose = await onDelete();
      if (shouldClose !== false) setOpen(false);
    } catch (error) {
      setFormError(error instanceof Error ? error.message : "Could not remove the schedule.");
    }
  }

  return (
    <>
      <button
        ref={triggerRef}
        className="btn btn-ghost btn-sm"
        type="button"
        disabled={disabled || busy || scheduleUnavailable}
        onClick={() => setOpen(true)}
        title={
          availability === "loading"
            ? "Loading this Task's Schedule"
            : availability === "error"
              ? availabilityError || "This Task's Schedule is unavailable"
              : schedule
                ? `Edit schedule: ${scheduleSummary(schedule)}`
                : "Schedule future Runs"
        }
        style={{ maxWidth: "min(100%, 360px)", overflow: "hidden", textOverflow: "ellipsis" }}
      >
        {availability === "loading"
          ? "Loading schedule…"
          : availability === "error"
            ? "Schedule unavailable"
            : schedule
              ? scheduleVisibleStatus(schedule)
              : "Schedule"}
      </button>
      {open && (
        <Modal
          title={schedule ? "Edit task schedule" : "Schedule this task"}
          width={560}
          initialFocusRef={initialFocusRef}
          returnFocusRef={triggerRef}
          dismissible={!busy}
          onClose={() => setOpen(false)}
          footer={
            <div style={{ display: "flex", flexDirection: "column", gap: 8, width: "100%" }}>
              {confirmDelete && (
                <div
                  ref={deleteWarningRef}
                  id="task-schedule-delete-warning"
                  role="alert"
                  tabIndex={-1}
                  style={{ color: "var(--red)", fontSize: 12, lineHeight: 1.45 }}
                >
                  Permanently deletes this Schedule and its occurrence history. This cannot be
                  undone.
                </div>
              )}
              <div style={{ display: "flex", alignItems: "center", gap: 8, width: "100%" }}>
                {schedule && (
                  <button
                    ref={confirmDeleteRef}
                    className={confirmDelete ? "btn btn-danger" : "btn btn-ghost"}
                    type="button"
                    disabled={formDisabled}
                    onClick={() => void remove()}
                    aria-describedby={confirmDelete ? "task-schedule-delete-warning" : undefined}
                  >
                    {operation === "delete"
                      ? "Removing schedule…"
                      : confirmDelete
                        ? "Confirm remove"
                        : "Remove schedule"}
                  </button>
                )}
                <button
                  className="btn btn-primary"
                  type="submit"
                  form="task-schedule-form"
                  disabled={formDisabled}
                  style={{ marginLeft: "auto" }}
                  aria-describedby="task-schedule-runtime-note"
                >
                  {operation === "save" ? "Saving schedule…" : "Save schedule"}
                </button>
              </div>
            </div>
          }
        >
          <form
            id="task-schedule-form"
            onSubmit={(event) => {
              event.preventDefault();
              if (!formDisabled) void submit();
            }}
            style={{ display: "flex", flexDirection: "column", gap: 16 }}
            aria-busy={busy || availability === "loading"}
          >
            <button
              type="submit"
              tabIndex={-1}
              aria-hidden="true"
              style={{
                position: "absolute",
                width: 1,
                height: 1,
                padding: 0,
                border: 0,
                clipPath: "inset(50%)",
                overflow: "hidden",
              }}
            />
            {availability !== "loaded" && (
              <div
                className={availability === "error" ? "page-banner page-banner--error" : undefined}
                role={availability === "error" ? "alert" : "status"}
                style={{ color: availability === "error" ? undefined : "var(--t3)", fontSize: 11 }}
              >
                {availability === "loading"
                  ? "Refreshing Schedule data… Editing is temporarily disabled."
                  : `Schedule data is unavailable: ${availabilityError || "unknown error"}. Close this dialog and retry the Task refresh.`}
              </div>
            )}
            {schedule && (
              <dl
                style={{
                  display: "grid",
                  gridTemplateColumns: "max-content minmax(0, 1fr)",
                  gap: "6px 12px",
                  margin: 0,
                  padding: 10,
                  border: "1px solid var(--border)",
                  borderRadius: "var(--radius-sm)",
                  background: "var(--bg2)",
                  fontSize: 11,
                }}
              >
                <dt style={{ color: "var(--t3)" }}>State</dt>
                <dd style={{ margin: 0, color: "var(--t1)" }}>{scheduleSummary(schedule)}</dd>
                {schedule.next_run_at && (
                  <>
                    <dt style={{ color: "var(--t3)" }}>Next Run</dt>
                    <dd style={{ margin: 0, color: "var(--t0)" }}>
                      <time dateTime={schedule.next_run_at}>
                        {formatScheduleDateTime(schedule.next_run_at, schedule.timezone)}
                      </time>
                    </dd>
                  </>
                )}
              </dl>
            )}
            <fieldset style={{ border: 0, padding: 0, margin: 0 }}>
              <legend className="field-label">Frequency</legend>
              <div style={{ display: "flex", gap: 14, marginTop: 7 }}>
                <label style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
                  <input
                    type="radio"
                    name="schedule-kind"
                    value="once"
                    checked={kind === "once"}
                    disabled={formDisabled}
                    onChange={() => {
                      setKind("once");
                      setFieldErrors({});
                    }}
                  />
                  Once
                </label>
                <label style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
                  <input
                    type="radio"
                    name="schedule-kind"
                    value="cron"
                    checked={kind === "cron"}
                    disabled={formDisabled}
                    onChange={() => {
                      setKind("cron");
                      setFieldErrors({});
                    }}
                  />
                  Recurring
                </label>
              </div>
            </fieldset>

            {kind === "once" ? (
              <div className="field">
                <label className="field-label" htmlFor="task-schedule-run-at">
                  Run at
                </label>
                <input
                  id="task-schedule-run-at"
                  name="schedule-run-at"
                  ref={runAtRef}
                  className="input"
                  type="datetime-local"
                  autoComplete="off"
                  required
                  disabled={formDisabled}
                  value={runAt}
                  onChange={(event) => {
                    setRunAt(event.target.value);
                    clearFieldError("runAt");
                  }}
                  aria-invalid={Boolean(fieldErrors.runAt)}
                  aria-describedby={describedBy(
                    "task-schedule-once-help",
                    "task-schedule-run-at-error",
                    Boolean(fieldErrors.runAt),
                  )}
                />
                <span id="task-schedule-once-help" className="field-hint">
                  Entered in this device&apos;s local time and stored as an exact instant.
                </span>
                {fieldErrors.runAt && (
                  <span
                    id="task-schedule-run-at-error"
                    role="alert"
                    style={{ color: "var(--red)", fontSize: 11 }}
                  >
                    {fieldErrors.runAt}
                  </span>
                )}
              </div>
            ) : (
              <div className="field">
                <label className="field-label" htmlFor="task-schedule-cron">
                  Cron expression
                </label>
                <input
                  id="task-schedule-cron"
                  name="schedule-cron-expression"
                  ref={cronRef}
                  className="input mono"
                  autoComplete="off"
                  required
                  disabled={formDisabled}
                  value={cronExpression}
                  onChange={(event) => {
                    setCronExpression(event.target.value);
                    clearFieldError("cron");
                  }}
                  aria-invalid={Boolean(fieldErrors.cron)}
                  aria-describedby={describedBy(
                    "task-schedule-cron-help",
                    "task-schedule-cron-error",
                    Boolean(fieldErrors.cron),
                  )}
                />
                <span id="task-schedule-cron-help" className="field-hint">
                  Five fields: minute, hour, day of month, month, day of week. No seconds.
                </span>
                {fieldErrors.cron && (
                  <span
                    id="task-schedule-cron-error"
                    role="alert"
                    style={{ color: "var(--red)", fontSize: 11 }}
                  >
                    {fieldErrors.cron}
                  </span>
                )}
              </div>
            )}

            <div className="field">
              <label className="field-label" htmlFor="task-schedule-timezone">
                Timezone
              </label>
              <input
                id="task-schedule-timezone"
                name="schedule-timezone"
                ref={timezoneRef}
                className="input"
                autoComplete="off"
                required
                readOnly={kind === "once"}
                disabled={formDisabled}
                list="task-schedule-timezones"
                value={timezone}
                onChange={(event) => {
                  setTimezone(event.target.value);
                  clearFieldError("timezone");
                }}
                aria-invalid={Boolean(fieldErrors.timezone)}
                aria-describedby={describedBy(
                  "task-schedule-timezone-help",
                  "task-schedule-timezone-error",
                  Boolean(fieldErrors.timezone),
                )}
              />
              <datalist id="task-schedule-timezones">
                {timezoneOptions.map((zone) => (
                  <option key={zone} value={zone} />
                ))}
              </datalist>
              <span id="task-schedule-timezone-help" className="field-hint">
                {kind === "once"
                  ? "One-off times use this device's IANA timezone and are stored as an exact instant."
                  : "Use an IANA name such as Europe/Madrid or UTC. Recurring Runs follow daylight-saving changes in this zone."}
              </span>
              {fieldErrors.timezone && (
                <span
                  id="task-schedule-timezone-error"
                  role="alert"
                  style={{ color: "var(--red)", fontSize: 11 }}
                >
                  {fieldErrors.timezone}
                </span>
              )}
            </div>

            <label style={{ display: "flex", alignItems: "flex-start", gap: 8 }}>
              <input
                type="checkbox"
                name="schedule-enabled"
                disabled={formDisabled}
                checked={enabled}
                onChange={(event) => setEnabled(event.target.checked)}
                style={{ marginTop: 2 }}
              />
              <span>
                <span style={{ display: "block", color: "var(--t0)", fontSize: 12 }}>
                  Schedule enabled
                </span>
                <span className="field-hint">Pause it without deleting its configuration.</span>
              </span>
            </label>

            <div
              id="task-schedule-runtime-note"
              style={{ fontSize: 12, lineHeight: 1.55, color: "var(--t2)" }}
            >
              This Hecate runtime starts scheduled Runs. After downtime, missed times coalesce into
              one Run; if another Run is active, that occurrence is skipped. Normal approvals still
              apply.
            </div>

            {formError && (
              <div
                id="task-schedule-error"
                className="page-banner page-banner--error"
                role="alert"
                aria-live="assertive"
              >
                {formError}
              </div>
            )}

            {schedule && (
              <section aria-labelledby="task-schedule-history-heading">
                <h3
                  id="task-schedule-history-heading"
                  className="kicker"
                  style={{ margin: "0 0 8px" }}
                >
                  Recent occurrences
                </h3>
                {historyState === "loading" || historyState === "idle" ? (
                  <div aria-busy="true" role="status" style={{ color: "var(--t3)", fontSize: 11 }}>
                    Loading occurrences…
                  </div>
                ) : historyState === "error" ? (
                  <div
                    className="page-banner page-banner--error"
                    role="alert"
                    style={{ fontSize: 11 }}
                  >
                    Occurrence history could not be loaded: {historyError || "unknown error"}.
                    Refresh this Task to retry.
                  </div>
                ) : occurrences.length === 0 ? (
                  <div style={{ color: "var(--t3)", fontSize: 11 }}>No occurrences yet.</div>
                ) : (
                  <ol style={{ listStyle: "none", margin: 0, padding: 0 }}>
                    {occurrences.slice(0, 8).map((occurrence) => (
                      <li
                        key={occurrence.id}
                        style={{
                          display: "grid",
                          gridTemplateColumns: "minmax(0, 1fr) auto",
                          gap: 10,
                          padding: "8px 0",
                          borderTop: "1px solid var(--border)",
                        }}
                      >
                        <span style={{ color: "var(--t1)", fontSize: 12 }}>
                          {occurrenceLabel(occurrence)}
                        </span>
                        <time
                          dateTime={occurrence.scheduled_for}
                          style={{
                            color: "var(--t3)",
                            fontFamily: "var(--font-mono)",
                            fontSize: 10,
                          }}
                        >
                          {formatScheduleDateTime(occurrence.scheduled_for, schedule.timezone)}
                        </time>
                      </li>
                    ))}
                  </ol>
                )}
              </section>
            )}
          </form>
        </Modal>
      )}
    </>
  );
}
