package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type CreateTodoInput struct {
	Title            string
	Body             string
	Tags             []string
	ColumnKey        string
	EstimationPoints *int64
	SprintID         *int64 // NULL = backlog
	AssigneeUserID   *int64 // NULL = unassigned
	AfterID          *int64
	BeforeID         *int64
}

func (s *Store) CreateTodo(ctx context.Context, projectID int64, in CreateTodoInput, mode Mode) (Todo, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || len(in.Title) > 200 {
		return Todo{}, fmt.Errorf("%w: invalid title", ErrValidation)
	}
	if len(in.Body) > 20000 {
		return Todo{}, fmt.Errorf("%w: body too large", ErrValidation)
	}
	if err := validateEstimationPoints(in.EstimationPoints); err != nil {
		return Todo{}, err
	}
	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return Todo{}, err
	}

	// Race-safety: acquire a write lock before reading MAX(local_id) by executing a write early,
	// and retry on UNIQUE(project_id, local_id) collisions.
	for attempt := 0; attempt < 10; attempt++ {
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
		if err != nil {
			return Todo{}, fmt.Errorf("begin create todo: %w", err)
		}

		p, err := s.getProjectForWriteTx(ctx, tx, projectID, mode)
		if err != nil {
			_ = tx.Rollback()
			return Todo{}, err
		}

		isAnonymousBoard := isAnonymousTemporaryBoard(p)

		if !isAnonymousBoard {
			enabled, err := authEnabledTx(ctx, tx)
			if err != nil {
				_ = tx.Rollback()
				return Todo{}, err
			}
			if enabled {
				userID, ok := UserIDFromContext(ctx)
				if !ok {
					_ = tx.Rollback()
					return Todo{}, ErrUnauthorized
				}
				role, err := s.getProjectRoleTx(ctx, tx, projectID, userID)
				if err != nil {
					_ = tx.Rollback()
					return Todo{}, err
				}
				if !CanCreateTodo(role) {
					_ = tx.Rollback()
					return Todo{}, ErrUnauthorized
				}
			}
		}

		if isAnonymousBoard && in.AssigneeUserID != nil {
			_ = tx.Rollback()
			return Todo{}, fmt.Errorf("%w: assignment is not allowed in anonymous mode", ErrValidation)
		}
		if !isAnonymousBoard && in.AssigneeUserID != nil {
			actorID, ok := UserIDFromContext(ctx)
			if !ok || actorID == 0 {
				_ = tx.Rollback()
				return Todo{}, ErrUnauthorized
			}
			role, err := s.getProjectRoleTx(ctx, tx, projectID, actorID)
			if err != nil || role == "" {
				_ = tx.Rollback()
				return Todo{}, ErrUnauthorized
			}
			var assigneeExists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`, *in.AssigneeUserID).Scan(&assigneeExists); err != nil {
				_ = tx.Rollback()
				return Todo{}, fmt.Errorf("check assignee exists: %w", err)
			}
			if !assigneeExists {
				_ = tx.Rollback()
				return Todo{}, fmt.Errorf("%w: assignee does not exist", ErrValidation)
			}
			var isMember bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM project_members WHERE project_id = ? AND user_id = ?)`, projectID, *in.AssigneeUserID).Scan(&isMember); err != nil {
				_ = tx.Rollback()
				return Todo{}, fmt.Errorf("check assignee membership: %w", err)
			}
			if !isMember {
				_ = tx.Rollback()
				return Todo{}, fmt.Errorf("%w: assignee is not a project member", ErrValidation)
			}
			if *in.AssigneeUserID == actorID {
				if role != RoleMaintainer {
					_ = tx.Rollback()
					return Todo{}, ErrUnauthorized
				}
			} else {
				if role != RoleMaintainer {
					_ = tx.Rollback()
					return Todo{}, ErrUnauthorized
				}
			}
		}

		// Get userID from context for tag ownership.
		// For anonymous boards: tags are board-scoped (no userID required)
		// For authenticated boards: tags are user-owned (userID required)
		userID, ok := UserIDFromContext(ctx)
		var userIDPtr *int64
		if !ok && len(tags) > 0 {
			if isAnonymousBoard {
				// Allow tags on anonymous boards without userID (board-scoped tags)
				userIDPtr = nil
			} else {
				// Require userID for authenticated boards
				enabled, err := authEnabledTx(ctx, tx)
				if err != nil {
					_ = tx.Rollback()
					return Todo{}, err
				}
				if enabled {
					_ = tx.Rollback()
					return Todo{}, fmt.Errorf("%w: userID required for tag operations", ErrUnauthorized)
				}
				// Pre-bootstrap (no users): tags are unsupported.
				tags = nil
				userIDPtr = nil
			}
		} else if ok {
			userIDPtr = &userID
		}

		// Write lock early (SQLite acquires write lock on first write in a deferred tx).
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at = updated_at WHERE id = ?`, projectID); err != nil {
			_ = tx.Rollback()
			return Todo{}, fmt.Errorf("lock project row: %w", err)
		}

		// Loud-fail if migration is incomplete (NULL local_id would defeat uniqueness).
		var nullCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM todos WHERE project_id = ? AND local_id IS NULL`, projectID).Scan(&nullCount); err != nil {
			_ = tx.Rollback()
			return Todo{}, fmt.Errorf("check null local_id: %w", err)
		}
		if nullCount != 0 {
			_ = tx.Rollback()
			return Todo{}, fmt.Errorf("%w: todos.local_id is NULL (migration incomplete)", ErrConflict)
		}

		var maxLocalID int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(local_id), 0) FROM todos WHERE project_id = ?`, projectID).Scan(&maxLocalID); err != nil {
			_ = tx.Rollback()
			return Todo{}, fmt.Errorf("select max local_id: %w", err)
		}
		localID := maxLocalID + 1

		if in.ColumnKey == "" {
			in.ColumnKey = DefaultColumnBacklog
		}
		targetCol, err := validateProjectColumnKeyTx(ctx, tx, projectID, in.ColumnKey)
		if err != nil {
			_ = tx.Rollback()
			return Todo{}, err
		}
		newRank, err := computeNewRank(ctx, tx, projectID, targetCol.Key, nil, in.AfterID, in.BeforeID)
		if err != nil {
			_ = tx.Rollback()
			return Todo{}, err
		}

		if in.SprintID != nil {
			var sprintProjectID int64
			if err := tx.QueryRowContext(ctx, `SELECT project_id FROM sprints WHERE id = ?`, *in.SprintID).Scan(&sprintProjectID); err != nil {
				_ = tx.Rollback()
				if errors.Is(err, sql.ErrNoRows) {
					return Todo{}, fmt.Errorf("%w: sprint not found", ErrValidation)
				}
				return Todo{}, fmt.Errorf("validate sprint: %w", err)
			}
			if sprintProjectID != projectID {
				_ = tx.Rollback()
				return Todo{}, fmt.Errorf("%w: sprint does not belong to project", ErrValidation)
			}
		}

		nowMs := time.Now().UTC().UnixMilli()
		var estimationPoints any
		if in.EstimationPoints != nil {
			estimationPoints = *in.EstimationPoints
		}
		var sprintIDArg any
		if in.SprintID != nil {
			sprintIDArg = *in.SprintID
		}
		doneAt := resolveDoneAtForColumnTransition(false, targetCol.IsDone, nowMs)
		var doneAtArg any = nil
		if doneAt != nil {
			doneAtArg = *doneAt
		}
		var assigneeArg any = nil
		if in.AssigneeUserID != nil {
			assigneeArg = *in.AssigneeUserID
		}
		res, err := tx.ExecContext(ctx, `
