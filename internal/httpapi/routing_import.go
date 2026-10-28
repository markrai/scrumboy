package httpapi

import (
	"net/http"
	"time"

	"scrumboy/internal/store"
	"scrumboy/internal/trelloimport"
)

func (s *Server) handleImportAPI(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 || rest[0] != "trello" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	switch {
	case len(rest) == 1:
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		s.handleTrelloImport(w, r)
		return
	case len(rest) == 2 && rest[1] == "preview":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		s.handleTrelloPreview(w, r)
		return
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
}

func (s *Server) handleTrelloPreview(w http.ResponseWriter, r *http.Request) {
	if s.storeMode() == store.ModeAnonymous && !s.admitAnonymousImport(w, r, 0) {
		return
	}
	body, err := readBodyBytes(w, r, s.maxTrelloImportBody)
	if err != nil {
		return
	}
	bundle, err := trelloimport.BuildImportBundle(body, time.Now().UTC())
	if err != nil {
		writeValidationError(w, "invalid Trello JSON", "invalid_trello_json", map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, bundle.Preview)
}

func (s *Server) handleTrelloImport(w http.ResponseWriter, r *http.Request) {
	body, err := readBodyBytes(w, r, s.maxTrelloImportBody)
	if err != nil {
		return
	}
	bundle, err := trelloimport.BuildImportBundle(body, time.Now().UTC())
	if err != nil {
		writeValidationError(w, "invalid Trello JSON", "invalid_trello_json", map[string]any{"detail": err.Error()})
		return
	}
	if len(bundle.Preview.HardErrors) > 0 || bundle.ExportData == nil {
		writeValidationError(w, "Trello import validation failed", "trello_import_validation_failed", map[string]any{
			"hardErrors": bundle.Preview.HardErrors,
			"warnings":   bundle.Preview.Warnings,
		})
		return
	}

	if s.storeMode() == store.ModeAnonymous && !s.admitAnonymousImport(w, r, 1) {
		return
	}

	ctx := s.requestContext(r)
	project, err := s.store.ImportTrelloProject(ctx, bundle.ExportData, bundle.ProjectImportMetadata, bundle.TodoImportMetadataByLocalID, s.storeMode())
	if err != nil {
		writeStoreErr(w, err, false)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project": map[string]any{
			"id":   project.ID,
			"name": project.Name,
			"slug": project.Slug,
		},
		"summary": map[string]any{
			"projects":           1,
			"todos":              bundle.Preview.Cards,
			"labels":             bundle.Preview.Labels,
			"openLists":          bundle.Preview.OpenLists,
			"closedLists":        bundle.Preview.ClosedLists,
			"archivedCards":      bundle.Preview.ArchivedCards,
			"checklists":         bundle.Preview.Checklists,
			"checklistItems":     bundle.Preview.ChecklistItems,
			"commentCardActions": bundle.Preview.CommentCardActions,
			"attachments":        bundle.Preview.Attachments,
			"customFieldItems":   bundle.Preview.CustomFieldItems,
		},
		"warnings": bundle.Preview.Warnings,
	})
}
