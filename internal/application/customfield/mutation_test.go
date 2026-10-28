package customfield

import (
	"context"

	"scrumboy/internal/store"
)

type mutationCall struct {
	operation string
	ctx       context.Context
	projectID int64
	key       string
	name      string
	typ       store.CustomFieldType
}

type recordingStore struct {
	accessCalls int
	calls       []mutationCall

	projectContext store.ProjectContext
	accessErr      error
	createResult   store.CustomField
	createErr      error
	deleteErr      error
}

func (f *recordingStore) GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error) {
	f.accessCalls++
	if f.accessErr != nil {
		return store.ProjectContext{}, f.accessErr
	}
	return f.projectContext, nil
}

func (f *recordingStore) CreateCustomField(ctx context.Context, projectID int64, key, name string, typ store.CustomFieldType) (store.CustomField, error) {
	f.calls = append(f.calls, mutationCall{operation: "create", ctx: ctx, projectID: projectID, key: key, name: name, typ: typ})
	return f.createResult, f.createErr
}

func (f *recordingStore) DeleteCustomField(ctx context.Context, projectID int64, key string) error {
	f.calls = append(f.calls, mutationCall{operation: "delete", ctx: ctx, projectID: projectID, key: key})
	return f.deleteErr
}

func (f *recordingStore) createCount() int {
	n := 0
	for _, call := range f.calls {
		if call.operation == "create" {
			n++
		}
	}
	return n
}

func (f *recordingStore) deleteCount() int {
	n := 0
	for _, call := range f.calls {
		if call.operation == "delete" {
			n++
		}
	}
	return n
}
