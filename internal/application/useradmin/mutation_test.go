package useradmin

import (
	"testing"

	"scrumboy/internal/store"
)

var (
	_ UserCreationStore     = (*store.Store)(nil)
	_ UserReadStore         = (*store.Store)(nil)
	_ UserRoleMutationStore = (*store.Store)(nil)
	_ UserDeletionStore     = (*store.Store)(nil)
)

func TestCreateCommandPreservesRawValues(t *testing.T) {
	tests := []struct {
		name     string
		command  CreateCommand
		wantMail string
		wantName string
		wantPass string
	}{
		{
			name: "mixed case and whitespace",
			command: CreateCommand{
				Email:    "  Mixed.Case@Example.COM  ",
				Name:     "  Created Name  ",
				Password: "  password123  ",
			},
			wantMail: "  Mixed.Case@Example.COM  ",
			wantName: "  Created Name  ",
			wantPass: "  password123  ",
		},
		{
			name:     "empty values remain representable",
			command:  CreateCommand{},
			wantMail: "",
			wantName: "",
			wantPass: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.command.Email != tt.wantMail {
				t.Fatalf("Email = %q, want %q", tt.command.Email, tt.wantMail)
			}
			if tt.command.Name != tt.wantName {
				t.Fatalf("Name = %q, want %q", tt.command.Name, tt.wantName)
			}
			if tt.command.Password != tt.wantPass {
				t.Fatal("Password was not preserved exactly")
			}
		})
	}
}

func TestRoleChangeCommandPreservesTargetAndRole(t *testing.T) {
	tests := []struct {
		name   string
		target int64
		role   store.SystemRole
	}{
		{name: "user", target: 41, role: store.SystemRoleUser},
		{name: "admin", target: 42, role: store.SystemRoleAdmin},
		{name: "owner remains representable", target: 43, role: store.SystemRoleOwner},
		{name: "empty role remains representable", target: 0, role: store.SystemRole("")},
		{name: "unknown role remains representable", target: -44, role: store.SystemRole("unknown")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := RoleChangeCommand{
				TargetUserID: tt.target,
				NewRole:      tt.role,
			}
			if command.TargetUserID != tt.target {
				t.Fatalf("TargetUserID = %d, want %d", command.TargetUserID, tt.target)
			}
			if command.NewRole != tt.role {
				t.Fatalf("NewRole = %q, want %q", command.NewRole, tt.role)
			}
		})
	}
}

func TestDeleteCommandPreservesTarget(t *testing.T) {
	for _, target := range []int64{45, 0, -46} {
		command := DeleteCommand{TargetUserID: target}
		if command.TargetUserID != target {
			t.Fatalf("TargetUserID = %d, want %d", command.TargetUserID, target)
		}
	}
}
