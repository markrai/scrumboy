// Package backupimport imports .sbz archive backups: it validates the archive, writes every
// needed image file before any database work, runs the store import with an asset plan, and
// cleans up staged files on every outcome. Existing data is never touched by a failed import.
package backupimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/google/uuid"

	"scrumboy/internal/assetrefs"
	"scrumboy/internal/assets/blobstore"
	"scrumboy/internal/assets/imagecheck"
	"scrumboy/internal/backuparchive"
	"scrumboy/internal/store"
)

// Store is the store surface the import service needs.
type Store interface {
	PreflightImport(ctx context.Context, data *store.ExportData, mode store.Mode, importMode string, fromArchive bool) error
	PreviewImportArchive(ctx context.Context, data *store.ExportData, mode store.Mode, importMode string) (*store.PreviewResult, error)
	ImportProjectsWithAssets(ctx context.Context, data *store.ExportData, mode store.Mode, importMode string, plan *store.ImportAssetPlan) (*store.ImportResult, error)
	ExistingAssetUUIDs(ctx context.Context, uuids []string) (map[string]struct{}, error)
	CheckImportAssetBudget(ctx context.Context, perProject []int64) error
	AssetSHAExists(ctx context.Context, sha string) (bool, error)
}

// Validator validates image bytes (shared decode semaphore with uploads).
type Validator interface {
	Validate(ctx context.Context, data []byte) (imagecheck.Result, error)
}

// FileRemover removes files of rows deleted by a committed transaction.
type FileRemover interface {
	RemoveFiles(refs []store.AssetFileRef)
}

// Default staging admission bounds: at most DefaultStagingSlots archives (each up to
// Limits.MaxArchiveBytes on disk) are received or processed at once, and at most
// DefaultStagingSlotsPerUser per user, so temporary disk use stays bounded however many
// requests arrive in parallel.
const (
	DefaultStagingSlots        = 2
	DefaultStagingSlotsPerUser = 1
)

var (
	// ErrStagingBusy reports that every archive staging slot is in use.
	ErrStagingBusy = errors.New("backupimport: too many backup archives are being processed")
	// ErrStagingUserBusy reports that the user already has an archive being processed.
	ErrStagingUserBusy = errors.New("backupimport: another backup archive of yours is still being processed")
)

// Service imports archives.
type Service struct {
	store     Store
	blobs     *blobstore.Store
	pending   *blobstore.Pending
	validator Validator
	remover   FileRemover
	limits    backuparchive.Limits

	stagingMu      sync.Mutex
	stagingActive  int
	stagingByUser  map[int64]int
	stagingSlots   int
	stagingPerUser int
}

// Options configures a Service.
type Options struct {
	Store     Store
	Blobs     *blobstore.Store
	Pending   *blobstore.Pending
	Validator Validator
	Remover   FileRemover
	Limits    *backuparchive.Limits
	// StagingSlots and StagingSlotsPerUser bound concurrent archive requests (defaults
	// DefaultStagingSlots and DefaultStagingSlotsPerUser).
	StagingSlots        int
	StagingSlotsPerUser int
}

// New constructs a Service.
func New(opts Options) *Service {
	limits := backuparchive.DefaultLimits
	if opts.Limits != nil {
		limits = *opts.Limits
	}
	slots, perUser := opts.StagingSlots, opts.StagingSlotsPerUser
	if slots <= 0 {
		slots = DefaultStagingSlots
	}
	if perUser <= 0 {
		perUser = DefaultStagingSlotsPerUser
	}
	return &Service{
		store: opts.Store, blobs: opts.Blobs, pending: opts.Pending, validator: opts.Validator, remover: opts.Remover, limits: limits,
		stagingByUser: map[int64]int{}, stagingSlots: slots, stagingPerUser: perUser,
	}
}

// AdmitStaging reserves an archive staging slot for userID without blocking. Callers acquire it
// before reading any request body and call release once the staged archive is removed.
func (s *Service) AdmitStaging(userID int64) (release func(), err error) {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()
	if s.stagingByUser[userID] >= s.stagingPerUser {
		return nil, ErrStagingUserBusy
	}
	if s.stagingActive >= s.stagingSlots {
		return nil, ErrStagingBusy
	}
	s.stagingActive++
	s.stagingByUser[userID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.stagingMu.Lock()
			defer s.stagingMu.Unlock()
			s.stagingActive--
			if s.stagingByUser[userID]--; s.stagingByUser[userID] <= 0 {
				delete(s.stagingByUser, userID)
			}
		})
	}, nil
}

// Limits returns the archive limits in force.
func (s *Service) Limits() backuparchive.Limits { return s.limits }

// PreviewArchive validates the archive and previews the import. Nothing is written.
func (s *Service) PreviewArchive(ctx context.Context, ra io.ReaderAt, size int64, mode store.Mode, importMode string) (*store.PreviewResult, error) {
	arch, err := backuparchive.Open(ra, size, s.limits)
	if err != nil {
		return nil, err
	}
	if _, _, err := neededAssets(arch); err != nil {
		return nil, err
	}
	return s.store.PreviewImportArchive(ctx, arch.Data, mode, importMode)
}

