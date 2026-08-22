package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	tagapp "scrumboy/internal/application/tag"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

type tagDeletionRESTCall struct {
	method         string
	projectID      int64
	actorUserID    int64
	tagID          int64
	name           string
	anonymousBoard bool
}

type tagDeletionRESTRecorder struct {
	calls             []tagDeletionRESTCall
	affectedProjects  []int64
	mutationErr       error
	boardNameTagID    int64
	boardNameErr      error
	personalNameTagID int64
	personalNameErr   error
}

var (
	_ tagapp.MineIDDeletionStore           = (*tagDeletionRESTRecorder)(nil)
	_ tagapp.MineNameDeletionStore         = (*tagDeletionRESTRecorder)(nil)
	_ tagapp.DurableProjectIDDeletionStore = (*tagDeletionRESTRecorder)(nil)
	_ tagapp.LegacyRowDeletionStore        = (*tagDeletionRESTRecorder)(nil)
	_ tagapp.BoardScopedTagNameReadStore   = (*tagDeletionRESTRecorder)(nil)
	_ tagapp.PersonalTagNameReadStore      = (*tagDeletionRESTRecorder)(nil)
)

func (r *tagDeletionRESTRecorder) reset() {
	r.calls = nil
	r.affectedProjects = nil
	r.mutationErr = nil
	r.boardNameTagID = 0
	r.boardNameErr = nil
	r.personalNameTagID = 0
	r.personalNameErr = nil
}

func (r *tagDeletionRESTRecorder) record(call tagDeletionRESTCall) {
	r.calls = append(r.calls, call)
}

func (r *tagDeletionRESTRecorder) DeleteMyTagByID(
	_ context.Context,
	userID int64,
	tagID int64,
) ([]int64, error) {
	r.record(tagDeletionRESTCall{method: "mine-id", actorUserID: userID, tagID: tagID})
	return append([]int64(nil), r.affectedProjects...), r.mutationErr
}

func (r *tagDeletionRESTRecorder) DeleteMyTagByName(
	_ context.Context,
	projectID int64,
	userID int64,
	name string,
) ([]int64, error) {
	r.record(tagDeletionRESTCall{
		method: "durable-name", projectID: projectID, actorUserID: userID, name: name,
	})
	return append([]int64(nil), r.affectedProjects...), r.mutationErr
}

func (r *tagDeletionRESTRecorder) DeleteTagForDurableProjectByID(
	_ context.Context,
	projectID int64,
	userID int64,
	tagID int64,
) ([]int64, error) {
	r.record(tagDeletionRESTCall{
		method: "durable-id", projectID: projectID, actorUserID: userID, tagID: tagID,
	})
	return append([]int64(nil), r.affectedProjects...), r.mutationErr
}

func (r *tagDeletionRESTRecorder) DeleteTag(
	_ context.Context,
	userID int64,
	tagID int64,
	isAnonymousBoard bool,
) error {
	r.record(tagDeletionRESTCall{
		method: "row-delete", actorUserID: userID, tagID: tagID, anonymousBoard: isAnonymousBoard,
	})
	return r.mutationErr
}

func (r *tagDeletionRESTRecorder) GetBoardScopedTagIDByName(
	_ context.Context,
	projectID int64,
	name string,
) (int64, error) {
	r.record(tagDeletionRESTCall{method: "board-name-read", projectID: projectID, name: name})
	return r.boardNameTagID, r.boardNameErr
}

func (r *tagDeletionRESTRecorder) GetTagIDByName(
	_ context.Context,
	userID int64,
	name string,
) (int64, error) {
	r.record(tagDeletionRESTCall{method: "personal-name-read", actorUserID: userID, name: name})
	return r.personalNameTagID, r.personalNameErr
}

func newTagDeletionRESTService(server *Server, recorder *tagDeletionRESTRecorder) *tagapp.RESTDeletionService {
	return tagapp.NewRESTDeletionService(tagapp.RESTDeletionServiceDependencies{
		MineID:        recorder,
		MineName:      recorder,
		DurableID:     recorder,
		Rows:          recorder,
		BoardNames:    recorder,
		PersonalNames: recorder,
		Publisher:     tagDeletionPublisher{server: server},
	})
}

