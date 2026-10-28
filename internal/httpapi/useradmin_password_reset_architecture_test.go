package httpapi

import "testing"

func TestAdminPasswordResetHandlerDoesNotCallStoreOrTokenUtilities(t *testing.T) {
	assertHandlerOmitsCalls(
		t,
		"routing_admin.go",
		"handleAdminUsersPasswordReset",
		"GetUser",
		"GetUserPasswordHash",
		"GeneratePasswordResetToken",
	)
	assertHandlerCalls(t, "routing_admin.go", "handleAdminUsersPasswordReset", "Prepare")
	assertHandlerCalls(t, "routing_admin.go", "handleAdminUsersPasswordReset", "Issue")
}
