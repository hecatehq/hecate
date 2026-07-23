const registeringStates = new Set([
  "registering_with_apple",
  "token_ready",
  "registering_with_cloud",
]);

export function notificationView(status, signedIn) {
  const available = status?.available === true;
  const authorization =
    typeof status?.authorization === "string" ? status.authorization : "checking";
  const registration = typeof status?.registration === "string" ? status.registration : "idle";
  const requested = status?.requested_enabled === true;
  const enabled = status?.enabled === true;
  const backgroundActive = status?.background_active === true;
  const pendingDelete = registration === "pending_delete";
  const busy = registeringStates.has(registration);
  const blocked = authorization === "denied";
  const failed = registration === "failed" || Boolean(status?.last_error);

  let stateLabel = "Off";
  if (authorization === "checking") stateLabel = "Checking";
  else if (blocked) stateLabel = "Blocked";
  else if (busy) stateLabel = "Connecting";
  else if (pendingDelete) stateLabel = "Cleanup pending";
  else if (failed) stateLabel = "Needs attention";
  else if (enabled || backgroundActive) stateLabel = "On";

  return {
    hidden: !available || (!signedIn && !backgroundActive && !pendingDelete),
    busy,
    stateLabel,
    state: enabled || backgroundActive ? "on" : blocked || failed ? "attention" : "off",
    message:
      typeof status?.message === "string" && status.message.trim()
        ? status.message
        : "Get an alert when a run needs approval or finishes.",
    error:
      typeof status?.last_error === "string" && status.last_error.trim() ? status.last_error : "",
    showEnable: signedIn && !enabled && !blocked && !busy,
    enableLabel: requested || failed ? "Try notifications again" : "Turn on notifications",
    showSettings: blocked,
    showDisable: requested || backgroundActive || pendingDelete,
  };
}
