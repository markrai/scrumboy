package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"scrumboy/internal/agora"
	todoapp "scrumboy/internal/application/todo"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/mcp"
	"scrumboy/internal/store"
)

type creatorRequestTransportFixture struct {
	server     *Server
	store      *store.Store
	test       *httptest.Server
	collector  *collectingConsumer
	actor      store.User
	creator    store.User
	project    store.Project
	todo       store.Todo
	client     *http.Client
	projectHub <-chan []byte
	creatorHub <-chan []byte
	otherHub   <-chan []byte
}

func newCreatorRequestTransportFixture(t *testing.T) *creatorRequestTransportFixture {
	t.Helper()
	st := newTestStore(t)
	adapter := mcp.New(st, mcp.Options{Mode: "full"})
	srv := NewServer(st, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		MCPHandler:     adapter,
		AgoraHandler:   agora.New(adapter, agora.Options{MaxRequestBytes: 1 << 20}),
	})
	collector := &collectingConsumer{}
	srv.fanout = eventbus.NewFanout(newSSEBridge(srv.hub, srv.creatorNotificationAuthorizer), collector)
	st.SetTodoAssignedPublisher(srv.PublishTodoAssigned)
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		srv.Close(ctx)
	})

	actor, err := st.BootstrapUser(context.Background(), "creator-request-actor@example.com", "password123", "Actor")
	if err != nil {
		t.Fatalf("bootstrap actor: %v", err)
	}
	creator, err := st.CreateUser(context.Background(), "creator-request-creator@example.com", "password123", "Creator")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	actorCtx := store.WithUserID(context.Background(), actor.ID)
	project, err := st.CreateProject(actorCtx, "Creator request adapter contract")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.AddProjectMember(actorCtx, actor.ID, project.ID, creator.ID, store.RoleMaintainer); err != nil {
		t.Fatalf("add creator member: %v", err)
	}
	creatorCtx := store.WithUserID(context.Background(), creator.ID)
	todo, err := st.CreateTodo(creatorCtx, project.ID, store.CreateTodoInput{
		Title:     "Created by historical creator",
		Body:      "initial body",
		ColumnKey: store.DefaultColumnBacklog,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}
	if todo.CreatedByUserID == nil || *todo.CreatedByUserID != creator.ID {
		t.Fatalf("createdByUserID=%v, want %d", todo.CreatedByUserID, creator.ID)
	}
	projectHub, unsubscribeProject := srv.hub.Subscribe(project.ID)
	t.Cleanup(unsubscribeProject)
	creatorHub, unsubscribeCreator := srv.hub.SubscribeUser(creator.ID)
	t.Cleanup(unsubscribeCreator)
	otherHub, unsubscribeOther := srv.hub.SubscribeUser(creator.ID + 1000)
	t.Cleanup(unsubscribeOther)

	client := newCookieClient(t)
	token, expiresAt, err := st.CreateSession(context.Background(), actor.ID, time.Hour)
	if err != nil {
		t.Fatalf("create actor session: %v", err)
	}
	baseURL, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test URL: %v", err)
	}
	client.Jar.SetCookies(baseURL, []*http.Cookie{{Name: "scrumboy_session", Value: token, Path: "/", Expires: expiresAt}})

	collector.events = nil
	return &creatorRequestTransportFixture{
		server: srv, store: st, test: ts, collector: collector,
		actor: actor, creator: creator, project: project, todo: todo, client: client,
		projectHub: projectHub, creatorHub: creatorHub, otherHub: otherHub,
	}
}

func (f *creatorRequestTransportFixture) resetEvents() {
	f.collector.events = nil
	drainHub(f.projectHub)
	drainHub(f.creatorHub)
	drainHub(f.otherHub)
}

func assertCreatorRequestEvent(t *testing.T, event eventbus.Event, fixture *creatorRequestTransportFixture, reason string) eventbus.TodoCreatorNotificationRequestedPayload {
	t.Helper()
	if event.Type != eventbus.TodoCreatorNotificationRequestedEventType || event.ProjectID != fixture.project.ID {
		t.Fatalf("event=%+v, want internal creator request for project %d", event, fixture.project.ID)
	}
	var payload eventbus.TodoCreatorNotificationRequestedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode creator request: %v", err)
	}
	if payload.ProjectID != fixture.project.ID || payload.ProjectSlug != fixture.project.Slug || payload.TodoID != fixture.todo.ID || payload.LocalID != fixture.todo.LocalID || payload.Title != fixture.todo.Title || payload.ActivityReason != reason || payload.CreatedByUserID != fixture.creator.ID || payload.ActorUserID != fixture.actor.ID {
		t.Fatalf("creator request payload=%+v", payload)
	}
	if !payload.MaterialChanged {
		t.Fatalf("creator request lacks committed material-change fact: %+v", payload)
	}
	return payload
}

