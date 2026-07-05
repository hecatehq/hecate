import type { ProjectCoordinationBackendNextActionRecord } from "../types/project";

type ConfigHint = NonNullable<ProjectCoordinationBackendNextActionRecord["config_hints"]>[number];

export function projectCoordinationConfigAssignment(hint: ConfigHint): string {
  return `${hint.env}=${hint.value}`;
}

export function projectCoordinationConfigBlock(
  hints: ProjectCoordinationBackendNextActionRecord["config_hints"],
): string {
  return (hints ?? []).map(projectCoordinationConfigAssignment).filter(Boolean).join("\n");
}

export function projectCoordinationNextActionConfigBlock(
  action: ProjectCoordinationBackendNextActionRecord,
): string {
  return action.config_block?.trim() || projectCoordinationConfigBlock(action.config_hints);
}
