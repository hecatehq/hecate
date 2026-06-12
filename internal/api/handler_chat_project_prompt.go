package api

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

const (
	projectChatPromptMaxBytes           = 6 * 1024
	projectChatPromptMemoryMaxItems     = 4
	projectChatPromptMemoryMaxBytes     = 1024
	projectChatPromptRoleMaxItems       = 8
	projectChatPromptSkillMaxItems      = 6
	projectChatPromptWorkMaxItems       = 6
	projectChatPromptAssignmentMaxItems = 8
	projectChatPromptInlineMaxRunes     = 220
)

func (h *Handler) hecateChatEffectiveSystemPrompt(ctx context.Context, session chat.Session, operatorPrompt string) string {
	operatorPrompt = strings.TrimSpace(operatorPrompt)
	projectPrompt := h.projectChatWorkflowSystemPrompt(ctx, session)
	switch {
	case projectPrompt == "":
		return operatorPrompt
	case operatorPrompt == "":
		return projectPrompt
	default:
		return projectPrompt + "\n\nOperator system prompt:\n" + operatorPrompt
	}
}

func (h *Handler) hecateChatTaskSystemPrompt(ctx context.Context, session chat.Session, operatorPrompt string, forceNewTask bool) string {
	effectivePrompt := h.hecateChatEffectiveSystemPrompt(ctx, session, operatorPrompt)
	if forceNewTask || strings.TrimSpace(session.TaskID) == "" || h == nil || h.taskStore == nil {
		return effectivePrompt
	}
	task, ok, err := h.taskStore.GetTask(ctx, strings.TrimSpace(session.TaskID))
	if err != nil || !ok {
		return effectivePrompt
	}
	return strings.TrimSpace(task.SystemPrompt)
}

func (h *Handler) projectChatWorkflowSystemPrompt(ctx context.Context, session chat.Session) string {
	project := h.projectSummary(ctx, session.ProjectID)
	if project == nil {
		return ""
	}

	sections := []string{projectChatWorkflowBoundary(*project)}
	if roles := h.projectChatRoleHints(ctx, project.ID); roles != "" {
		sections = append(sections, roles)
	}
	if skills := h.projectChatSkillHints(ctx, project.ID); skills != "" {
		sections = append(sections, skills)
	}
	if work := h.projectChatWorkHints(ctx, project.ID); work != "" {
		sections = append(sections, work)
	}
	if memoryText := h.projectChatMemoryPrompt(ctx, project.ID); memoryText != "" {
		sections = append(sections, memoryText)
	}
	text := strings.Join(sections, "\n\n")
	text, _ = truncatePromptContextText(text, projectChatPromptMaxBytes)
	return text
}

func projectChatWorkflowBoundary(project projects.Project) string {
	return strings.Join([]string{
		"Project chat guidance",
		"Project: " + labelWithID(project.Name, project.ID),
		strings.Join([]string{
			"Project workflow boundary:",
			"- Keep the Chat surface conversational. Infer project-planning intent from the operator's request instead of asking them to use a separate form.",
			"- For requests to plan, split, queue, assign, hand off, or remember project work, treat the durable change as Project Assistant proposal intent.",
			"- Project Assistant is a proposal author only. Do not create or start chats, tasks, runs, external-agent sessions, promoted memory, or durable project records through generic tools or direct API calls.",
			"- If a proposal-only Hecate Project Assistant capability is available, use it to draft typed proposal actions for explicit operator review. If it is not available, describe the proposed action set in chat.",
			"- Assignments proposed from chat must stay queued and unstarted. Memory from model or document output should become memory candidates for operator promotion, not promoted memory.",
		}, "\n"),
	}, "\n")
}