func TestTagDeletionRESTHandlersDelegateToApplicationService(t *testing.T) {
	fx := newTagMutationRESTFixture(t)
	if fx.server.tagDeletions == nil {
		t.Fatal("NewServer did not compose the REST tag deletion service")
	}

	durableProjectID, durableSlug := createProjectAPI(t, fx.client, fx.ts, "REST Deletion Migration Durable")
	creatorSlug := createAnonBoardViaHTTP(t, fx.client, fx.ts)
	creatorProjectID := projectIDBySlug(t, fx.db, creatorSlug)
	anonymousClient := newCookieClient(t)
	anonymousSlug := createAnonBoardViaHTTP(t, anonymousClient, fx.ts)
	anonymousProjectID := projectIDBySlug(t, fx.db, anonymousSlug)

	recorder := &tagDeletionRESTRecorder{}
	fx.server.tagDeletions = newTagDeletionRESTService(fx.server, recorder)

	tests := []struct {
		name             string
		client           *http.Client
		path             string
		affectedProjects []int64
		wantCall         tagDeletionRESTCall
		wantProjects     []int64
		wantRefreshName  string
		wantActorUserID  int64
	}{
		{
			name:             "mine ID",
			client:           fx.client,
			path:             "/api/tags/mine/101",
			affectedProjects: []int64{durableProjectID, durableProjectID},
			wantCall:         tagDeletionRESTCall{method: "mine-id", actorUserID: fx.ownerID, tagID: 101},
			wantProjects:     []int64{durableProjectID},
			wantActorUserID:  fx.ownerID,
		},
		{
			name:             "durable board ID",
			client:           fx.client,
			path:             "/api/board/" + durableSlug + "/tags/id/102",
			affectedProjects: []int64{durableProjectID, 909, 909},
			wantCall: tagDeletionRESTCall{
				method: "durable-id", projectID: durableProjectID, actorUserID: fx.ownerID, tagID: 102,
			},
			wantProjects:    []int64{durableProjectID, 909},
			wantActorUserID: fx.ownerID,
		},
		{
			name:             "durable board name",
			client:           fx.client,
			path:             "/api/board/" + durableSlug + "/tags/durable-board-name",
			affectedProjects: []int64{durableProjectID, 910, 910},
			wantCall: tagDeletionRESTCall{
				method: "durable-name", projectID: durableProjectID, actorUserID: fx.ownerID, name: "durable-board-name",
			},
			wantProjects:    []int64{durableProjectID, 910},
			wantRefreshName: "durable-board-name",
			wantActorUserID: fx.ownerID,
		},
		{
			name:             "numeric project ID",
			client:           fx.client,
			path:             "/api/projects/" + strconv.FormatInt(durableProjectID, 10) + "/tags/id/103",
			affectedProjects: []int64{durableProjectID},
			wantCall: tagDeletionRESTCall{
				method: "durable-id", projectID: durableProjectID, actorUserID: fx.ownerID, tagID: 103,
			},
			wantProjects:    []int64{durableProjectID},
			wantActorUserID: fx.ownerID,
		},
		{
			name:             "numeric project name",
			client:           fx.client,
			path:             "/api/projects/" + strconv.FormatInt(durableProjectID, 10) + "/tags/numeric-name",
			affectedProjects: []int64{durableProjectID},
			wantCall: tagDeletionRESTCall{
				method: "durable-name", projectID: durableProjectID, actorUserID: fx.ownerID, name: "numeric-name",
			},
			wantProjects:    []int64{durableProjectID},
			wantRefreshName: "numeric-name",
			wantActorUserID: fx.ownerID,
		},
		{
			name:   "creator temporary board ID",
			client: fx.client,
			path:   "/api/board/" + creatorSlug + "/tags/id/104",
			wantCall: tagDeletionRESTCall{
				method: "row-delete", actorUserID: fx.ownerID, tagID: 104,
			},
			wantProjects:    []int64{creatorProjectID},
			wantActorUserID: fx.ownerID,
		},
		{
			name:   "anonymous temporary board ID",
			client: anonymousClient,
			path:   "/api/board/" + anonymousSlug + "/tags/id/105",
			wantCall: tagDeletionRESTCall{
				method: "row-delete", tagID: 105, anonymousBoard: true,
			},
			wantProjects: []int64{anonymousProjectID},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder.reset()
			recorder.affectedProjects = append([]int64(nil), tc.affectedProjects...)
			fx.resetEvents()

			resp, body := doJSON(t, tc.client, http.MethodDelete, fx.ts+tc.path, nil, nil)
			assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
			if len(body) != 0 {
				t.Fatalf("204 response body=%q, want empty", body)
			}

			if len(recorder.calls) != 1 || recorder.calls[0] != tc.wantCall {
				t.Fatalf("calls=%+v want exactly %+v", recorder.calls, tc.wantCall)
			}
			assertTagDeletionRESTRefreshes(
				t,
				fx.collector.events,
				tc.wantProjects,
				tc.wantRefreshName,
				tc.wantActorUserID,
			)
		})
	}
}

