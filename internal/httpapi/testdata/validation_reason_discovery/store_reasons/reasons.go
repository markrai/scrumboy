package fixture

const (
	ReasonEmailInUse                 = "email_in_use"
	ReasonCustomFieldKeyExists       = "custom_field_key_exists"
	ReasonInvalidDueAt               = "invalid_due_at"
	ReasonWallDrawingVersionConflict = "wall_drawing_version_conflict"
	ReasonTodoArchived               = "todo_archived"
	ReasonOnlyDefinedValidation      = "only_defined_validation"
	// Value is a known conflict snake so unused consts still classify as CONFLICT.
	ReasonOnlyDefinedConflictKnown = "owns_projects"
	// Value is a known INTERNAL snake (redacted import failures).
	ReasonImportInternalRedacted = "import_internal_redacted"
	// Emitted under both CONFLICT and VALIDATION_ERROR in this fixture.
	ReasonDualCategory = "fixture_dual_category"
)

var (
	ErrConflict   = errSentinel("conflict")
	ErrValidation = errSentinel("validation")
)

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

type reasonedStoreError struct {
	base   error
	reason string
	msg    string
}

func reasoned(base error, reason, message string) error {
	return priorityError(base, reason, message)
}

func priorityError(base error, reason, message string) error {
	return &reasonedStoreError{base: base, reason: reason, msg: message}
}

func drawingConflict(reason, msg string) error {
	return &reasonedStoreError{base: ErrConflict, reason: reason, msg: msg}
}

func drawingError(reason, msg string) error {
	return &reasonedStoreError{base: ErrValidation, reason: reason, msg: msg}
}

func emit() {
	_ = reasoned(ErrConflict, ReasonEmailInUse, "conflict")
	_ = priorityError(ErrConflict, ReasonCustomFieldKeyExists, "conflict")
	_ = reasoned(ErrValidation, ReasonInvalidDueAt, "validation")
	_ = drawingConflict(ReasonWallDrawingVersionConflict, "conflict")
	_ = &reasonedStoreError{base: ErrConflict, reason: ReasonTodoArchived, msg: "archived"}
	_ = reasoned(ErrConflict, ReasonDualCategory, "conflict")
	_ = reasoned(ErrValidation, ReasonDualCategory, "validation")
}