INSERT INTO todos(project_id, local_id, title, body, column_key, rank, estimation_points, assignee_user_id, sprint_id, created_at, updated_at, done_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			projectID, localID, in.Title, in.Body, in.ColumnKey, newRank, estimationPoints, assigneeArg, sprintIDArg, nowMs, nowMs, doneAtArg,
		)
		if err != nil {
			// Retry on unique collision for (project_id, local_id).
			if strings.Contains(err.Error(), "UNIQUE constraint failed: todos.project_id, todos.local_id") ||
				strings.Contains(err.Error(), "idx_todos_project_local_id") {
				_ = tx.Rollback()
				continue
			}
			_ = tx.Rollback()
			return Todo{}, fmt.Errorf("insert todo: %w", err)
		}
		todoID, err := res.LastInsertId()
		if err != nil {
			_ = tx.Rollback()
			return Todo{}, fmt.Errorf("last insert id todo: %w", err)
		}

		if userIDPtr != nil || isAnonymousBoard {
			if err := setTodoTags(ctx, tx, projectID, todoID, userIDPtr, isAnonymousBoard, tags); err != nil {
				_ = tx.Rollback()
				return Todo{}, err
			}
		}

		if err := touchProject(ctx, tx, projectID, nowMs); err != nil {
			_ = tx.Rollback()
			return Todo{}, err
		}

		meta := map[string]any{"local_id": localID, "column_key": in.ColumnKey, "title": in.Title}
		if err := insertAuditEventTx(ctx, tx, projectID, userIDPtr, "todo_created", "todo", &todoID, meta); err != nil {
			_ = tx.Rollback()
			return Todo{}, fmt.Errorf("audit todo_created: %w", err)
		}

		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			return Todo{}, fmt.Errorf("commit create todo: %w", err)
		}

		// Track activity: todo creation resets expiration
		if err := s.UpdateBoardActivity(ctx, projectID); err != nil {
			// Log but don't fail the request - activity tracking is best-effort
			_ = err
		}

		todo := Todo{
			ID:               todoID,
			ProjectID:        projectID,
			LocalID:          localID,
			Title:            in.Title,
			Body:             in.Body,
			ColumnKey:        in.ColumnKey,
			Rank:             newRank,
			EstimationPoints: cloneInt64Ptr(in.EstimationPoints),
			AssigneeUserID:   cloneInt64Ptr(in.AssigneeUserID),
			SprintID:         cloneInt64Ptr(in.SprintID),
			Tags:             tags,
			CreatedAt:        time.UnixMilli(nowMs).UTC(),
			UpdatedAt:        time.UnixMilli(nowMs).UTC(),
		}
		if doneAt != nil {
			t := time.UnixMilli(*doneAt).UTC()
			todo.DoneAt = &t
		}
		hadAssignee := in.AssigneeUserID != nil && !isAnonymousBoard
		todo.AssignmentChanged = hadAssignee
		if s.todoAssignedPublisher != nil && hadAssignee {
			actorID, _ := UserIDFromContext(ctx)
			s.todoAssignedPublisher(ctx, projectID, todoID, localID, nil, in.AssigneeUserID, actorID)
		}
		return todo, nil
	}

	return Todo{}, fmt.Errorf("%w: could not allocate local_id (too much contention)", ErrConflict)
}

