package todolink

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

var _ MCPAccessStore = (*store.Store)(nil)
var _ LinkReadStore = (*store.Store)(nil)

type mcpMutationTestContextKey struct{}

type mcpMutationAccessCall struct {
	ctx  context.Context
	slug string
	mode store.Mode
}

type mcpMutationSourceCall struct {
	ctx           context.Context
	projectID     int64
	sourceLocalID int64
	mode          store.Mode
}

type mcpMutationStoreCall struct {
	operation     string
	ctx           context.Context
	projectID     int64
	sourceLocalID int64
	targetLocalID int64
	linkType      string
	mode          store.Mode
}

type mcpMutationReadCall struct {
	direction     string
	ctx           context.Context
	projectID     int64
	sourceLocalID int64
	mode          store.Mode
}

type mcpMutationFake struct {
	trace []string

	accessCalls    []mcpMutationAccessCall
	projectContext store.ProjectContext
	accessErr      error

	sourceCalls []mcpMutationSourceCall
	sourceTodo  store.Todo
	sourceErr   error

	mutationCalls []mcpMutationStoreCall
	addErr        error
	removeErr     error
	committed     bool

	readCalls   []mcpMutationReadCall
	outbound    []store.TodoLinkTarget
	inbound     []store.TodoLinkTarget
	outboundErr error
	inboundErr  error

	honorContextErr bool
	cancelAfter     string
	cancel          context.CancelFunc
}

func newMCPMutationFake() *mcpMutationFake {
	return &mcpMutationFake{
		projectContext: store.ProjectContext{
			Project: store.Project{ID: 101, Slug: "resolved-project"},
			Role:    store.RoleViewer,
		},
		sourceTodo: store.Todo{ID: 9001, ProjectID: 999, LocalID: 998},
		outbound: []store.TodoLinkTarget{
			{LocalID: 301, Title: "Outbound", LinkType: "blocks"},
		},
		inbound: []store.TodoLinkTarget{
			{LocalID: 302, Title: "Inbound", LinkType: "parent"},
		},
	}
}

func (f *mcpMutationFake) GetProjectContextBySlug(
	ctx context.Context,
	slug string,
	mode store.Mode,
) (store.ProjectContext, error) {
	f.trace = append(f.trace, "access")
	f.accessCalls = append(f.accessCalls, mcpMutationAccessCall{ctx: ctx, slug: slug, mode: mode})
	if f.accessErr != nil {
		return store.ProjectContext{}, f.accessErr
	}
	if f.honorContextErr && ctx.Err() != nil {
		return store.ProjectContext{}, ctx.Err()
	}
	if f.cancelAfter == "access" && f.cancel != nil {
		f.cancel()
	}
	return f.projectContext, nil
}

func (f *mcpMutationFake) GetTodoByLocalID(
	ctx context.Context,
	projectID int64,
	localID int64,
	mode store.Mode,
) (store.Todo, error) {
	f.trace = append(f.trace, "source")
	f.sourceCalls = append(f.sourceCalls, mcpMutationSourceCall{
		ctx:           ctx,
		projectID:     projectID,
		sourceLocalID: localID,
		mode:          mode,
	})
	if f.sourceErr != nil {
		return store.Todo{}, f.sourceErr
	}
	if f.honorContextErr && ctx.Err() != nil {
		return store.Todo{}, ctx.Err()
	}
	if f.cancelAfter == "source" && f.cancel != nil {
		f.cancel()
	}
	return f.sourceTodo, nil
}

func (f *mcpMutationFake) AddLink(
	ctx context.Context,
	projectID int64,
	fromLocalID int64,
	toLocalID int64,
	linkType string,
	mode store.Mode,
) error {
	f.trace = append(f.trace, "add")
	f.mutationCalls = append(f.mutationCalls, mcpMutationStoreCall{
		operation:     "add",
		ctx:           ctx,
		projectID:     projectID,
		sourceLocalID: fromLocalID,
		targetLocalID: toLocalID,
		linkType:      linkType,
		mode:          mode,
	})
	if f.addErr != nil {
		return f.addErr
	}
	if f.honorContextErr && ctx.Err() != nil {
		return ctx.Err()
	}
	f.committed = true
	if f.cancelAfter == "add" && f.cancel != nil {
		f.cancel()
	}
	return nil
}

