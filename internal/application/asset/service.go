package asset

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"scrumboy/internal/assets/blobstore"
	"scrumboy/internal/assets/imagecheck"
	"scrumboy/internal/store"
)

// ErrBusy reports that the server is already holding the maximum number of upload bodies.
var ErrBusy = errors.New("asset: too many concurrent uploads")

// ServiceStore is the store surface the upload service needs.
type ServiceStore interface {
	AuthorizeAssetUpload(ctx context.Context, projectID int64, todoLocalID *int64) error
	FindProjectAssetBySHA(ctx context.Context, projectID int64, sha string) (store.ProjectAsset, bool, error)
	CheckAssetBudget(ctx context.Context, projectID, addBytes int64) error
	CreateProjectAsset(ctx context.Context, in store.CreateProjectAssetInput) (store.ProjectAsset, bool, error)
	GetAssetForServe(ctx context.Context, uuid string) (store.AssetServeInfo, error)
}

// Service validates and stores uploaded note images.
type Service struct {
	store   ServiceStore
	blobs   *blobstore.Store
	pending *blobstore.Pending
	limits  imagecheck.Limits
	// readSlots bounds request bodies held in memory (each at most limits.MaxBytes).
	readSlots chan struct{}
	// decodeSlots bounds concurrent full decodes (see imagecheck.Limits for memory sizing).
	decodeSlots chan struct{}
}

// ServiceOptions configures a Service.
type ServiceOptions struct {
	Store       ServiceStore
	Blobs       *blobstore.Store
	Pending     *blobstore.Pending
	Limits      *imagecheck.Limits
	ReadSlots   int // default 8
	DecodeSlots int // default 2
}

// NewService constructs a Service.
func NewService(opts ServiceOptions) *Service {
	limits := imagecheck.DefaultLimits
	if opts.Limits != nil {
		limits = *opts.Limits
	}
	readSlots, decodeSlots := opts.ReadSlots, opts.DecodeSlots
	if readSlots == 0 {
		readSlots = 8
	}
	if decodeSlots <= 0 {
		decodeSlots = 2
	}
	if readSlots < 0 {
		readSlots = 0
	}
	return &Service{
		store:       opts.Store,
		blobs:       opts.Blobs,
		pending:     opts.Pending,
		limits:      limits,
		readSlots:   make(chan struct{}, readSlots),
		decodeSlots: make(chan struct{}, decodeSlots),
	}
}

// Limits returns the image limits in force.
func (s *Service) Limits() imagecheck.Limits { return s.limits }

// AcquireRead reserves a slot for reading one request body without blocking. The returned
// release func must be called when the body is no longer held.
func (s *Service) AcquireRead() (func(), error) {
	select {
	case s.readSlots <- struct{}{}:
		return func() { <-s.readSlots }, nil
	default:
		return nil, ErrBusy
	}
}

// Validate runs the image validator under the shared decode semaphore (also used by imports).
func (s *Service) Validate(ctx context.Context, data []byte) (imagecheck.Result, error) {
	select {
	case s.decodeSlots <- struct{}{}:
	case <-ctx.Done():
		return imagecheck.Result{}, ctx.Err()
	}
	defer func() { <-s.decodeSlots }()
	return imagecheck.Validate(data, s.limits)
}

// Upload validates data and stores it as an asset of projectID. The file is fully written before
// the row is inserted; any failure after the write removes the file. deduped reports that an
// identical existing asset of the project was returned instead.
func (s *Service) Upload(ctx context.Context, projectID int64, todoLocalID *int64, originalName string, data []byte) (store.ProjectAsset, bool, error) {
	res, err := s.Validate(ctx, data)
	if err != nil {
		return store.ProjectAsset{}, false, err
	}
	if existing, ok, err := s.store.FindProjectAssetBySHA(ctx, projectID, res.SHA256); err != nil {
		return store.ProjectAsset{}, false, err
	} else if ok {
		// Re-run the authoritative path so authorization and the draft refresh still apply.
		a, _, err := s.store.CreateProjectAsset(ctx, store.CreateProjectAssetInput{
			UUID: existing.UUID, ProjectID: projectID, TodoLocalID: todoLocalID, MimeType: existing.MimeType,
			SizeBytes: existing.SizeBytes, Width: existing.Width, Height: existing.Height, SHA256: existing.SHA256, OriginalName: originalName,
		})
		return a, true, err
	}
	if err := s.store.CheckAssetBudget(ctx, projectID, int64(len(res.Data))); err != nil {
		return store.ProjectAsset{}, false, err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return store.ProjectAsset{}, false, fmt.Errorf("asset uuid: %w", err)
	}
	assetUUID := id.String()
	release := s.pending.Hold(assetUUID)
	defer release()
	if err := s.blobs.CreateExclusive(assetUUID, res.Ext, res.Data); err != nil {
		return store.ProjectAsset{}, false, err
	}
	a, deduped, err := s.store.CreateProjectAsset(ctx, store.CreateProjectAssetInput{
		UUID:         assetUUID,
		ProjectID:    projectID,
		TodoLocalID:  todoLocalID,
		MimeType:     res.MIME,
		SizeBytes:    int64(len(res.Data)),
		Width:        res.Width,
		Height:       res.Height,
		SHA256:       res.SHA256,
		OriginalName: originalName,
	})
	if err != nil || deduped {
		_ = s.blobs.Remove(assetUUID, res.Ext)
	}
	if err != nil {
		return store.ProjectAsset{}, false, err
	}
	return a, deduped, nil
}
