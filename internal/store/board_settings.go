package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ProjectBoardSettingsPatch is the atomic REST board-settings mutation.
// Agenda and sprint capability fields live on the same projects row.
type ProjectBoardSettingsPatch struct {
	DefaultSprintWeeks *int
	SprintsEnabled     *bool
	AgendaEnabled      *bool
	AgendaTimezone     *string
	AgendaTitle        *string
	AgendaColor        *string
}

// ProjectBoardSettings is the persisted board-settings snapshot returned after a patch.
type ProjectBoardSettings struct {
	DefaultSprintWeeks int
	SprintsEnabled     bool
	AgendaEnabled      bool
	AgendaTimezone     string
	AgendaTitle        string
	AgendaColor        string
}

func (p ProjectBoardSettingsPatch) empty() bool {
	return p.DefaultSprintWeeks == nil && p.SprintsEnabled == nil && p.AgendaEnabled == nil && p.AgendaTimezone == nil && p.AgendaTitle == nil && p.AgendaColor == nil
}

func (p ProjectBoardSettingsPatch) hasAgenda() bool {
	return p.AgendaEnabled != nil || p.AgendaTimezone != nil || p.AgendaTitle != nil || p.AgendaColor != nil
}

func (s *Store) UpdateProjectBoardSettings(ctx context.Context, projectID, userID int64, patch ProjectBoardSettingsPatch) (ProjectBoardSettings, error) {
	if patch.empty() {
		return ProjectBoardSettings{}, fmt.Errorf("%w: empty patch", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return ProjectBoardSettings{}, fmt.Errorf("begin update project board settings: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := serializeProjectWriteTx(ctx, tx, projectID); err != nil {
		return ProjectBoardSettings{}, err
	}
	p, err := scanProject(tx.QueryRowContext(ctx, `SELECT id, name, image, slug, dominant_color, estimation_mode, default_sprint_weeks, sprints_enabled, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at FROM projects WHERE id=? AND import_batch_id IS NULL`, projectID))
	if err != nil {
		return ProjectBoardSettings{}, err
	}
	if err := s.checkProjectSettingsAuthTx(ctx, tx, p, userID); err != nil {
		return ProjectBoardSettings{}, err
	}

	if patch.hasAgenda() {
		if _, err := applyProjectAgendaSettingsTx(ctx, tx, projectID, patch.AgendaEnabled, patch.AgendaTimezone, patch.AgendaTitle, patch.AgendaColor); err != nil {
			return ProjectBoardSettings{}, err
		}
	}
	if patch.DefaultSprintWeeks != nil {
		if err := applyDefaultSprintWeeksIfChangedTx(ctx, tx, projectID, userID, *patch.DefaultSprintWeeks); err != nil {
			return ProjectBoardSettings{}, err
		}
	}
	if patch.SprintsEnabled != nil {
		if err := applySprintsEnabledIfChangedTx(ctx, tx, projectID, userID, *patch.SprintsEnabled); err != nil {
			return ProjectBoardSettings{}, err
		}
	}

	result, err := getProjectBoardSettingsQueryer(ctx, tx, projectID)
	if err != nil {
		return ProjectBoardSettings{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProjectBoardSettings{}, fmt.Errorf("commit update project board settings: %w", err)
	}
	return result, nil
}

func getProjectBoardSettingsQueryer(ctx context.Context, q sqlRowQueryer, projectID int64) (ProjectBoardSettings, error) {
	var (
		weeks, sprintsInt, agendaInt int
		timezone, title, color       string
	)
	err := q.QueryRowContext(ctx, `
SELECT default_sprint_weeks, sprints_enabled, agenda_enabled, agenda_timezone, agenda_title, agenda_color
FROM projects
WHERE id = ? AND import_batch_id IS NULL`, projectID).Scan(&weeks, &sprintsInt, &agendaInt, &timezone, &title, &color)
	if err != nil {
		if err == sql.ErrNoRows {
			return ProjectBoardSettings{}, ErrNotFound
		}
		return ProjectBoardSettings{}, fmt.Errorf("get project board settings: %w", err)
	}
	return ProjectBoardSettings{
		DefaultSprintWeeks: weeks,
		SprintsEnabled:     sprintsInt == 1,
		AgendaEnabled:      agendaInt == 1,
		AgendaTimezone:     timezone,
		AgendaTitle:        normalizeAgendaTitle(title),
		AgendaColor:        normalizeAgendaColor(color),
	}, nil
}
