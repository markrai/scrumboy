package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	todolinkapp "scrumboy/internal/application/todolink"
	"scrumboy/internal/store"
)

type todoLinkMutationMappingFake struct {
	accessErr   error
	sourceErr   error
	addErr      error
	removeErr   error
	outboundErr error
	inboundErr  error
}

func (f *todoLinkMutationMappingFake) GetProjectContextBySlug(
	context.Context,
	string,
	store.Mode,
) (store.ProjectContext, error) {
	if f.accessErr != nil {
		return store.ProjectContext{}, f.accessErr
	}
	return store.ProjectContext{Project: store.Project{ID: 41}}, nil
}

func (f *todoLinkMutationMappingFake) GetTodoByLocalID(
	context.Context,
	int64,
	int64,
	store.Mode,
) (store.Todo, error) {
	if f.sourceErr != nil {
		return store.Todo{}, f.sourceErr
	}
	return store.Todo{ProjectID: 41, LocalID: 7}, nil
}

func (f *todoLinkMutationMappingFake) AddLink(
	context.Context,
	int64,
	int64,
	int64,
	string,
	store.Mode,
) error {
	return f.addErr
}

func (f *todoLinkMutationMappingFake) RemoveLink(
	context.Context,
	int64,
	int64,
	int64,
	store.Mode,
) error {
	return f.removeErr
}

func (f *todoLinkMutationMappingFake) ListLinksForTodo(
	context.Context,
	int64,
	int64,
	store.Mode,
) ([]store.TodoLinkTarget, error) {
	return nil, f.outboundErr
}

func (f *todoLinkMutationMappingFake) ListBacklinksForTodo(
	context.Context,
	int64,
	int64,
	store.Mode,
) ([]store.TodoLinkTarget, error) {
	return nil, f.inboundErr
}

func newTodoLinkMutationMappingService(fake *todoLinkMutationMappingFake) *todolinkapp.MCPMutationService {
	return todolinkapp.NewMCPMutationService(todolinkapp.MCPMutationServiceDependencies{
		Access:    fake,
		Sources:   fake,
		Mutations: fake,
		Links:     fake,
	})
}

func todoLinkSourceMappingError(t *testing.T, cause error) error {
	t.Helper()
	fake := &todoLinkMutationMappingFake{sourceErr: cause}
	_, err := newTodoLinkMutationMappingService(fake).Prepare(
		context.Background(),
		todolinkapp.MCPMutationTarget{
			ProjectSlug:   "mapping-project",
			SourceLocalID: 7,
			Mode:          store.ModeFull,
		},
	)
	if err == nil || !errors.Is(err, todolinkapp.ErrMCPSourceLookupFailed) {
		t.Fatalf("Prepare() error=%v want source classification", err)
	}
	return err
}

func todoLinkProjectionMappingError(t *testing.T, cause error) error {
	t.Helper()
	fake := &todoLinkMutationMappingFake{outboundErr: cause}
	service := newTodoLinkMutationMappingService(fake)
	prepared, err := service.Prepare(
		context.Background(),
		todolinkapp.MCPMutationTarget{
			ProjectSlug:   "mapping-project",
			SourceLocalID: 7,
			Mode:          store.ModeFull,
		},
	)
	if err != nil {
		t.Fatalf("Prepare() error=%v", err)
	}
	_, err = prepared.Add(todolinkapp.AddCommand{TargetLocalID: 8, LinkType: "blocks"})
	if err == nil || !errors.Is(err, todolinkapp.ErrMCPProjectionFailed) {
		t.Fatalf("Add() error=%v want projection classification", err)
	}
	return err
}

func assertTodoLinkMutationMappedError(
	t *testing.T,
	got *adapterError,
	wantStatus int,
	wantCode string,
	wantMessage string,
) {
	t.Helper()
	if got == nil {
		t.Fatal("mapped error=nil")
	}
	if got.Status != wantStatus || got.Code != wantCode || got.Message != wantMessage {
		t.Fatalf(
			"mapped error=status %d code %q message %q, want %d %q %q",
			got.Status,
			got.Code,
			got.Message,
			wantStatus,
			wantCode,
			wantMessage,
		)
	}
}