func (h *Handler) projectChatRoleHints(ctx context.Context, projectID string) string {
	if h == nil || h.projectWork == nil || strings.TrimSpace(projectID) == "" {
		return ""
	}
	roles, err := h.projectWork.ListRoles(ctx, strings.TrimSpace(projectID))
	if err != nil || len(roles) == 0 {
		return ""
	}
	sort.SliceStable(roles, func(i, j int) bool {
		left := firstNonEmptyString(strings.TrimSpace(roles[i].Name), strings.TrimSpace(roles[i].ID))
		right := firstNonEmptyString(strings.TrimSpace(roles[j].Name), strings.TrimSpace(roles[j].ID))
		return left < right
	})
	lines := []string{"Role hints:"}
	for i, role := range roles {
		if i >= projectChatPromptRoleMaxItems {
			lines = append(lines, fmt.Sprintf("- %d additional roles omitted.", len(roles)-i))
			break
		}
		line := "- " + labelWithID(role.Name, role.ID)
		if description := strings.TrimSpace(role.Description); description != "" {
			line += ": " + description
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (h *Handler) projectChatSkillHints(ctx context.Context, projectID string) string {
	skills := h.projectChatEnabledSkills(ctx, projectID)
	if len(skills) == 0 {
		return ""
	}
	return projectChatSkillHintText(skills, projectChatPromptSkillMaxItems)
}

func (h *Handler) projectChatWorkHints(ctx context.Context, projectID string) string {
	snapshot := h.projectChatWorkSnapshot(ctx, projectID)
	if len(snapshot.WorkItems) == 0 && len(snapshot.Assignments) == 0 {
		return ""
	}
	return projectChatWorkHintText(snapshot, projectChatPromptWorkMaxItems, projectChatPromptAssignmentMaxItems)
}

func (h *Handler) projectChatMemoryPrompt(ctx context.Context, projectID string) string {
	entries := h.enabledProjectMemoryEntries(ctx, projectID)
	if len(entries) == 0 {
		return ""
	}
	remaining := projectChatPromptMaxBytes
	sections := []string{"Accepted project memory:"}
	included := 0
	for _, entry := range entries {
		if included >= projectChatPromptMemoryMaxItems {
			sections = append(sections, fmt.Sprintf("- %d additional memory entries omitted.", len(entries)-included))
			break
		}
		section, ok := projectChatMemorySection(entry, &remaining)
		if !ok {
			continue
		}
		sections = append(sections, section)
		included++
	}
	if included == 0 {
		return ""
	}
	return strings.Join(sections, "\n")
}

func projectChatMemorySection(entry memory.Entry, remaining *int) (string, bool) {
	title := firstNonEmptyString(strings.TrimSpace(entry.Title), strings.TrimSpace(entry.ID))
	body := strings.TrimSpace(entry.Body)
	if title == "" || body == "" {
		return "", false
	}
	header := fmt.Sprintf("Project memory: %s\nID: %s\nTrust: %s", title, strings.TrimSpace(entry.ID), firstNonEmptyString(strings.TrimSpace(entry.TrustLabel), contextTrustOperatorMemory))
	section, _ := boundedPromptContextSection(header, body, projectChatPromptMemoryMaxBytes, remaining)
	if strings.TrimSpace(section) == "" {
		return "", false
	}
	return section, true
}

type projectChatWorkSnapshot struct {
	WorkItems   []projectwork.WorkItem
	Assignments []projectwork.Assignment
}

func (h *Handler) projectChatEnabledSkills(ctx context.Context, projectID string) []projectskills.Skill {
	if h == nil || h.projectSkills == nil || strings.TrimSpace(projectID) == "" {
		return nil
	}
	items, err := h.projectSkills.List(ctx, strings.TrimSpace(projectID))
	if err != nil {
		return nil
	}
	out := make([]projectskills.Skill, 0, len(items))
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		status := strings.TrimSpace(item.Status)
		if status != "" && status != projectskills.StatusAvailable {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := firstNonEmptyString(strings.TrimSpace(out[i].Title), strings.TrimSpace(out[i].ID))
		right := firstNonEmptyString(strings.TrimSpace(out[j].Title), strings.TrimSpace(out[j].ID))
		if left != right {
			return left < right
		}
		return strings.TrimSpace(out[i].ID) < strings.TrimSpace(out[j].ID)
	})
	return out
}

func (h *Handler) projectChatWorkSnapshot(ctx context.Context, projectID string) projectChatWorkSnapshot {
	if h == nil || h.projectWork == nil || strings.TrimSpace(projectID) == "" {
		return projectChatWorkSnapshot{}
	}
	projectID = strings.TrimSpace(projectID)
	workItems, err := h.projectWork.ListWorkItems(ctx, projectID)
	if err != nil {
		workItems = nil
	}
	assignments, err := h.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		assignments = nil
	}
	return projectChatWorkSnapshot{WorkItems: workItems, Assignments: assignments}
}

func projectChatSkillHintText(skills []projectskills.Skill, maxItems int) string {
	if len(skills) == 0 {
		return ""
	}
	lines := []string{"Project skills (metadata only; skill bodies are not loaded):"}
	for i, skill := range skills {
		if i >= maxItems {
			lines = append(lines, fmt.Sprintf("- %d additional enabled skills omitted.", len(skills)-i))
			break
		}
		line := "- " + labelWithID(skill.Title, skill.ID)
		details := make([]string, 0, 2)
		if description := compactProjectChatInline(skill.Description); description != "" {
			details = append(details, description)
		}
		if path := strings.TrimSpace(skill.Path); path != "" {
			details = append(details, "Path: "+path)
		}
		if len(details) > 0 {
			line += ": " + strings.Join(details, " ")
		}
		lines = append(lines, line)
	}
	lines = append(lines, "Use skills as procedures/guidance, not as role assignments.")
	return strings.Join(lines, "\n")
}

func projectChatWorkHintText(snapshot projectChatWorkSnapshot, maxWorkItems, maxAssignments int) string {
	if len(snapshot.WorkItems) == 0 && len(snapshot.Assignments) == 0 {
		return ""
	}
	lines := []string{"Project work snapshot:"}
	if len(snapshot.WorkItems) > 0 {
		lines = append(lines, "Work item status counts: "+projectChatWorkStatusCounts(snapshot.WorkItems))
		included := 0
		for _, item := range snapshot.WorkItems {
			if included >= maxWorkItems {
				lines = append(lines, fmt.Sprintf("- %d additional work items omitted.", len(snapshot.WorkItems)-included))
				break
			}
			line := "- Work item " + labelWithID(item.Title, item.ID) + ": status=" + firstNonEmptyString(strings.TrimSpace(item.Status), projectwork.WorkItemStatusBacklog)
			if priority := strings.TrimSpace(item.Priority); priority != "" {
				line += ", priority=" + priority
			}
			if owner := strings.TrimSpace(item.OwnerRoleID); owner != "" {
				line += ", owner_role=" + owner
			}
			if brief := compactProjectChatInline(item.Brief); brief != "" {
				line += "\n  Brief: " + brief
			}
			lines = append(lines, line)
			included++
		}
	}
	if len(snapshot.Assignments) > 0 {
		lines = append(lines, "Assignments:")
		included := 0
		for _, assignment := range snapshot.Assignments {
			if included >= maxAssignments {
				lines = append(lines, fmt.Sprintf("- %d additional assignments omitted.", len(snapshot.Assignments)-included))
				break
			}
			line := "- Assignment " + firstNonEmptyString(strings.TrimSpace(assignment.ID), "assignment") +
				": work_item=" + strings.TrimSpace(assignment.WorkItemID) +
				", role=" + strings.TrimSpace(assignment.RoleID) +
				", status=" + firstNonEmptyString(strings.TrimSpace(assignment.Status), projectwork.AssignmentStatusQueued) +
				", driver=" + firstNonEmptyString(strings.TrimSpace(assignment.DriverKind), projectwork.AssignmentDriverHecateTask)
			lines = append(lines, line)
			included++
		}
	}
	return strings.Join(lines, "\n")
}

func projectChatWorkStatusCounts(items []projectwork.WorkItem) string {
	if len(items) == 0 {
		return "none"
	}
	counts := make(map[string]int)
	for _, item := range items {
		status := firstNonEmptyString(strings.TrimSpace(item.Status), projectwork.WorkItemStatusBacklog)
		counts[status]++
	}
	order := []string{
		projectwork.WorkItemStatusBacklog,
		projectwork.WorkItemStatusReady,
		projectwork.WorkItemStatusRunning,
		projectwork.WorkItemStatusReview,
		projectwork.WorkItemStatusBlocked,
		projectwork.WorkItemStatusDone,
		projectwork.WorkItemStatusCancelled,
	}
	parts := make([]string, 0, len(counts))
	for _, status := range order {
		if count := counts[status]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", status, count))
			delete(counts, status)
		}
	}
	var extra []string
	for status, count := range counts {
		extra = append(extra, fmt.Sprintf("%s=%d", status, count))
	}
	sort.Strings(extra)
	parts = append(parts, extra...)
	return strings.Join(parts, ", ")
}

