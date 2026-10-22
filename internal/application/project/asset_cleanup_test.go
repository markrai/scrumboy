package project

import (
	"context"
	"errors"
	"sync"
	"testing"

	"scrumboy/internal/store"
)

type recordingRemover struct {
	mu   sync.Mutex
	refs []store.AssetFileRef
}

func (r *recordingRemover) RemoveFiles(refs []store.AssetFileRef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refs = append(r.refs, refs...)
}

func TestProjectDeletionServiceRemovesFilesOnlyAfterSuccess(t *testing.T) {
	files := []store.AssetFileRef{{UUID: "e2a1b940-7bf3-4f28-86d1-cf3c1538f902", MimeType: "image/png"}}

	ok := &restDeletionFake{deleteResult: store.DeletedProjectSnapshot{ProjectID: 1, AssetFiles: files}}
	remover := &recordingRemover{}
	svc := NewRESTDeletionService(RESTDeletionServiceDependencies{Projects: ok, Publisher: ok, AssetFiles: remover})
	if err := svc.Prepare(context.Background(), RESTDeletionCommand{ProjectID: 1, ActorUserID: 2}).Delete(); err != nil {
		t.Fatal(err)
	}
	if len(remover.refs) != 1 || remover.refs[0] != files[0] {
		t.Fatalf("REST remover got %+v", remover.refs)
	}

	failing := &restDeletionFake{deleteResult: store.DeletedProjectSnapshot{AssetFiles: files}, deleteErr: errors.New("commit failed")}
	remover = &recordingRemover{}
	svc = NewRESTDeletionService(RESTDeletionServiceDependencies{Projects: failing, Publisher: failing, AssetFiles: remover})
	if err := svc.Prepare(context.Background(), RESTDeletionCommand{ProjectID: 1, ActorUserID: 2}).Delete(); err == nil {
		t.Fatal("want error")
	}
	if len(remover.refs) != 0 {
		t.Fatal("files removed after a failed deletion")
	}
}

func TestRemoveDeletedProjectAssetsNilSafe(t *testing.T) {
	removeDeletedProjectAssets(nil, []store.AssetFileRef{{UUID: "x"}})
	r := &recordingRemover{}
	removeDeletedProjectAssets(r, nil)
	if len(r.refs) != 0 {
		t.Fatal("empty refs must not call the remover")
	}
}
