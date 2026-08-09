package todolink

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

type restMutationTestContextKey struct{}

type restMutationSourceCall struct {
	ctx           context.Context
	projectID     int64
	sourceLocalID int64
	mode          store.Mode
}

type restMutationStoreCall struct {
	operation     string
	ctx           context.Context
	projectID     int64
	sourceLocalID int64
	targetLocalID int64
	linkType      string
	mode          store.Mode
}

type restMutationPublishCall struct {
	ctx       context.Context
	projectID int64
}

type restMutationFake struct {
	trace []string

	sourceCalls []restMutationSourceCall
	sourceTodo  store.Todo
	sourceErr   error

	mutationCalls   []restMutationStoreCall
	addErr          error
	removeErr       error
	honorContextErr bool

	publishCalls []restMutationPublishCall
}

func (f *restMutationFake) GetTodoByLocalID(
	ctx context.Context,
	projectID int64,
	localID int64,
	mode store.Mode,
) (store.Todo, error) {
	f.trace = append(f.trace, "lookup")
	f.sourceCalls = append(f.sourceCalls, restMutationSourceCall{
		ctx:           ctx,
		projectID:     projectID,
		sourceLocalID: localID,
		mode:          mode,
	})
	if f.honorContextErr && ctx.Err() != nil {
		return store.Todo{}, ctx.Err()
	}
	return f.sourceTodo, f.sourceErr
}

func (f *restMutationFake) AddLink(
	ctx context.Context,
	projectID int64,
	fromLocalID int64,
	toLocalID int64,
	linkType string,
	mode store.Mode,
) error {
	f.trace = append(f.trace, "add")
	f.mutationCalls = append(f.mutationCalls, restMutationStoreCall{
		operation:     "add",
		ctx:           ctx,
		projectID:     projectID,
		sourceLocalID: fromLocalID,
		targetLocalID: toLocalID,
		linkType:      linkType,
		mode:          mode,
	})
	if f.honorContextErr && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.addErr
}

func (f *restMutationFake) RemoveLink(
	ctx context.Context,
	projectID int64,
	fromLocalID int64,
	toLocalID int64,
	mode store.Mode,
) error {
	f.trace = append(f.trace, "remove")
	f.mutationCalls = append(f.mutationCalls, restMutationStoreCall{
		operation:     "remove",
		ctx:           ctx,
		projectID:     projectID,
		sourceLocalID: fromLocalID,
		targetLocalID: toLocalID,
		mode:          mode,
	})
	if f.honorContextErr && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.removeErr
}

func (f *restMutationFake) PublishTodoLinksUpdated(ctx context.Context, projectID int64) {
	f.trace = append(f.trace, "publish")
	f.publishCalls = append(f.publishCalls, restMutationPublishCall{
		ctx:       ctx,
		projectID: projectID,
	})
}

func newRESTMutationTestService(fake *restMutationFake) *RESTMutationService {
	return NewRESTMutationService(RESTMutationServiceDependencies{
		Sources:   fake,
		Mutations: fake,
		Publisher: fake,
	})
}

func restMutationTestContext(marker string) context.Context {
	return context.WithValue(context.Background(), restMutationTestContextKey{}, marker)
}

func assertRESTMutationTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("call trace=%v want=%v", got, want)
	}
}

func prepareRESTMutationForTest(
	t *testing.T,
	fake *restMutationFake,
	ctx context.Context,
	target ResolvedRESTMutationTarget,
) *PreparedRESTMutation {
	t.Helper()
	prepared, err := newRESTMutationTestService(fake).Prepare(ctx, target)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return prepared
}

func TestRESTMutationPrepareBindsSourceGate(t *testing.T) {
	fake := &restMutationFake{}
	ctx := restMutationTestContext("source gate")
	target := ResolvedRESTMutationTarget{
		ProjectID:     41,
		SourceLocalID: 7,
		Mode:          store.ModeAnonymous,
	}

	prepared, err := newRESTMutationTestService(fake).Prepare(ctx, target)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prepared == nil {
		t.Fatal("Prepare returned nil capability")
	}
	if len(fake.sourceCalls) != 1 {
		t.Fatalf("source calls=%d want=1", len(fake.sourceCalls))
	}
	call := fake.sourceCalls[0]
	if call.ctx != ctx || call.ctx.Value(restMutationTestContextKey{}) != "source gate" ||
		call.projectID != 41 || call.sourceLocalID != 7 || call.mode != store.ModeAnonymous {
		t.Fatalf("source call=%+v", call)
	}
	if len(fake.mutationCalls) != 0 || len(fake.publishCalls) != 0 {
		t.Fatalf("preparation caused side effects: mutations=%+v publications=%+v", fake.mutationCalls, fake.publishCalls)
	}
	assertRESTMutationTrace(t, fake.trace, "lookup")
}

