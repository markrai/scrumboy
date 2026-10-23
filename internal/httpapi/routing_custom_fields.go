package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"scrumboy/internal/store"
)

type customFieldJSON struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Position int    `json:"position"`
}

func customFieldToJSON(f store.CustomField) customFieldJSON {
	return customFieldJSON{Key: f.Key, Name: f.Name, Type: string(f.Type), Position: f.Position}
}

// decodeCustomFieldsPatch parses {"key": "text"|number|null}; booleans, arrays
// and objects are rejected so a typed field never stores a coerced value.
func decodeCustomFieldsPatch(raw json.RawMessage) (store.CustomFieldValues, error) {
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil || in == nil {
		return nil, errors.New("not an object")
	}
	out := make(store.CustomFieldValues, len(in))
	for k, v := range in {
		switch v.(type) {
		case nil, string, float64:
			out[k] = v
		default:
			return nil, errors.New("unsupported value type")
		}
	}
	return out, nil
}

// handleBoardCustomFieldRoutes serves the project's custom field definitions:
//
//	GET    /api/board/{slug}/custom-fields          (any reader)
//	POST   /api/board/{slug}/custom-fields          (maintainer)
//	DELETE /api/board/{slug}/custom-fields/{key}    (maintainer)
//
// Values are set through PATCH /api/board/{slug}/todos/{localId} (customFields).
func (s *Server) handleBoardCustomFieldRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	if len(rest) < 2 || rest[1] != "custom-fields" || len(rest) > 3 {
		return false
	}
	ctx := s.requestContext(r)
	project := pc.Project

	if len(rest) == 2 && r.Method == http.MethodGet {
		fields, err := s.store.ListProjectCustomFields(ctx, project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		items := make([]customFieldJSON, 0, len(fields))
		for _, f := range fields {
			items = append(items, customFieldToJSON(f))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return true
	}

	isCreate := len(rest) == 2 && r.Method == http.MethodPost
	isDelete := len(rest) == 3 && r.Method == http.MethodDelete
	if !isCreate && !isDelete {
		return false
	}
	if !pc.Role.HasMinimumRole(store.RoleMaintainer) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
		return true
	}

	if isCreate {
		var in struct {
			Key  string `json:"key"`
			Name string `json:"name"`
			Type string `json:"type"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" {
			writeValidationError(w, "name required", "invalid_custom_field_name", map[string]any{"field": "name"})
			return true
		}
		typ := store.CustomFieldType(strings.TrimSpace(in.Type))
		if !store.ValidCustomFieldType(typ) {
			writeValidationError(w, "type must be text, url or number", "invalid_custom_field_type", map[string]any{"field": "type"})
			return true
		}
		field, err := s.store.CreateCustomField(ctx, project.ID, in.Key, in.Name, typ)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusCreated, customFieldToJSON(field))
		return true
	}

	key := strings.TrimSpace(rest[2])
	if key == "" {
		writeValidationError(w, "invalid custom field key", "invalid_custom_field_key", map[string]any{"field": "key"})
		return true
	}
	if err := s.store.DeleteCustomField(ctx, project.ID, key); err != nil {
		writeStoreErr(w, err, true)
		return true
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}
