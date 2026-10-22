package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrStorageBudget reports that storing an asset would exceed a configured byte budget.
var ErrStorageBudget = errors.New("storage budget exceeded")

// StorageBudgetError carries which budget would be exceeded.
type StorageBudgetError struct {
	Scope      string // "project" or "instance"
	LimitBytes int64
	UsedBytes  int64
}

func (e *StorageBudgetError) Error() string {
	return fmt.Sprintf("storage budget exceeded: %s budget %d bytes, %d used", e.Scope, e.LimitBytes, e.UsedBytes)
}

func (e *StorageBudgetError) Unwrap() error { return ErrStorageBudget }

// CreateProjectAssetInput describes an uploaded asset whose file is already fully written.
type CreateProjectAssetInput struct {
	UUID         string
	ProjectID    int64
	TodoLocalID  *int64
	MimeType     string
	SizeBytes    int64
	Width        int
	Height       int
	SHA256       string
	OriginalName string
}

// AssetServeInfo is an asset row plus the context needed to authorize serving it.
type AssetServeInfo struct {
	Asset       ProjectAsset
	ProjectSlug string
	// MemberRole is the requesting user's role on the asset's project ("" for none or no user).
	MemberRole ProjectRole
}

// AuthorizeAssetUpload is the read-only pre-check run before an upload body is read. The same
// rule is re-applied inside CreateProjectAsset's transaction, which is authoritative.
func (s *Store) AuthorizeAssetUpload(ctx context.Context, projectID int64, todoLocalID *int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin authorize asset upload: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = s.authorizeAssetUploadTx(ctx, tx, projectID, todoLocalID)
	return err
}

// authorizeAssetUploadTx applies the upload rule: an authenticated user; a durable, non-staging
// project; and either edit scope on the named non-archived story, or story-creation rights.
// Nonmembers get ErrNotFound so existence is not revealed.
func (s *Store) authorizeAssetUploadTx(ctx context.Context, tx *sql.Tx, projectID int64, todoLocalID *int64) (int64, error) {
	userID, ok := UserIDFromContext(ctx)
	if !ok || userID == 0 {
		return 0, ErrUnauthorized
	}
	var durable bool
	err := tx.QueryRowContext(ctx, `SELECT expires_at IS NULL AND import_batch_id IS NULL FROM projects WHERE id = ?`, projectID).Scan(&durable)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !durable) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("authorize asset upload: %w", err)
	}
	role, err := s.getProjectRoleTx(ctx, tx, projectID, userID)
	if err != nil {
		return 0, err
	}
	if role == "" {
		return 0, ErrNotFound
	}
	if todoLocalID == nil {
		if !CanCreateTodo(role) {
			return 0, ErrForbidden
		}
		return userID, nil
	}
	todoID, err := getTodoIDByLocalIDTx(ctx, tx, projectID, *todoLocalID)
	if err != nil {
		return 0, err
	}
	todo, err := getTodoTx(ctx, tx, todoID)
	if err != nil {
		return 0, err
	}
	if GetTodoEditScope(role, userID, &todo) == TodoEditNone {
		return 0, ErrForbidden
	}
	if todo.ArchivedAt != nil {
		return 0, todoArchivedError()
	}
	return userID, nil
}

// AssetUsage returns committed asset bytes for the project and for the instance.
func (s *Store) AssetUsage(ctx context.Context, projectID int64) (int64, int64, error) {
	var project, instance int64
	if err := s.db.QueryRowContext(ctx, `
SELECT COALESCE((SELECT SUM(size_bytes) FROM project_assets WHERE project_id = ?), 0),
       COALESCE((SELECT SUM(size_bytes) FROM project_assets), 0)`, projectID).Scan(&project, &instance); err != nil {
		return 0, 0, fmt.Errorf("asset usage: %w", err)
	}
	return project, instance, nil
}

// CheckAssetBudget reports ErrStorageBudget if adding addBytes would exceed a budget (advisory
// pre-check; checkAssetBudgetTx inside the insert transaction is authoritative).
func (s *Store) CheckAssetBudget(ctx context.Context, projectID, addBytes int64) error {
	project, instance, err := s.AssetUsage(ctx, projectID)
	if err != nil {
		return err
	}
	return s.assetBudgets.check(project, instance, addBytes)
}