func (f *mcpMutationFake) RemoveLink(
	ctx context.Context,
	projectID int64,
	fromLocalID int64,
	toLocalID int64,
	mode store.Mode,
) error {
	f.trace = append(f.trace, "remove")
	f.mutationCalls = append(f.mutationCalls, mcpMutationStoreCall{
		operation:     "remove",
		ctx:           ctx,
		projectID:     projectID,
		sourceLocalID: fromLocalID,
		targetLocalID: toLocalID,
		mode:          mode,
	})
	if f.removeErr != nil {
		return f.removeErr
	}
	if f.honorContextErr && ctx.Err() != nil {
		return ctx.Err()
	}
	f.committed = true
	if f.cancelAfter == "remove" && f.cancel != nil {
		f.cancel()
	}
	return nil
}

func (f *mcpMutationFake) ListLinksForTodo(
	ctx context.Context,
	projectID int64,
	localID int64,
	mode store.Mode,
) ([]store.TodoLinkTarget, error) {
	f.trace = append(f.trace, "outbound")
	f.readCalls = append(f.readCalls, mcpMutationReadCall{
		direction:     "outbound",
		ctx:           ctx,
		projectID:     projectID,
		sourceLocalID: localID,
		mode:          mode,
	})
	if f.outboundErr != nil {
		return nil, f.outboundErr
	}
	if f.honorContextErr && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if f.cancelAfter == "outbound" && f.cancel != nil {
		f.cancel()
	}
	return f.outbound, nil
}

func (f *mcpMutationFake) ListBacklinksForTodo(
	ctx context.Context,
	projectID int64,
	localID int64,
	mode store.Mode,
) ([]store.TodoLinkTarget, error) {
	f.trace = append(f.trace, "inbound")
	f.readCalls = append(f.readCalls, mcpMutationReadCall{
		direction:     "inbound",
		ctx:           ctx,
		projectID:     projectID,
		sourceLocalID: localID,
		mode:          mode,
	})
	if f.inboundErr != nil {
		return nil, f.inboundErr
	}
	if f.honorContextErr && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if f.cancelAfter == "inbound" && f.cancel != nil {
		f.cancel()
	}
	return f.inbound, nil
}

func newMCPMutationTestService(fake *mcpMutationFake) *MCPMutationService {
	return NewMCPMutationService(MCPMutationServiceDependencies{
		Access:    fake,
		Sources:   fake,
		Mutations: fake,
		Links:     fake,
	})
}

func mcpMutationTestContext(marker string) context.Context {
	return context.WithValue(context.Background(), mcpMutationTestContextKey{}, marker)
}

func assertMCPMutationTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("call trace=%v want=%v", got, want)
	}
}

func assertMCPMutationContext(t *testing.T, got context.Context, wantMarker string) {
	t.Helper()
	if got.Value(mcpMutationTestContextKey{}) != wantMarker {
		t.Fatalf("context marker=%v want=%q", got.Value(mcpMutationTestContextKey{}), wantMarker)
	}
}

func prepareMCPMutationForTest(
	t *testing.T,
	fake *mcpMutationFake,
	ctx context.Context,
	target MCPMutationTarget,
) *PreparedMCPMutation {
	t.Helper()
	prepared, err := newMCPMutationTestService(fake).Prepare(ctx, target)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	return prepared
}

func TestMCPMutationServiceConstructorIsInert(t *testing.T) {
	fake := newMCPMutationFake()
	service := newMCPMutationTestService(fake)

	if service == nil {
		t.Fatal("NewMCPMutationService() returned nil")
	}
	if service.access != fake || service.sources != fake || service.mutations != fake || service.links != fake {
		t.Fatal("NewMCPMutationService() did not preserve supplied dependencies")
	}
	assertMCPMutationTrace(t, fake.trace)
}