func TestRESTMutationPrepareFailureCreatesNoCapability(t *testing.T) {
	wantErr := errors.New("source lookup failed")
	fake := &restMutationFake{sourceErr: wantErr}

	prepared, err := newRESTMutationTestService(fake).Prepare(
		restMutationTestContext("source failure"),
		ResolvedRESTMutationTarget{ProjectID: 42, SourceLocalID: 8, Mode: store.ModeFull},
	)
	if prepared != nil {
		t.Fatalf("prepared=%v want=nil", prepared)
	}
	if err != wantErr {
		t.Fatalf("Prepare error=%v want same error %v", err, wantErr)
	}
	if len(fake.sourceCalls) != 1 || len(fake.mutationCalls) != 0 || len(fake.publishCalls) != 0 {
		t.Fatalf("calls: source=%d mutations=%d publications=%d", len(fake.sourceCalls), len(fake.mutationCalls), len(fake.publishCalls))
	}
	assertRESTMutationTrace(t, fake.trace, "lookup")
}

func TestPreparedRESTMutationBindsTargetByValueAndDiscardsSourceResult(t *testing.T) {
	// The deliberately inconsistent lookup result is not a supported store
	// behavior. It proves only that lookup is a prerequisite gate and that the
	// prepared target remains authoritative for orchestration identity.
	fake := &restMutationFake{sourceTodo: store.Todo{ProjectID: 999, LocalID: 888}}
	ctx := restMutationTestContext("bound target")
	target := ResolvedRESTMutationTarget{
		ProjectID:     51,
		SourceLocalID: 11,
		Mode:          store.ModeFull,
	}
	prepared := prepareRESTMutationForTest(t, fake, ctx, target)

	target.ProjectID = 151
	target.SourceLocalID = 111
	target.Mode = store.ModeAnonymous

	if err := prepared.Remove(RemoveCommand{TargetLocalID: 12}); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(fake.mutationCalls) != 1 {
		t.Fatalf("mutation calls=%d want=1", len(fake.mutationCalls))
	}
	call := fake.mutationCalls[0]
	if call.ctx != ctx || call.projectID != 51 || call.sourceLocalID != 11 ||
		call.targetLocalID != 12 || call.mode != store.ModeFull {
		t.Fatalf("remove call=%+v", call)
	}
	if len(fake.publishCalls) != 1 {
		t.Fatalf("publication calls=%d want=1", len(fake.publishCalls))
	}
	published := fake.publishCalls[0]
	if published.ctx != ctx || published.projectID != 51 {
		t.Fatalf("publication=%+v want bound context and original project 51", published)
	}
	assertRESTMutationTrace(t, fake.trace, "lookup", "remove", "publish")
}

func TestPreparedRESTMutationAdd(t *testing.T) {
	tests := []struct {
		name     string
		linkType string
	}{
		{name: "explicit link type", linkType: "blocks"},
		{name: "empty link type remains unchanged", linkType: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restMutationFake{}
			ctx := restMutationTestContext(tt.name)
			prepared := prepareRESTMutationForTest(t, fake, ctx, ResolvedRESTMutationTarget{
				ProjectID:     61,
				SourceLocalID: 21,
				Mode:          store.ModeAnonymous,
			})

			if err := prepared.Add(AddCommand{TargetLocalID: 22, LinkType: tt.linkType}); err != nil {
				t.Fatalf("Add: %v", err)
			}
			if len(fake.sourceCalls) != 1 || len(fake.mutationCalls) != 1 || len(fake.publishCalls) != 1 {
				t.Fatalf("calls: source=%d mutations=%d publications=%d", len(fake.sourceCalls), len(fake.mutationCalls), len(fake.publishCalls))
			}
			call := fake.mutationCalls[0]
			if call.operation != "add" || call.ctx != ctx || call.projectID != 61 ||
				call.sourceLocalID != 21 || call.targetLocalID != 22 || call.linkType != tt.linkType ||
				call.mode != store.ModeAnonymous {
				t.Fatalf("add call=%+v", call)
			}
			published := fake.publishCalls[0]
			if published.ctx != ctx || published.projectID != 61 {
				t.Fatalf("publication=%+v want bound context and project 61", published)
			}
			assertRESTMutationTrace(t, fake.trace, "lookup", "add", "publish")
		})
	}
}