type UpdateTodoInput struct {
	Title            string
	Body             string
	Tags             []string
	EstimationPoints *int64
	AssigneeUserID   *int64
	SprintID         *int64 // when nil, don't update; when non-nil, set to that sprint
	ClearSprint      bool   // when true, set sprint_id to NULL (backlog); overrides SprintID
}

// resolveDoneAtForColumnTransition returns the value to write for done_at.
// Set only when transitioning into done from non-done; never clear on reopen.
func resolveDoneAtForColumnTransition(previousDone, newDone bool, nowMs int64) *int64 {
	if newDone && !previousDone {
		return &nowMs
	}
	return nil
}

func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func sameInt64Ptr(a, b *int64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func cloneInt64Ptr(v *int64) *int64 {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

func validateEstimationPoints(v *int64) error {
	if v == nil {
		return nil
	}
	switch *v {
	case 1, 2, 3, 5, 8, 13, 20, 40:
		return nil
	default:
		return fmt.Errorf("%w: estimation points must be one of 1,2,3,5,8,13,20,40", ErrValidation)
	}
}

func (s *Store) UpdateTodo(ctx context.Context, todoID int64, in UpdateTodoInput, mode Mode) (Todo, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return Todo{}, fmt.Errorf("begin update todo: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := getTodoTx(ctx, tx, todoID)
	if err != nil {
		return Todo{}, err
	}
	p, err := s.getProjectForWriteTx(ctx, tx, existing.ProjectID, mode)
	if err != nil {
		return Todo{}, err
	}

	isAnonymousBoard := isAnonymousTemporaryBoard(p)
	if isAnonymousBoard && in.AssigneeUserID != nil {
		return Todo{}, fmt.Errorf("%w: assignment is not allowed in anonymous mode", ErrValidation)
	}

	userID, ok := UserIDFromContext(ctx)
	var actorRole ProjectRole
	if ok {
		role, err := s.getProjectRoleTx(ctx, tx, existing.ProjectID, userID)
		if err != nil {
			return Todo{}, err
		}
		actorRole = role
	}

	if !isAnonymousBoard {
		enabled, err := authEnabledTx(ctx, tx)
		if err != nil {
			return Todo{}, err
		}
		if enabled {
			switch GetTodoEditScope(actorRole, userID, &existing) {
			case TodoEditNone:
				return Todo{}, ErrUnauthorized
			case TodoEditBodyOnly:
				if len(in.Body) > 20000 {
					return Todo{}, fmt.Errorf("%w: body too large", ErrValidation)
				}
				nowMs := time.Now().UTC().UnixMilli()
				if _, err := tx.ExecContext(ctx, `UPDATE todos SET body = ?, updated_at = ? WHERE id = ?`,
					in.Body, nowMs, todoID); err != nil {
					return Todo{}, fmt.Errorf("update todo body: %w", err)
				}
				if err := touchProject(ctx, tx, existing.ProjectID, nowMs); err != nil {
					return Todo{}, err
				}
				if err := tx.Commit(); err != nil {
					return Todo{}, fmt.Errorf("commit update todo: %w", err)
				}
				existing.Body = in.Body
				existing.UpdatedAt = time.UnixMilli(nowMs).UTC()
				if err := s.UpdateBoardActivity(ctx, existing.ProjectID); err != nil {
					_ = err
				}
				return existing, nil
			case TodoEditFull:
				// fall through to full update path
			}
		}
	}

	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || len(in.Title) > 200 {
		return Todo{}, fmt.Errorf("%w: invalid title", ErrValidation)
	}
	if len(in.Body) > 20000 {
		return Todo{}, fmt.Errorf("%w: body too large", ErrValidation)
	}
	if err := validateEstimationPoints(in.EstimationPoints); err != nil {
		return Todo{}, err
	}
	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return Todo{}, err
	}

	assignmentChanged := !sameInt64Ptr(existing.AssigneeUserID, in.AssigneeUserID)
	if !isAnonymousBoard && assignmentChanged && !ok {
		return Todo{}, ErrUnauthorized
	}
	if assignmentChanged && (!ok || userID == 0) {
		return Todo{}, ErrUnauthorized
	}

	if !isAnonymousBoard && in.AssigneeUserID != nil {
		if actorRole == "" {
			return Todo{}, ErrUnauthorized
		}

		var assigneeExists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`, *in.AssigneeUserID).Scan(&assigneeExists); err != nil {
			return Todo{}, fmt.Errorf("check assignee exists: %w", err)
		}
		if !assigneeExists {
			return Todo{}, fmt.Errorf("%w: assignee does not exist", ErrValidation)
		}

		var isMember bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1
				FROM project_members
				WHERE project_id = ? AND user_id = ?
			)
		`, existing.ProjectID, *in.AssigneeUserID).Scan(&isMember); err != nil {
			return Todo{}, fmt.Errorf("check assignee membership: %w", err)
		}
		if !isMember {
			return Todo{}, fmt.Errorf("%w: assignee is not a project member", ErrValidation)
		}

		if *in.AssigneeUserID == userID {
			if actorRole != RoleMaintainer {
				return Todo{}, ErrUnauthorized
			}
		} else {
			if actorRole != RoleMaintainer {
				return Todo{}, ErrUnauthorized
			}
		}
	}

	if in.ClearSprint || in.SprintID != nil {
		if !actorRole.HasMinimumRole(RoleMaintainer) {
			return Todo{}, ErrUnauthorized
		}
		if in.SprintID != nil {
			var sprintProjectID int64
			if err := tx.QueryRowContext(ctx, `SELECT project_id FROM sprints WHERE id = ?`, *in.SprintID).Scan(&sprintProjectID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return Todo{}, fmt.Errorf("%w: sprint not found", ErrValidation)
				}
				return Todo{}, fmt.Errorf("validate sprint: %w", err)
			}
			if sprintProjectID != existing.ProjectID {
				return Todo{}, fmt.Errorf("%w: sprint does not belong to project", ErrValidation)
			}
		}
	}

	var userIDPtr *int64
	if !ok && len(tags) > 0 {
		if isAnonymousBoard {
			// Allow tags on anonymous boards without userID (board-scoped tags)
			userIDPtr = nil
		} else {
			// Require userID for authenticated boards
			enabled, err := authEnabledTx(ctx, tx)
			if err != nil {
				return Todo{}, err
			}
			if enabled {
				return Todo{}, fmt.Errorf("%w: userID required for tag operations", ErrUnauthorized)
			}
			// Pre-bootstrap (no users): tags are unsupported.
			tags = nil
			userIDPtr = nil
		}
	} else if ok {
		userIDPtr = &userID
	}

	var assigneeValue any
	if in.AssigneeUserID != nil {
		assigneeValue = *in.AssigneeUserID
	} else {
		assigneeValue = nil
	}
	updateSprint := in.ClearSprint || in.SprintID != nil
	var sprintIDValue any
	if in.ClearSprint {
		sprintIDValue = nil
	} else if in.SprintID != nil {
		sprintIDValue = *in.SprintID
	}
	var estimationPoints any
	if in.EstimationPoints != nil {
		estimationPoints = *in.EstimationPoints
	}

	nowMs := time.Now().UTC().UnixMilli()
	if updateSprint {
		if _, err := tx.ExecContext(ctx, `
			UPDATE todos
			SET title = ?, body = ?, assignee_user_id = ?, estimation_points = ?, sprint_id = ?, updated_at = ?
			WHERE id = ?
		`, in.Title, in.Body, assigneeValue, estimationPoints, sprintIDValue, nowMs, todoID); err != nil {
			return Todo{}, fmt.Errorf("update todo: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE todos
			SET title = ?, body = ?, assignee_user_id = ?, estimation_points = ?, updated_at = ?
			WHERE id = ?
		`, in.Title, in.Body, assigneeValue, estimationPoints, nowMs, todoID); err != nil {
			return Todo{}, fmt.Errorf("update todo: %w", err)
		}
	}

	if assignmentChanged {
		var fromAssignee any
		if existing.AssigneeUserID != nil {
			fromAssignee = *existing.AssigneeUserID
		} else {
			fromAssignee = nil
		}
		var toAssignee any
		if in.AssigneeUserID != nil {
			toAssignee = *in.AssigneeUserID
		} else {
			toAssignee = nil
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO todo_assignee_events(
				project_id,
				todo_id,
				actor_user_id,
				from_assignee_user_id,
				to_assignee_user_id,
				reason,
				created_at
			)
			VALUES (?, ?, ?, ?, ?, NULL, ?)
		`, existing.ProjectID, todoID, userID, fromAssignee, toAssignee, nowMs); err != nil {
			return Todo{}, fmt.Errorf("insert todo assignee event: %w", err)
		}
	}

	if userIDPtr != nil || isAnonymousBoard {
		if err := setTodoTags(ctx, tx, existing.ProjectID, todoID, userIDPtr, isAnonymousBoard, tags); err != nil {
			return Todo{}, err
		}
		existing.Tags = tags
	}

	if err := touchProject(ctx, tx, existing.ProjectID, nowMs); err != nil {
		return Todo{}, err
	}

	// Audit todo_updated: diff title, body, sprint, estimation, tags; emit only if changed
	var effectiveSprint *int64
	if in.ClearSprint {
		effectiveSprint = nil
	} else if in.SprintID != nil {
		effectiveSprint = in.SprintID
	} else {
		effectiveSprint = existing.SprintID
	}
	var changedFields []string
	if existing.Title != in.Title {
		changedFields = append(changedFields, "title")
	}
	if existing.Body != in.Body {
		changedFields = append(changedFields, "body")
	}
	if !sameInt64Ptr(existing.SprintID, effectiveSprint) {
		changedFields = append(changedFields, "sprint")
	}
	if !sameInt64Ptr(existing.EstimationPoints, in.EstimationPoints) {
		changedFields = append(changedFields, "estimation")
	}
	existingTags := make(map[string]struct{}, len(existing.Tags))
	for _, t := range existing.Tags {
		existingTags[t] = struct{}{}
	}
	inTags := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		inTags[t] = struct{}{}
	}
	var tagsAdded, tagsRemoved []string
	for _, t := range tags {
		if _, ok := existingTags[t]; !ok {
			tagsAdded = append(tagsAdded, t)
		}
	}
	for _, t := range existing.Tags {
		if _, ok := inTags[t]; !ok {
			tagsRemoved = append(tagsRemoved, t)
		}
	}
	if len(tagsAdded) > 0 || len(tagsRemoved) > 0 {
		changedFields = append(changedFields, "tags")
	}
	if len(changedFields) > 0 {
		meta := map[string]any{"changed_fields": changedFields}
		for _, f := range changedFields {
			switch f {
			case "body":
				meta["before_len"] = len(existing.Body)
				meta["after_len"] = len(in.Body)
			case "title":
				meta["before_len"] = len(existing.Title)
				meta["after_len"] = len(in.Title)
			case "tags":
				meta["tags_added"] = tagsAdded
				meta["tags_removed"] = tagsRemoved
			}
		}
		if containsString(changedFields, "sprint") || containsString(changedFields, "estimation") {
			before := make(map[string]any)
			after := make(map[string]any)
			if containsString(changedFields, "sprint") {
				before["sprint_id"] = existing.SprintID
				after["sprint_id"] = effectiveSprint
			}
			if containsString(changedFields, "estimation") {
				before["estimation_points"] = existing.EstimationPoints
				after["estimation_points"] = in.EstimationPoints
			}
			meta["before"] = before
			meta["after"] = after
		}
		actorID, hasActor := UserIDFromContext(ctx)
		var actorUserID *int64
		if hasActor {
			actorUserID = &actorID
		}
		if err := insertAuditEventTx(ctx, tx, existing.ProjectID, actorUserID, "todo_updated", "todo", &todoID, meta); err != nil {
			return Todo{}, fmt.Errorf("audit todo_updated: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Todo{}, fmt.Errorf("commit update todo: %w", err)
	}

	// Track activity: todo update resets expiration
	if err := s.UpdateBoardActivity(ctx, existing.ProjectID); err != nil {
		_ = err // Log but don't fail
	}

	oldAssignee := existing.AssigneeUserID
	existing.Title = in.Title
	existing.Body = in.Body
	existing.AssigneeUserID = cloneInt64Ptr(in.AssigneeUserID)
	existing.EstimationPoints = cloneInt64Ptr(in.EstimationPoints)
	if updateSprint {
		if in.ClearSprint {
			existing.SprintID = nil
		} else {
			existing.SprintID = cloneInt64Ptr(in.SprintID)
		}
	}
	existing.UpdatedAt = time.UnixMilli(nowMs).UTC()
	existing.AssignmentChanged = assignmentChanged

	if s.todoAssignedPublisher != nil && assignmentChanged && !isAnonymousBoard {
		actorID, _ := UserIDFromContext(ctx)
		s.todoAssignedPublisher(ctx, existing.ProjectID, todoID, existing.LocalID, oldAssignee, in.AssigneeUserID, actorID)
	}

	return existing, nil
}

func (s *Store) DeleteTodo(ctx context.Context, todoID int64, mode Mode) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin delete todo: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := getTodoTx(ctx, tx, todoID)
	if err != nil {
		return err
	}
	p, err := s.getProjectForWriteTx(ctx, tx, existing.ProjectID, mode)
	if err != nil {
		return err
	}

	if !isAnonymousTemporaryBoard(p) {
		enabled, err := authEnabledTx(ctx, tx)
		if err != nil {
			return err
		}
		if enabled {
			userID, ok := UserIDFromContext(ctx)
			if !ok {
				return ErrUnauthorized
			}
			role, err := s.getProjectRoleTx(ctx, tx, existing.ProjectID, userID)
			if err != nil {
				return err
			}
			if !CanDeleteTodo(role) {
				return ErrUnauthorized
			}
		}
	}

	// Manual cleanup: from_local_id/to_local_id are not FK-constrained to todos.local_id.
	if _, err := tx.ExecContext(ctx, `
DELETE FROM todo_links
WHERE project_id = ? AND (from_local_id = ? OR to_local_id = ?)`,
		existing.ProjectID, existing.LocalID, existing.LocalID,
	); err != nil {
		return fmt.Errorf("delete todo links: %w", err)
	}

	var actorUserID *int64
	if userID, ok := UserIDFromContext(ctx); ok {
		actorUserID = &userID
	}
	meta := map[string]any{"local_id": existing.LocalID, "column_key": existing.ColumnKey}
	if err := insertAuditEventTx(ctx, tx, existing.ProjectID, actorUserID, "todo_deleted", "todo", &todoID, meta); err != nil {
		return fmt.Errorf("audit todo_deleted: %w", err)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM todos WHERE id=?`, todoID)
	if err != nil {
		return fmt.Errorf("delete todo: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected delete todo: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}

	nowMs := time.Now().UTC().UnixMilli()
	if err := touchProject(ctx, tx, existing.ProjectID, nowMs); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete todo: %w", err)
	}

	// Track activity: todo deletion resets expiration
	if err := s.UpdateBoardActivity(ctx, existing.ProjectID); err != nil {
		_ = err // Log but don't fail
	}

	return nil
}

func (s *Store) MoveTodo(ctx context.Context, todoID int64, toColumnKey string, afterID, beforeID *int64, mode Mode) (Todo, error) {
	if afterID != nil && *afterID == todoID {
		return Todo{}, fmt.Errorf("%w: afterId cannot equal todoId", ErrValidation)
	}
	if beforeID != nil && *beforeID == todoID {
		return Todo{}, fmt.Errorf("%w: beforeId cannot equal todoId", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return Todo{}, fmt.Errorf("begin move todo: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := getTodoTx(ctx, tx, todoID)
	if err != nil {
		return Todo{}, err
	}

	p, err := getProjectTx(ctx, tx, existing.ProjectID, s)
	if err != nil {
		return Todo{}, err
	}
	if !isAnonymousTemporaryBoard(p) {
		enabled, err := authEnabledTx(ctx, tx)
		if err != nil {
			return Todo{}, err
		}
		if enabled {
			userID, ok := UserIDFromContext(ctx)
			if !ok {
				return Todo{}, ErrUnauthorized
			}
			role, err := s.getProjectRoleTx(ctx, tx, existing.ProjectID, userID)
			if err != nil {
				return Todo{}, err
			}
			if !CanMoveTodo(role) {
				return Todo{}, ErrUnauthorized
			}
		}
	}

	targetCol, err := validateProjectColumnKeyTx(ctx, tx, existing.ProjectID, toColumnKey)
	if err != nil {
		return Todo{}, err
	}
	currentCol, err := validateProjectColumnKeyTx(ctx, tx, existing.ProjectID, existing.ColumnKey)
	if err != nil {
		return Todo{}, err
	}
	newRank, err := computeNewRank(ctx, tx, existing.ProjectID, toColumnKey, &todoID, afterID, beforeID)
	if err != nil {
		return Todo{}, err
	}

	nowMs := time.Now().UTC().UnixMilli()
	doneAtMs := resolveDoneAtForColumnTransition(currentCol.IsDone, targetCol.IsDone, nowMs)
	if doneAtMs != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE todos SET column_key=?, rank=?, updated_at=?, done_at=? WHERE id=?`, toColumnKey, newRank, nowMs, *doneAtMs, todoID); err != nil {
			return Todo{}, fmt.Errorf("update moved todo: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE todos SET column_key=?, rank=?, updated_at=? WHERE id=?`, toColumnKey, newRank, nowMs, todoID); err != nil {
			return Todo{}, fmt.Errorf("update moved todo: %w", err)
		}
	}

	if err := touchProject(ctx, tx, existing.ProjectID, nowMs); err != nil {
		return Todo{}, err
	}

	var actorUserID *int64
	if userID, ok := UserIDFromContext(ctx); ok {
		actorUserID = &userID
	}
	meta := map[string]any{"from_column": existing.ColumnKey, "to_column": toColumnKey, "local_id": existing.LocalID}
	if err := insertAuditEventTx(ctx, tx, existing.ProjectID, actorUserID, "todo_moved", "todo", &todoID, meta); err != nil {
		return Todo{}, fmt.Errorf("audit todo_moved: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Todo{}, fmt.Errorf("commit move todo: %w", err)
	}

	// Track activity: todo move resets expiration
	if err := s.UpdateBoardActivity(ctx, existing.ProjectID); err != nil {
		_ = err // Log but don't fail
	}

	existing.ColumnKey = toColumnKey
	existing.Rank = newRank
	existing.UpdatedAt = time.UnixMilli(nowMs).UTC()
	if doneAtMs != nil {
		t := time.UnixMilli(*doneAtMs).UTC()
		existing.DoneAt = &t
	}
	return existing, nil
}

func getTodoTx(ctx context.Context, tx *sql.Tx, todoID int64) (Todo, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, project_id, local_id, title, body, column_key, rank, estimation_points, assignee_user_id, sprint_id, created_at, updated_at, done_at FROM todos WHERE id=?`, todoID)
	var t Todo
	var columnKey string
	var createdAtMs, updatedAtMs int64
	var localID sql.NullInt64
	var estimationPoints sql.NullInt64
	var assigneeUserID sql.NullInt64
	var sprintID sql.NullInt64
	var doneAtMs sql.NullInt64
	if err := row.Scan(&t.ID, &t.ProjectID, &localID, &t.Title, &t.Body, &columnKey, &t.Rank, &estimationPoints, &assigneeUserID, &sprintID, &createdAtMs, &updatedAtMs, &doneAtMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Todo{}, ErrNotFound
		}
		return Todo{}, fmt.Errorf("get todo: %w", err)
	}
	if !localID.Valid {
		return Todo{}, fmt.Errorf("%w: todos.local_id is NULL (migration incomplete)", ErrConflict)
	}
	t.LocalID = localID.Int64
	t.ColumnKey = columnKey
	if estimationPoints.Valid {
		v := estimationPoints.Int64
		t.EstimationPoints = &v
	}
	if assigneeUserID.Valid {
		v := assigneeUserID.Int64
		t.AssigneeUserID = &v
	} else {
		t.AssigneeUserID = nil
	}
	if sprintID.Valid {
		v := sprintID.Int64
		t.SprintID = &v
	} else {
		t.SprintID = nil
	}
	t.CreatedAt = time.UnixMilli(createdAtMs).UTC()
	t.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
	if doneAtMs.Valid {
		dt := time.UnixMilli(doneAtMs.Int64).UTC()
		t.DoneAt = &dt
	}

	tags, err := listTodoTagsTx(ctx, tx, t.ID)
	if err != nil {
		return Todo{}, err
	}
	t.Tags = tags

	return t, nil
}

func getTodoIDByLocalIDTx(ctx context.Context, tx *sql.Tx, projectID, localID int64) (int64, error) {
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM todos WHERE project_id = ? AND local_id = ?`, projectID, localID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("get todo id by local_id: %w", err)
	}
	return id, nil
}

func (s *Store) GetProjectIDForTodo(ctx context.Context, todoID int64) (int64, error) {
	var projectID int64
	if err := s.db.QueryRowContext(ctx, `SELECT project_id FROM todos WHERE id = ?`, todoID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("get project id for todo: %w", err)
	}
	return projectID, nil
}

func (s *Store) GetTodoByLocalID(ctx context.Context, projectID, localID int64, mode Mode) (Todo, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return Todo{}, fmt.Errorf("begin get todo by local_id: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := s.getProjectForReadTx(ctx, tx, projectID, mode); err != nil {
		return Todo{}, err
	}

	id, err := getTodoIDByLocalIDTx(ctx, tx, projectID, localID)
	if err != nil {
		return Todo{}, err
	}
	t, err := getTodoTx(ctx, tx, id)
	if err != nil {
		return Todo{}, err
	}
	if err := tx.Commit(); err != nil {
		return Todo{}, fmt.Errorf("commit get todo by local_id: %w", err)
	}
	return t, nil
}

func (s *Store) UpdateTodoByLocalID(ctx context.Context, projectID, localID int64, in UpdateTodoInput, mode Mode) (Todo, error) {
	t, err := s.GetTodoByLocalID(ctx, projectID, localID, mode)
	if err != nil {
		return Todo{}, err
	}
	return s.UpdateTodo(ctx, t.ID, in, mode)
}

func (s *Store) DeleteTodoByLocalID(ctx context.Context, projectID, localID int64, mode Mode) error {
	t, err := s.GetTodoByLocalID(ctx, projectID, localID, mode)
	if err != nil {
		return err
	}
	return s.DeleteTodo(ctx, t.ID, mode)
}

func (s *Store) MoveTodoByLocalID(ctx context.Context, projectID, localID int64, toColumnKey string, afterLocalID, beforeLocalID *int64, mode Mode) (Todo, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return Todo{}, fmt.Errorf("begin move todo by local_id: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	todoID, err := getTodoIDByLocalIDTx(ctx, tx, projectID, localID)
	if err != nil {
		return Todo{}, err
	}
	var afterID, beforeID *int64
	if afterLocalID != nil {
		id, err := getTodoIDByLocalIDTx(ctx, tx, projectID, *afterLocalID)
		if err != nil {
			return Todo{}, err
		}
		afterID = &id
	}
	if beforeLocalID != nil {
		id, err := getTodoIDByLocalIDTx(ctx, tx, projectID, *beforeLocalID)
		if err != nil {
			return Todo{}, err
		}
		beforeID = &id
	}
	if err := tx.Commit(); err != nil {
		return Todo{}, fmt.Errorf("commit resolve ids: %w", err)
	}

	return s.MoveTodo(ctx, todoID, toColumnKey, afterID, beforeID, mode)
}