func assertAuthorizedCreatorNotificationEvent(t *testing.T, event eventbus.Event, fixture *creatorRequestTransportFixture, reason string) eventbus.TodoCreatorNotificationRecipientAuthorizedPayload {
	t.Helper()
	if event.Type != eventbus.TodoCreatorNotificationRecipientAuthorizedEventType || event.ProjectID != fixture.project.ID {
		t.Fatalf("event=%+v, want internal authorized creator recipient for project %d", event, fixture.project.ID)
	}
	var payload eventbus.TodoCreatorNotificationRecipientAuthorizedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode authorized creator recipient: %v", err)
	}
	if payload.ProjectID != fixture.project.ID || payload.ProjectSlug != fixture.project.Slug || payload.TodoID != fixture.todo.ID || payload.LocalID != fixture.todo.LocalID || payload.Title != fixture.todo.Title || payload.ActivityReason != reason || payload.RecipientUserID != fixture.creator.ID || payload.ActorUserID != fixture.actor.ID {
		t.Fatalf("authorized creator recipient payload=%+v", payload)
	}
	if !payload.MaterialChanged {
		t.Fatalf("authorized payload lost material-change fact: %+v", payload)
	}
	return payload
}

func assertCreatorAuthorizationThenRefresh(t *testing.T, fixture *creatorRequestTransportFixture, reason string) {
	t.Helper()
	events := fixture.collector.events
	if len(events) != 3 {
		t.Fatalf("events=%+v, want request, authorized recipient, then refresh", events)
	}
	request := assertCreatorRequestEvent(t, events[0], fixture, reason)
	authorized := assertAuthorizedCreatorNotificationEvent(t, events[1], fixture, reason)
	if !request.CardActivityCandidate || !authorized.CardActivityCandidate {
		t.Fatalf("REST/legacy activity candidate facts request=%+v authorized=%+v", request, authorized)
	}
	if events[2].Type != "board.refresh_needed" || events[2].ProjectID != fixture.project.ID {
		t.Fatalf("third event=%+v, want board.refresh_needed", events[2])
	}
	var refresh struct {
		Reason      string `json:"reason"`
		ActorUserID int64  `json:"actorUserId"`
	}
	if err := json.Unmarshal(events[2].Payload, &refresh); err != nil {
		t.Fatalf("decode refresh: %v", err)
	}
	if refresh.Reason != reason || refresh.ActorUserID != fixture.actor.ID {
		t.Fatalf("refresh=%+v, want reason=%q actor=%d", refresh, reason, fixture.actor.ID)
	}
}

func assertCreatorAuthorizationWithoutRefresh(t *testing.T, fixture *creatorRequestTransportFixture, reason string) {
	t.Helper()
	if len(fixture.collector.events) != 2 {
		t.Fatalf("events=%+v, want request and authorized recipient with zero board refresh", fixture.collector.events)
	}
	request := assertCreatorRequestEvent(t, fixture.collector.events[0], fixture, reason)
	authorized := assertAuthorizedCreatorNotificationEvent(t, fixture.collector.events[1], fixture, reason)
	if request.CardActivityCandidate || authorized.CardActivityCandidate {
		t.Fatalf("MCP/Agora must not invent card-activity fallback: request=%+v authorized=%+v", request, authorized)
	}

}

func assertCreatorRequestOnly(t *testing.T, fixture *creatorRequestTransportFixture, reason string) {
	t.Helper()
	if len(fixture.collector.events) != 1 {
		t.Fatalf("events=%+v, want denied creator request only", fixture.collector.events)
	}
	assertCreatorRequestEvent(t, fixture.collector.events[0], fixture, reason)
}

