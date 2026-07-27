package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"scrumboy/internal/store"
)

func readJSON(w http.ResponseWriter, r *http.Request, maxBody int64, dst any) error {
	if r.Body == nil {
		writeValidationError(w, "missing body", "missing_body", nil)
		return errors.New("missing body")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeValidationError(w, "invalid json", "invalid_json", map[string]any{"detail": err.Error()})
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeValidationError(w, "invalid json", "invalid_json", map[string]any{"detail": "extra data"})
		return errors.New("extra json data")
	}
	return nil
}

func readBodyBytes(w http.ResponseWriter, r *http.Request, maxBody int64) ([]byte, error) {
	if r.Body == nil {
		writeValidationError(w, "missing body", "missing_body", nil)
		return nil, errors.New("missing body")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", fmt.Sprintf("upload exceeds the %d byte limit", maxBody), nil)
			return nil, err
		}
		writeValidationError(w, "invalid request body", "invalid_request_body", map[string]any{"detail": err.Error()})
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		writeValidationError(w, "missing body", "missing_body", nil)
		return nil, errors.New("missing body")
	}
	return body, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeInternal(w http.ResponseWriter, err error) {
	writeError(w, http.StatusInternalServerError, "INTERNAL", "internal error", map[string]any{"detail": err.Error()})
}

func writeStoreErr(w http.ResponseWriter, err error, hideUnauthorized bool) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	case errors.Is(err, store.ErrUnauthorized):
		if hideUnauthorized {
			// Map to 404 to prevent existence probing on resource endpoints
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		} else {
			// Return 401 for entry points (so SPA can show login)
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		}
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
	case errors.Is(err, store.ErrValidation):
		writeValidationError(w, err.Error(), validationReasonFromStoreError(err), nil)
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "CONFLICT", err.Error(), nil)
	case errors.Is(err, store.ErrTooManyAttempts):
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "too many attempts; please sign in again", nil)
	case errors.Is(err, store.Err2FAEncryptionNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Two-factor authentication is not configured. Set SCRUMBOY_ENCRYPTION_KEY (e.g. openssl rand -base64 32) and restart.", nil)
	default:
		if strings.Contains(err.Error(), "too large") {
			writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", err.Error(), nil)
			return
		}
		writeInternal(w, err)
	}
}

func validationDetails(reason string, extra map[string]any) map[string]any {
	if reason == "" && len(extra) == 0 {
		return nil
	}
	details := make(map[string]any, len(extra)+1)
	for key, value := range extra {
		details[key] = value
	}
	if reason != "" {
		details["reason"] = reason
	}
	return details
}

func writeValidationError(w http.ResponseWriter, message, reason string, details map[string]any) {
	writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", message, validationDetails(reason, details))
}

