import type {
  AgentPresetRecord,
  CreateAgentPresetPayload,
  UpdateAgentPresetPayload,
} from "../../types/agent-preset";
import type { ProjectRecord, ProjectSkillRecord, ProjectWorkRoleRecord } from "../../types/project";
import { splitIDs } from "./projectUtils";

export type AgentPresetForm = {
  id: string;
  name: string;
  description: string;
  instructions: string;
  surface: string;
  providerHint: string;
  modelHint: string;
  executionProfile: string;
  toolsEnabled: boolean;
  writesAllowed: boolean;
  networkAllowed: boolean;
  browserAllowed: boolean;
  browserAllowedOrigins: string;
  approvalPolicy: string;
  projectMemoryPolicy: string;
  contextSourcePolicy: string;
  skillIDs: string;
  externalAgentKind: string;
};

export type RoleForm = {
  id: string;
  name: string;
  description: string;
  instructions: string;
  defaultDriverKind: string;
  defaultProvider: string;
  defaultModel: string;
  defaultAgentPreset: string;
  skillIDs: string;
};

export function emptyRoleForm(): RoleForm {
  return {
    id: "",
    name: "",
    description: "",
    instructions: "",
    defaultDriverKind: "",
    defaultProvider: "",
    defaultModel: "",
    defaultAgentPreset: "",
    skillIDs: "",
  };
}

export function emptyAgentPresetForm(): AgentPresetForm {
  return {
    id: "",
    name: "",
    description: "",
    instructions: "",
    surface: "any",
    providerHint: "",
    modelHint: "",
    executionProfile: "",
    toolsEnabled: true,
    writesAllowed: false,
    networkAllowed: false,
    browserAllowed: false,
    browserAllowedOrigins: "",
    approvalPolicy: "inherit",
    projectMemoryPolicy: "inherit",
    contextSourcePolicy: "inherit",
    skillIDs: "",
    externalAgentKind: "",
  };
}

export function presetFormFromRecord(preset: AgentPresetRecord): AgentPresetForm {
  return {
    id: preset.id,
    name: preset.name,
    description: preset.description ?? "",
    instructions: preset.instructions ?? "",
    surface: preset.surface || "any",
    providerHint: preset.provider_hint ?? "",
    modelHint: preset.model_hint ?? "",
    executionProfile: preset.execution_profile ?? "",
    toolsEnabled: preset.tools_enabled,
    writesAllowed: preset.writes_allowed,
    networkAllowed: preset.network_allowed,
    browserAllowed: preset.browser_allowed ?? false,
    browserAllowedOrigins: (preset.browser_allowed_origins ?? []).join("\n"),
    approvalPolicy: preset.approval_policy || "inherit",
    projectMemoryPolicy: preset.project_memory_policy || "inherit",
    contextSourcePolicy: preset.context_source_policy || "inherit",
    skillIDs: (preset.skill_ids ?? []).join(", "),
    externalAgentKind: preset.external_agent_kind ?? "",
  };
}

export function presetCreatePayloadFromForm(form: AgentPresetForm): CreateAgentPresetPayload {
  const payload = presetUpdatePayloadFromForm(form) as CreateAgentPresetPayload;
  const id = form.id.trim();
  if (id) payload.id = id;
  return payload;
}

export function presetUpdatePayloadFromForm(form: AgentPresetForm): UpdateAgentPresetPayload {
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    instructions: form.instructions.trim(),
    surface: form.surface.trim() || "any",
    provider_hint: form.providerHint.trim(),
    model_hint: form.modelHint.trim(),
    execution_profile: form.executionProfile.trim(),
    tools_enabled: form.toolsEnabled,
    writes_allowed: form.writesAllowed,
    network_allowed: form.networkAllowed,
    browser_allowed: form.browserAllowed,
    browser_allowed_origins: form.browserAllowed
      ? splitBrowserOrigins(form.browserAllowedOrigins)
      : [],
    approval_policy: form.approvalPolicy.trim() || "inherit",
    project_memory_policy: form.projectMemoryPolicy.trim() || "inherit",
    context_source_policy: form.contextSourcePolicy.trim() || "inherit",
    skill_ids: uniqueSkillIDs(splitIDs(form.skillIDs)),
    external_agent_kind: form.externalAgentKind.trim(),
  };
}

