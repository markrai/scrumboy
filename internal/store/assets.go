package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"scrumboy/internal/assetrefs"
)

// ProjectAsset is the metadata row for one immutable image file owned by a project.
type ProjectAsset struct {
	ID             int64
	UUID           string
	ProjectID      int64
	UploaderUserID *int64
	MimeType       string
	SizeBytes      int64
	Width          int
	Height         int
	SHA256         string
	OriginalName   string
	CreatedAt      time.Time
	// UnreferencedAt is nil while at least one story references the asset.
	UnreferencedAt *time.Time
}

// NewProjectAsset is the input for inserting an asset row whose file is already on disk.
type NewProjectAsset struct {
	UUID           string
	ProjectID      int64
	UploaderUserID *int64
	MimeType       string
	SizeBytes      int64
	Width          int
	Height         int
	SHA256         string
	OriginalName   string
}

// AssetFileRef identifies one asset file on disk.
type AssetFileRef struct {
	UUID     string
	MimeType string
}

const maxAssetOriginalNameBytes = 255

// AssetExtensionForMIME maps an allowed asset MIME type to its file extension.
func AssetExtensionForMIME(mime string) (string, bool) {
	switch mime {
	case "image/png":
		return "png", true
	case "image/jpeg":
		return "jpg", true
	case "image/gif":
		return "gif", true
	default:
		return "", false
	}
}

// IsCanonicalAssetUUID reports whether s is a lowercase canonical UUIDv4, the only form
// accepted for asset identities (same rule as the project_assets.uuid CHECK).
func IsCanonicalAssetUUID(s string) bool {
	return assetrefs.IsCanonicalUUID(s)
}

func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// SanitizeAssetOriginalName returns display-only name text: trimmed, control characters and path
// separators removed, truncated to 255 bytes on a rune boundary. It is never used for paths or headers.
func SanitizeAssetOriginalName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '/' || r == '\\' || r == 0 || unicode.IsControl(r) || r == utf8.RuneError {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if len(out) <= maxAssetOriginalNameBytes {
		return out
	}
	cut := maxAssetOriginalNameBytes
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return strings.TrimSpace(out[:cut])
}

func validateNewProjectAsset(in NewProjectAsset) error {
	if !IsCanonicalAssetUUID(in.UUID) {
		return fmt.Errorf("%w: invalid asset uuid", ErrValidation)
	}
	if _, ok := AssetExtensionForMIME(in.MimeType); !ok {
		return fmt.Errorf("%w: unsupported asset type", ErrValidation)
	}
	if in.ProjectID <= 0 || in.SizeBytes <= 0 || in.Width <= 0 || in.Height <= 0 {
		return fmt.Errorf("%w: invalid asset metadata", ErrValidation)
	}
	if !isLowerHex64(in.SHA256) {
		return fmt.Errorf("%w: invalid asset sha256", ErrValidation)
	}
	return nil
}

