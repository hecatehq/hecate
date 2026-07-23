function kindLabel(kind) {
  if (kind === "desktop_host") return "Desktop app";
  if (kind === "hosted_runtime") return "Hosted";
  return "Hecate";
}

function connectionName(connection) {
  const raw =
    typeof connection?.name === "string" && connection.name.trim() ? connection.name.trim() : "";
  if (connection?.kind !== "desktop_host") return raw || "Unnamed Hecate";

  if (!raw || /^(this mac|hecate desktop app)$/i.test(raw)) {
    return "Hecate on Mac";
  }
  if (/^hecate on /i.test(raw)) return raw;
  return `Hecate on ${raw}`;
}

function relativeSeen(value, now) {
  if (!value) return "";
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return "";
  const elapsed = Math.max(0, now - timestamp);
  const minutes = Math.floor(elapsed / 60_000);
  if (minutes < 1) return "seen now";
  if (minutes < 60) return `seen ${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `seen ${hours}h ago`;
  return `seen ${Math.floor(hours / 24)}d ago`;
}

export function connectionView(connection, now = Date.now(), pendingStart = false) {
  const name = connectionName(connection);
  const kind = kindLabel(connection?.kind);
  const remoteDisabled = connection?.kind === "desktop_host" && connection?.remote_enabled !== true;
  const reachable = connection?.reachable === true;
  const canStart =
    connection?.kind === "hosted_runtime" &&
    connection?.can_start === true &&
    connection?.status !== "starting" &&
    !pendingStart &&
    !reachable;
  const canOpen = reachable && !remoteDisabled;
  const canAct = canOpen || canStart;
  const starting =
    !reachable &&
    connection?.kind === "hosted_runtime" &&
    (connection?.status === "starting" || pendingStart);
  let statusLabel = "Available";
  let statusState = "online";
  if (canStart) {
    statusLabel = "Start";
    statusState = "attention";
  } else if (starting) {
    statusLabel = "Starting";
    statusState = "attention";
  } else if (!reachable) {
    statusLabel = "Offline";
    statusState = "offline";
  } else if (remoteDisabled) {
    statusLabel = "Remote access off";
    statusState = "attention";
  }
  const detail = [
    kind,
    typeof connection?.version === "string" && connection.version.trim()
      ? `v${connection.version.trim()}`
      : "",
    relativeSeen(connection?.last_seen_at, now),
  ]
    .filter(Boolean)
    .join(" · ");

  return {
    name,
    detail,
    canOpen,
    canStart,
    canAct,
    action: canOpen ? "open" : canStart ? "start" : "none",
    statusLabel,
    statusState,
    ariaLabel: canOpen ? `Open ${name}` : canStart ? `Start ${name}` : `${name}: ${statusLabel}`,
  };
}