export function presetReferenceSummary(
  preset: AgentPresetRecord,
  project: ProjectRecord,
  roles: ProjectWorkRoleRecord[],
) {
  const references: string[] = [];
  if (project.default_agent_profile === preset.id) {
    references.push("this project's default preset");
  }
  const roleNames = roles
    .filter((role) => role.default_agent_profile === preset.id)
    .map((role) => role.name || role.id);
  if (roleNames.length > 0) {
    references.push(`roles ${roleNames.join(", ")}`);
  }
  if (references.length === 0) {
    return "No current project defaults or roles reference it.";
  }
  return `Referenced by ${references.join("; ")}. Those references will fall back until changed.`;
}

export function roleFormFromRecord(role: ProjectWorkRoleRecord): RoleForm {
  return {
    id: role.id,
    name: role.name,
    description: role.description ?? "",
    instructions: role.instructions ?? "",
    defaultDriverKind: role.default_driver_kind ?? "",
    defaultProvider: role.default_provider ?? "",
    defaultModel: role.default_model ?? "",
    defaultAgentPreset: role.default_agent_profile ?? "",
    skillIDs: (role.skill_ids ?? []).join(", "),
  };
}

export function rolePayloadFromForm(form: RoleForm) {
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    instructions: form.instructions.trim(),
    default_driver_kind: form.defaultDriverKind.trim(),
    default_provider: form.defaultProvider.trim(),
    default_model: form.defaultModel.trim(),
    default_agent_profile: form.defaultAgentPreset.trim(),
    skill_ids: uniqueSkillIDs(splitIDs(form.skillIDs)),
  };
}

export function sortProjectSkillsForPicker(skills: ProjectSkillRecord[]) {
  return skills.slice().sort((a, b) => {
    if (a.enabled !== b.enabled) return a.enabled ? -1 : 1;
    const rank = projectSkillStatusRank(a.status) - projectSkillStatusRank(b.status);
    return (
      rank ||
      a.title.localeCompare(b.title) ||
      a.path.localeCompare(b.path) ||
      a.id.localeCompare(b.id)
    );
  });
}

export function projectSkillBadgeClass(skill: ProjectSkillRecord) {
  if (skill.status === "available" && skill.enabled) return "badge badge-green";
  if (skill.status === "available") return "badge badge-muted";
  return "badge badge-amber";
}

export function projectSkillSelectionWarnings(
  skillID: string,
  indexedSkills: Map<string, ProjectSkillRecord>,
) {
  const skill = indexedSkills.get(skillID);
  if (!skill) return [`Skill ${skillID} is not registered in this project.`];
  const warnings: string[] = [];
  if (!skill.enabled) warnings.push(`Skill ${skillID} is disabled.`);
  if (skill.status !== "available") warnings.push(`Skill ${skillID} is ${skill.status}.`);
  return warnings;
}

export function projectSkillStatusRank(status: string): number {
  switch (status) {
    case "available":
      return 0;
    case "conflict":
      return 1;
    case "invalid":
      return 2;
    case "missing":
      return 3;
    default:
      return 4;
  }
}

export function uniqueSkillIDs(ids: string[]): string[] {
  return Array.from(new Set(ids));
}

export function splitBrowserOrigins(value: string): string[] {
  return Array.from(
    new Set(
      value
        .split(/[\n,]/)
        .map((item) => item.trim())
        .filter(Boolean),
    ),
  );
}

// The backend remains authoritative, but validating this exact browser
// capability shape in the form avoids a generic save error for common copied
// URLs. Do not normalize the value here: the API owns canonical persistence.
export function browserAllowedOriginsValidationError(value: string): string | null {
  const origins = splitBrowserOrigins(value);
  if (origins.length === 0) {
    return "Add at least one exact origin before saving this browser-enabled preset.";
  }
  for (const origin of origins) {
    let parsed: URL;
    try {
      parsed = new URL(origin);
    } catch {
      return "Use exact http(s) origins only, without paths, query strings, fragments, credentials, or wildcards.";
    }
    if (
      (parsed.protocol !== "http:" && parsed.protocol !== "https:") ||
      !parsed.hostname ||
      parsed.username ||
      parsed.password ||
      (parsed.pathname !== "" && parsed.pathname !== "/") ||
      parsed.search ||
      parsed.hash ||
      parsed.hostname.includes("*")
    ) {
      return "Use exact http(s) origins only, without paths, query strings, fragments, credentials, or wildcards.";
    }
  }
  return null;
}
