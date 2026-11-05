package store_test

import (
	"context"
	"errors"
	"testing"

	"scrumboy/internal/store"
)

// Trello import makes its actor the new project's owner, so it refuses a service account.
func TestServiceAccount_TrelloImportCannotGrantOwnership(t *testing.T) {
	st, sqlDB, cleanup := newTrelloTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@example.com", "CorrectHorse123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := st.CreateServiceAccount(ctx, owner.ID, "Bot")
	if err != nil {
		t.Fatal(err)
	}
	bundle := buildBundle(t)
	svcCtx := store.WithUserID(ctx, svc.ID)

	for name, mode := range map[string]store.Mode{"full": store.ModeFull, "anonymous": store.ModeAnonymous} {
		if _, err := st.ImportTrelloProject(svcCtx, bundle.ExportData, bundle.ProjectImportMetadata, bundle.TodoImportMetadataByLocalID, mode); !errors.Is(err, store.ErrServiceAccountRestricted) {
			t.Errorf("%s mode: err = %v, want ErrServiceAccountRestricted", name, err)
		}
	}
	var projects int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if projects != 0 {
		t.Fatalf("Trello import created %d projects", projects)
	}

	// The owner's import is unaffected.
	project, err := st.ImportTrelloProject(store.WithUserID(ctx, owner.ID), bundle.ExportData, bundle.ProjectImportMetadata, bundle.TodoImportMetadataByLocalID, store.ModeFull)
	if err != nil {
		t.Fatalf("human import: %v", err)
	}
	if project.OwnerUserID == nil || *project.OwnerUserID != owner.ID {
		t.Fatalf("owner = %v", project.OwnerUserID)
	}
}