func TestPreparedRESTMutationRemove(t *testing.T) {
	fake := &restMutationFake{}
	ctx := restMutationTestContext("remove")
	prepared := prepareRESTMutationForTest(t, fake, ctx, ResolvedRESTMutationTarget{
		ProjectID:     71,
		SourceLocalID: 31,
		Mode:          store.ModeFull,
	})

	if err := prepared.Remove(RemoveCommand{TargetLocalID: 32}); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(fake.sourceCalls) != 1 || len(fake.mutationCalls) != 1 || len(fake.publishCalls) != 1 {
		t.Fatalf("calls: source=%d mutations=%d publications=%d", len(fake.sourceCalls), len(fake.mutationCalls), len(fake.publishCalls))
	}
	call := fake.mutationCalls[0]
	if call.operation != "remove" || call.ctx != ctx || call.projectID != 71 ||
		call.sourceLocalID != 31 || call.targetLocalID != 32 || call.mode != store.ModeFull {
		t.Fatalf("remove call=%+v", call)
	}
	published := fake.publishCalls[0]
	if published.ctx != ctx || published.projectID != 71 {
		t.Fatalf("publication=%+v want bound context and project 71", published)
	}
	assertRESTMutationTrace(t, fake.trace, "lookup", "remove", "publish")
}

func TestPreparedRESTMutationFailureSuppressesPublication(t *testing.T) {
	addErr := errors.New("add failed")
	removeErr := errors.New("remove failed")
	tests := []struct {
		name      string
		operation string
		fake      *restMutationFake
		wantErr   error
	}{
		{name: "add", operation: "add", fake: &restMutationFake{addErr: addErr}, wantErr: addErr},
		{name: "remove", operation: "remove", fake: &restMutationFake{removeErr: removeErr}, wantErr: removeErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prepared := prepareRESTMutationForTest(t, tt.fake, restMutationTestContext(tt.name), ResolvedRESTMutationTarget{
				ProjectID:     81,
				SourceLocalID: 41,
				Mode:          store.ModeFull,
			})

			var err error
			if tt.operation == "add" {
				err = prepared.Add(AddCommand{TargetLocalID: 42, LinkType: "relates_to"})
			} else {
				err = prepared.Remove(RemoveCommand{TargetLocalID: 42})
			}
			if err != tt.wantErr {
				t.Fatalf("%s error=%v want same error %v", tt.operation, err, tt.wantErr)
			}
			if len(tt.fake.sourceCalls) != 1 || len(tt.fake.mutationCalls) != 1 || len(tt.fake.publishCalls) != 0 {
				t.Fatalf("calls: source=%d mutations=%d publications=%d", len(tt.fake.sourceCalls), len(tt.fake.mutationCalls), len(tt.fake.publishCalls))
			}
			assertRESTMutationTrace(t, tt.fake.trace, "lookup", tt.operation)
		})
	}
}

func TestPreparedRESTMutationUsesCancelledBoundContext(t *testing.T) {
	tests := []struct {
		name      string
		operation string
	}{
		{name: "add", operation: "add"},
		{name: "remove", operation: "remove"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restMutationFake{honorContextErr: true}
			ctx, cancel := context.WithCancel(restMutationTestContext("cancelled " + tt.name))
			prepared := prepareRESTMutationForTest(t, fake, ctx, ResolvedRESTMutationTarget{
				ProjectID:     91,
				SourceLocalID: 51,
				Mode:          store.ModeFull,
			})
			cancel()

			var err error
			if tt.operation == "add" {
				err = prepared.Add(AddCommand{TargetLocalID: 52, LinkType: "blocks"})
			} else {
				err = prepared.Remove(RemoveCommand{TargetLocalID: 52})
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s error=%v want=%v", tt.operation, err, context.Canceled)
			}
			if len(fake.sourceCalls) != 1 || len(fake.mutationCalls) != 1 || len(fake.publishCalls) != 0 {
				t.Fatalf("calls: source=%d mutations=%d publications=%d", len(fake.sourceCalls), len(fake.mutationCalls), len(fake.publishCalls))
			}
			if fake.mutationCalls[0].ctx != ctx {
				t.Fatalf("mutation context=%v want bound cancelled context", fake.mutationCalls[0].ctx)
			}
			assertRESTMutationTrace(t, fake.trace, "lookup", tt.operation)
		})
	}
}

func TestRESTMutationServiceNilPublisher(t *testing.T) {
	fake := &restMutationFake{}
	service := NewRESTMutationService(RESTMutationServiceDependencies{
		Sources:   fake,
		Mutations: fake,
	})
	prepared, err := service.Prepare(
		restMutationTestContext("nil publisher"),
		ResolvedRESTMutationTarget{ProjectID: 101, SourceLocalID: 61, Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if err := prepared.Add(AddCommand{TargetLocalID: 62, LinkType: "blocks"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(fake.sourceCalls) != 1 || len(fake.mutationCalls) != 1 || len(fake.publishCalls) != 0 {
		t.Fatalf("calls: source=%d mutations=%d external publications=%d", len(fake.sourceCalls), len(fake.mutationCalls), len(fake.publishCalls))
	}
	assertRESTMutationTrace(t, fake.trace, "lookup", "add")
}