func validationReasonFromStoreError(err error) string {
	if err == nil || !errors.Is(err, store.ErrValidation) {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if strings.HasPrefix(msg, store.ErrValidation.Error()+": ") {
		msg = strings.TrimSpace(strings.TrimPrefix(msg, store.ErrValidation.Error()+": "))
	}
	if msg == "" || msg == store.ErrValidation.Error() {
		return ""
	}
	if reason, ok := validationReasonByMessage[msg]; ok {
		return reason
	}
	for _, item := range validationReasonPrefixes {
		if strings.HasPrefix(msg, item.prefix) {
			return item.reason
		}
	}
	return ""
}

var validationReasonByMessage = map[string]string{
	"invalid email":                                         "invalid_email",
	"invalid user id":                                       "invalid_user_id",
	"invalid project id":                                    "invalid_project_id",
	"invalid slug":                                          "invalid_slug",
	"invalid name":                                          "invalid_name",
	"invalid project name":                                  "invalid_project_name",
	"invalid sprint name":                                   "invalid_sprint_name",
	"invalid title":                                         "invalid_title",
	"body too large":                                        "body_too_large",
	"invalid role":                                          "invalid_role",
	"invalid system role":                                   "invalid_system_role",
	"cannot delete yourself":                                "cannot_delete_self",
	"cannot delete the last owner":                          "cannot_delete_last_owner",
	"cannot demote the last owner":                          "cannot_demote_last_owner",
	"cannot remove last maintainer":                         "cannot_remove_last_maintainer",
	"project workflow must have at least 2 columns":         "project_workflow_min_columns",
	"workflow column name cannot be empty":                  "workflow_column_name_required",
	"project workflow must have exactly one done column":    "project_workflow_done_column_required",
	"project has no done column":                            "project_workflow_done_column_required",
	"invalid workflow column key":                           "invalid_workflow_key",
	"invalid workflow column name":                          "invalid_workflow_column_name",
	"invalid workflow column color":                         "invalid_workflow_column_color",
	"invalid columnKey":                                     "invalid_column_key",
	"cannot delete done workflow column":                    "cannot_delete_done_workflow_column",
	"estimation points must be one of 1,2,3,5,8,13,20,40":   "invalid_estimation_points",
	"assignment is not allowed in anonymous mode":           "assignment_not_allowed_anonymous",
	"assignee does not exist":                               "assignee_not_found",
	"assignee is not a project member":                      "assignee_not_project_member",
	"sprint not found":                                      "sprint_not_found",
	"sprint does not belong to project":                     "sprint_not_in_project",
	"a sprint with this name already exists in the project": "sprint_name_exists",
	"end_at must be >= start_at":                            "sprint_end_before_start",
	"sprint must be PLANNED to activate":                    "sprint_activate_requires_planned",
	"sprint end date is on or before now; cannot activate":  "sprint_end_in_past",
	"only endAt can be updated for ACTIVE sprint":           "active_sprint_only_end_at",
	"startAt cannot be updated for this sprint state":       "active_sprint_only_end_at",
	"dates cannot be updated for CLOSED sprint":             "closed_sprint_dates_locked",
	"endAt cannot be updated for CLOSED sprint":             "closed_sprint_dates_locked",
	"too many tags":                                         "too_many_tags",
	"invalid tag name":                                      "invalid_tag_name",
	"cannot link todo to itself":                            "cannot_link_todo_to_itself",
	"invalid link_type":                                     "invalid_link_type",
	"invalid color":                                         "invalid_color",
	"note text too long":                                    "wall_note_too_long",
	"wall note limit reached":                               "wall_note_limit_reached",
	"from and to required":                                  "wall_edge_endpoints_required",
	"self-edges not allowed":                                "self_edges_not_allowed",
	"wall edge limit reached":                               "wall_edge_limit_reached",
	"cannot import full scope into anonymous mode":          "import_full_scope_anonymous_forbidden",
	"Replace All is forbidden in anonymous mode":            "replace_all_anonymous_forbidden",
	"target board is not an anonymous board":                "target_board_not_anonymous",
	"invalid tag color in preferences":                      "invalid_tag_color",
	"project missing name":                                  "project_missing_name",
	"project missing slug":                                  "project_missing_slug",
	"defaultSprintWeeks must be 1 or 2":                     "invalid_default_sprint_weeks",
	"todo missing localId":                                  "todo_missing_local_id",
	"todo missing title":                                    "todo_missing_title",
}

var validationReasonPrefixes = []struct {
	prefix string
	reason string
}{
	{"workflow may have at most ", "workflow_column_limit_reached"},
	{"duplicate workflow column key ", "duplicate_workflow_column_key"},
	{"invalid workflow column key ", "invalid_workflow_key"},
	{"invalid workflow column color ", "invalid_workflow_column_color"},
	{"invalid tag color ", "invalid_tag_color"},
	{"invalid tag ", "invalid_tag"},
}

func writeError(w http.ResponseWriter, status int, code, message string, details any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
	})
}