func assertCreatorActivityDelivery(
	t *testing.T,
	fixture *creatorRequestTransportFixture,
	reason string,
	wantProjectRefreshes int,
	wantProjectAssignments int,
) {
	t.Helper()
	select {
	case message := <-fixture.creatorHub:
		var wire creatorActivityWireEvent
		if err := json.Unmarshal(message, &wire); err != nil {
			t.Fatalf("decode creator activity: %v", err)
		}
		if wire.ID == "" || wire.Type != "todo.creator_activity" ||
			wire.ProjectID != fixture.project.ID || wire.ProjectSlug != fixture.project.Slug ||
			wire.Payload.TodoID != fixture.todo.ID || wire.Payload.LocalID != fixture.todo.LocalID ||
			wire.Payload.Title != fixture.todo.Title || wire.Payload.ActivityReason != reason {
			t.Fatalf("creator activity=%+v", wire)
		}
		if bytes.Contains(message, []byte("actorUserId")) || bytes.Contains(message, []byte("recipientUserId")) {
			t.Fatalf("creator activity exposes internal user identifiers: %s", message)
		}
	default:
		t.Fatal("eligible creator received no private activity SSE")
	}
	assertNoCreatorActivityHubMessage(t, fixture.creatorHub, "duplicate creator")
	assertNoCreatorActivityHubMessage(t, fixture.otherHub, "other user")

	refreshes := 0
	assignments := 0
	for {
		select {
		case message := <-fixture.projectHub:
			var wire struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(message, &wire); err != nil {
				t.Fatalf("decode project SSE: %v", err)
			}
			switch wire.Type {
			case "refresh_needed":
				refreshes++
			case "todo.assigned":
				assignments++
			case "todo.creator_activity":
				t.Fatalf("creator activity leaked to project SSE: %s", message)
			default:
				t.Fatalf("unexpected project SSE type %q: %s", wire.Type, message)
			}
		default:
			if refreshes != wantProjectRefreshes || assignments != wantProjectAssignments {
				t.Fatalf("project SSE refreshes/assignments=%d/%d, want %d/%d", refreshes, assignments, wantProjectRefreshes, wantProjectAssignments)
			}
			return
		}
	}
}

func assertCreatorActivityDenied(t *testing.T, fixture *creatorRequestTransportFixture) {
	t.Helper()
	assertNoCreatorActivityHubMessage(t, fixture.creatorHub, "creator")
	assertNoCreatorActivityHubMessage(t, fixture.otherHub, "other user")
	assertNoCreatorActivityHubMessage(t, fixture.projectHub, "project")
}

