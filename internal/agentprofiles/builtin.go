package agentprofiles

import (
	"strings"
	"time"
)

func BuiltInProfiles() []Profile {
	profiles := []Profile{
		{
			ID:                  "project_assignment",
			Name:                "Project Assignment",
			Description:         "General project work with tool access, write access, and visible-only project context until a role or project chooses a sharper profile.",
			Instructions:        "Use the project, work item, assignment, and available context to complete the scoped task. Keep changes reviewable and preserve operator control over risky actions.",
			Surface:             SurfaceHecateTask,
			ExecutionProfile:    "coding_agent",
			ToolsEnabled:        true,
			WritesAllowed:       true,
			NetworkAllowed:      false,
			ApprovalPolicy:      ApprovalInherit,
			ProjectMemoryPolicy: MemoryVisibleOnly,
			ContextSourcePolicy: ContextVisibleOnly,
		},
		{
			ID:                  "planning",
			Name:                "Planning",
			Description:         "Turns ambiguous project intent into scoped work items, sequencing, and acceptance criteria.",
			Instructions:        "Clarify the outcome, split work into reviewable slices, call out dependencies and risks, and avoid starting execution without operator approval.",
			Surface:             SurfaceHecateTask,
			ExecutionProfile:    "repo_local",
			ToolsEnabled:        true,
			WritesAllowed:       false,
			NetworkAllowed:      false,
			ApprovalPolicy:      ApprovalRequire,
			ProjectMemoryPolicy: MemoryInclude,
			ContextSourcePolicy: ContextIncludeEnabled,
		},
		{
			ID:                  "architecture",
			Name:                "Architecture",
			Description:         "Investigates system shape, boundaries, trade-offs, and rollout risk before implementation.",
			Instructions:        "Read existing design and code paths first, preserve established boundaries, identify migration and observability impact, and produce concrete recommendations.",
			Surface:             SurfaceHecateTask,
			ExecutionProfile:    "repo_local",
			ToolsEnabled:        true,
			WritesAllowed:       false,
			NetworkAllowed:      false,
			ApprovalPolicy:      ApprovalRequire,
			ProjectMemoryPolicy: MemoryInclude,
			ContextSourcePolicy: ContextIncludeEnabled,
		},
		{
			ID:                  "implementation",
			Name:                "Implementation",
			Description:         "Implements scoped code changes with tests, docs, and evidence for review.",
			Instructions:        "Read the surrounding code before editing, keep the patch narrow, add or update focused tests, update docs when behavior changes, and leave clear verification notes.",
			Surface:             SurfaceHecateTask,
			ExecutionProfile:    "coding_agent",
			ToolsEnabled:        true,
			WritesAllowed:       true,
			NetworkAllowed:      false,
			ApprovalPolicy:      ApprovalRequire,
			ProjectMemoryPolicy: MemoryInclude,
			ContextSourcePolicy: ContextIncludeEnabled,
		},
		{
			ID:                  "frontend_implementation",
			Name:                "Frontend Implementation",
			Description:         "Builds user-facing surfaces with ergonomic interaction, responsive layout, and focused UI tests.",
			Instructions:        "Respect the existing design system, make the first screen directly usable, prevent text and control overlap, cover important states, and verify the changed workflow.",
			Surface:             SurfaceHecateTask,
			ExecutionProfile:    "coding_agent",
			ToolsEnabled:        true,
			WritesAllowed:       true,
			NetworkAllowed:      false,
			ApprovalPolicy:      ApprovalRequire,
			ProjectMemoryPolicy: MemoryInclude,
			ContextSourcePolicy: ContextIncludeEnabled,
		},
		{
			ID:                  "design_review",
			Name:                "Design Review",
			Description:         "Reviews interaction flow, information hierarchy, visual quality, and product fit.",
			Instructions:        "Evaluate the workflow from the operator's perspective, identify confusing states, suggest simpler flows, and separate must-fix usability issues from polish.",
			Surface:             SurfaceHecateTask,
			ExecutionProfile:    "repo_local",
			ToolsEnabled:        true,
			WritesAllowed:       false,
			NetworkAllowed:      false,
			ApprovalPolicy:      ApprovalRequire,
			ProjectMemoryPolicy: MemoryInclude,
			ContextSourcePolicy: ContextIncludeEnabled,
		},
		{
			ID:                  "reliability_ops",
			Name:                "Reliability Ops",
			Description:         "Checks deployment, runtime safety, observability, failure modes, and operational readiness.",
			Instructions:        "Trace the runtime path, look for unsafe defaults, missing telemetry, retry or cleanup gaps, and document the verification needed before shipping.",
			Surface:             SurfaceHecateTask,
			ExecutionProfile:    "coding_agent",
			ToolsEnabled:        true,
			WritesAllowed:       true,
			NetworkAllowed:      false,
			ApprovalPolicy:      ApprovalRequire,
			ProjectMemoryPolicy: MemoryInclude,
			ContextSourcePolicy: ContextIncludeEnabled,
		},
		{
			ID:                  "documentation",
			Name:                "Documentation",
			Description:         "Turns implementation details and decisions into clear operator and contributor documentation.",
			Instructions:        "Update the docs closest to the changed behavior, keep wording practical, distinguish facts from future intent, and avoid stale process labels.",
			Surface:             SurfaceHecateTask,
			ExecutionProfile:    "coding_agent",
			ToolsEnabled:        true,
			WritesAllowed:       true,
			NetworkAllowed:      false,
			ApprovalPolicy:      ApprovalRequire,
			ProjectMemoryPolicy: MemoryInclude,
			ContextSourcePolicy: ContextIncludeEnabled,
		},
		{
			ID:                  "review_qa",
			Name:                "Review QA",
			Description:         "Reviews diffs, product behavior, regressions, verification gaps, and release risk.",
			Instructions:        "Lead with actionable findings, ground each issue in evidence, check tests and docs, and avoid speculative feedback when the patch is sound.",
			Surface:             SurfaceHecateTask,
			ExecutionProfile:    "repo_local",
			ToolsEnabled:        true,
			WritesAllowed:       false,
			NetworkAllowed:      false,
			ApprovalPolicy:      ApprovalRequire,
			ProjectMemoryPolicy: MemoryInclude,
			ContextSourcePolicy: ContextIncludeEnabled,
		},
		{
			ID:                  "safe_external_review",
			Name:                "Safe External Review",
			Description:         "Prepares an external-agent review posture without granting writes, network, or automatic context body injection.",
			Instructions:        "Review the supplied assignment context and evidence. Do not assume extra workspace access; ask the operator for missing context instead of taking actions outside the assignment.",
			Surface:             SurfaceExternalAgent,
			ExecutionProfile:    "repo_local",
			ToolsEnabled:        false,
			WritesAllowed:       false,
			NetworkAllowed:      false,
			ApprovalPolicy:      ApprovalRequire,
			ProjectMemoryPolicy: MemoryVisibleOnly,
			ContextSourcePolicy: ContextVisibleOnly,
		},
	}
	for idx := range profiles {
		profiles[idx] = normalizeProfile(profiles[idx], profiles[idx].UpdatedAt)
		profiles[idx].BuiltIn = true
		profiles[idx].CreatedAt = time.Time{}
		profiles[idx].UpdatedAt = time.Time{}
	}
	sortProfiles(profiles)
	return profiles
}

func BuiltInProfile(id string) (Profile, bool) {
	id = strings.TrimSpace(id)
	for _, profile := range BuiltInProfiles() {
		if profile.ID == id {
			return cloneProfile(profile), true
		}
	}
	return Profile{}, false
}

func IsBuiltInProfileID(id string) bool {
	_, ok := BuiltInProfile(id)
	return ok
}
