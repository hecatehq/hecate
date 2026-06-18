package agentprofiles

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("agent profile not found")
	ErrInvalid  = errors.New("invalid agent profile")
	ErrBuiltIn  = errors.New("built-in agent profile cannot be mutated")
)

const (
	SurfaceAny           = "any"
	SurfaceHecateChat    = "hecate_chat"
	SurfaceHecateTask    = "hecate_task"
	SurfaceExternalAgent = "external_agent"

	ApprovalInherit = "inherit"
	ApprovalRequire = "require"
	ApprovalBlock   = "block"
	ApprovalAllow   = "allow"

	MemoryInherit     = "inherit"
	MemoryInclude     = "include"
	MemoryVisibleOnly = "visible_only"
	MemoryExclude     = "exclude"

	ContextInherit        = "inherit"
	ContextIncludeEnabled = "include_enabled"
	ContextVisibleOnly    = "visible_only"
	ContextExclude        = "exclude"
)

type Profile struct {
	ID                   string
	Name                 string
	Description          string
	Instructions         string
	Surface              string
	ProviderHint         string
	ModelHint            string
	ExecutionProfile     string
	ToolsEnabled         bool
	WritesAllowed        bool
	NetworkAllowed       bool
	ApprovalPolicy       string
	ProjectMemoryPolicy  string
	ContextSourcePolicy  string
	SkillIDs             []string
	ExternalAgentKind    string
	ExternalAgentOptions map[string]string
	BuiltIn              bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type Store interface {
	Backend() string
	Create(ctx context.Context, profile Profile) (Profile, error)
	Get(ctx context.Context, id string) (Profile, bool, error)
	List(ctx context.Context) ([]Profile, error)
	Update(ctx context.Context, id string, update func(*Profile)) (Profile, error)
	Delete(ctx context.Context, id string) error
}

type MemoryStore struct {
	mu       sync.Mutex
	profiles map[string]Profile
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{profiles: make(map[string]Profile)}
}

func (s *MemoryStore) Backend() string { return "memory" }

func (s *MemoryStore) Create(_ context.Context, profile Profile) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile = normalizeProfile(profile, time.Now().UTC())
	if IsBuiltInProfileID(profile.ID) || profile.BuiltIn {
		return Profile{}, ErrBuiltIn
	}
	if err := validateProfile(profile); err != nil {
		return Profile{}, err
	}
	s.profiles[profile.ID] = cloneProfile(profile)
	return cloneProfile(profile), nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (Profile, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	if profile, ok := BuiltInProfile(id); ok {
		return profile, true, nil
	}
	profile, ok := s.profiles[id]
	if !ok {
		return Profile{}, false, nil
	}
	return cloneProfile(profile), true, nil
}

func (s *MemoryStore) List(_ context.Context) ([]Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := BuiltInProfiles()
	for _, profile := range s.profiles {
		if IsBuiltInProfileID(profile.ID) {
			continue
		}
		items = append(items, cloneProfile(profile))
	}
	sortProfiles(items)
	return items, nil
}

func (s *MemoryStore) Update(_ context.Context, id string, update func(*Profile)) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	if IsBuiltInProfileID(id) {
		return Profile{}, ErrBuiltIn
	}
	profile, ok := s.profiles[id]
	if !ok {
		return Profile{}, ErrNotFound
	}
	profile = cloneProfile(profile)
	originalID := profile.ID
	createdAt := profile.CreatedAt
	if update != nil {
		update(&profile)
	}
	profile.ID = originalID
	profile.CreatedAt = createdAt
	profile.UpdatedAt = time.Now().UTC()
	profile = normalizeProfile(profile, profile.UpdatedAt)
	if err := validateProfile(profile); err != nil {
		return Profile{}, err
	}
	s.profiles[id] = cloneProfile(profile)
	return cloneProfile(profile), nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	if IsBuiltInProfileID(id) {
		return ErrBuiltIn
	}
	if _, ok := s.profiles[id]; !ok {
		return ErrNotFound
	}
	delete(s.profiles, id)
	return nil
}

func normalizeProfile(profile Profile, now time.Time) Profile {
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Description = strings.TrimSpace(profile.Description)
	profile.Instructions = strings.TrimSpace(profile.Instructions)
	profile.Surface = strings.TrimSpace(profile.Surface)
	profile.ProviderHint = strings.TrimSpace(profile.ProviderHint)
	profile.ModelHint = strings.TrimSpace(profile.ModelHint)
	profile.ExecutionProfile = strings.TrimSpace(profile.ExecutionProfile)
	profile.ApprovalPolicy = strings.TrimSpace(profile.ApprovalPolicy)
	profile.ProjectMemoryPolicy = strings.TrimSpace(profile.ProjectMemoryPolicy)
	profile.ContextSourcePolicy = strings.TrimSpace(profile.ContextSourcePolicy)
	profile.ExternalAgentKind = strings.TrimSpace(profile.ExternalAgentKind)
	if profile.Surface == "" {
		profile.Surface = SurfaceAny
	}
	if profile.ApprovalPolicy == "" {
		profile.ApprovalPolicy = ApprovalInherit
	}
	if profile.ProjectMemoryPolicy == "" {
		profile.ProjectMemoryPolicy = MemoryInherit
	}
	if profile.ContextSourcePolicy == "" {
		profile.ContextSourcePolicy = ContextInherit
	}
	profile.SkillIDs = normalizeStringSlice(profile.SkillIDs)
	profile.ExternalAgentOptions = normalizeStringMap(profile.ExternalAgentOptions)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	if profile.UpdatedAt.IsZero() {
		profile.UpdatedAt = profile.CreatedAt
	}
	return profile
}

func validateProfile(profile Profile) error {
	if profile.ID == "" {
		return ErrInvalid
	}
	if profile.Name == "" {
		return ErrInvalid
	}
	if !oneOf(profile.Surface, SurfaceAny, SurfaceHecateChat, SurfaceHecateTask, SurfaceExternalAgent) {
		return ErrInvalid
	}
	if !oneOf(profile.ApprovalPolicy, ApprovalInherit, ApprovalRequire, ApprovalBlock, ApprovalAllow) {
		return ErrInvalid
	}
	if !oneOf(profile.ProjectMemoryPolicy, MemoryInherit, MemoryInclude, MemoryVisibleOnly, MemoryExclude) {
		return ErrInvalid
	}
	if !oneOf(profile.ContextSourcePolicy, ContextInherit, ContextIncludeEnabled, ContextVisibleOnly, ContextExclude) {
		return ErrInvalid
	}
	return nil
}

func cloneProfile(profile Profile) Profile {
	profile.SkillIDs = append([]string(nil), profile.SkillIDs...)
	profile.ExternalAgentOptions = cloneStringMap(profile.ExternalAgentOptions)
	return profile
}

func cloneProfiles(profiles []Profile) []Profile {
	out := make([]Profile, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, cloneProfile(profile))
	}
	return out
}

func sortProfiles(items []Profile) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].BuiltIn != items[j].BuiltIn {
			return items[i].BuiltIn
		}
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].Name < items[j].Name
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
}

func normalizeStringSlice(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func normalizeStringMap(items map[string]string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for key, value := range items {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneStringMap(items map[string]string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for key, value := range items {
		out[key] = value
	}
	return out
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
