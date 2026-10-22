package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"scrumboy/internal/assetrefs"
)

// ImportStagedAsset is an archive image whose file is already written (under NewUUID) before
// the import's database work begins.
type ImportStagedAsset struct {
	ArchiveID string
	NewUUID   string
	MimeType  string
	SHA256    string
	Name      string
	SizeBytes int64
	Width     int
	Height    int
}

// ImportAssetPlan carries staged archive images into an import and reports what happened.
type ImportAssetPlan struct {
	// ByProject maps an index into ExportData.Projects to that project's staged images.
	ByProject map[int][]ImportStagedAsset

	// Filled by the import:
	// UsedNewUUIDs are staged files that ended up with a row (others are removed by the caller).
	UsedNewUUIDs map[string]struct{}
	// RemovedAssets are files of projects deleted by a Replace swap, for post-commit removal.
	RemovedAssets []AssetFileRef
}

func (p *ImportAssetPlan) markUsed(uuid string) {
	if p.UsedNewUUIDs == nil {
		p.UsedNewUUIDs = map[string]struct{}{}
	}
	p.UsedNewUUIDs[uuid] = struct{}{}
}

// applyImportAssetsTx inserts (or reuses, by content within the target project) the staged
// images of one imported project and returns its todos with image destinations remapped to the
// target asset UUIDs. Only image destinations change: prose, code and links keep their bytes.
// It must run before the todos are written, so their sync links the new rows. With a nil plan
// it returns todos unchanged.
//
// Every manifest image that a story embeds (per assetrefs.Extract) must be staged and must be
// rewritten; otherwise the whole import fails with ErrValidation before anything commits.
func (s *Store) applyImportAssetsTx(ctx context.Context, tx *sql.Tx, plan *ImportAssetPlan, projectIndex int, projectID int64, pExport ProjectExport, nowMs int64) ([]TodoExport, error) {
	todos := pExport.Todos
	if plan == nil {
		return todos, nil
	}
	mapping := map[string]string{}
	for _, staged := range plan.ByProject[projectIndex] {
		if existing, ok, err := findProjectAssetBySHATx(ctx, tx, projectID, staged.SHA256); err != nil {
			return nil, err
		} else if ok {
			if err := refreshDedupedAssetTx(ctx, tx, &existing, nowMs); err != nil {
				return nil, err
			}
			mapping[staged.ArchiveID] = existing.UUID
			continue
		}
		if err := s.checkAssetBudgetTx(ctx, tx, projectID, staged.SizeBytes); err != nil {
			return nil, err
		}
		if _, err := insertProjectAssetTx(ctx, tx, NewProjectAsset{
			UUID:         staged.NewUUID,
			ProjectID:    projectID,
			MimeType:     staged.MimeType,
			SizeBytes:    staged.SizeBytes,
			Width:        staged.Width,
			Height:       staged.Height,
			SHA256:       staged.SHA256,
			OriginalName: staged.Name,
		}, nowMs); err != nil {
			return nil, fmt.Errorf("import asset %s: %w", staged.ArchiveID, err)
		}
		plan.markUsed(staged.NewUUID)
		mapping[staged.ArchiveID] = staged.NewUUID
	}
	inManifest := make(map[string]struct{}, len(pExport.Assets))
	for _, a := range pExport.Assets {
		inManifest[a.ID] = struct{}{}
	}
	out := make([]TodoExport, len(todos))
	copy(out, todos)
	for i := range out {
		for _, id := range assetrefs.Extract(out[i].Body) {
			if _, ok := inManifest[id]; !ok {
				continue
			}
			if _, ok := mapping[id]; !ok {
				return nil, fmt.Errorf("%w: story %d embeds image %s, which was not restored", ErrValidation, out[i].LocalID, id)
			}
		}
		if len(mapping) == 0 {
			continue
		}
		body, err := assetrefs.RemapImageDestinations(out[i].Body, mapping)
		if err != nil {
			if errors.Is(err, assetrefs.ErrTooManyReferences) || errors.Is(err, assetrefs.ErrRemapInvariant) || errors.Is(err, assetrefs.ErrUnmappedReference) || errors.Is(err, assetrefs.ErrSharedLinkDestination) || errors.Is(err, assetrefs.ErrNoteTooLarge) {
				return nil, fmt.Errorf("%w: story %d images could not be remapped: %v", ErrValidation, out[i].LocalID, err)
			}
			return nil, err
		}
		out[i].Body = body
	}
	return out, nil
}

// ImportProjectsWithAssets imports an archive (.sbz) whose images were already written to disk.
// It applies the same rules as ImportProjectsWithTarget, plus asset rows, budgets and remapping.
func (s *Store) ImportProjectsWithAssets(ctx context.Context, data *ExportData, mode Mode, importMode string, plan *ImportAssetPlan) (*ImportResult, error) {
	if plan == nil {
		plan = &ImportAssetPlan{}
	}
	return s.importProjects(ctx, data, mode, importMode, "", plan)
}

// PreflightImport runs every validation an import performs before writing, without writing. The
// archive import service calls it before staging any image files.
func (s *Store) PreflightImport(ctx context.Context, data *ExportData, mode Mode, importMode string, fromArchive bool) error {
	if err := validateBackupData(data, fromArchive); err != nil {
		return err
	}
	if data.Scope == "full" && mode == ModeAnonymous {
		return fmt.Errorf("%w: cannot import full scope into anonymous mode", ErrValidation)
	}
	if importMode == "replace" && mode == ModeAnonymous {
		return fmt.Errorf("%w: Replace All is forbidden in anonymous mode", ErrValidation)
	}
	switch importMode {
	case "replace", "merge", "copy":
	default:
		return fmt.Errorf("%w: invalid import mode %q", ErrValidation, importMode)
	}
	return s.validateImportPreflight(ctx, data, mode, importMode)
}

// PreviewImportArchive previews an archive import (format 1.3), including image counts.
func (s *Store) PreviewImportArchive(ctx context.Context, data *ExportData, mode Mode, importMode string) (*PreviewResult, error) {
	res, err := s.previewImport(ctx, data, mode, importMode, true)
	if err != nil {
		return nil, err
	}
	for _, p := range data.Projects {
		res.Assets += len(p.Assets)
		for _, a := range p.Assets {
			res.AssetBytes += a.SizeBytes
		}
	}
	return res, nil
}
