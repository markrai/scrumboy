package project

import (
	"context"
	"errors"
	"testing"

	"scrumboy/internal/store"
)

type restDeletionFake struct {
	trace projectServiceTrace

	deleteCalls     int
	deleteCtx       context.Context
	deleteProjectID int64
	deleteActorID   int64
	deleteResult    store.DeletedProjectSnapshot
	deleteErr       error

	publishCalls    int
	publishCtx      context.Context
	publishSnapshot store.DeletedProjectSnapshot
	mutatePublished bool
}

func (f *restDeletionFake) DeleteProject(
	ctx context.Context,
	projectID int64,
	actorID int64,
) (store.DeletedProjectSnapshot, error) {
	f.trace.add("delete")
	f.deleteCalls++
	f.deleteCtx = ctx
	f.deleteProjectID = projectID
	f.deleteActorID = actorID
	return f.deleteResult, f.deleteErr
}

func (f *restDeletionFake) PublishProjectDeleted(
	ctx context.Context,
	snapshot store.DeletedProjectSnapshot,
) {
	f.trace.add("publish-project-deleted")
	f.publishCalls++
	f.publishCtx = ctx
	f.publishSnapshot = snapshot
	if f.mutatePublished && len(snapshot.MemberUserIDs) > 0 {
		snapshot.MemberUserIDs[0] = 999
	}
}

func TestRESTDeletionDeletesExactlyOnceThenPublishesSnapshot(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "rest-delete")
	sourceMembers := []int64{2, 3}
	fake := &restDeletionFake{
		deleteResult: store.DeletedProjectSnapshot{
			ProjectID:     71,
			Name:          "Deleted project",
			MemberUserIDs: sourceMembers,
		},
		mutatePublished: true,
	}
	service := NewRESTDeletionService(RESTDeletionServiceDependencies{Projects: fake, Publisher: fake})
	prepared := service.Prepare(ctx, RESTDeletionCommand{ProjectID: 71, ActorUserID: 72})
	assertProjectServiceTrace(t, &fake.trace)
	if fake.deleteCalls != 0 || fake.publishCalls != 0 {
		t.Fatalf("Prepare() performed deletion/publication = %d/%d", fake.deleteCalls, fake.publishCalls)
	}

	if err := prepared.Delete(); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertProjectServiceTrace(t, &fake.trace, "delete", "publish-project-deleted")
	if fake.deleteCalls != 1 || fake.publishCalls != 1 ||
		fake.deleteProjectID != 71 || fake.deleteActorID != 72 {
		t.Fatalf("delete/publish captures = %+v", fake)
	}
	assertProjectServiceContext(t, fake.deleteCtx, ctx)
	assertProjectServiceContext(t, fake.publishCtx, ctx)
	if fake.publishSnapshot.ProjectID != 71 || fake.publishSnapshot.Name != "Deleted project" {
		t.Fatalf("published snapshot = %+v", fake.publishSnapshot)
	}
	if sourceMembers[0] != 2 {
		t.Fatalf("publisher mutation escaped into store snapshot: %v", sourceMembers)
	}
}

func TestRESTDeletionReturnsMutationErrorWithoutPublication(t *testing.T) {
	wantErr := errors.New("delete failed")
	fake := &restDeletionFake{deleteErr: wantErr}
	prepared := NewRESTDeletionService(RESTDeletionServiceDependencies{Projects: fake, Publisher: fake}).Prepare(
		context.Background(),
		RESTDeletionCommand{ProjectID: 73, ActorUserID: 74},
	)
	if err := prepared.Delete(); err != wantErr {
		t.Fatalf("Delete() error = %v, want exact mutation error", err)
	}
	assertProjectServiceTrace(t, &fake.trace, "delete")
	if fake.deleteCalls != 1 || fake.publishCalls != 0 {
		t.Fatalf("delete/publish calls = %d/%d", fake.deleteCalls, fake.publishCalls)
	}
}

func TestRESTDeletionAllowsNilPublisher(t *testing.T) {
	fake := &restDeletionFake{deleteResult: store.DeletedProjectSnapshot{ProjectID: 75}}
	prepared := NewRESTDeletionService(RESTDeletionServiceDependencies{Projects: fake}).Prepare(
		context.Background(),
		RESTDeletionCommand{ProjectID: 75, ActorUserID: 76},
	)
	if err := prepared.Delete(); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertProjectServiceTrace(t, &fake.trace, "delete")
}
