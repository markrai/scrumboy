package project

import "context"

// RESTClaimPublisher publishes the semantic invalidation required after one
// successful Temporary Board claim.
type RESTClaimPublisher interface {
	PublishBoardClaimed(ctx context.Context, projectID int64)
}

type nopRESTClaimPublisher struct{}

func (nopRESTClaimPublisher) PublishBoardClaimed(context.Context, int64) {}

// RESTClaimServiceDependencies contains only the authoritative conditional
// claim and success-publication capabilities.
type RESTClaimServiceDependencies struct {
	Claims    TemporaryBoardClaimStore
	Publisher RESTClaimPublisher
}

// RESTClaimService deliberately performs no eligibility read. The store's
// conditional claim remains the final authorization and concurrency authority.
type RESTClaimService struct {
	claims    TemporaryBoardClaimStore
	publisher RESTClaimPublisher
}

// NewRESTClaimService constructs the additive REST claim service.
func NewRESTClaimService(deps RESTClaimServiceDependencies) *RESTClaimService {
	publisher := deps.Publisher
	if publisher == nil {
		publisher = nopRESTClaimPublisher{}
	}
	return &RESTClaimService{claims: deps.Claims, publisher: publisher}
}

// PreparedRESTClaim binds the adapter-established project and actor without a
// project read, eligibility check, or write.
type PreparedRESTClaim struct {
	ctx         context.Context
	service     *RESTClaimService
	projectID   int64
	actorUserID int64
}

// Prepare binds scalar claim values without consulting persistence.
func (s *RESTClaimService) Prepare(ctx context.Context, command ClaimCommand) *PreparedRESTClaim {
	return &PreparedRESTClaim{
		ctx:         ctx,
		service:     s,
		projectID:   command.ProjectID,
		actorUserID: command.ActorUserID,
	}
}

// Claim calls the authoritative conditional store mutation exactly once and
// publishes only after success.
func (p *PreparedRESTClaim) Claim() error {
	if err := p.service.claims.ClaimTemporaryBoard(p.ctx, p.projectID, p.actorUserID); err != nil {
		return err
	}
	p.service.publisher.PublishBoardClaimed(p.ctx, p.projectID)
	return nil
}
