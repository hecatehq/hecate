package cairnlinebridge

import (
	"context"
	"errors"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/projects"
)

// UpsertProject is the first non-authoritative Cairnline write-adapter seam for
// Hecate project identity plus embedded root/context-source state. Live Hecate
// routes still write Hecate stores; this function proves the portable service
// can accept the same mutation shape before authority moves.
func UpsertProject(ctx context.Context, service *cairnline.Service, project projects.Project) (cairnline.Project, error) {
	if service == nil {
		return cairnline.Project{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item := Project(project)
	if strings.TrimSpace(item.ID) == "" {
		return cairnline.Project{}, errors.Join(cairnline.ErrInvalid, errors.New("project id is required"))
	}
	var written cairnline.Project
	if _, err := service.GetProject(ctx, item.ID); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Project{}, err
		}
		created, err := service.CreateProject(ctx, item)
		if err != nil {
			return cairnline.Project{}, err
		}
		written = created
	} else {
		updated, err := service.UpdateProject(ctx, item)
		if err != nil {
			return cairnline.Project{}, err
		}
		written = updated
	}
	return written, nil
}

// UpsertProjectDefaults mirrors only portable project defaults. Hecate-owned
// runtime launch posture such as Agent Presets, provider/model hints, tools,
// and workspace prompt policy stays in Hecate's runtime overlay.
func UpsertProjectDefaults(ctx context.Context, service *cairnline.Service, project projects.Project) (cairnline.Project, error) {
	if service == nil {
		return cairnline.Project{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item := Project(project)
	if strings.TrimSpace(item.ID) == "" {
		return cairnline.Project{}, errors.Join(cairnline.ErrInvalid, errors.New("project id is required"))
	}
	existing, err := service.GetProject(ctx, item.ID)
	if err != nil {
		if errors.Is(err, cairnline.ErrNotFound) {
			return UpsertProject(ctx, service, project)
		}
		return cairnline.Project{}, err
	}
	existing.DefaultRootID = item.DefaultRootID
	updated, err := service.UpdateProject(ctx, existing)
	if err != nil {
		if errors.Is(err, cairnline.ErrNotFound) {
			return UpsertProject(ctx, service, project)
		}
		return cairnline.Project{}, err
	}
	return updated, nil
}

// UpsertProjectMetadata mirrors portable project identity metadata without
// replacing Cairnline-owned root/context-source state. Missing projects fall
// back to the full bootstrap write so out-of-order mirrors still converge.
func UpsertProjectMetadata(ctx context.Context, service *cairnline.Service, project projects.Project) (cairnline.Project, error) {
	if service == nil {
		return cairnline.Project{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item := Project(project)
	if strings.TrimSpace(item.ID) == "" {
		return cairnline.Project{}, errors.Join(cairnline.ErrInvalid, errors.New("project id is required"))
	}
	existing, err := service.GetProject(ctx, item.ID)
	if err != nil {
		if errors.Is(err, cairnline.ErrNotFound) {
			return UpsertProject(ctx, service, project)
		}
		return cairnline.Project{}, err
	}
	existing.Name = item.Name
	existing.Description = item.Description
	updated, err := service.UpdateProject(ctx, existing)
	if err != nil {
		if errors.Is(err, cairnline.ErrNotFound) {
			return UpsertProject(ctx, service, project)
		}
		return cairnline.Project{}, err
	}
	return updated, nil
}

func ReplaceProjectRoots(ctx context.Context, service *cairnline.Service, project projects.Project, roots []projects.Root) (cairnline.Project, error) {
	if service == nil {
		return cairnline.Project{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item := Project(project)
	if strings.TrimSpace(item.ID) == "" {
		return cairnline.Project{}, errors.Join(cairnline.ErrInvalid, errors.New("project id is required"))
	}
	existing, err := service.GetProject(ctx, item.ID)
	if err != nil {
		if errors.Is(err, cairnline.ErrNotFound) {
			return UpsertProject(ctx, service, project)
		}
		return cairnline.Project{}, err
	}
	replacement := make([]cairnline.Root, 0, len(roots))
	for _, root := range roots {
		replacement = append(replacement, Root(root))
	}
	existing.Roots = replacement
	existing.DefaultRootID = item.DefaultRootID
	updated, err := service.UpdateProject(ctx, existing)
	if err != nil {
		if errors.Is(err, cairnline.ErrNotFound) {
			return UpsertProject(ctx, service, project)
		}
		return cairnline.Project{}, err
	}
	return updated, nil
}

func ReplaceProjectContextSources(ctx context.Context, service *cairnline.Service, project projects.Project, sources []projects.ContextSource) (cairnline.Project, error) {
	if service == nil {
		return cairnline.Project{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item := Project(project)
	if strings.TrimSpace(item.ID) == "" {
		return cairnline.Project{}, errors.Join(cairnline.ErrInvalid, errors.New("project id is required"))
	}
	existing, err := service.GetProject(ctx, item.ID)
	if err != nil {
		if errors.Is(err, cairnline.ErrNotFound) {
			return UpsertProject(ctx, service, project)
		}
		return cairnline.Project{}, err
	}
	replacement := make([]cairnline.Source, 0, len(sources))
	for _, source := range sources {
		replacement = append(replacement, Source(source))
	}
	existing.ContextSources = replacement
	updated, err := service.UpdateProject(ctx, existing)
	if err != nil {
		if errors.Is(err, cairnline.ErrNotFound) {
			return UpsertProject(ctx, service, project)
		}
		return cairnline.Project{}, err
	}
	return updated, nil
}

// DeleteProject removes the portable project record. Execution-profile ids on
// Cairnline records are opaque host hints, not separately mirrored resources.
func DeleteProject(ctx context.Context, service *cairnline.Service, project projects.Project) error {
	if service == nil {
		return errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	projectID := strings.TrimSpace(project.ID)
	if projectID == "" {
		return errors.Join(cairnline.ErrInvalid, errors.New("project id is required"))
	}
	return service.DeleteProject(ctx, projectID)
}

func UpsertRoot(ctx context.Context, service *cairnline.Service, project projects.Project, root projects.Root) (cairnline.Root, error) {
	if service == nil {
		return cairnline.Root{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	projectID := strings.TrimSpace(project.ID)
	if projectID == "" {
		return cairnline.Root{}, errors.Join(cairnline.ErrInvalid, errors.New("project id is required"))
	}
	item := Root(root)
	if strings.TrimSpace(item.ID) == "" {
		return cairnline.Root{}, errors.Join(cairnline.ErrInvalid, errors.New("root id is required"))
	}
	if _, err := service.GetProject(ctx, projectID); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Root{}, err
		}
		if _, err := UpsertProject(ctx, service, project); err != nil {
			return cairnline.Root{}, err
		}
		if existing, err := service.GetRoot(ctx, projectID, item.ID); err == nil {
			return existing, nil
		} else if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Root{}, err
		}
		_, created, err := service.CreateRoot(ctx, projectID, item)
		return created, err
	}
	if _, err := service.GetRoot(ctx, projectID, item.ID); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Root{}, err
		}
		_, created, err := service.CreateRoot(ctx, projectID, item)
		return created, err
	}
	_, updated, err := service.UpdateRoot(ctx, projectID, item.ID, item)
	return updated, err
}

func DeleteRoot(ctx context.Context, service *cairnline.Service, projectID, rootID string) error {
	if service == nil {
		return errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	projectID = strings.TrimSpace(projectID)
	rootID = strings.TrimSpace(rootID)
	if projectID == "" {
		return errors.Join(cairnline.ErrInvalid, errors.New("project id is required"))
	}
	if rootID == "" {
		return errors.Join(cairnline.ErrInvalid, errors.New("root id is required"))
	}
	_, _, err := service.DeleteRoot(ctx, projectID, rootID)
	return err
}

func UpsertContextSource(ctx context.Context, service *cairnline.Service, project projects.Project, source projects.ContextSource) (cairnline.Source, error) {
	if service == nil {
		return cairnline.Source{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	projectID := strings.TrimSpace(project.ID)
	if projectID == "" {
		return cairnline.Source{}, errors.Join(cairnline.ErrInvalid, errors.New("project id is required"))
	}
	item := Source(source)
	if strings.TrimSpace(item.ID) == "" {
		return cairnline.Source{}, errors.Join(cairnline.ErrInvalid, errors.New("context source id is required"))
	}
	if _, err := service.GetProject(ctx, projectID); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Source{}, err
		}
		if _, err := UpsertProject(ctx, service, project); err != nil {
			return cairnline.Source{}, err
		}
		if existing, err := service.GetContextSource(ctx, projectID, item.ID); err == nil {
			return existing, nil
		} else if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Source{}, err
		}
		_, created, err := service.CreateContextSource(ctx, projectID, item)
		return created, err
	}
	if _, err := service.GetContextSource(ctx, projectID, item.ID); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Source{}, err
		}
		_, created, err := service.CreateContextSource(ctx, projectID, item)
		return created, err
	}
	_, updated, err := service.UpdateContextSource(ctx, projectID, item.ID, item)
	return updated, err
}

func DeleteContextSource(ctx context.Context, service *cairnline.Service, projectID, sourceID string) error {
	if service == nil {
		return errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	projectID = strings.TrimSpace(projectID)
	sourceID = strings.TrimSpace(sourceID)
	if projectID == "" {
		return errors.Join(cairnline.ErrInvalid, errors.New("project id is required"))
	}
	if sourceID == "" {
		return errors.Join(cairnline.ErrInvalid, errors.New("context source id is required"))
	}
	_, _, err := service.DeleteContextSource(ctx, projectID, sourceID)
	return err
}