func TestTodoCreatorRequestAdapterAndCardinalityContracts(t *testing.T) {
	fixture := newCreatorRequestTransportFixture(t)
	base := fixture.test.URL

	t.Run("modern REST update", func(t *testing.T) {
		fixture.resetEvents()
		var got todoJSON
		response, body := doJSON(t, fixture.client, http.MethodPatch,
			fmt.Sprintf("%s/api/board/%s/todos/%d", base, fixture.project.Slug, fixture.todo.LocalID),
			map[string]any{
				"title": fixture.todo.Title, "body": "modern REST update", "tags": []string{},
				"estimationPoints": nil, "assigneeUserId": nil,
			}, &got)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("modern update status=%d body=%s", response.StatusCode, body)
		}
		assertCreatorAuthorizationThenRefresh(t, fixture, "todo_updated")
		assertCreatorActivityDelivery(t, fixture, "todo_updated", 1, 0)
	})

	t.Run("modern REST move", func(t *testing.T) {
		fixture.resetEvents()
		response, body := doJSON(t, fixture.client, http.MethodPost,
			fmt.Sprintf("%s/api/board/%s/todos/%d/move", base, fixture.project.Slug, fixture.todo.LocalID),
			map[string]any{"toColumnKey": store.DefaultColumnDoing}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("modern move status=%d body=%s", response.StatusCode, body)
		}
		assertCreatorAuthorizationThenRefresh(t, fixture, "todo_moved")
		assertCreatorActivityDelivery(t, fixture, "todo_moved", 1, 0)
	})

	t.Run("legacy numeric update", func(t *testing.T) {
		fixture.resetEvents()
		response, body := doJSON(t, fixture.client, http.MethodPatch,
			fmt.Sprintf("%s/api/todos/%d", base, fixture.todo.ID),
			map[string]any{
				"title": fixture.todo.Title, "body": "legacy update", "tags": []string{},
				"estimationPoints": nil, "assigneeUserId": nil,
			}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("legacy update status=%d body=%s", response.StatusCode, body)
		}
		assertCreatorAuthorizationThenRefresh(t, fixture, "todo_updated")
		assertCreatorActivityDelivery(t, fixture, "todo_updated", 1, 0)
	})

	t.Run("legacy numeric move", func(t *testing.T) {
		fixture.resetEvents()
		response, body := doJSON(t, fixture.client, http.MethodPost,
			fmt.Sprintf("%s/api/todos/%d/move", base, fixture.todo.ID),
			map[string]any{"toColumnKey": store.DefaultColumnDone}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("legacy move status=%d body=%s", response.StatusCode, body)
		}
		assertCreatorAuthorizationThenRefresh(t, fixture, "todo_moved")
		assertCreatorActivityDelivery(t, fixture, "todo_moved", 1, 0)
	})

	t.Run("assignment change preserves store event and suppresses direct refresh", func(t *testing.T) {
		fixture.resetEvents()
		response, body := doJSON(t, fixture.client, http.MethodPatch,
			fmt.Sprintf("%s/api/board/%s/todos/%d", base, fixture.project.Slug, fixture.todo.LocalID),
			map[string]any{
				"title": fixture.todo.Title, "body": "assignment change", "tags": []string{},
				"estimationPoints": nil, "assigneeUserId": fixture.actor.ID,
			}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("assignment update status=%d body=%s", response.StatusCode, body)
		}
		events := fixture.collector.events
		if len(events) != 3 || events[0].Type != "todo.assigned" || events[1].Type != eventbus.TodoCreatorNotificationRequestedEventType || events[2].Type != eventbus.TodoCreatorNotificationRecipientAuthorizedEventType {
			t.Fatalf("events=%+v, want todo.assigned, creator request, authorized recipient, and no direct refresh", events)
		}
		request := assertCreatorRequestEvent(t, events[1], fixture, "todo_updated")
		authorized := assertAuthorizedCreatorNotificationEvent(t, events[2], fixture, "todo_updated")
		if !request.AssignmentChanged || request.ToAssigneeUserID == nil || *request.ToAssigneeUserID != fixture.actor.ID ||
			!authorized.AssignmentChanged || authorized.ToAssigneeUserID == nil || *authorized.ToAssigneeUserID != fixture.actor.ID {
			t.Fatalf("assignment policy facts request=%+v authorized=%+v", request, authorized)
		}
		assertCreatorActivityDelivery(t, fixture, "todo_updated", 1, 1)
	})

	t.Run("legacy assignment change preserves store event and suppresses direct refresh", func(t *testing.T) {
		fixture.resetEvents()
		response, body := doJSON(t, fixture.client, http.MethodPatch,
			fmt.Sprintf("%s/api/todos/%d", base, fixture.todo.ID),
			map[string]any{
				"title": fixture.todo.Title, "body": "legacy assignment clear", "tags": []string{},
				"estimationPoints": nil, "assigneeUserId": nil,
			}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("legacy assignment update status=%d body=%s", response.StatusCode, body)
		}
		events := fixture.collector.events
		if len(events) != 3 || events[0].Type != "todo.assigned" || events[1].Type != eventbus.TodoCreatorNotificationRequestedEventType || events[2].Type != eventbus.TodoCreatorNotificationRecipientAuthorizedEventType {
			t.Fatalf("events=%+v, want todo.assigned, creator request, authorized recipient, and no direct refresh", events)
		}
		request := assertCreatorRequestEvent(t, events[1], fixture, "todo_updated")
		authorized := assertAuthorizedCreatorNotificationEvent(t, events[2], fixture, "todo_updated")
		if !request.AssignmentChanged || request.ToAssigneeUserID != nil ||
			!authorized.AssignmentChanged || authorized.ToAssigneeUserID != nil {
			t.Fatalf("legacy assignment policy facts request=%+v authorized=%+v", request, authorized)
		}
		assertCreatorActivityDelivery(t, fixture, "todo_updated", 1, 0)
	})

	t.Run("MCP update delivers privately and retains zero board refresh", func(t *testing.T) {
		fixture.resetEvents()
		response, body := doJSON(t, fixture.client, http.MethodPost, base+"/mcp", map[string]any{
			"tool": "todos_update",
			"input": map[string]any{
				"projectSlug": fixture.project.Slug,
				"localId":     fixture.todo.LocalID,
				"patch":       map[string]any{"body": "MCP update"},
			},
		}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("MCP update status=%d body=%s", response.StatusCode, body)
		}
		assertCreatorAuthorizationWithoutRefresh(t, fixture, "todo_updated")
		assertCreatorActivityDelivery(t, fixture, "todo_updated", 0, 0)
	})

	t.Run("MCP move delivers privately and retains zero board refresh", func(t *testing.T) {
		fixture.resetEvents()
		response, body := doJSON(t, fixture.client, http.MethodPost, base+"/mcp", map[string]any{
			"tool": "todos_move",
			"input": map[string]any{
				"projectSlug": fixture.project.Slug,
				"localId":     fixture.todo.LocalID,
				"toColumnKey": store.DefaultColumnBacklog,
			},
		}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("MCP move status=%d body=%s", response.StatusCode, body)
		}
		assertCreatorAuthorizationWithoutRefresh(t, fixture, "todo_moved")
		assertCreatorActivityDelivery(t, fixture, "todo_moved", 0, 0)
	})

	t.Run("Agora uses the same bound MCP adapter and delivers privately", func(t *testing.T) {
		fixture.resetEvents()
		response, body := doJSON(t, fixture.client, http.MethodPost, base+"/agora/v1/invoke", map[string]any{
			"tool": "todos_update",
			"arguments": map[string]any{
				"projectSlug": fixture.project.Slug,
				"localId":     fixture.todo.LocalID,
				"patch":       map[string]any{"body": "Agora update through MCP"},
			},
		}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("Agora update status=%d body=%s", response.StatusCode, body)
		}
		assertCreatorAuthorizationWithoutRefresh(t, fixture, "todo_updated")
		assertCreatorActivityDelivery(t, fixture, "todo_updated", 0, 0)
	})

	t.Run("Agora move uses the same bound MCP adapter and retains zero board refresh", func(t *testing.T) {
		fixture.resetEvents()
		response, body := doJSON(t, fixture.client, http.MethodPost, base+"/agora/v1/invoke", map[string]any{
			"tool": "todos_move",
			"arguments": map[string]any{
				"projectSlug": fixture.project.Slug,
				"localId":     fixture.todo.LocalID,
				"toColumnKey": store.DefaultColumnDoing,
			},
		}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("Agora move status=%d body=%s", response.StatusCode, body)
		}
		assertCreatorAuthorizationWithoutRefresh(t, fixture, "todo_moved")
		assertCreatorActivityDelivery(t, fixture, "todo_moved", 0, 0)
	})

	t.Run("removed historical creator still only produces internal consideration", func(t *testing.T) {
		actorCtx := store.WithUserID(context.Background(), fixture.actor.ID)
		if err := fixture.store.RemoveProjectMember(actorCtx, fixture.actor.ID, fixture.project.ID, fixture.creator.ID); err != nil {
			t.Fatalf("remove historical creator: %v", err)
		}
		fixture.resetEvents()
		response, body := doJSON(t, fixture.client, http.MethodPost, base+"/mcp", map[string]any{
			"tool": "todos_update",
			"input": map[string]any{
				"projectSlug": fixture.project.Slug,
				"localId":     fixture.todo.LocalID,
				"patch":       map[string]any{"body": "after creator removal"},
			},
		}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("removed-creator MCP update status=%d body=%s", response.StatusCode, body)
		}
		assertCreatorRequestOnly(t, fixture, "todo_updated")
		assertCreatorActivityDenied(t, fixture)
	})
}

func TestMCPAdapterCreatorRequestBindingIsOneShot(t *testing.T) {
	st := newTestStore(t)
	adapter := mcp.New(st, mcp.Options{Mode: "full"})
	server := NewServer(st, Options{ScrumboyMode: "full", MCPHandler: adapter})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		server.Close(ctx)
	})

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("second MCP creator-request binding did not panic")
		}
	}()
	adapter.BindCreatorNotificationRequestPublisher(todoapp.CreatorNotificationRequestPublisherFunc(
		func(context.Context, todoapp.CreatorNotificationRequest) {},
	))
}
