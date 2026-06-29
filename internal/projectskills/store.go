package projectskills

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	FormatSkillMD = "skill_md"

	suggestedToolsMaxItems        = 16
	suggestedToolsSummaryMaxItems = 8

	StatusAvailable = "available"
	StatusMissing   = "missing"
	StatusInvalid   = "invalid"
	StatusConflict  = "conflict"

	TrustWorkspaceSkill = "workspace_skill"
)

var (
	ErrNotFound = errors.New("project skill not found")
	ErrInvalid  = errors.New("invalid project skill")
)

type Skill struct {
	ID                     string
	ProjectID              string
	Title                  string
	Description            string
	Path                   string
	RootID                 string
	Format                 string
	SuggestedTools         []string
	RequiredPermissions    RequiredPermissions
	Enabled                bool
	Status                 string
	TrustLabel             string
	SourceContextSourceIDs []string
	Warnings               []string
	DiscoveredAt           time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type RequiredPermissions struct {
	Tools   *bool `json:"tools,omitempty"`
	Writes  *bool `json:"writes,omitempty"`
	Network *bool `json:"network,omitempty"`
}

func (permissions RequiredPermissions) Empty() bool {
	return permissions.Tools == nil && permissions.Writes == nil && permissions.Network == nil
}

type Store interface {
	Backend() string
	List(ctx context.Context, projectID string) ([]Skill, error)
	UpsertDiscovered(ctx context.Context, projectID string, discovered []Skill) ([]Skill, error)
	Update(ctx context.Context, projectID, id string, update func(*Skill)) (Skill, error)
	DeleteProject(ctx context.Context, projectID string) (int, error)
	Clear(ctx context.Context) (int, error)
}

type MemoryStore struct {
	mu     sync.Mutex
	skills map[string]map[string]Skill
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{skills: make(map[string]map[string]Skill)}
}

func (s *MemoryStore) Backend() string { return "memory" }

func (s *MemoryStore) List(_ context.Context, projectID string) ([]Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSortedSkills(s.skills[strings.TrimSpace(projectID)]), nil
}

func (s *MemoryStore) UpsertDiscovered(_ context.Context, projectID string, discovered []Skill) ([]Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, ErrInvalid
	}
	merged := mergeDiscoveredSkills(skillsFromMap(s.skills[projectID]), discovered, projectID, time.Now().UTC())
	next := make(map[string]Skill, len(merged))
	for _, skill := range merged {
		next[skill.ID] = cloneSkill(skill)
	}
	s.skills[projectID] = next
	return cloneSortedSkills(next), nil
}

func (s *MemoryStore) Update(_ context.Context, projectID, id string, update func(*Skill)) (Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	projectSkills := s.skills[projectID]
	if len(projectSkills) == 0 {
		return Skill{}, ErrNotFound
	}
	skill, ok := projectSkills[id]
	if !ok {
		return Skill{}, ErrNotFound
	}
	skill = cloneSkill(skill)
	originalID := skill.ID
	originalProjectID := skill.ProjectID
	createdAt := skill.CreatedAt
	discoveredAt := skill.DiscoveredAt
	if update != nil {
		update(&skill)
	}
	skill.ID = originalID
	skill.ProjectID = originalProjectID
	skill.CreatedAt = createdAt
	skill.DiscoveredAt = discoveredAt
	skill.UpdatedAt = time.Now().UTC()
	skill = normalizeSkill(skill, skill.UpdatedAt)
	if err := validateSkill(skill); err != nil {
		return Skill{}, err
	}
	projectSkills[id] = cloneSkill(skill)
	return cloneSkill(skill), nil
}

func (s *MemoryStore) DeleteProject(_ context.Context, projectID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	count := len(s.skills[projectID])
	delete(s.skills, projectID)
	return count, nil
}

func (s *MemoryStore) Clear(_ context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, items := range s.skills {
		count += len(items)
	}
	s.skills = make(map[string]map[string]Skill)
	return count, nil
}