func (b AssetBudgets) check(projectUsed, instanceUsed, addBytes int64) error {
	if b.ProjectBytes > 0 && projectUsed+addBytes > b.ProjectBytes {
		return &StorageBudgetError{Scope: "project", LimitBytes: b.ProjectBytes, UsedBytes: projectUsed}
	}
	if b.InstanceBytes > 0 && instanceUsed+addBytes > b.InstanceBytes {
		return &StorageBudgetError{Scope: "instance", LimitBytes: b.InstanceBytes, UsedBytes: instanceUsed}
	}
	return nil
}

// checkAssetBudgetTx enforces the budgets inside a write transaction. With SQLite's single
// connection all write transactions serialize, so concurrent inserts cannot both pass a check
// that only one of them fits.
func (s *Store) checkAssetBudgetTx(ctx context.Context, tx *sql.Tx, projectID, addBytes int64) error {
	if s.assetBudgets.ProjectBytes <= 0 && s.assetBudgets.InstanceBytes <= 0 {
		return nil
	}
	var project, instance int64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE((SELECT SUM(size_bytes) FROM project_assets WHERE project_id = ?), 0),
       COALESCE((SELECT SUM(size_bytes) FROM project_assets), 0)`, projectID).Scan(&project, &instance); err != nil {
		return fmt.Errorf("asset budget: %w", err)
	}
	return s.assetBudgets.check(project, instance, addBytes)
}

// findProjectAssetBySHATx returns the oldest asset in the project with these bytes, if any.
func findProjectAssetBySHATx(ctx context.Context, tx *sql.Tx, projectID int64, sha string) (ProjectAsset, bool, error) {
	a, err := scanProjectAsset(tx.QueryRowContext(ctx, `SELECT `+projectAssetColumns+` FROM project_assets WHERE project_id = ? AND sha256 = ? ORDER BY id LIMIT 1`, projectID, sha))
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectAsset{}, false, nil
	}
	if err != nil {
		return ProjectAsset{}, false, fmt.Errorf("find asset by sha256: %w", err)
	}
	return a, true, nil
}

// refreshDedupedAssetTx restarts the draft window of an unreferenced asset reused by a new
// upload or import of the same bytes, so the reuse cannot be collected moments later.
func refreshDedupedAssetTx(ctx context.Context, tx *sql.Tx, a *ProjectAsset, nowMs int64) error {
	if a.UnreferencedAt == nil {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project_assets SET unreferenced_at = ? WHERE id = ? AND unreferenced_at IS NOT NULL`, nowMs, a.ID); err != nil {
		return fmt.Errorf("refresh deduped asset: %w", err)
	}
	t := time.UnixMilli(nowMs).UTC()
	a.UnreferencedAt = &t
	return nil
}

