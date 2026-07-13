import type { ProjectAssignmentDriverKind } from "../../types/project";

export const PROJECT_ASSIGNMENT_DESTINATIONS: ReadonlyArray<{
  kind: ProjectAssignmentDriverKind;
  label: string;
}> = [
  { kind: "manual", label: "Human" },
  { kind: "hecate_task", label: "Hecate Task" },
  { kind: "external_agent", label: "External Agent" },
];

export const HUMAN_ASSIGNMENT_DESCRIPTION = "Track work completed by a person outside Hecate.";

export function projectAssignmentDestinationLabel(kind: string): string {
  return (
    PROJECT_ASSIGNMENT_DESTINATIONS.find((destination) => destination.kind === kind)?.label ||
    kind.replace(/_/g, " ")
  );
}