func TestTagDeletionRESTAnonymousNameResolutionDelegatesInOrder(t *testing.T) {
	fx := newTagMutationRESTFixture(t)
	anonymousClient := newCookieClient(t)
	anonymousSlug := createAnonBoardViaHTTP(t, anonymousClient, fx.ts)
	anonymousProjectID := projectIDBySlug(t, fx.db, anonymousSlug)
	recorder := &tagDeletionRESTRecorder{}
	fx.server.tagDeletions = newTagDeletionRESTService(fx.server, recorder)

	t.Run("board-scoped row wins without actor", func(t *testing.T) {
		recorder.reset()
		recorder.boardNameTagID = 201
		fx.resetEvents()

		resp, body := doJSON(
			t,
			anonymousClient,
			http.MethodDelete,
			fx.ts+"/api/board/"+anonymousSlug+"/tags/board-name",
			nil,
			nil,
		)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		if len(body) != 0 {
			t.Fatalf("204 response body=%q, want empty", body)
		}
		wantCalls := []tagDeletionRESTCall{
			{method: "board-name-read", projectID: anonymousProjectID, name: "board-name"},
			{method: "row-delete", tagID: 201, anonymousBoard: true},
		}
		assertTagDeletionRESTCalls(t, recorder.calls, wantCalls)
		assertTagDeletionRESTRefreshes(t, fx.collector.events, []int64{anonymousProjectID}, "board-name", 0)
	})

	t.Run("signed caller deleting board-scoped row keeps mutation and event identities distinct", func(t *testing.T) {
		recorder.reset()
		recorder.boardNameTagID = 202
		fx.resetEvents()

		resp, body := doJSON(
			t,
			fx.client,
			http.MethodDelete,
			fx.ts+"/api/board/"+anonymousSlug+"/tags/signed-board-name",
			nil,
			nil,
		)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		if len(body) != 0 {
			t.Fatalf("204 response body=%q, want empty", body)
		}
		wantCalls := []tagDeletionRESTCall{
			{method: "board-name-read", projectID: anonymousProjectID, name: "signed-board-name"},
			{method: "row-delete", tagID: 202, anonymousBoard: true},
		}
		assertTagDeletionRESTCalls(t, recorder.calls, wantCalls)
		assertTagDeletionRESTRefreshes(
			t,
			fx.collector.events,
			[]int64{anonymousProjectID},
			"signed-board-name",
			fx.ownerID,
		)
	})

	t.Run("signed caller falls back to personal row", func(t *testing.T) {
		recorder.reset()
		recorder.boardNameErr = store.ErrNotFound
		recorder.personalNameTagID = 203
		fx.resetEvents()

		resp, body := doJSON(
			t,
			fx.client,
			http.MethodDelete,
			fx.ts+"/api/board/"+anonymousSlug+"/tags/personal-name",
			nil,
			nil,
		)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		if len(body) != 0 {
			t.Fatalf("204 response body=%q, want empty", body)
		}
		wantCalls := []tagDeletionRESTCall{
			{method: "board-name-read", projectID: anonymousProjectID, name: "personal-name"},
			{method: "personal-name-read", actorUserID: fx.ownerID, name: "personal-name"},
			{method: "row-delete", actorUserID: fx.ownerID, tagID: 203, anonymousBoard: true},
		}
		assertTagDeletionRESTCalls(t, recorder.calls, wantCalls)
		assertTagDeletionRESTRefreshes(
			t,
			fx.collector.events,
			[]int64{anonymousProjectID},
			"personal-name",
			fx.ownerID,
		)
	})
}