// MergeDiscovered applies the store rediscovery rules without committing them.
func MergeDiscovered(existing, discovered []Skill, projectID string, now time.Time) []Skill {
	return mergeDiscoveredSkills(existing, discovered, projectID, now)
}

func mergeDiscoveredSkills(existing, discovered []Skill, projectID string, now time.Time) []Skill {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	existingByID := make(map[string]Skill, len(existing))
	for _, skill := range existing {
		skill = normalizeSkill(skill, now)
		if skill.ID != "" {
			existingByID[skill.ID] = skill
		}
	}
	seen := make(map[string]bool, len(discovered))
	out := make([]Skill, 0, len(discovered)+len(existing))
	for _, discoveredSkill := range discovered {
		discoveredSkill.ProjectID = projectID
		discoveredSkill = normalizeSkill(discoveredSkill, now)
		if discoveredSkill.ID == "" {
			continue
		}
		seen[discoveredSkill.ID] = true
		if previous, ok := existingByID[discoveredSkill.ID]; ok {
			discoveredSkill.CreatedAt = previous.CreatedAt
			discoveredSkill.Enabled = previous.Enabled
			if previous.Title != "" {
				discoveredSkill.Title = previous.Title
			}
			if previous.Description != "" {
				discoveredSkill.Description = previous.Description
			}
			if previous.TrustLabel != "" {
				discoveredSkill.TrustLabel = previous.TrustLabel
			}
		}
		discoveredSkill.DiscoveredAt = now
		discoveredSkill.UpdatedAt = now
		discoveredSkill = normalizeSkill(discoveredSkill, now)
		out = append(out, discoveredSkill)
	}
	for _, previous := range existing {
		previous = normalizeSkill(previous, now)
		if previous.ID == "" || seen[previous.ID] {
			continue
		}
		previous.ProjectID = projectID
		previous.Status = StatusMissing
		previous.Warnings = appendUniqueStrings(previous.Warnings, "Skill was not found during the latest discovery.")
		previous.DiscoveredAt = now
		previous.UpdatedAt = now
		out = append(out, normalizeSkill(previous, now))
	}
	sortSkills(out)
	return out
}