func compactProjectChatInline(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= projectChatPromptInlineMaxRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:projectChatPromptInlineMaxRunes])) + " [truncated]"
}

func boundedPromptContextSection(header, body string, itemMaxBytes int, remaining *int) (string, bool) {
	if remaining == nil || *remaining <= 0 {
		return "", false
	}
	header = strings.TrimSpace(header)
	body = strings.TrimSpace(body)
	if header == "" || body == "" {
		return "", false
	}
	limit := itemMaxBytes
	if *remaining < limit {
		limit = *remaining
	}
	text := header + "\n" + body
	text, truncated := truncatePromptContextText(text, limit)
	if text == "" {
		return "", truncated
	}
	*remaining -= len(text)
	return text, truncated
}

func truncatePromptContextText(text string, maxBytes int) (string, bool) {
	text = strings.TrimSpace(text)
	if maxBytes <= 0 {
		return "", text != ""
	}
	if len(text) <= maxBytes {
		return text, false
	}
	if maxBytes <= len("\n[truncated]") {
		return "", true
	}
	cut := maxBytes - len("\n[truncated]")
	raw := []byte(text)
	for cut > 0 && !utf8.Valid(raw[:cut]) {
		cut--
	}
	if cut <= 0 {
		return "", true
	}
	return strings.TrimSpace(string(raw[:cut])) + "\n[truncated]", true
}
