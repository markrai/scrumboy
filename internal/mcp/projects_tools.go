package mcp

import (
	"context"

	"scrumboy/internal/store"
)

func (a *Adapter) handleProjectsList(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(403, CodeCapabilityUnavailable, "projects.list is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(403, CodeCapabilityUnavailable, "projects.list is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(401, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	entries, listErr := a.store.ListProjects(ctx)
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}

	items := make([]projectItem, 0, len(entries))
	for _, entry := range entries {
		items = append(items, projectListEntryToItem(entry))
	}

	return map[string]any{"items": items}, map[string]any{}, nil
}

func projectListEntryToItem(entry store.ProjectListEntry) projectItem {
	return projectItem{
		ProjectSlug:        entry.Project.Slug,
		ProjectID:          entry.Project.ID,
		Name:               entry.Project.Name,
		Image:              entry.Project.Image,
		DominantColor:      entry.Project.DominantColor,
		DefaultSprintWeeks: entry.Project.DefaultSprintWeeks,
		ExpiresAt:          entry.Project.ExpiresAt,
		CreatedAt:          entry.Project.CreatedAt,
		UpdatedAt:          entry.Project.UpdatedAt,
		Role:               entry.Role.String(),
	}
}
