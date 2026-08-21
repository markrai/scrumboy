package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	tagapp "scrumboy/internal/application/tag"
	"scrumboy/internal/store"
)

type tagColorRESTCall struct {
	method             string
	projectID          int64
	viewerUserID       *int64
	tagID              int64
	name               string
	color              *string
	linkTemporaryBoard bool
}

type tagColorRESTRecorder struct {
	calls []tagColorRESTCall
}

var (
	_ tagapp.MineColorStore               = (*tagColorRESTRecorder)(nil)
	_ tagapp.DurableProjectIDColorStore   = (*tagColorRESTRecorder)(nil)
	_ tagapp.TemporaryBoardIDColorStore   = (*tagColorRESTRecorder)(nil)
	_ tagapp.DurableProjectNameColorStore = (*tagColorRESTRecorder)(nil)
	_ tagapp.TemporaryBoardNameColorStore = (*tagColorRESTRecorder)(nil)
)

func cloneTagColorTestInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTagColorTestString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (r *tagColorRESTRecorder) record(call tagColorRESTCall) error {
	call.viewerUserID = cloneTagColorTestInt64(call.viewerUserID)
	call.color = cloneTagColorTestString(call.color)
	r.calls = append(r.calls, call)
	return nil
}

func (r *tagColorRESTRecorder) UpdateMyTagColor(_ context.Context, userID, tagID int64, color *string) error {
	return r.record(tagColorRESTCall{
		method:       "mine-id",
		viewerUserID: &userID,
		tagID:        tagID,
		color:        color,
	})
}

func (r *tagColorRESTRecorder) UpdateTagColorForDurableProjectByID(
	_ context.Context,
	projectID int64,
	viewerUserID int64,
	tagID int64,
	color *string,
) error {
	return r.record(tagColorRESTCall{
		method:       "durable-id",
		projectID:    projectID,
		viewerUserID: &viewerUserID,
		tagID:        tagID,
		color:        color,
	})
}

func (r *tagColorRESTRecorder) UpdateTagColorForTemporaryBoard(
	_ context.Context,
	projectID int64,
	viewerUserID *int64,
	tagID int64,
	color *string,
) error {
	return r.record(tagColorRESTCall{
		method:       "temporary-id",
		projectID:    projectID,
		viewerUserID: viewerUserID,
		tagID:        tagID,
		color:        color,
	})
}

func (r *tagColorRESTRecorder) SetViewerTagColorByName(
	_ context.Context,
	projectID int64,
	viewerUserID int64,
	name string,
	color *string,
) error {
	return r.record(tagColorRESTCall{
		method:       "durable-name",
		projectID:    projectID,
		viewerUserID: &viewerUserID,
		name:         name,
		color:        color,
	})
}

func (r *tagColorRESTRecorder) UpdateTagColorForProject(
	_ context.Context,
	projectID int64,
	viewerUserID *int64,
	tagName string,
	color *string,
	linkTemporaryBoard bool,
) error {
	return r.record(tagColorRESTCall{
		method:             "temporary-name",
		projectID:          projectID,
		viewerUserID:       viewerUserID,
		name:               tagName,
		color:              color,
		linkTemporaryBoard: linkTemporaryBoard,
	})
}

