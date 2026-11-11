package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"scrumboy/internal/drawrefs"
	"scrumboy/internal/version"
)

type TodoDrawingExport struct {
	ID   string          `json:"id"`
	Data json.RawMessage `json:"data"`
}

func ExportHasDrawings(data *ExportData) bool {
	if data == nil {
		return false
	}
	for _, p := range data.Projects {
		if p.Wall != nil && len(p.Wall.Drawings) > 0 {
			return true
		}
		for _, t := range p.Todos {
			if len(t.Drawings) > 0 || hasNotesInk(t) {
				return true
			}
		}
	}
	return false
}

func hasNotesInk(t TodoExport) bool {
	trimmed := bytes.TrimSpace(t.NotesInk)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func (s *Store) attachTodoNotesInkForExport(ctx context.Context, projectID int64, todos []Todo, exports []TodoExport) error {
	rows, err := s.db.QueryContext(ctx, `SELECT todo_id, data FROM todo_notes_ink WHERE project_id = ?`, projectID)
	if err != nil {
		return fmt.Errorf("export notes ink for project %d: %w", projectID, err)
	}
	byTodo := map[int64]string{}
	for rows.Next() {
		var todoID int64
		var data string
		if err := rows.Scan(&todoID, &data); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan export notes ink: %w", err)
		}
		byTodo[todoID] = data
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range exports {
		if i >= len(todos) {
			break
		}
		if data, ok := byTodo[todos[i].ID]; ok {
			exports[i].NotesInk = json.RawMessage(data)
		}
	}
	return nil
}

func (s *Store) attachTodoDrawingsForExport(ctx context.Context, projectID int64, todos []Todo, exports []TodoExport) error {
	if err := s.attachTodoNotesInkForExport(ctx, projectID, todos, exports); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, todo_id, data FROM todo_drawings WHERE project_id = ?`, projectID)
	if err != nil {
		return fmt.Errorf("export drawings for project %d: %w", projectID, err)
	}
	type row struct {
		todoID int64
		data   string
	}
	byID := map[string]row{}
	for rows.Next() {
		var id, data string
		var todoID int64
		if err := rows.Scan(&id, &todoID, &data); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan export drawing: %w", err)
		}
		byID[id] = row{todoID: todoID, data: data}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range exports {
		if i >= len(todos) {
			break
		}
		refs := drawrefs.Extract(exports[i].Body)
		if len(refs) == 0 {
			continue
		}
		out := make([]TodoDrawingExport, 0, len(refs))
		for _, id := range refs {
			r, ok := byID[id]
			if !ok || r.todoID != todos[i].ID {
				return fmt.Errorf("%w: story #%d references drawing %s, which does not exist; open and save the story to repair it before exporting", ErrValidation, exports[i].LocalID, id)
			}
			out = append(out, TodoDrawingExport{ID: id, Data: json.RawMessage(r.data)})
		}
		exports[i].Drawings = out
	}
	return nil
}

type pendingImportDrawing struct {
	id   string
	data []byte
}

func importDrawingRecords(t TodoExport) (map[string][]byte, error) {
	records := make(map[string][]byte, len(t.Drawings))
	for _, rec := range t.Drawings {
		if !drawrefs.IsDrawingID(rec.ID) {
			return nil, drawingError(ReasonInvalidDrawing, fmt.Sprintf("story %d has an invalid drawing id", t.LocalID))
		}
		_, canonical, err := ParseDrawingDocument(rec.Data)
		if err != nil {
			return nil, fmt.Errorf("story %d drawing %s: %w", t.LocalID, rec.ID, err)
		}
		if prev, dup := records[rec.ID]; dup {
			if string(prev) != string(canonical) {
				return nil, drawingError(ReasonConflictingDrawingRecord, fmt.Sprintf("story %d has conflicting records for drawing %s", t.LocalID, rec.ID))
			}
			continue
		}
		records[rec.ID] = canonical
	}
	return records, nil
}

func validateTodoExportDrawings(t TodoExport) error {
	if hasNotesInk(t) {
		if _, _, err := ParseNotesInkDocument(t.NotesInk); err != nil {
			return fmt.Errorf("story %d notes ink: %w", t.LocalID, err)
		}
	}
	records, err := importDrawingRecords(t)
	if err != nil {
		return err
	}
	refs := drawrefs.Extract(t.Body)
	if len(refs) > MaxDrawingsPerTodo {
		return drawingError(ReasonDrawingLimit, fmt.Sprintf("story %d references too many drawings", t.LocalID))
	}
	total := 0
	for _, id := range refs {
		data, ok := records[id]
		if !ok {
			return drawingError(ReasonUnresolvedDrawingReference, fmt.Sprintf("story %d references drawing %s, which the backup does not contain", t.LocalID, id))
		}
		total += len(data)
	}
	if total > MaxStoredDrawingBytesPerTodo {
		return drawingError(ReasonDrawingTooLarge, fmt.Sprintf("story %d drawings exceed the stored budget", t.LocalID))
	}
	return nil
}

func validateBackupDrawings(data *ExportData, fromArchive bool) error {
	drawingsAllowed := fromArchive && data.Version == version.ArchiveExportFormatVersion
	for _, p := range data.Projects {
		if p.Wall != nil && (p.Wall.DrawingsPresent || len(p.Wall.Drawings) > 0) {
			if !drawingsAllowed {
				return fmt.Errorf("%w: format %s project %q contains wall drawings; drawings require a %s .sbz archive", ErrValidation, data.Version, p.Name, version.ArchiveExportFormatVersion)
			}
			if _, err := ValidateWallDrawingsForImport(p.Wall.Drawings); err != nil {
				return fmt.Errorf("project %q wall drawings: %w", p.Name, err)
			}
		}
		for _, t := range p.Todos {
			if (t.DrawingsPresent || len(t.Drawings) > 0 || t.NotesInkPresent || len(t.NotesInk) > 0) && !drawingsAllowed {
				return fmt.Errorf("%w: format %s story %d contains drawings; drawings require a %s .sbz archive", ErrValidation, data.Version, t.LocalID, version.ArchiveExportFormatVersion)
			}
			if err := validateTodoExportDrawings(t); err != nil {
				return err
			}
		}
	}
	return nil
}

func projectSlugTx(ctx context.Context, tx *sql.Tx, projectID int64) (string, error) {
	var slug string
	if err := tx.QueryRowContext(ctx, `SELECT slug FROM projects WHERE id = ?`, projectID).Scan(&slug); err != nil {
		return "", fmt.Errorf("load project slug: %w", err)
	}
	return slug, nil
}

type importDrawingPlan struct {
	rows [][]pendingImportDrawing
	ink  [][]byte
}

func planImportDrawingsTx(ctx context.Context, tx *sql.Tx, projectID int64, todos []TodoExport) ([]TodoExport, importDrawingPlan, error) {
	plan := importDrawingPlan{rows: make([][]pendingImportDrawing, len(todos)), ink: make([][]byte, len(todos))}
	needs := false
	for _, t := range todos {
		if len(t.Drawings) > 0 || hasNotesInk(t) || len(drawrefs.Extract(t.Body)) > 0 {
			needs = true
			break
		}
	}
	if !needs {
		return todos, plan, nil
	}
	slug, err := projectSlugTx(ctx, tx, projectID)
	if err != nil {
		return nil, plan, err
	}
	rows := plan.rows
	out := make([]TodoExport, len(todos))
	copy(out, todos)
	for i := range out {
		if err := validateTodoExportDrawings(out[i]); err != nil {
			return nil, plan, err
		}
		if hasNotesInk(out[i]) {
			_, canonical, err := ParseNotesInkDocument(out[i].NotesInk)
			if err != nil {
				return nil, plan, err
			}
			plan.ink[i] = canonical
		}
		refs := drawrefs.Extract(out[i].Body)
		if len(refs) == 0 {
			continue
		}
		records, err := importDrawingRecords(out[i])
		if err != nil {
			return nil, plan, err
		}
		mapping := make(map[string]string, len(refs))
		for _, id := range refs {
			fresh := drawrefs.NewID()
			mapping[id] = fresh
			rows[i] = append(rows[i], pendingImportDrawing{id: fresh, data: records[id]})
		}
		body, err := drawrefs.Remap(out[i].Body, mapping, slug)
		if err != nil {
			if errors.Is(err, drawrefs.ErrRemapInvariant) || errors.Is(err, drawrefs.ErrTooManyReferences) || errors.Is(err, drawrefs.ErrNoteTooLarge) || errors.Is(err, drawrefs.ErrInvalidMapping) {
				return nil, plan, drawingError(ReasonUnresolvedDrawingReference, fmt.Sprintf("story %d drawings could not be remapped: %v", out[i].LocalID, err))
			}
			return nil, plan, err
		}
		out[i].Body = body
		out[i].Drawings = nil
	}
	return out, plan, nil
}

func writeImportedDrawingsTx(ctx context.Context, tx *sql.Tx, projectID int64, imported *importedTodos, plan importDrawingPlan) error {
	nowMs := time.Now().UTC().UnixMilli()
	rows := plan.rows
	for i, todoID := range imported.todoIDs {
		if todoID == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM todo_drawings WHERE todo_id = ? AND project_id = ?`, todoID, projectID); err != nil {
			return fmt.Errorf("clear imported story drawings: %w", err)
		}
		if i < len(plan.ink) && len(plan.ink[i]) > 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM todo_notes_ink WHERE todo_id = ? AND project_id = ?`, todoID, projectID); err != nil {
				return fmt.Errorf("clear imported story notes ink: %w", err)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO todo_notes_ink (todo_id, project_id, data, version, created_at, updated_at) VALUES (?, ?, ?, 1, ?, ?)`,
				todoID, projectID, string(plan.ink[i]), nowMs, nowMs); err != nil {
				return fmt.Errorf("insert imported notes ink: %w", err)
			}
		}
		if i >= len(rows) {
			continue
		}
		for _, d := range rows[i] {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO todo_drawings (id, todo_id, project_id, data, version, created_at, updated_at) VALUES (?, ?, ?, ?, 1, ?, ?)`,
				d.id, todoID, projectID, string(d.data), nowMs, nowMs); err != nil {
				return fmt.Errorf("insert imported drawing: %w", err)
			}
		}
	}
	return nil
}