func TestMCPMutationPrepareBindsAccessAndSource(t *testing.T) {
	fake := newMCPMutationFake()
	ctx := mcpMutationTestContext("prepare")
	target := MCPMutationTarget{ProjectSlug: "requested-project", SourceLocalID: 201, Mode: store.ModeFull}

	prepared, err := newMCPMutationTestService(fake).Prepare(ctx, target)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if prepared == nil {
		t.Fatal("Prepare() returned nil capability")
	}
	assertMCPMutationTrace(t, fake.trace, "access", "source")
	if len(fake.accessCalls) != 1 || len(fake.sourceCalls) != 1 {
		t.Fatalf("access calls=%d source calls=%d want 1 each", len(fake.accessCalls), len(fake.sourceCalls))
	}
	access := fake.accessCalls[0]
	assertMCPMutationContext(t, access.ctx, "prepare")
	if access.slug != target.ProjectSlug || access.mode != target.Mode {
		t.Fatalf("access call=%+v want slug=%q mode=%q", access, target.ProjectSlug, target.Mode)
	}
	source := fake.sourceCalls[0]
	assertMCPMutationContext(t, source.ctx, "prepare")
	if source.projectID != fake.projectContext.Project.ID || source.sourceLocalID != target.SourceLocalID || source.mode != target.Mode {
		t.Fatalf("source call=%+v want project=%d source=%d mode=%q", source, fake.projectContext.Project.ID, target.SourceLocalID, target.Mode)
	}
	if prepared.ctx != ctx || prepared.projectID != fake.projectContext.Project.ID || prepared.sourceLocalID != target.SourceLocalID || prepared.mode != target.Mode {
		t.Fatalf("prepared binding=%+v", prepared)
	}
	if len(fake.mutationCalls) != 0 || len(fake.readCalls) != 0 {
		t.Fatalf("preparation performed operation calls: mutations=%d reads=%d", len(fake.mutationCalls), len(fake.readCalls))
	}
}

func TestMCPMutationPrepareAccessFailureReturnsRawError(t *testing.T) {
	fake := newMCPMutationFake()
	wantErr := errors.New("private access diagnostic")
	fake.accessErr = wantErr

	prepared, err := newMCPMutationTestService(fake).Prepare(
		mcpMutationTestContext("access failure"),
		MCPMutationTarget{ProjectSlug: "missing", SourceLocalID: 201, Mode: store.ModeFull},
	)
	if prepared != nil {
		t.Fatalf("Prepare() capability=%+v want nil", prepared)
	}
	if err != wantErr {
		t.Fatalf("Prepare() error=%v want exact %v", err, wantErr)
	}
	if errors.Is(err, ErrMCPSourceLookupFailed) || errors.Is(err, ErrMCPProjectionFailed) {
		t.Fatalf("raw access error unexpectedly classified: %v", err)
	}
	assertMCPMutationTrace(t, fake.trace, "access")
	if len(fake.sourceCalls) != 0 || len(fake.mutationCalls) != 0 || len(fake.readCalls) != 0 {
		t.Fatal("access failure did not short-circuit remaining stages")
	}
}

func TestMCPMutationPrepareClassifiesSourceFailureWithoutLeakingCause(t *testing.T) {
	fake := newMCPMutationFake()
	cause := fmt.Errorf("private source diagnostic: %w", store.ErrUnauthorized)
	fake.sourceErr = cause

	prepared, err := newMCPMutationTestService(fake).Prepare(
		mcpMutationTestContext("source failure"),
		MCPMutationTarget{ProjectSlug: "project", SourceLocalID: 201, Mode: store.ModeFull},
	)
	if prepared != nil {
		t.Fatalf("Prepare() capability=%+v want nil", prepared)
	}
	if err == nil {
		t.Fatal("Prepare() error=nil")
	}
	if err.Error() != ErrMCPSourceLookupFailed.Error() {
		t.Fatalf("error text=%q want static %q", err.Error(), ErrMCPSourceLookupFailed.Error())
	}
	if strings.Contains(err.Error(), cause.Error()) || strings.Contains(err.Error(), "private source diagnostic") {
		t.Fatalf("source wrapper leaked cause text: %q", err.Error())
	}
	if errors.Unwrap(err) != cause {
		t.Fatalf("errors.Unwrap(error)=%v want exact cause %v", errors.Unwrap(err), cause)
	}
	if !errors.Is(err, ErrMCPSourceLookupFailed) || !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("source error classification/cause chain missing: %v", err)
	}
	if errors.Is(err, ErrMCPProjectionFailed) {
		t.Fatalf("source error unexpectedly classified as projection: %v", err)
	}
	assertMCPMutationTrace(t, fake.trace, "access", "source")
	if len(fake.mutationCalls) != 0 || len(fake.readCalls) != 0 {
		t.Fatal("source failure did not short-circuit operation stages")
	}
}