func TestMapTodoLinkMutationPrepareErrorPreservesAccessAndSourcePolicy(t *testing.T) {
	t.Run("raw access unauthorized remains auth required", func(t *testing.T) {
		mapped := mapTodoLinkMutationPrepareError(fmt.Errorf("access: %w", store.ErrUnauthorized))
		assertTodoLinkMutationMappedError(
			t,
			mapped,
			http.StatusUnauthorized,
			CodeAuthRequired,
			"Sign-in required for this tool",
		)
	})

	t.Run("classified source unauthorized remains forbidden", func(t *testing.T) {
		classified := todoLinkSourceMappingError(t, fmt.Errorf("private source cause: %w", store.ErrUnauthorized))
		mapped := mapTodoLinkMutationPrepareError(classified)
		assertTodoLinkMutationMappedError(t, mapped, http.StatusForbidden, CodeForbidden, "forbidden")
		if strings.Contains(mapped.Message, todolinkapp.ErrMCPSourceLookupFailed.Error()) || mapped.Details != nil {
			t.Fatalf("source classification leaked into public mapping: %+v", mapped)
		}
	})

	t.Run("raw and classified not found retain store mapping", func(t *testing.T) {
		raw := mapTodoLinkMutationPrepareError(fmt.Errorf("access: %w", store.ErrNotFound))
		classified := mapTodoLinkMutationPrepareError(
			todoLinkSourceMappingError(t, fmt.Errorf("source: %w", store.ErrNotFound)),
		)
		for name, mapped := range map[string]*adapterError{"raw": raw, "classified": classified} {
			t.Run(name, func(t *testing.T) {
				assertTodoLinkMutationMappedError(t, mapped, http.StatusNotFound, CodeNotFound, "not found")
			})
		}
	})
}

func TestMapTodoLinkMutationOperationErrorPreservesMutationAndProjectionPolicy(t *testing.T) {
	t.Run("raw mutation unauthorized remains forbidden", func(t *testing.T) {
		mapped := mapTodoLinkMutationOperationError(fmt.Errorf("mutation: %w", store.ErrUnauthorized))
		assertTodoLinkMutationMappedError(t, mapped, http.StatusForbidden, CodeForbidden, "forbidden")
	})

	t.Run("classified projection unauthorized remains auth required", func(t *testing.T) {
		classified := todoLinkProjectionMappingError(t, fmt.Errorf("private read cause: %w", store.ErrUnauthorized))
		mapped := mapTodoLinkMutationOperationError(classified)
		assertTodoLinkMutationMappedError(
			t,
			mapped,
			http.StatusUnauthorized,
			CodeAuthRequired,
			"Sign-in required for this tool",
		)
		if strings.Contains(mapped.Message, todolinkapp.ErrMCPProjectionFailed.Error()) || mapped.Details != nil {
			t.Fatalf("projection classification leaked into public mapping: %+v", mapped)
		}
	})

	t.Run("raw mutation and classified projection not found retain store mapping", func(t *testing.T) {
		raw := mapTodoLinkMutationOperationError(fmt.Errorf("mutation: %w", store.ErrNotFound))
		classified := mapTodoLinkMutationOperationError(
			todoLinkProjectionMappingError(t, fmt.Errorf("projection: %w", store.ErrNotFound)),
		)
		for name, mapped := range map[string]*adapterError{"raw": raw, "classified": classified} {
			t.Run(name, func(t *testing.T) {
				assertTodoLinkMutationMappedError(t, mapped, http.StatusNotFound, CodeNotFound, "not found")
			})
		}
	})
}

func TestMapTodoLinkMutationProjectionErrorUsesCauseAndSanitizesClient(t *testing.T) {
	const sensitive = "private todo-link projection database diagnostic"
	classified := todoLinkProjectionMappingError(t, errors.New(sensitive))
	mapped := mapTodoLinkMutationOperationError(classified)

	assertTodoLinkMutationMappedError(t, mapped, http.StatusInternalServerError, CodeInternal, "internal error")
	if mapped.Cause == nil || !strings.Contains(mapped.Cause.Error(), sensitive) {
		t.Fatalf("private cause=%v want underlying diagnostic", mapped.Cause)
	}
	if strings.Contains(mapped.Cause.Error(), todolinkapp.ErrMCPProjectionFailed.Error()) {
		t.Fatalf("private cause retained wrapper text instead of dependency cause: %v", mapped.Cause)
	}

	encoded, err := json.Marshal(clientErrorResponseBody(mapped))
	if err != nil {
		t.Fatalf("marshal client error: %v", err)
	}
	for _, forbidden := range []string{sensitive, todolinkapp.ErrMCPProjectionFailed.Error()} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("client error leaked %q: %s", forbidden, encoded)
		}
	}
}

type todoLinkMutationStageWithoutCause struct {
	classification error
}

func (e *todoLinkMutationStageWithoutCause) Error() string {
	return e.classification.Error()
}

func (e *todoLinkMutationStageWithoutCause) Is(target error) bool {
	return target == e.classification
}

func TestMapTodoLinkMutationClassifiedErrorWithoutCauseIsInternal(t *testing.T) {
	malformed := &todoLinkMutationStageWithoutCause{classification: todolinkapp.ErrMCPProjectionFailed}
	mapped := mapTodoLinkMutationOperationError(malformed)

	assertTodoLinkMutationMappedError(t, mapped, http.StatusInternalServerError, CodeInternal, "internal error")
	if mapped.Cause != malformed {
		t.Fatalf("invariant cause=%v want exact malformed error", mapped.Cause)
	}
	body := clientErrorResponseBody(mapped)
	details, ok := body.Details.(map[string]any)
	if !ok || len(details) != 0 {
		t.Fatalf("client details=%v want empty", body.Details)
	}
}