// neededAssets returns, per project index, the archive images that some story embeds (per
// assetrefs.Extract, the same rule GC and serving use). Every one of them is required: if a
// story's body cannot be remapped, the import fails here, before any file is written or any
// existing project is touched. Manifest images no story embeds are skipped with a warning.
func neededAssets(arch *backuparchive.Archive) (map[int][]string, []string, error) {
	needed := map[int][]string{}
	var warnings []string
	for pi, p := range arch.Data.Projects {
		inProject := map[string]bool{}
		for _, a := range p.Assets {
			inProject[a.ID] = true
		}
		if len(inProject) == 0 {
			continue
		}
		used := map[string]bool{}
		for _, t := range p.Todos {
			// A dry run with fresh IDs (the import mints new ones) proves the store import can
			// rewrite every embedded destination of this story without retargeting a link. The
			// identity mapping would hide a shared link reference definition.
			fresh := map[string]string{}
			for _, id := range assetrefs.Extract(t.Body) {
				if inProject[id] {
					used[id] = true
					fresh[id] = uuid.NewString()
				}
			}
			if len(fresh) == 0 {
				continue
			}
			if _, err := assetrefs.RemapImageDestinations(t.Body, fresh); err != nil {
				return nil, nil, fmt.Errorf("%w: story %d in project %q: images cannot be restored: %v", backuparchive.ErrInvalidArchive, t.LocalID, p.Name, err)
			}
		}
		for _, a := range p.Assets {
			if used[a.ID] {
				needed[pi] = append(needed[pi], a.ID)
			} else {
				warnings = append(warnings, fmt.Sprintf("Image %s in project %q is not embedded by any story and was skipped", a.ID, p.Name))
			}
		}
	}
	return needed, warnings, nil
}

// ImportArchive imports a validated archive. Order: validate everything, write image files
// (no database transaction held), run the store import, then remove unused or replaced files.
func (s *Service) ImportArchive(ctx context.Context, ra io.ReaderAt, size int64, mode store.Mode, importMode string) (*store.ImportResult, error) {
	arch, err := backuparchive.Open(ra, size, s.limits)
	if err != nil {
		return nil, err
	}
	if err := s.store.PreflightImport(ctx, arch.Data, mode, importMode, true); err != nil {
		return nil, err
	}
	needed, skipWarnings, err := neededAssets(arch)
	if err != nil {
		return nil, err
	}

	// Advisory budget pre-check before writing any file; the import transaction re-checks.
	perProject := make([]int64, 0, len(needed))
	for _, ids := range needed {
		var n int64
		for _, id := range ids {
			a := arch.Assets[id]
			if exists, err := s.store.AssetSHAExists(ctx, a.SHA256); err != nil {
				return nil, err
			} else if !exists {
				n += a.SizeBytes
			}
		}
		perProject = append(perProject, n)
	}
	if err := s.store.CheckImportAssetBudget(ctx, perProject); err != nil {
		return nil, err
	}

	plan := &store.ImportAssetPlan{ByProject: map[int][]store.ImportStagedAsset{}}
	var staged []store.AssetFileRef
	var releases []func()
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	cleanupStaged := func(keep map[string]struct{}) {
		for _, ref := range staged {
			if _, ok := keep[ref.UUID]; ok {
				continue
			}
			ext, _ := store.AssetExtensionForMIME(ref.MimeType)
			_ = s.blobs.Remove(ref.UUID, ext)
		}
	}

	for pi, ids := range needed {
		for _, id := range ids {
			a := arch.Assets[id]
			data, err := arch.ReadAsset(id)
			if err != nil {
				cleanupStaged(nil)
				return nil, err
			}
			res, err := s.validator.Validate(ctx, data)
			if err != nil {
				cleanupStaged(nil)
				return nil, fmt.Errorf("%w: image %s: %v", backuparchive.ErrInvalidArchive, id, err)
			}
			if res.SHA256 != a.SHA256 || res.MIME != a.MimeType {
				cleanupStaged(nil)
				return nil, fmt.Errorf("%w: image %s does not match its manifest", backuparchive.ErrInvalidArchive, id)
			}
			newID, err := uuid.NewRandom()
			if err != nil {
				cleanupStaged(nil)
				return nil, err
			}
			releases = append(releases, s.pending.Hold(newID.String()))
			if err := s.blobs.CreateExclusive(newID.String(), res.Ext, res.Data); err != nil {
				cleanupStaged(nil)
				return nil, err
			}
			staged = append(staged, store.AssetFileRef{UUID: newID.String(), MimeType: res.MIME})
			plan.ByProject[pi] = append(plan.ByProject[pi], store.ImportStagedAsset{
				ArchiveID: id,
				NewUUID:   newID.String(),
				MimeType:  res.MIME,
				SHA256:    res.SHA256,
				Name:      a.Name,
				SizeBytes: int64(len(res.Data)),
				Width:     res.Width,
				Height:    res.Height,
			})
		}
	}

	result, err := s.store.ImportProjectsWithAssets(ctx, arch.Data, mode, importMode, plan)
	if err != nil {
		// Remove staged files that have no row. Rows that survived (a failed staging cleanup)
		// keep their files; the staging reaper and orphan sweep reclaim them later.
		ids := make([]string, len(staged))
		for i, ref := range staged {
			ids[i] = ref.UUID
		}
		existing, existErr := s.store.ExistingAssetUUIDs(context.WithoutCancel(ctx), ids)
		if existErr != nil {
			return nil, err
		}
		cleanupStaged(existing)
		return nil, err
	}
	cleanupStaged(plan.UsedNewUUIDs)
	if s.remover != nil {
		s.remover.RemoveFiles(plan.RemovedAssets)
	}
	result.Warnings = append(result.Warnings, skipWarnings...)
	return result, nil
}