func TestTagDeletionRESTCreatorTemporaryNameRejectionStaysPubliclyStable(t *testing.T) {
	fx := newTagMutationRESTFixture(t)
	creatorSlug := createAnonBoardViaHTTP(t, fx.client, fx.ts)
	recorder := &tagDeletionRESTRecorder{}
	fx.server.tagDeletions = newTagDeletionRESTService(fx.server, recorder)
	fx.resetEvents()

	var out apiErrorEnvelope
	resp, body := doJSON(
		t,
		fx.client,
		http.MethodDelete,
		fx.ts+"/api/board/"+creatorSlug+"/tags/refused-name",
		nil,
		&out,
	)
	assertTagMutationRESTStatus(t, resp, body, http.StatusBadRequest)
	assertAPIError(t, out, "VALIDATION_ERROR", "", "name_based_tag_route_not_allowed")
	if len(recorder.calls) != 0 {
		t.Fatalf("rejected preparation reached persistence: %+v", recorder.calls)
	}
	if len(fx.collector.events) != 0 {
		t.Fatalf("rejected preparation published events: %+v", fx.collector.events)
	}
}

func TestTagDeletionRESTFailedMutationPublishesNoRefresh(t *testing.T) {
	fx := newTagMutationRESTFixture(t)
	projectID, _ := createProjectAPI(t, fx.client, fx.ts, "REST Deletion Failure")
	recorder := &tagDeletionRESTRecorder{mutationErr: store.ErrNotFound}
	fx.server.tagDeletions = newTagDeletionRESTService(fx.server, recorder)
	fx.resetEvents()

	var out apiErrorEnvelope
	resp, body := doJSON(
		t,
		fx.client,
		http.MethodDelete,
		fx.ts+"/api/projects/"+strconv.FormatInt(projectID, 10)+"/tags/id/404",
		nil,
		&out,
	)
	assertTagMutationRESTStatus(t, resp, body, http.StatusNotFound)
	assertAPIError(t, out, "NOT_FOUND", "")
	wantCall := tagDeletionRESTCall{
		method: "durable-id", projectID: projectID, actorUserID: fx.ownerID, tagID: 404,
	}
	if len(recorder.calls) != 1 || recorder.calls[0] != wantCall {
		t.Fatalf("calls=%+v want exactly %+v", recorder.calls, wantCall)
	}
	if len(fx.collector.events) != 0 {
		t.Fatalf("failed mutation published events: %+v", fx.collector.events)
	}
}

func assertTagDeletionRESTCalls(t *testing.T, got, want []tagDeletionRESTCall) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls=%+v want=%+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls[%d]=%+v want=%+v; all calls=%+v", i, got[i], want[i], got)
		}
	}
}

func assertTagDeletionRESTRefreshes(
	t *testing.T,
	events []eventbus.Event,
	wantProjects []int64,
	wantName string,
	wantActorUserID int64,
) {
	t.Helper()
	if len(events) != len(wantProjects) {
		t.Fatalf("events=%+v want projects=%v", events, wantProjects)
	}
	for i, projectID := range wantProjects {
		event := events[i]
		if event.Type != "board.refresh_needed" || event.ProjectID != projectID {
			t.Fatalf("event[%d]=%+v want refresh for project %d", i, event, projectID)
		}
		var payload refreshNeededPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("decode event[%d]: %v", i, err)
		}
		if payload.Reason != "tag_deleted" ||
			payload.Name != wantName ||
			payload.ActorUserID != wantActorUserID ||
			payload.LocalID != 0 ||
			payload.Title != "" {
			t.Fatalf("payload[%d]=%+v", i, payload)
		}
	}
}
