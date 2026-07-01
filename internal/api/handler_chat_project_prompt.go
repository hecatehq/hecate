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
	projectChatPromptRootMaxItems       = 4
	projectChatPromptMemoryMaxItems     = 4
	projectChatPromptMemoryMaxBytes     = 1024
	projectChatPromptRoleMaxItems       = 8
	projectChatPromptSkillMaxItems      = 6
	projectChatPromptWorkMaxItems       = 6
	projectChatPromptAssignmentMaxItems = 8
	projectChatPromptInlineMaxRunes     = 220
)

var (
	projectChatPromptWorkItemStatuses = []string{
		projectwork.WorkItemStatusBacklog,
		projectwork.WorkItemStatusReady,
		projectwork.WorkItemStatusRunning,
		projectwork.WorkItemStatusReview,
		projectwork.WorkItemStatusBlocked,
	}
	projectChatPromptAssignmentStatuses = []string{
		projectwork.AssignmentStatusQueued,
		projectwork.AssignmentStatusRunning,
		projectwork.AssignmentStatusAwaitingApproval,
	}
)

type projectChatWorkflowPromptPlan struct {
	Text              string
	IncludedMemoryIDs map[string]bool
}

type projectChatPromptBuilder struct {
	sections  []string
	remaining int
}

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
	return h.projectChatWorkflowPromptPlan(ctx, session).Text
}

func (h *Handler) projectChatWorkflowPromptPlan(ctx context.Context, session chat.Session) projectChatWorkflowPromptPlan {
	plan := projectChatWorkflowPromptPlan{IncludedMemoryIDs: make(map[string]bool)}
	if !isHecateChatSession(session) {
		return plan
	}
	project := h.projectSummary(ctx, session.ProjectID)
	if project == nil {
		return plan
	}

	builder := projectChatPromptBuilder{remaining: projectChatPromptMaxBytes}
	builder.append(projectChatWorkflowBoundary(*project))
	if roots := projectChatRootHints(*project); roots != "" {
		builder.append(roots)
	}
	if roles := h.projectChatRoleHints(ctx, project.ID); roles != "" {
		builder.append(roles)
	}
	if skills := h.projectChatSkillHints(ctx, project.ID); skills != "" {
		builder.append(skills)
	}
	if work := h.projectChatWorkHints(ctx, project.ID); work != "" {
		builder.append(work)
	}
	builder.appendMemory(h.enabledProjectMemoryEntries(ctx, project.ID), plan.IncludedMemoryIDs)
	plan.Text = builder.text()
	return plan
}

func (b *projectChatPromptBuilder) append(section string) bool {
	section = strings.TrimSpace(section)
	if b == nil || section == "" {
		return false
	}
	separatorBytes := 0
	if len(b.sections) > 0 {
		separatorBytes = len("\n\n")
	}
	if b.remaining <= separatorBytes {
		return false
	}
	text, _ := truncatePromptContextText(section, b.remaining-separatorBytes)
	if text == "" {
		return false
	}
	b.sections = append(b.sections, text)
	b.remaining -= separatorBytes + len(text)
	return true
}

func (b *projectChatPromptBuilder) appendMemory(entries []memory.Entry, includedIDs map[string]bool) {
	if b == nil || len(entries) == 0 {
		return
	}
	separatorBytes := 0
	if len(b.sections) > 0 {
		separatorBytes = len("\n\n")
	}
	if b.remaining <= separatorBytes {
		return
	}
	remaining := b.remaining - separatorBytes
	header := "Accepted project memory:"
	if remaining <= len(header) {
		return
	}
	parts := []string{header}
	remaining -= len(header)
	included := 0
	for _, entry := range entries {
		if included >= projectChatPromptMemoryMaxItems {
			appendProjectChatMemoryLine(&parts, &remaining, fmt.Sprintf("- %d additional memory entries omitted.", len(entries)-included))
			break
		}
		if remaining <= len("\n") {
			break
		}
		entryRemaining := remaining - len("\n")
		section, ok := projectChatMemorySection(entry, &entryRemaining)
		if !ok {
			continue
		}
		parts = append(parts, section)
		remaining = entryRemaining
		if id := strings.TrimSpace(entry.ID); id != "" && includedIDs != nil {
			includedIDs[id] = true
		}
		included++
	}
	if included == 0 {
		return
	}
	text := strings.Join(parts, "\n")
	b.sections = append(b.sections, text)
	b.remaining -= separatorBytes + len(text)
}

func appendProjectChatMemoryLine(parts *[]string, remaining *int, line string) {
	if parts == nil || remaining == nil || *remaining <= len("\n") {
		return
	}
	text, _ := truncatePromptContextText(line, *remaining-len("\n"))
	if text == "" {
		return
	}
	*parts = append(*parts, text)
	*remaining -= len("\n") + len(text)
}