// FindProjectAssetBySHA is the read-only dedupe pre-check (skips writing a duplicate file).
func (s *Store) FindProjectAssetBySHA(ctx context.Context, projectID int64, sha string) (ProjectAsset, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ProjectAsset{}, false, fmt.Errorf("begin find asset: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return findProjectAssetBySHATx(ctx, tx, projectID, sha)
}

// CreateProjectAsset re-authorizes the upload, dedupes by content within the project, enforces
// the storage budgets, and inserts the row. deduped reports that an existing asset was returned
// (the caller then removes the file it wrote).
func (s *Store) CreateProjectAsset(ctx context.Context, in CreateProjectAssetInput) (ProjectAsset, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return ProjectAsset{}, false, fmt.Errorf("begin create asset: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := serializeProjectWriteTx(ctx, tx, in.ProjectID); err != nil {
		return ProjectAsset{}, false, err
	}
	userID, err := s.authorizeAssetUploadTx(ctx, tx, in.ProjectID, in.TodoLocalID)
	if err != nil {
		return ProjectAsset{}, false, err
	}
	nowMs := time.Now().UTC().UnixMilli()
	if existing, ok, err := findProjectAssetBySHATx(ctx, tx, in.ProjectID, in.SHA256); err != nil {
		return ProjectAsset{}, false, err
	} else if ok {
		if err := refreshDedupedAssetTx(ctx, tx, &existing, nowMs); err != nil {
			return ProjectAsset{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return ProjectAsset{}, false, fmt.Errorf("commit create asset: %w", err)
		}
		return existing, true, nil
	}
	if err := s.checkAssetBudgetTx(ctx, tx, in.ProjectID, in.SizeBytes); err != nil {
		return ProjectAsset{}, false, err
	}
	a, err := insertProjectAssetTx(ctx, tx, NewProjectAsset{
		UUID:           in.UUID,
		ProjectID:      in.ProjectID,
		UploaderUserID: &userID,
		MimeType:       in.MimeType,
		SizeBytes:      in.SizeBytes,
		Width:          in.Width,
		Height:         in.Height,
		SHA256:         in.SHA256,
		OriginalName:   in.OriginalName,
	}, nowMs)
	if err != nil {
		return ProjectAsset{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ProjectAsset{}, false, fmt.Errorf("commit create asset: %w", err)
	}
	return a, false, nil
}

// GetAssetForServe returns the asset and its project slug, plus the requesting user's role. It
// returns ErrNotFound unless the asset exists and its project is durable and not staging, so
// staging or temporary assets are never served to anyone.
func (s *Store) GetAssetForServe(ctx context.Context, uuid string) (AssetServeInfo, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AssetServeInfo{}, fmt.Errorf("begin get asset for serve: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	a, err := getProjectAssetByUUIDTx(ctx, tx, uuid)
	if err != nil {
		return AssetServeInfo{}, err
	}
	var slug sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT slug FROM projects WHERE id = ? AND expires_at IS NULL AND import_batch_id IS NULL`, a.ProjectID).Scan(&slug)
	if errors.Is(err, sql.ErrNoRows) {
		return AssetServeInfo{}, ErrNotFound
	}
	if err != nil {
		return AssetServeInfo{}, fmt.Errorf("get asset project: %w", err)
	}
	info := AssetServeInfo{Asset: a, ProjectSlug: slug.String}
	if userID, ok := UserIDFromContext(ctx); ok && userID != 0 {
		role, err := s.getProjectRoleTx(ctx, tx, a.ProjectID, userID)
		if err != nil {
			return AssetServeInfo{}, err
		}
		info.MemberRole = role
	}
	return info, nil
}

// IsAssetPubliclyReferenced re-checks public eligibility of the project inside the read and
// reports whether a non-archived story of that project references the asset.
func (s *Store) IsAssetPubliclyReferenced(ctx context.Context, projectID int64, expectedSlug string, assetID int64) (bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, fmt.Errorf("begin public asset read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := loadEligiblePublicProjectTx(ctx, tx, projectID, expectedSlug); err != nil {
		return false, err
	}
	var ok bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM todo_assets ta
  JOIN todos t ON t.id = ta.todo_id
  WHERE ta.asset_id = ? AND t.project_id = ? AND t.archived_at IS NULL
)`, assetID, projectID).Scan(&ok); err != nil {
		return false, fmt.Errorf("public asset reference: %w", err)
	}
	return ok, nil
}

// CheckImportAssetBudget is the advisory pre-check for an archive import that adds perProject
// bytes to new or existing projects (already-stored identical bytes excluded by the caller). The
// in-transaction checks during the import are authoritative.
func (s *Store) CheckImportAssetBudget(ctx context.Context, perProject []int64) error {
	var total int64
	for _, n := range perProject {
		if s.assetBudgets.ProjectBytes > 0 && n > s.assetBudgets.ProjectBytes {
			return &StorageBudgetError{Scope: "project", LimitBytes: s.assetBudgets.ProjectBytes}
		}
		total += n
	}
	_, instance, err := s.AssetUsage(ctx, 0)
	if err != nil {
		return err
	}
	if s.assetBudgets.InstanceBytes > 0 && instance+total > s.assetBudgets.InstanceBytes {
		return &StorageBudgetError{Scope: "instance", LimitBytes: s.assetBudgets.InstanceBytes, UsedBytes: instance}
	}
	return nil
}

// AssetSHAExists reports whether any stored asset has these bytes (an import may dedupe to it).
func (s *Store) AssetSHAExists(ctx context.Context, sha string) (bool, error) {
	var ok bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM project_assets WHERE sha256 = ?)`, sha).Scan(&ok); err != nil {
		return false, fmt.Errorf("asset sha exists: %w", err)
	}
	return ok, nil
}
