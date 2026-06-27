package cairnlinebridge

import (
	"context"
	"errors"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/projectskills"
)

// UpsertProjectSkill mirrors Hecate project-skill metadata into Cairnline
// without loading or executing SKILL.md bodies. It covers the current
// Hecate/Cairnline mutation shape: discovery records plus operator edits.
func UpsertProjectSkill(ctx context.Context, service *cairnline.Service, skill projectskills.Skill) (cairnline.ProjectSkill, error) {
	if service == nil {
		return cairnline.ProjectSkill{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item := ProjectSkill(skill)
	if _, err := service.GetProjectSkill(ctx, item.ProjectID, item.ID); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.ProjectSkill{}, err
		}
		created, err := service.CreateProjectSkill(ctx, item)
		if err != nil {
			return cairnline.ProjectSkill{}, err
		}
		if created.Enabled == item.Enabled {
			return created, nil
		}
		return service.UpdateProjectSkill(ctx, item)
	}
	return service.UpdateProjectSkill(ctx, item)
}

func UpsertProjectSkills(ctx context.Context, service *cairnline.Service, skills []projectskills.Skill) ([]cairnline.ProjectSkill, error) {
	out := make([]cairnline.ProjectSkill, 0, len(skills))
	for _, skill := range skills {
		written, err := UpsertProjectSkill(ctx, service, skill)
		if err != nil {
			return nil, err
		}
		out = append(out, written)
	}
	return out, nil
}