// insertProjectAssetTx inserts an asset row; the file must already be fully written.
// The new asset starts unreferenced (unreferenced_at = nowMs), which starts its draft window.
func insertProjectAssetTx(ctx context.Context, tx *sql.Tx, in NewProjectAsset, nowMs int64) (ProjectAsset, error) {
	if err := validateNewProjectAsset(in); err != nil {
		return ProjectAsset{}, err
	}
	name := SanitizeAssetOriginalName(in.OriginalName)
	var uploader any
	if in.UploaderUserID != nil {
		uploader = *in.UploaderUserID
	}
	res, err := tx.ExecContext(ctx, `
INSERT INTO project_assets(uuid, project_id, uploader_user_id, mime_type, size_bytes, width, height, sha256, original_name, created_at, unreferenced_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.UUID, in.ProjectID, uploader, in.MimeType, in.SizeBytes, in.Width, in.Height, in.SHA256, name, nowMs, nowMs)
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "UNIQUE constraint failed"):
			return ProjectAsset{}, fmt.Errorf("%w: asset uuid already exists", ErrConflict)
		case strings.Contains(msg, "CHECK constraint failed"):
			return ProjectAsset{}, fmt.Errorf("%w: invalid asset metadata", ErrValidation)
		case strings.Contains(msg, "FOREIGN KEY constraint failed"):
			return ProjectAsset{}, ErrNotFound
		}
		return ProjectAsset{}, fmt.Errorf("insert project asset: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ProjectAsset{}, fmt.Errorf("project asset id: %w", err)
	}
	created := time.UnixMilli(nowMs).UTC()
	return ProjectAsset{
		ID:             id,
		UUID:           in.UUID,
		ProjectID:      in.ProjectID,
		UploaderUserID: cloneInt64Ptr(in.UploaderUserID),
		MimeType:       in.MimeType,
		SizeBytes:      in.SizeBytes,
		Width:          in.Width,
		Height:         in.Height,
		SHA256:         in.SHA256,
		OriginalName:   name,
		CreatedAt:      created,
		UnreferencedAt: &created,
	}, nil
}

const projectAssetColumns = `id, uuid, project_id, uploader_user_id, mime_type, size_bytes, width, height, sha256, original_name, created_at, unreferenced_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProjectAsset(row rowScanner) (ProjectAsset, error) {
	var (
		a         ProjectAsset
		uploader  sql.NullInt64
		createdMs int64
		unrefMs   sql.NullInt64
	)
	if err := row.Scan(&a.ID, &a.UUID, &a.ProjectID, &uploader, &a.MimeType, &a.SizeBytes, &a.Width, &a.Height, &a.SHA256, &a.OriginalName, &createdMs, &unrefMs); err != nil {
		return ProjectAsset{}, err
	}
	if uploader.Valid {
		v := uploader.Int64
		a.UploaderUserID = &v
	}
	a.CreatedAt = time.UnixMilli(createdMs).UTC()
	if unrefMs.Valid {
		t := time.UnixMilli(unrefMs.Int64).UTC()
		a.UnreferencedAt = &t
	}
	return a, nil
}

func getProjectAssetByUUIDTx(ctx context.Context, tx *sql.Tx, uuid string) (ProjectAsset, error) {
	if !IsCanonicalAssetUUID(uuid) {
		return ProjectAsset{}, ErrNotFound
	}
	a, err := scanProjectAsset(tx.QueryRowContext(ctx, `SELECT `+projectAssetColumns+` FROM project_assets WHERE uuid = ?`, uuid))
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectAsset{}, ErrNotFound
	}
	if err != nil {
		return ProjectAsset{}, fmt.Errorf("get project asset: %w", err)
	}
	return a, nil
}

// GetProjectAssetByUUID returns an asset row regardless of project state. Callers that serve
// content must use GetAssetForServe, which applies project-state rules.
func (s *Store) GetProjectAssetByUUID(ctx context.Context, uuid string) (ProjectAsset, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ProjectAsset{}, fmt.Errorf("begin get project asset: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return getProjectAssetByUUIDTx(ctx, tx, uuid)
}

// listProjectAssetUUIDsTx captures the files owned by the given projects. Deleting transactions call
// it before deleting the projects so the files can be removed after commit.
func listProjectAssetUUIDsTx(ctx context.Context, tx *sql.Tx, projectIDs []int64) ([]AssetFileRef, error) {
	var out []AssetFileRef
	for start := 0; start < len(projectIDs); start += sqlInChunk {
		chunk := projectIDs[start:min(start+sqlInChunk, len(projectIDs))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := tx.QueryContext(ctx, `SELECT uuid, mime_type FROM project_assets WHERE project_id IN (`+sqlPlaceholders(len(chunk))+`) ORDER BY id`, args...)
		if err != nil {
			return nil, fmt.Errorf("list project asset files: %w", err)
		}
		for rows.Next() {
			var ref AssetFileRef
			if err := rows.Scan(&ref.UUID, &ref.MimeType); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan project asset file: %w", err)
			}
			out = append(out, ref)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate project asset files: %w", err)
		}
		_ = rows.Close()
	}
	return out, nil
}

// sqlInChunk bounds the number of bound parameters in one IN (...) list.
const sqlInChunk = 500

func sqlPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// exportAssetsForProject lists the assets referenced by at least one story of the project
// (archived stories included, unreferenced drafts excluded), in id order.
func (s *Store) exportAssetsForProject(ctx context.Context, projectID int64) ([]AssetExport, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT pa.uuid, pa.mime_type, pa.size_bytes, pa.width, pa.height, pa.sha256, pa.original_name
FROM project_assets pa
WHERE pa.project_id = ?
  AND EXISTS (SELECT 1 FROM todo_assets ta JOIN todos t ON t.id = ta.todo_id WHERE ta.asset_id = pa.id AND t.project_id = pa.project_id)
ORDER BY pa.id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("export assets for project %d: %w", projectID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []AssetExport
	for rows.Next() {
		var a AssetExport
		if err := rows.Scan(&a.ID, &a.MimeType, &a.SizeBytes, &a.Width, &a.Height, &a.SHA256, &a.Name); err != nil {
			return nil, fmt.Errorf("scan export asset: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export assets for project %d: %w", projectID, err)
	}
	return out, nil
}

// ExportHasAssets reports whether any exported project carries assets.
func ExportHasAssets(data *ExportData) bool {
	if data == nil {
		return false
	}
	for _, p := range data.Projects {
		if len(p.Assets) > 0 {
			return true
		}
	}
	return false
}
