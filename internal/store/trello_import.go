package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"scrumboy/internal/version"
)

func (s *Store) ImportTrelloProject(ctx context.Context, data *ExportData, projectImportMetadata string, todoImportMetadataByLocalID map[int64]string, mode Mode) (Project, error) {
	if data == nil || len(data.Projects) != 1 {
		return Project{}, fmt.Errorf("%w: Trello import requires exactly one project payload", ErrValidation)
	}
	if data.Version != version.ExportFormatVersion {
		return Project{}, fmt.Errorf("%w: unsupported export version %q (expected %s)", ErrValidation, data.Version, version.ExportFormatVersion)
	}
	if err := s.validateImportPreflight(ctx, data, mode, "copy"); err != nil {
		return Project{}, err
	}

	projectExport := data.Projects[0]
	now := time.Now().UTC()
	nowMs := now.UnixMilli()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return Project{}, fmt.Errorf("begin Trello import tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	newProjectID, projectRow, err := s.insertTrelloProjectRow(ctx, tx, projectExport, projectImportMetadata, mode, nowMs)
	if err != nil {
		return Project{}, err
	}

	if len(projectExport.WorkflowColumns) >= 2 {
		if err := s.insertWorkflowColumnsExec(ctx, tx, newProjectID, workflowColumnsFromExport(projectExport.WorkflowColumns)); err != nil {
			return Project{}, fmt.Errorf("insert Trello workflow columns: %w", err)
		}
	} else {
		if err := s.ensureDefaultWorkflowColumnsExec(ctx, tx, tx, newProjectID); err != nil {
			return Project{}, fmt.Errorf("ensure default workflow columns for Trello import: %w", err)
		}
	}

	// Trello has no native priority concept: always seed default tiers, imported todos get no priority.
	if err := s.ensureDefaultPriorityTiersExec(ctx, tx, newProjectID); err != nil {
		return Project{}, fmt.Errorf("ensure default priority tiers for Trello import: %w", err)
	}

	usedTodoMetadata := make(map[int64]struct{}, len(todoImportMetadataByLocalID))
	for _, todoExport := range projectExport.Todos {
		if err := validateEstimationPoints(todoExport.EstimationPoints); err != nil {
			return Project{}, err
		}
		columnKey, warned := resolveImportColumnKey(ctx, tx, newProjectID, todoExport.Status)
		if warned {
			return Project{}, fmt.Errorf("%w: todo %q resolved to an unknown workflow column", ErrValidation, todoExport.Title)
		}

		createdAtMs := todoExport.CreatedAt.UnixMilli()
		updatedAtMs := todoExport.UpdatedAt.UnixMilli()
		status := StatusBacklog
		if parsed, ok := ParseStatus(todoExport.Status); ok {
			status = parsed
		}

		var estimationPoints any
		if todoExport.EstimationPoints != nil {
			estimationPoints = *todoExport.EstimationPoints
		}

		var todoImportMetadata any
		if todoImportMetadataByLocalID != nil {
			if metadata, ok := todoImportMetadataByLocalID[todoExport.LocalID]; ok {
				usedTodoMetadata[todoExport.LocalID] = struct{}{}
				todoImportMetadata = metadata
			}
		}

		res, err := tx.ExecContext(ctx, `
			INSERT INTO todos(project_id, local_id, title, body, column_key, rank, estimation_points, assignee_user_id, sprint_id, created_at, updated_at, done_at, import_metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?, NULL, NULL, ?, ?, ?, ?)`,
			newProjectID,
			todoExport.LocalID,
			todoExport.Title,
			todoExport.Body,
			columnKey,
			todoExport.Rank,
			estimationPoints,
			createdAtMs,
			updatedAtMs,
			resolveImportDoneAt(todoExport.DoneAt, status, updatedAtMs),
			todoImportMetadata,
		)
		if err != nil {
			return Project{}, fmt.Errorf("insert Trello todo %q: %w", todoExport.Title, err)
		}
		todoID, err := res.LastInsertId()
		if err != nil {
			return Project{}, fmt.Errorf("last insert id for Trello todo %q: %w", todoExport.Title, err)
		}
		if err := s.importTodoTags(ctx, tx, newProjectID, todoID, todoExport.Tags, mode); err != nil {
			return Project{}, fmt.Errorf("import Trello todo tags for %q: %w", todoExport.Title, err)
		}
	}

	for localID := range todoImportMetadataByLocalID {
		if _, ok := usedTodoMetadata[localID]; ok {
			continue
		}
		return Project{}, fmt.Errorf("Trello todo metadata for local id %d does not match any imported todo", localID)
	}

	for _, tagExport := range projectExport.Tags {
		if err := s.importTag(ctx, tx, newProjectID, tagExport.Name, tagExport.Color, mode); err != nil {
			return Project{}, fmt.Errorf("import Trello tag %q: %w", tagExport.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Project{}, fmt.Errorf("commit Trello import tx: %w", err)
	}
	return projectRow, nil
}

func (s *Store) insertTrelloProjectRow(ctx context.Context, tx *sql.Tx, projectExport ProjectExport, projectImportMetadata string, mode Mode, nowMs int64) (int64, Project, error) {
	name := strings.TrimSpace(projectExport.Name)
	if name == "" {
		return 0, Project{}, fmt.Errorf("%w: invalid project name", ErrValidation)
	}

	if mode == ModeAnonymous {
		return s.insertTrelloTemporaryProjectRow(ctx, tx, name, projectImportMetadata, nowMs)
	}
	return s.insertTrelloDurableProjectRow(ctx, tx, name, projectExport, projectImportMetadata, nowMs)
}

func (s *Store) insertTrelloDurableProjectRow(ctx context.Context, tx *sql.Tx, name string, projectExport ProjectExport, projectImportMetadata string, nowMs int64) (int64, Project, error) {
	enabled, err := authEnabledTx(ctx, tx)
	if err != nil {
		return 0, Project{}, err
	}
	var ownerUserID *int64
	if enabled {
		userID, ok := UserIDFromContext(ctx)
		if !ok {
			return 0, Project{}, ErrUnauthorized
		}
		ownerUserID = &userID
	}

	baseSlug, err := generateSlugFromName(name)
	if err != nil {
		baseSlug = "trello-board"
	}
	defaultImage := "/scrumboy.png"
	dominantColor := strings.TrimSpace(projectExport.DominantColor)
	if dominantColor == "" {
		dominantColor = "#888888"
	}

	var (
		projectID int64
		slug      string
	)
	for i := 0; i < 100; i++ {
		if i == 0 {
			slug = baseSlug
		} else {
			suffix := fmt.Sprintf("-%d", i+1)
			maxBaseLen := maxSlugLen - len(suffix)
			base := baseSlug
			if len(base) > maxBaseLen {
				base = strings.TrimRight(base[:maxBaseLen], "-")
			}
			if base == "" {
				base = "trello-board"
			}
			slug = base + suffix
		}
		exists, err := slugExistsTx(ctx, tx, slug)
		if err != nil {
			return 0, Project{}, fmt.Errorf("check Trello project slug: %w", err)
		}
		if exists {
			continue
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO projects(name, image, dominant_color, slug, estimation_mode, default_sprint_weeks, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at, import_metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, NULL, ?, ?, ?)`,
			name,
			defaultImage,
			dominantColor,
			slug,
			EstimationModeModifiedFibonacci,
			2,
			ownerUserID,
			nowMs,
			nowMs,
			nowMs,
			nullIfEmpty(projectImportMetadata),
		)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed: projects.slug") {
				continue
			}
			return 0, Project{}, fmt.Errorf("insert Trello project: %w", err)
		}
		projectID, err = res.LastInsertId()
		if err != nil {
			return 0, Project{}, fmt.Errorf("last insert id Trello project: %w", err)
		}
		if ownerUserID != nil {
			if _, err := tx.ExecContext(ctx, `
				INSERT OR IGNORE INTO project_members(project_id, user_id, role, created_at)
				VALUES (?, ?, ?, ?)`, projectID, *ownerUserID, RoleMaintainer, nowMs); err != nil {
				return 0, Project{}, fmt.Errorf("ensure Trello maintainer membership: %w", err)
			}
		}
		return projectID, Project{
			ID:                 projectID,
			Name:               name,
			Image:              &defaultImage,
			DominantColor:      dominantColor,
			EstimationMode:     EstimationModeModifiedFibonacci,
			DefaultSprintWeeks: 2,
			Slug:               slug,
			OwnerUserID:        ownerUserID,
			LastActivityAt:     time.UnixMilli(nowMs).UTC(),
			CreatedAt:          time.UnixMilli(nowMs).UTC(),
			UpdatedAt:          time.UnixMilli(nowMs).UTC(),
		}, nil
	}
	return 0, Project{}, fmt.Errorf("%w: could not generate unique slug after 100 attempts", ErrConflict)
}

func (s *Store) insertTrelloTemporaryProjectRow(ctx context.Context, tx *sql.Tx, name string, projectImportMetadata string, nowMs int64) (int64, Project, error) {
	defaultImage := "/scrumboy.png"
	expiresAtMs := temporaryBoardExpiresAtMs(nowMs)
	var creatorUserID *int64
	if userID, ok := UserIDFromContext(ctx); ok {
		creatorUserID = &userID
	}

	var projectID int64
	var slug string
	for i := 0; i < 100; i++ {
		random, err := randomSlug(8)
		if err != nil {
			return 0, Project{}, err
		}
		slug = random
		res, err := tx.ExecContext(ctx, `
			INSERT INTO projects(name, image, dominant_color, slug, estimation_mode, default_sprint_weeks, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at, import_metadata)
			VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?)`,
			name,
			defaultImage,
			"#888888",
			slug,
			EstimationModeModifiedFibonacci,
			2,
			creatorUserID,
			nowMs,
			expiresAtMs,
			nowMs,
			nowMs,
			nullIfEmpty(projectImportMetadata),
		)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed: projects.slug") {
				continue
			}
			return 0, Project{}, fmt.Errorf("insert Trello temporary board: %w", err)
		}
		projectID, err = res.LastInsertId()
		if err != nil {
			return 0, Project{}, fmt.Errorf("last insert id Trello temporary board: %w", err)
		}
		expiresAt := time.UnixMilli(expiresAtMs).UTC()
		return projectID, Project{
			ID:                 projectID,
			Name:               name,
			Image:              &defaultImage,
			DominantColor:      "#888888",
			EstimationMode:     EstimationModeModifiedFibonacci,
			DefaultSprintWeeks: 2,
			Slug:               slug,
			CreatorUserID:      creatorUserID,
			LastActivityAt:     time.UnixMilli(nowMs).UTC(),
			ExpiresAt:          &expiresAt,
			CreatedAt:          time.UnixMilli(nowMs).UTC(),
			UpdatedAt:          time.UnixMilli(nowMs).UTC(),
		}, nil
	}
	return 0, Project{}, fmt.Errorf("%w: could not generate temporary board slug after 100 attempts", ErrConflict)
}

func nullIfEmpty(v string) any {
	if strings.TrimSpace(v) == "" {
		return sql.NullString{}
	}
	return strings.TrimSpace(v)
}
