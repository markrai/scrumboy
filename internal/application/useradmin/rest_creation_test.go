package useradmin

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type restCreationContextKey struct{}

type restCreationFake struct {
	calls    int
	email    string
	name     string
	password string
	marker   any
	user     store.User
	err      error
}

func (f *restCreationFake) CreateUser(
	ctx context.Context,
	email string,
	password string,
	name string,
) (store.User, error) {
	f.calls++
	f.email = email
	f.password = password
	f.name = name
	f.marker = ctx.Value(restCreationContextKey{})
	return f.user, f.err
}

func TestRESTCreationServiceForwardsRawValuesContextAndResult(t *testing.T) {
	createdAt := time.Date(2026, time.August, 24, 20, 30, 0, 0, time.UTC)
	wantUser := store.User{
		ID:               71,
		Email:            "mixed.case@example.com",
		Name:             "Created Name",
		SystemRole:       store.SystemRoleUser,
		CreatedAt:        createdAt,
		HasLocalPassword: true,
	}
	fake := &restCreationFake{user: wantUser}
	service := NewRESTCreationService(RESTCreationServiceDependencies{Creations: fake})
	ctx := context.WithValue(context.Background(), restCreationContextKey{}, "creation-context")

	got, err := service.Create(ctx, CreateCommand{
		Email:    "  Mixed.Case@Example.COM  ",
		Name:     "  Created Name  ",
		Password: "  password123  ",
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if fake.calls != 1 || fake.email != "  Mixed.Case@Example.COM  " ||
		fake.password != "  password123  " || fake.name != "  Created Name  " ||
		fake.marker != "creation-context" {
		t.Fatalf("creation port capture = %+v", fake)
	}
	if !reflect.DeepEqual(got, wantUser) {
		t.Fatalf("Create() user = %+v, want %+v", got, wantUser)
	}
}

func TestRESTCreationServiceDoesNotValidateOrNormalize(t *testing.T) {
	tests := []struct {
		name    string
		command CreateCommand
	}{
		{name: "empty", command: CreateCommand{}},
		{name: "invalid-looking", command: CreateCommand{Email: "not-an-email", Name: "", Password: "short"}},
		{name: "padded", command: CreateCommand{Email: " USER@EXAMPLE.COM ", Name: " Name ", Password: " password123 "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restCreationFake{}
			service := NewRESTCreationService(RESTCreationServiceDependencies{Creations: fake})

			if _, err := service.Create(context.Background(), tt.command); err != nil {
				t.Fatalf("Create(): %v", err)
			}
			if fake.calls != 1 || fake.email != tt.command.Email ||
				fake.password != tt.command.Password || fake.name != tt.command.Name {
				t.Fatalf("creation port capture = %+v, command = %+v", fake, tt.command)
			}
		})
	}
}

func TestRESTCreationServicePreservesStoreErrorWithoutRetry(t *testing.T) {
	wantErr := errors.New("creation failed")
	fake := &restCreationFake{err: wantErr}
	service := NewRESTCreationService(RESTCreationServiceDependencies{Creations: fake})

	got, err := service.Create(context.Background(), CreateCommand{
		Email: "user@example.com", Name: "User", Password: "password123",
	})
	if !reflect.DeepEqual(got, store.User{}) || err != wantErr {
		t.Fatalf("Create() = (%+v, %v), want zero user and original error", got, err)
	}
	if fake.calls != 1 {
		t.Fatalf("CreateUser calls = %d, want 1", fake.calls)
	}
}