func normalizeSkill(skill Skill, now time.Time) Skill {
	skill.ID = normalizeID(skill.ID)
	skill.ProjectID = strings.TrimSpace(skill.ProjectID)
	skill.Title = strings.TrimSpace(skill.Title)
	skill.Description = strings.TrimSpace(skill.Description)
	skill.Path = strings.TrimSpace(skill.Path)
	skill.RootID = strings.TrimSpace(skill.RootID)
	skill.Format = strings.TrimSpace(skill.Format)
	skill.Status = strings.TrimSpace(skill.Status)
	skill.TrustLabel = strings.TrimSpace(skill.TrustLabel)
	var omittedSuggestedTools int
	skill.SuggestedTools, omittedSuggestedTools = normalizeSuggestedTools(skill.SuggestedTools)
	if omittedSuggestedTools > 0 {
		skill.Warnings = append(skill.Warnings, fmt.Sprintf("Suggested tools list was capped at %d entries (+%d more omitted).", suggestedToolsMaxItems, omittedSuggestedTools))
	}
	if skill.Format == "" {
		skill.Format = FormatSkillMD
	}
	if skill.Status == "" {
		skill.Status = StatusAvailable
	}
	if skill.TrustLabel == "" {
		skill.TrustLabel = TrustWorkspaceSkill
	}
	if skill.Title == "" {
		skill.Title = titleFromID(skill.ID)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if skill.CreatedAt.IsZero() {
		skill.CreatedAt = now
	}
	if skill.DiscoveredAt.IsZero() {
		skill.DiscoveredAt = now
	}
	if skill.UpdatedAt.IsZero() {
		skill.UpdatedAt = skill.CreatedAt
	}
	skill.SourceContextSourceIDs = normalizeStringSlice(skill.SourceContextSourceIDs)
	skill.Warnings = normalizeStringSlice(skill.Warnings)
	return skill
}

func validateSkill(skill Skill) error {
	if skill.ID == "" || skill.ProjectID == "" {
		return ErrInvalid
	}
	if skill.Path == "" && skill.Status != StatusMissing {
		return ErrInvalid
	}
	if !oneOf(skill.Status, StatusAvailable, StatusMissing, StatusInvalid, StatusConflict) {
		return ErrInvalid
	}
	if skill.Format != FormatSkillMD {
		return ErrInvalid
	}
	return nil
}

func cloneSortedSkills(items map[string]Skill) []Skill {
	out := skillsFromMap(items)
	sortSkills(out)
	return out
}

func skillsFromMap(items map[string]Skill) []Skill {
	if len(items) == 0 {
		return nil
	}
	out := make([]Skill, 0, len(items))
	for _, item := range items {
		out = append(out, cloneSkill(item))
	}
	return out
}

func cloneSkill(skill Skill) Skill {
	skill.SuggestedTools = append([]string(nil), skill.SuggestedTools...)
	skill.RequiredPermissions = cloneRequiredPermissions(skill.RequiredPermissions)
	skill.SourceContextSourceIDs = append([]string(nil), skill.SourceContextSourceIDs...)
	skill.Warnings = append([]string(nil), skill.Warnings...)
	return skill
}

func cloneRequiredPermissions(permissions RequiredPermissions) RequiredPermissions {
	return RequiredPermissions{
		Tools:   cloneBoolPointer(permissions.Tools),
		Writes:  cloneBoolPointer(permissions.Writes),
		Network: cloneBoolPointer(permissions.Network),
	}
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func sortSkills(items []Skill) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Enabled != items[j].Enabled {
			return items[i].Enabled
		}
		if statusRank(items[i].Status) != statusRank(items[j].Status) {
			return statusRank(items[i].Status) < statusRank(items[j].Status)
		}
		if items[i].Title != items[j].Title {
			return items[i].Title < items[j].Title
		}
		if items[i].Path != items[j].Path {
			return items[i].Path < items[j].Path
		}
		return items[i].ID < items[j].ID
	})
}

func statusRank(status string) int {
	switch status {
	case StatusAvailable:
		return 0
	case StatusConflict:
		return 1
	case StatusInvalid:
		return 2
	case StatusMissing:
		return 3
	default:
		return 4
	}
}

func normalizeStringSlice(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func normalizeSuggestedTools(items []string) ([]string, int) {
	items = normalizeStringSlice(items)
	if len(items) <= suggestedToolsMaxItems {
		return items, 0
	}
	omitted := len(items) - suggestedToolsMaxItems
	return append([]string(nil), items[:suggestedToolsMaxItems]...), omitted
}

func SuggestedToolsSummary(items []string) string {
	items = normalizeStringSlice(items)
	if len(items) == 0 {
		return ""
	}
	omitted := 0
	if len(items) > suggestedToolsSummaryMaxItems {
		omitted = len(items) - suggestedToolsSummaryMaxItems
		items = items[:suggestedToolsSummaryMaxItems]
	}
	summary := strings.Join(items, ", ")
	if omitted > 0 {
		summary += fmt.Sprintf(", +%d more", omitted)
	}
	return summary
}

func appendUniqueStrings(items []string, values ...string) []string {
	return normalizeStringSlice(append(items, values...))
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func normalizeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastUnderscore = false
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if !lastUnderscore && builder.Len() > 0 {
				builder.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(builder.String(), "_")
}

func titleFromID(id string) string {
	parts := strings.Split(normalizeID(id), "_")
	for idx, part := range parts {
		if part == "" {
			continue
		}
		parts[idx] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}