func TestTagColorRESTHandlersDelegateToApplicationService(t *testing.T) {
	fx := newTagMutationRESTFixture(t)
	if fx.server.tagColors == nil {
		t.Fatal("NewServer did not compose the REST tag color service")
	}

	durableProjectID, durableSlug := createProjectAPI(t, fx.client, fx.ts, "REST Color Migration Durable")
	creatorSlug := createAnonBoardViaHTTP(t, fx.client, fx.ts)
	creatorProjectID := projectIDBySlug(t, fx.db, creatorSlug)
	anonymousClient := newCookieClient(t)
	anonymousSlug := createAnonBoardViaHTTP(t, anonymousClient, fx.ts)
	anonymousProjectID := projectIDBySlug(t, fx.db, anonymousSlug)

	recorder := &tagColorRESTRecorder{}
	fx.server.tagColors = tagapp.NewRESTColorService(tagapp.RESTColorServiceDependencies{
		MineColor:          recorder,
		DurableIDColor:     recorder,
		TemporaryIDColor:   recorder,
		DurableNameColor:   recorder,
		TemporaryNameColor: recorder,
		Publisher:          tagColorPublisher{server: fx.server},
	})

	ownerID := fx.ownerID
	rawColor := "  #123456  "
	tests := []struct {
		name               string
		client             *http.Client
		path               string
		wantMethod         string
		wantProjectID      int64
		wantViewerUserID   *int64
		wantTagID          int64
		wantName           string
		wantTemporaryLink  bool
		wantRefresh        bool
		wantRefreshActorID int64
	}{
		{
			name:             "mine ID",
			client:           fx.client,
			path:             "/api/tags/mine/101/color",
			wantMethod:       "mine-id",
			wantViewerUserID: &ownerID,
			wantTagID:        101,
		},
		{
			name:               "durable board ID",
			client:             fx.client,
			path:               "/api/board/" + durableSlug + "/tags/id/102/color",
			wantMethod:         "durable-id",
			wantProjectID:      durableProjectID,
			wantViewerUserID:   &ownerID,
			wantTagID:          102,
			wantRefresh:        true,
			wantRefreshActorID: ownerID,
		},
		{
			name:               "durable board name",
			client:             fx.client,
			path:               "/api/board/" + durableSlug + "/tags/durable-board-name/color",
			wantMethod:         "durable-name",
			wantProjectID:      durableProjectID,
			wantViewerUserID:   &ownerID,
			wantName:           "durable-board-name",
			wantRefresh:        true,
			wantRefreshActorID: ownerID,
		},
		{
			name:               "numeric project ID",
			client:             fx.client,
			path:               "/api/projects/" + strconv.FormatInt(durableProjectID, 10) + "/tags/id/103/color",
			wantMethod:         "durable-id",
			wantProjectID:      durableProjectID,
			wantViewerUserID:   &ownerID,
			wantTagID:          103,
			wantRefresh:        true,
			wantRefreshActorID: ownerID,
		},
		{
			name:               "numeric project name",
			client:             fx.client,
			path:               "/api/projects/" + strconv.FormatInt(durableProjectID, 10) + "/tags/numeric-name/color",
			wantMethod:         "durable-name",
			wantProjectID:      durableProjectID,
			wantViewerUserID:   &ownerID,
			wantName:           "numeric-name",
			wantRefresh:        true,
			wantRefreshActorID: ownerID,
		},
		{
			name:               "creator temporary board ID",
			client:             fx.client,
			path:               "/api/board/" + creatorSlug + "/tags/id/104/color",
			wantMethod:         "temporary-id",
			wantProjectID:      creatorProjectID,
			wantViewerUserID:   &ownerID,
			wantTagID:          104,
			wantRefresh:        true,
			wantRefreshActorID: ownerID,
		},
		{
			name:               "creator temporary board name",
			client:             fx.client,
			path:               "/api/board/" + creatorSlug + "/tags/creator-name/color",
			wantMethod:         "temporary-name",
			wantProjectID:      creatorProjectID,
			wantViewerUserID:   &ownerID,
			wantName:           "creator-name",
			wantTemporaryLink:  true,
			wantRefresh:        true,
			wantRefreshActorID: ownerID,
		},
		{
			name:          "anonymous temporary board ID",
			client:        anonymousClient,
			path:          "/api/board/" + anonymousSlug + "/tags/id/105/color",
			wantMethod:    "temporary-id",
			wantProjectID: anonymousProjectID,
			wantTagID:     105,
			wantRefresh:   true,
		},
		{
			name:              "anonymous temporary board name",
			client:            anonymousClient,
			path:              "/api/board/" + anonymousSlug + "/tags/anonymous-name/color",
			wantMethod:        "temporary-name",
			wantProjectID:     anonymousProjectID,
			wantName:          "anonymous-name",
			wantTemporaryLink: true,
			wantRefresh:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder.calls = nil
			fx.resetEvents()

			resp, body := doJSON(
				t,
				tc.client,
				http.MethodPatch,
				fx.ts+tc.path,
				map[string]any{"color": rawColor},
				nil,
			)
			assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)

			if len(recorder.calls) != 1 {
				t.Fatalf("calls=%+v want exactly one application-selected mutation", recorder.calls)
			}
			call := recorder.calls[0]
			if call.method != tc.wantMethod ||
				call.projectID != tc.wantProjectID ||
				call.tagID != tc.wantTagID ||
				call.name != tc.wantName ||
				call.linkTemporaryBoard != tc.wantTemporaryLink {
				t.Fatalf("call=%+v does not match expected route projection", call)
			}
			if tc.wantViewerUserID == nil {
				if call.viewerUserID != nil {
					t.Fatalf("viewer=%v want nil", *call.viewerUserID)
				}
			} else if call.viewerUserID == nil || *call.viewerUserID != *tc.wantViewerUserID {
				t.Fatalf("viewer=%v want %d", call.viewerUserID, *tc.wantViewerUserID)
			}
			if call.color == nil || *call.color != rawColor {
				t.Fatalf("color=%v want exact unnormalized %q", call.color, rawColor)
			}

			if !tc.wantRefresh {
				if len(fx.collector.events) != 0 {
					t.Fatalf("mine color published events: %+v", fx.collector.events)
				}
				return
			}
			if len(fx.collector.events) != 1 {
				t.Fatalf("events=%+v want exactly one refresh", fx.collector.events)
			}
			event := fx.collector.events[0]
			if event.ProjectID != tc.wantProjectID || event.Type != "board.refresh_needed" {
				t.Fatalf("event=%+v want one refresh for project %d", event, tc.wantProjectID)
			}
			var payload refreshNeededPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("decode refresh payload: %v", err)
			}
			if payload.Reason != "tag_color_updated" ||
				payload.Name != tc.wantName ||
				payload.ActorUserID != tc.wantRefreshActorID ||
				payload.LocalID != 0 ||
				payload.Title != "" {
				t.Fatalf("payload=%+v does not match route publication", payload)
			}
		})
	}
}

func TestResolvedRESTTagProjectPreservesProjectKinds(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour)
	creatorUserID := int64(42)

	tests := []struct {
		name    string
		project store.Project
		want    tagapp.ResolvedProject
	}{
		{
			name:    "durable",
			project: store.Project{ID: 1},
			want:    tagapp.ResolvedProject{ProjectID: 1, Kind: tagapp.DurableProject},
		},
		{
			name:    "creator-owned temporary",
			project: store.Project{ID: 2, CreatorUserID: &creatorUserID, ExpiresAt: &expiresAt},
			want:    tagapp.ResolvedProject{ProjectID: 2, Kind: tagapp.CreatorOwnedTemporaryBoard},
		},
		{
			name:    "anonymous temporary",
			project: store.Project{ID: 3, ExpiresAt: &expiresAt},
			want:    tagapp.ResolvedProject{ProjectID: 3, Kind: tagapp.AnonymousTemporaryBoard},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolvedRESTTagProject(tc.project); got != tc.want {
				t.Fatalf("resolved=%+v want=%+v", got, tc.want)
			}
		})
	}
}
