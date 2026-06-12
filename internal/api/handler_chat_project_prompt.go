package api

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
)

const (
	projectChatPromptMaxBytes       = 6 * 1024
	projectChatPromptMemoryMaxItems = 4
	projectChatPromptMemoryMaxBytes = 1024
	projectChatPromptRoleMaxItems   = 8
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
	if memoryText := h.projectChatMemoryPrompt(ctx, project.ID); memoryText != "" {
		sections = append(sections, memoryText)
	}
	text := strings.Join(sections, "\n\n")
	text, _ = truncatePromptContextText(text, projectChatPromptMaxBytes)
	return text
}

func projectChatWorkflowBoundary(project projects.Project) string {
	label := firstNonEmptyString(strings.TrimSpace(project.Name), strings.TrimSpace(project.ID))
	return strings.Join([]string{
		"Hecate project chat guidance:",
		fmt.Sprintf("- This chat is linked to project %q (%s).", label, strings.TrimSpace(project.ID)),
		"- Keep the Chat surface conversational. Infer project-planning intent from the operator's request instead of asking them to use a separate form.",
		"- For requests to plan, split, queue, assign, hand off, or remember project work, treat the durable change as Project Assistant proposal intent.",
		"- Project Assistant is a proposal author only. Do not create or start chats, tasks, runs, external-agent sessions, promoted memory, or durable project records through generic tools or direct API calls.",
		"- If a proposal-only Hecate Project Assistant capability is available, use it to draft typed proposal actions for explicit operator review. If it is not available, describe the proposed action set in chat.",
		"- Assignments proposed from chat must stay queued and unstarted. Memory from model or document output should become memory candidates for operator promotion, not promoted memory.",
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
	lines := []string{"Available project responsibilities:"}
	for i, role := range roles {
		if i >= projectChatPromptRoleMaxItems {
			lines = append(lines, fmt.Sprintf("- %d additional roles omitted.", len(roles)-i))
			break
		}
		label := firstNonEmptyString(strings.TrimSpace(role.Name), strings.TrimSpace(role.ID))
		line := fmt.Sprintf("- %s (%s)", label, strings.TrimSpace(role.ID))
		if description := strings.TrimSpace(role.Description); description != "" {
			line += ": " + description
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (h *Handler) projectChatMemoryPrompt(ctx context.Context, projectID string) string {
	entries := h.enabledProjectMemoryEntries(ctx, projectID)
	if len(entries) == 0 {
		return ""
	}
	remaining := projectChatPromptMaxBytes
	sections := []string{"Accepted project memory excerpts:"}
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
	header := fmt.Sprintf("- Project memory: %s\n  ID: %s\n  Trust: %s", title, strings.TrimSpace(entry.ID), firstNonEmptyString(strings.TrimSpace(entry.TrustLabel), contextTrustOperatorMemory))
	section, _ := boundedPromptContextSection(header, indentProjectChatMemoryBody(body), projectChatPromptMemoryMaxBytes, remaining)
	if strings.TrimSpace(section) == "" {
		return "", false
	}
	return section, true
}

func indentProjectChatMemoryBody(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = "  " + strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}
