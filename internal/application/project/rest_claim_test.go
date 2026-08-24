package project

import (
	"context"
	"errors"
	"testing"
)

type restClaimFake struct {
	trace projectServiceTrace

	claimCalls     int
	claimCtx       context.Context
	claimProjectID int64
	claimActorID   int64
	claimErr       error

	publishCalls     int
	publishCtx       context.Context
	publishProjectID int64
}

func (f *restClaimFake) ClaimTemporaryBoard(
	ctx context.Context,
	projectID int64,
	actorID int64,
) error {
	f.trace.add("claim")
	f.claimCalls++
	f.claimCtx = ctx
	f.claimProjectID = projectID
	f.claimActorID = actorID
	return f.claimErr
}

func (f *restClaimFake) PublishBoardClaimed(ctx context.Context, projectID int64) {
	f.trace.add("publish-board-claimed")
	f.publishCalls++
	f.publishCtx = ctx
	f.publishProjectID = projectID
}

func TestRESTClaimUsesConditionalMutationAsSoleAuthorityThenPublishes(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "rest-claim")
	fake := &restClaimFake{}
	service := NewRESTClaimService(RESTClaimServiceDependencies{Claims: fake, Publisher: fake})
	prepared := service.Prepare(ctx, ClaimCommand{ProjectID: 91, ActorUserID: 92})
	assertProjectServiceTrace(t, &fake.trace)
	if fake.claimCalls != 0 || fake.publishCalls != 0 {
		t.Fatalf("Prepare() performed claim/publication = %d/%d", fake.claimCalls, fake.publishCalls)
	}

	if err := prepared.Claim(); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	assertProjectServiceTrace(t, &fake.trace, "claim", "publish-board-claimed")
	if fake.claimCalls != 1 || fake.publishCalls != 1 ||
		fake.claimProjectID != 91 || fake.claimActorID != 92 || fake.publishProjectID != 91 {
		t.Fatalf("claim/publish captures = %+v", fake)
	}
	assertProjectServiceContext(t, fake.claimCtx, ctx)
	assertProjectServiceContext(t, fake.publishCtx, ctx)
}

func TestRESTClaimReturnsConditionalMutationErrorWithoutPublicationOrRetry(t *testing.T) {
	wantErr := errors.New("conditional claim failed")
	fake := &restClaimFake{claimErr: wantErr}
	prepared := NewRESTClaimService(RESTClaimServiceDependencies{Claims: fake, Publisher: fake}).Prepare(
		context.Background(),
		ClaimCommand{ProjectID: 93, ActorUserID: 94},
	)
	if err := prepared.Claim(); err != wantErr {
		t.Fatalf("Claim() error = %v, want exact conditional mutation error", err)
	}
	assertProjectServiceTrace(t, &fake.trace, "claim")
	if fake.claimCalls != 1 || fake.publishCalls != 0 {
		t.Fatalf("claim/publish calls = %d/%d", fake.claimCalls, fake.publishCalls)
	}
}

func TestRESTClaimAllowsNilPublisher(t *testing.T) {
	fake := &restClaimFake{}
	prepared := NewRESTClaimService(RESTClaimServiceDependencies{Claims: fake}).Prepare(
		context.Background(),
		ClaimCommand{ProjectID: 95, ActorUserID: 96},
	)
	if err := prepared.Claim(); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	assertProjectServiceTrace(t, &fake.trace, "claim")
}