func (b projectChatPromptBuilder) text() string {
	return strings.Join(b.sections, "\n\n")
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

func projectChatRootHints(project projects.Project) string {
	if len(project.Roots) == 0 {
		return ""
	}
	roots := append([]projects.Root(nil), project.Roots...)
	defaultRootID := strings.TrimSpace(project.DefaultRootID)
	sort.SliceStable(roots, func(i, j int) bool {
		leftDefault := defaultRootID != "" && strings.TrimSpace(roots[i].ID) == defaultRootID
		rightDefault := defaultRootID != "" && strings.TrimSpace(roots[j].ID) == defaultRootID
		if leftDefault != rightDefault {
			return leftDefault
		}
		if roots[i].Active != roots[j].Active {
			return roots[i].Active
		}
		return false
	})

	lines := []string{"Project roots (metadata only; files are not read):"}
	for i, root := range roots {
		if i >= projectChatPromptRootMaxItems {
			lines = append(lines, fmt.Sprintf("- %d additional roots omitted.", len(roots)-i))
			break
		}
		label := compactProjectChatField(firstNonEmptyString(strings.TrimSpace(root.Path), "root"))
		line := "- Root " + labelWithID(label, root.ID)
		details := []string{"active=" + fmt.Sprint(root.Active)}
		if defaultRootID != "" && strings.TrimSpace(root.ID) == defaultRootID {
			details = append(details, "default=true")
		}
		if kind := strings.TrimSpace(root.Kind); kind != "" {
			details = append(details, "kind="+kind)
		}
		if branch := compactProjectChatField(root.GitBranch); branch != "" {
			details = append(details, "branch="+branch)
		}
		if remote := compactProjectChatField(root.GitRemote); remote != "" {
			details = append(details, "remote="+remote)
		}
		line += ": " + strings.Join(details, ", ")
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (h *Handler) projectChatRoleHints(ctx context.Context, projectID string) string {
	if roles, ok := h.strictEmbeddedCairnlineProjectChatRoles(ctx, projectID); ok {
		return projectChatRoleHintText(roles)
	}
	if h == nil || h.projectWork == nil || strings.TrimSpace(projectID) == "" {
		return ""
	}
	roles, err := h.projectWork.ListRoles(ctx, strings.TrimSpace(projectID))
	if err != nil || len(roles) == 0 {
		return ""
	}
	return projectChatRoleHintText(roles)
}

func projectChatRoleHintText(roles []projectwork.AgentRoleProfile) string {
	if len(roles) == 0 {
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
	builder := projectChatPromptBuilder{remaining: projectChatPromptMaxBytes}
	builder.appendMemory(entries, nil)
	return builder.text()
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
	WorkItems            []projectwork.WorkItem
	Assignments          []projectwork.Assignment
	WorkItemsTruncated   bool
	AssignmentsTruncated bool
}

func (h *Handler) projectChatEnabledSkills(ctx context.Context, projectID string) []projectskills.Skill {
	if items, ok := h.strictEmbeddedCairnlineProjectChatSkills(ctx, projectID); ok {
		return items
	}
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
	sortProjectChatSkills(out)
	return out
}

func sortProjectChatSkills(out []projectskills.Skill) {
	sort.SliceStable(out, func(i, j int) bool {
		left := firstNonEmptyString(strings.TrimSpace(out[i].Title), strings.TrimSpace(out[i].ID))
		right := firstNonEmptyString(strings.TrimSpace(out[j].Title), strings.TrimSpace(out[j].ID))
		if left != right {
			return left < right
		}
		return strings.TrimSpace(out[i].ID) < strings.TrimSpace(out[j].ID)
	})
}

func (h *Handler) projectChatWorkSnapshot(ctx context.Context, projectID string) projectChatWorkSnapshot {
	if snapshot, ok := h.strictEmbeddedCairnlineProjectChatWorkSnapshot(ctx, projectID); ok {
		return snapshot
	}
	if h == nil || h.projectWork == nil || strings.TrimSpace(projectID) == "" {
		return projectChatWorkSnapshot{}
	}
	projectID = strings.TrimSpace(projectID)
	workItems, err := h.projectWork.ListWorkItems(ctx, projectID, projectwork.ListOptions{
		Statuses: projectChatPromptWorkItemStatuses,
		Limit:    projectChatPromptWorkMaxItems + 1,
	})
	if err != nil {
		workItems = nil
	}
	workItemsTruncated := len(workItems) > projectChatPromptWorkMaxItems
	if workItemsTruncated {
		workItems = workItems[:projectChatPromptWorkMaxItems]
	}
	assignments, err := h.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID}, projectwork.ListOptions{
		Statuses: projectChatPromptAssignmentStatuses,
		Limit:    projectChatPromptAssignmentMaxItems + 1,
	})
	if err != nil {
		assignments = nil
	}
	assignmentsTruncated := len(assignments) > projectChatPromptAssignmentMaxItems
	if assignmentsTruncated {
		assignments = assignments[:projectChatPromptAssignmentMaxItems]
	}
	return projectChatWorkSnapshot{
		WorkItems:            workItems,
		Assignments:          assignments,
		WorkItemsTruncated:   workItemsTruncated,
		AssignmentsTruncated: assignmentsTruncated,
	}
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
	lines := []string{"Active project work snapshot:"}
	if len(snapshot.WorkItems) > 0 {
		lines = append(lines, "Shown active work item status counts: "+projectChatWorkStatusCounts(snapshot.WorkItems))
		included := 0
		for _, item := range snapshot.WorkItems {
			if included >= maxWorkItems {
				lines = append(lines, "- Additional active work items omitted.")
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
		if snapshot.WorkItemsTruncated {
			lines = append(lines, "- Additional active work items omitted.")
		}
	}
	if len(snapshot.Assignments) > 0 {
		lines = append(lines, "Active assignments:")
		included := 0
		for _, assignment := range snapshot.Assignments {
			if included >= maxAssignments {
				lines = append(lines, "- Additional active assignments omitted.")
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
		if snapshot.AssignmentsTruncated {
			lines = append(lines, "- Additional active assignments omitted.")
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
	return truncateProjectChatInlineValue(value)
}

func compactProjectChatField(value string) string {
	value = strings.TrimSpace(value)
	return truncateProjectChatInlineValue(value)
}

func truncateProjectChatInlineValue(value string) string {
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
