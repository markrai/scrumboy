package store

// Stable public reason codes for errors whose human message alone is not mapped
// to a locale key. The API layer exposes them as error.details.reason and the
// web client looks up errors.<CODE>.<reason>.
const (
	ReasonInvalidDueAt = "invalid_due_at"

	ReasonInvalidCustomFieldName = "invalid_custom_field_name"
	ReasonInvalidCustomFieldType = "invalid_custom_field_type"
	ReasonInvalidCustomFieldKey  = "invalid_custom_field_key"
	ReasonCustomFieldLimit       = "custom_field_limit_reached"
	ReasonCustomFieldKeyExists   = "custom_field_key_exists"
	ReasonCustomFieldUnknown     = "custom_field_unknown"
	ReasonCustomFieldNotText     = "custom_field_value_not_text"
	ReasonCustomFieldEmpty       = "custom_field_value_empty"
	ReasonCustomFieldTooLong     = "custom_field_value_too_long"
	ReasonCustomFieldNotURL      = "custom_field_value_not_url"
	ReasonCustomFieldNotNumber   = "custom_field_value_not_number"
	ReasonCustomFieldNumberRange = "custom_field_number_out_of_range"

	ReasonProjectNameNoSlug = "project_name_no_slug"

	ReasonEmailInUse         = "email_in_use"
	ReasonAlreadyMember      = "already_member"
	ReasonSoleMaintainer     = "sole_maintainer"
	ReasonOwnsProjects       = "owns_projects"
	ReasonCannotDemoteSelf   = "cannot_demote_self"
	ReasonCannotDemoteLastMt = "cannot_demote_last_maintainer"

	// Import / backup validation (structured details; safe for localization).
	ReasonImportProjectMissingName         = "import_project_missing_name"
	ReasonImportDuplicateSlug              = "import_duplicate_slug"
	ReasonImportProjectMissingSlug         = "import_project_missing_slug"
	ReasonImportTodoMissingTitle           = "import_todo_missing_title"
	ReasonImportTodoMissingLocalID         = "import_todo_missing_local_id"
	ReasonImportTodoInvalidEstimation      = "import_todo_invalid_estimation"
	ReasonImportTodoUnknownColumn          = "import_todo_unknown_column"
	ReasonImportTodoUnknownPriority        = "import_todo_unknown_priority"
	ReasonImportTodoUnknownSprint          = "import_todo_unknown_sprint"
	ReasonImportWorkflowMinColumns         = "import_workflow_min_columns"
	ReasonImportWorkflowDoneColumnRequired = "import_workflow_done_column_required"
	ReasonImportInvalidLink                = "import_invalid_link"
	ReasonImportWorkLimitExceeded          = "import_work_limit_exceeded"
	ReasonImportRecordInvalid              = "import_record_invalid"
	// ReasonImportFileInvalid is for file-level (non-project-scoped) format errors;
	// these have no meaningful "project" to include in details.
	ReasonImportFileInvalid   = "import_file_invalid"
	ReasonImportInternalRedacted           = "import_internal_redacted"
)

// reasoned wraps base with a public reason code and the message shown in logs.
// An optional details map may be passed for structured localization parameters.
func reasoned(base error, reason, message string, details ...map[string]any) error {
	var d map[string]any
	if len(details) > 0 {
		d = details[0]
	}
	if d == nil {
		return priorityError(base, reason, message)
	}
	return reasonedWithDetails(base, reason, message, d)
}
