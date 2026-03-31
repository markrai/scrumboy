package mcp

import "context"

func (a *Adapter) handleSystemGetCapabilities(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	data := capabilitiesData{
		ServerMode:         a.mode,
		Auth:               auth,
		BootstrapAvailable: bootstrapAvailable,
		Identity: identityCapabilities{
			Project: "projectSlug",
			Todo:    []string{"projectSlug", "localId"},
		},
		Pagination: paginationCapabilities{
			DefaultInput:       []string{"limit", "cursor"},
			DefaultOutput:      []string{"nextCursor", "hasMore"},
			FutureSpecialCases: []string{"board.get"},
		},
		ImplementedTools: a.implementedTools(),
		PlannedTools:     a.plannedTools(),
	}

	return data, map[string]any{"adapterVersion": 1}, nil
}
