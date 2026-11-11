package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	MaxNotesInkStrokes   = 1000
	MaxNotesInkBytes     = 512 * 1024
	maxNotesInkDimension = 200000
)

var notesInkLimits = drawingLimits{maxBytes: MaxNotesInkBytes, maxStrokes: MaxNotesInkStrokes, maxDimension: maxNotesInkDimension}

type NotesInkChange struct {
	Present bool
	Data    json.RawMessage
}

func (c NotesInkChange) clears() bool {
	trimmed := bytes.TrimSpace(c.Data)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func ParseNotesInkDocument(raw []byte) (DrawingDocument, []byte, error) {
	return parseDrawingDocumentWithLimits(raw, notesInkLimits)
}

func (s *Store) syncTodoNotesInkTx(ctx context.Context, tx *sql.Tx, projectID, todoID int64, change NotesInkChange, nowMs int64) error {
	if !change.Present {
		return nil
	}
	if s.drawingsDisabled {
		return drawingError(ReasonDrawingsDisabled, "drawings are disabled")
	}
	if change.clears() {
		if _, err := tx.ExecContext(ctx, `DELETE FROM todo_notes_ink WHERE todo_id = ? AND project_id = ?`, todoID, projectID); err != nil {
			return fmt.Errorf("clear notes ink: %w", err)
		}
		return nil
	}
	_, canonical, err := ParseNotesInkDocument(change.Data)
	if err != nil {
		return err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT data FROM todo_notes_ink WHERE todo_id = ? AND project_id = ?`, todoID, projectID).Scan(&existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO todo_notes_ink (todo_id, project_id, data, version, created_at, updated_at) VALUES (?, ?, ?, 1, ?, ?)`,
			todoID, projectID, string(canonical), nowMs, nowMs); err != nil {
			return fmt.Errorf("insert notes ink: %w", err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("load notes ink: %w", err)
	}
	if existing == string(canonical) {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE todo_notes_ink SET data = ?, version = version + 1, updated_at = ? WHERE todo_id = ? AND project_id = ?`,
		string(canonical), nowMs, todoID, projectID); err != nil {
		return fmt.Errorf("update notes ink: %w", err)
	}
	return nil
}

func todoNotesInkTx(ctx context.Context, tx *sql.Tx, projectID, todoID int64) (json.RawMessage, error) {
	var data string
	err := tx.QueryRowContext(ctx, `SELECT data FROM todo_notes_ink WHERE todo_id = ? AND project_id = ?`, todoID, projectID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load notes ink: %w", err)
	}
	return json.RawMessage(data), nil
}

func (s *Store) GetTodoNotesInkByLocalID(ctx context.Context, projectID, localID int64) (json.RawMessage, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin notes ink: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var todoID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM todos WHERE project_id = ? AND local_id = ?`, projectID, localID).Scan(&todoID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load todo for notes ink: %w", err)
	}
	return todoNotesInkTx(ctx, tx, projectID, todoID)
}

func (s *Store) GetPublicTodoNotesInk(ctx context.Context, projectID int64, expectedSlug string, localID int64) (json.RawMessage, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin public notes ink: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := loadEligiblePublicProjectTx(ctx, tx, projectID, expectedSlug); err != nil {
		return nil, err
	}
	var todoID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM todos WHERE project_id = ? AND local_id = ? AND archived_at IS NULL`, projectID, localID).Scan(&todoID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load public todo for notes ink: %w", err)
	}
	return todoNotesInkTx(ctx, tx, projectID, todoID)
}
