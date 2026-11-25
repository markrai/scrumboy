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
)

// reasoned wraps base with a public reason code and the message shown in logs.
func reasoned(base error, reason, message string) error {
	return priorityError(base, reason, message)
}