func TestPreparedMCPMutationBindsTargetByValueAndKeepsResolvedIdentity(t *testing.T) {
	fake := newMCPMutationFake()
	fake.projectContext.Project.ID = 151
	fake.sourceTodo = store.Todo{ID: 9000, ProjectID: 991, LocalID: 992}
	ctx := mcpMutationTestContext("target copy")
	target := MCPMutationTarget{ProjectSlug: "original", SourceLocalID: 211, Mode: store.ModeFull}
	prepared := prepareMCPMutationForTest(t, fake, ctx, target)

	target.ProjectSlug = "replacement"
	target.SourceLocalID = 811
	target.Mode = store.ModeAnonymous

	if _, err := prepared.Add(AddCommand{TargetLocalID: 212, LinkType: "blocks"}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	assertMCPMutationTrace(t, fake.trace, "access", "source", "add", "outbound", "inbound")
	if fake.accessCalls[0].slug != "original" {
		t.Fatalf("access slug=%q want original", fake.accessCalls[0].slug)
	}
	for _, call := range fake.mutationCalls {
		if call.projectID != 151 || call.sourceLocalID != 211 || call.mode != store.ModeFull {
			t.Fatalf("mutation used replacement/source-result identity: %+v", call)
		}
	}
	for _, call := range fake.readCalls {
		if call.projectID != 151 || call.sourceLocalID != 211 || call.mode != store.ModeFull {
			t.Fatalf("read used replacement/source-result identity: %+v", call)
		}
	}
}

func TestPreparedMCPMutationAdd(t *testing.T) {
	t.Run("explicit link type and exact result", func(t *testing.T) {
		fake := newMCPMutationFake()
		ctx := mcpMutationTestContext("add")
		prepared := prepareMCPMutationForTest(t, fake, ctx, MCPMutationTarget{
			ProjectSlug:   "project",
			SourceLocalID: 221,
			Mode:          store.ModeFull,
		})

		result, err := prepared.Add(AddCommand{TargetLocalID: 222, LinkType: "duplicates"})
		if err != nil {
			t.Fatalf("Add() error = %v", err)
		}
		assertMCPMutationTrace(t, fake.trace, "access", "source", "add", "outbound", "inbound")
		if len(fake.accessCalls) != 1 || len(fake.sourceCalls) != 1 || len(fake.mutationCalls) != 1 || len(fake.readCalls) != 2 {
			t.Fatalf("calls access=%d source=%d mutation=%d reads=%d", len(fake.accessCalls), len(fake.sourceCalls), len(fake.mutationCalls), len(fake.readCalls))
		}
		call := fake.mutationCalls[0]
		assertMCPMutationContext(t, call.ctx, "add")
		if call.operation != "add" || call.projectID != 101 || call.sourceLocalID != 221 || call.targetLocalID != 222 || call.linkType != "duplicates" || call.mode != store.ModeFull {
			t.Fatalf("add call=%+v", call)
		}
		for _, read := range fake.readCalls {
			assertMCPMutationContext(t, read.ctx, "add")
			if read.projectID != 101 || read.sourceLocalID != 221 || read.mode != store.ModeFull {
				t.Fatalf("read call=%+v", read)
			}
		}
		if !reflect.DeepEqual(result, LinkSet{Outbound: fake.outbound, Inbound: fake.inbound}) {
			t.Fatalf("Add() result=%+v", result)
		}
		if &result.Outbound[0] != &fake.outbound[0] || &result.Inbound[0] != &fake.inbound[0] {
			t.Fatal("Add() copied read slices")
		}
		if !fake.committed {
			t.Fatal("Add() did not reach successful mutation")
		}
	})

	t.Run("empty link type is unchanged", func(t *testing.T) {
		fake := newMCPMutationFake()
		prepared := prepareMCPMutationForTest(t, fake, mcpMutationTestContext("empty type"), MCPMutationTarget{
			ProjectSlug:   "project",
			SourceLocalID: 223,
			Mode:          store.ModeFull,
		})
		if _, err := prepared.Add(AddCommand{TargetLocalID: 224}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
		if got := fake.mutationCalls[0].linkType; got != "" {
			t.Fatalf("Add() link type=%q want unchanged empty value", got)
		}
	})
}

func TestPreparedMCPMutationRemove(t *testing.T) {
	fake := newMCPMutationFake()
	ctx := mcpMutationTestContext("remove")
	prepared := prepareMCPMutationForTest(t, fake, ctx, MCPMutationTarget{
		ProjectSlug:   "project",
		SourceLocalID: 231,
		Mode:          store.ModeFull,
	})

	result, err := prepared.Remove(RemoveCommand{TargetLocalID: 232})
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	assertMCPMutationTrace(t, fake.trace, "access", "source", "remove", "outbound", "inbound")
	if len(fake.accessCalls) != 1 || len(fake.sourceCalls) != 1 || len(fake.mutationCalls) != 1 || len(fake.readCalls) != 2 {
		t.Fatalf("calls access=%d source=%d mutation=%d reads=%d", len(fake.accessCalls), len(fake.sourceCalls), len(fake.mutationCalls), len(fake.readCalls))
	}
	call := fake.mutationCalls[0]
	assertMCPMutationContext(t, call.ctx, "remove")
	if call.operation != "remove" || call.projectID != 101 || call.sourceLocalID != 231 || call.targetLocalID != 232 || call.mode != store.ModeFull {
		t.Fatalf("remove call=%+v", call)
	}
	for _, read := range fake.readCalls {
		assertMCPMutationContext(t, read.ctx, "remove")
		if read.projectID != 101 || read.sourceLocalID != 231 || read.mode != store.ModeFull {
			t.Fatalf("read call=%+v", read)
		}
	}
	if !reflect.DeepEqual(result, LinkSet{Outbound: fake.outbound, Inbound: fake.inbound}) {
		t.Fatalf("Remove() result=%+v", result)
	}
}

func TestPreparedMCPMutationPreservesNilAndEmptyLinkSlices(t *testing.T) {
	fake := newMCPMutationFake()
	fake.outbound = nil
	fake.inbound = make([]store.TodoLinkTarget, 0)
	prepared := prepareMCPMutationForTest(t, fake, mcpMutationTestContext("slices"), MCPMutationTarget{
		ProjectSlug:   "project",
		SourceLocalID: 241,
		Mode:          store.ModeFull,
	})

	result, err := prepared.Add(AddCommand{TargetLocalID: 242, LinkType: "parent"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if result.Outbound != nil {
		t.Fatalf("Outbound=%v want nil", result.Outbound)
	}
	if result.Inbound == nil || len(result.Inbound) != 0 {
		t.Fatalf("Inbound=%v want allocated empty slice", result.Inbound)
	}
}

func TestPreparedMCPMutationFailuresShortCircuitReads(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		cause     error
		wantTrace []string
	}{
		{
			name:      "add",
			operation: "add",
			cause:     fmt.Errorf("private add diagnostic: %w", store.ErrUnauthorized),
			wantTrace: []string{"access", "source", "add"},
		},
		{
			name:      "remove",
			operation: "remove",
			cause:     errors.New("private remove diagnostic"),
			wantTrace: []string{"access", "source", "remove"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := newMCPMutationFake()
			prepared := prepareMCPMutationForTest(t, fake, mcpMutationTestContext(test.name), MCPMutationTarget{
				ProjectSlug:   "project",
				SourceLocalID: 251,
				Mode:          store.ModeFull,
			})
			var (
				result LinkSet
				err    error
			)
			if test.operation == "add" {
				fake.addErr = test.cause
				result, err = prepared.Add(AddCommand{TargetLocalID: 252, LinkType: "blocks"})
			} else {
				fake.removeErr = test.cause
				result, err = prepared.Remove(RemoveCommand{TargetLocalID: 252})
			}
			if err != test.cause {
				t.Fatalf("operation error=%v want exact %v", err, test.cause)
			}
			if errors.Is(err, ErrMCPSourceLookupFailed) || errors.Is(err, ErrMCPProjectionFailed) {
				t.Fatalf("mutation error unexpectedly classified: %v", err)
			}
			if !reflect.DeepEqual(result, LinkSet{}) {
				t.Fatalf("result=%+v want zero", result)
			}
			assertMCPMutationTrace(t, fake.trace, test.wantTrace...)
			if len(fake.mutationCalls) != 1 || len(fake.readCalls) != 0 || fake.committed {
				t.Fatalf("mutation calls=%d reads=%d committed=%v", len(fake.mutationCalls), len(fake.readCalls), fake.committed)
			}
		})
	}
}

func TestPreparedMCPMutationClassifiesProjectionFailuresWithoutLeakingCause(t *testing.T) {
	tests := []struct {
		name        string
		operation   string
		outboundErr error
		inboundErr  error
		underlying  error
		wantTrace   []string
		wantReads   int
	}{
		{
			name:        "add outbound",
			operation:   "add",
			outboundErr: fmt.Errorf("private outbound diagnostic: %w", store.ErrUnauthorized),
			underlying:  store.ErrUnauthorized,
			wantTrace:   []string{"access", "source", "add", "outbound"},
			wantReads:   1,
		},
		{
			name:       "remove inbound",
			operation:  "remove",
			inboundErr: fmt.Errorf("private inbound diagnostic: %w", store.ErrNotFound),
			underlying: store.ErrNotFound,
			wantTrace:  []string{"access", "source", "remove", "outbound", "inbound"},
			wantReads:  2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := newMCPMutationFake()
			fake.outboundErr = test.outboundErr
			fake.inboundErr = test.inboundErr
			prepared := prepareMCPMutationForTest(t, fake, mcpMutationTestContext(test.name), MCPMutationTarget{
				ProjectSlug:   "project",
				SourceLocalID: 261,
				Mode:          store.ModeFull,
			})
			var (
				result LinkSet
				err    error
				cause  error
			)
			if test.operation == "add" {
				result, err = prepared.Add(AddCommand{TargetLocalID: 262, LinkType: "blocks"})
				cause = test.outboundErr
			} else {
				result, err = prepared.Remove(RemoveCommand{TargetLocalID: 262})
				cause = test.inboundErr
			}
			if err == nil {
				t.Fatal("operation error=nil")
			}
			if err.Error() != ErrMCPProjectionFailed.Error() {
				t.Fatalf("error text=%q want static %q", err.Error(), ErrMCPProjectionFailed.Error())
			}
			if strings.Contains(err.Error(), cause.Error()) || strings.Contains(err.Error(), "private") {
				t.Fatalf("projection wrapper leaked cause text: %q", err.Error())
			}
			if errors.Unwrap(err) != cause {
				t.Fatalf("errors.Unwrap(error)=%v want exact cause %v", errors.Unwrap(err), cause)
			}
			if !errors.Is(err, ErrMCPProjectionFailed) || !errors.Is(err, test.underlying) {
				t.Fatalf("projection classification/cause chain missing: %v", err)
			}
			if errors.Is(err, ErrMCPSourceLookupFailed) {
				t.Fatalf("projection error unexpectedly classified as source: %v", err)
			}
			if !reflect.DeepEqual(result, LinkSet{}) {
				t.Fatalf("result=%+v want zero", result)
			}
			assertMCPMutationTrace(t, fake.trace, test.wantTrace...)
			if len(fake.mutationCalls) != 1 || len(fake.readCalls) != test.wantReads || !fake.committed {
				t.Fatalf("mutation calls=%d reads=%d committed=%v", len(fake.mutationCalls), len(fake.readCalls), fake.committed)
			}
		})
	}
}

func TestPreparedMCPMutationCancellationBoundaries(t *testing.T) {
	t.Run("before prepare", func(t *testing.T) {
		fake := newMCPMutationFake()
		fake.honorContextErr = true
		ctx, cancel := context.WithCancel(mcpMutationTestContext("before prepare"))
		cancel()
		prepared, err := newMCPMutationTestService(fake).Prepare(ctx, MCPMutationTarget{
			ProjectSlug:   "project",
			SourceLocalID: 271,
			Mode:          store.ModeFull,
		})
		if prepared != nil || err != context.Canceled {
			t.Fatalf("Prepare() capability=%+v error=%v want nil/context.Canceled", prepared, err)
		}
		assertMCPMutationTrace(t, fake.trace, "access")
		assertMCPMutationContext(t, fake.accessCalls[0].ctx, "before prepare")
	})

	t.Run("after access before source", func(t *testing.T) {
		fake := newMCPMutationFake()
		fake.honorContextErr = true
		ctx, cancel := context.WithCancel(mcpMutationTestContext("source cancellation"))
		fake.cancelAfter = "access"
		fake.cancel = cancel
		prepared, err := newMCPMutationTestService(fake).Prepare(ctx, MCPMutationTarget{
			ProjectSlug:   "project",
			SourceLocalID: 272,
			Mode:          store.ModeFull,
		})
		if prepared != nil || !errors.Is(err, context.Canceled) || !errors.Is(err, ErrMCPSourceLookupFailed) {
			t.Fatalf("Prepare() capability=%+v error=%v", prepared, err)
		}
		if err.Error() != ErrMCPSourceLookupFailed.Error() || errors.Unwrap(err) != context.Canceled {
			t.Fatalf("source cancellation wrapper text/cause=%q/%v", err.Error(), errors.Unwrap(err))
		}
		assertMCPMutationTrace(t, fake.trace, "access", "source")
		assertMCPMutationContext(t, fake.sourceCalls[0].ctx, "source cancellation")
	})

	for _, operation := range []string{"add", "remove"} {
		t.Run("after prepare before "+operation, func(t *testing.T) {
			fake := newMCPMutationFake()
			fake.honorContextErr = true
			ctx, cancel := context.WithCancel(mcpMutationTestContext(operation + " cancellation"))
			prepared := prepareMCPMutationForTest(t, fake, ctx, MCPMutationTarget{
				ProjectSlug:   "project",
				SourceLocalID: 273,
				Mode:          store.ModeFull,
			})
			cancel()
			var err error
			if operation == "add" {
				_, err = prepared.Add(AddCommand{TargetLocalID: 274, LinkType: "blocks"})
			} else {
				_, err = prepared.Remove(RemoveCommand{TargetLocalID: 274})
			}
			if err != context.Canceled {
				t.Fatalf("operation error=%v want raw context.Canceled", err)
			}
			if errors.Is(err, ErrMCPProjectionFailed) || errors.Is(err, ErrMCPSourceLookupFailed) {
				t.Fatalf("mutation cancellation unexpectedly classified: %v", err)
			}
			assertMCPMutationTrace(t, fake.trace, "access", "source", operation)
			assertMCPMutationContext(t, fake.mutationCalls[0].ctx, operation+" cancellation")
			if len(fake.readCalls) != 0 || fake.committed {
				t.Fatalf("reads=%d committed=%v after mutation cancellation", len(fake.readCalls), fake.committed)
			}
		})
	}

	t.Run("after mutation before outbound", func(t *testing.T) {
		fake := newMCPMutationFake()
		fake.honorContextErr = true
		ctx, cancel := context.WithCancel(mcpMutationTestContext("outbound cancellation"))
		fake.cancelAfter = "add"
		fake.cancel = cancel
		prepared := prepareMCPMutationForTest(t, fake, ctx, MCPMutationTarget{
			ProjectSlug:   "project",
			SourceLocalID: 275,
			Mode:          store.ModeFull,
		})
		result, err := prepared.Add(AddCommand{TargetLocalID: 276, LinkType: "blocks"})
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrMCPProjectionFailed) {
			t.Fatalf("Add() error=%v", err)
		}
		if err.Error() != ErrMCPProjectionFailed.Error() || errors.Unwrap(err) != context.Canceled {
			t.Fatalf("projection cancellation wrapper text/cause=%q/%v", err.Error(), errors.Unwrap(err))
		}
		if !reflect.DeepEqual(result, LinkSet{}) {
			t.Fatalf("result=%+v want zero", result)
		}
		assertMCPMutationTrace(t, fake.trace, "access", "source", "add", "outbound")
		if len(fake.readCalls) != 1 || !fake.committed {
			t.Fatalf("reads=%d committed=%v", len(fake.readCalls), fake.committed)
		}
	})

	t.Run("after outbound before inbound", func(t *testing.T) {
		fake := newMCPMutationFake()
		fake.honorContextErr = true
		ctx, cancel := context.WithCancel(mcpMutationTestContext("inbound cancellation"))
		fake.cancelAfter = "outbound"
		fake.cancel = cancel
		prepared := prepareMCPMutationForTest(t, fake, ctx, MCPMutationTarget{
			ProjectSlug:   "project",
			SourceLocalID: 277,
			Mode:          store.ModeFull,
		})
		result, err := prepared.Remove(RemoveCommand{TargetLocalID: 278})
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrMCPProjectionFailed) {
			t.Fatalf("Remove() error=%v", err)
		}
		if err.Error() != ErrMCPProjectionFailed.Error() || errors.Unwrap(err) != context.Canceled {
			t.Fatalf("projection cancellation wrapper text/cause=%q/%v", err.Error(), errors.Unwrap(err))
		}
		if !reflect.DeepEqual(result, LinkSet{}) {
			t.Fatalf("result=%+v want zero", result)
		}
		assertMCPMutationTrace(t, fake.trace, "access", "source", "remove", "outbound", "inbound")
		if len(fake.readCalls) != 2 || !fake.committed {
			t.Fatalf("reads=%d committed=%v", len(fake.readCalls), fake.committed)
		}
	})
}
