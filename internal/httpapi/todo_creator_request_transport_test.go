package httpapi

import (
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
	server    *Server
	store     *store.Store
	test      *httptest.Server
	collector *collectingConsumer
	actor     store.User
	creator   store.User
	project   store.Project
	todo      store.Todo
	client    *http.Client
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
	srv.fanout = eventbus.NewFanout(newSSEBridge(srv.hub), collector)
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
	}
}

func (f *creatorRequestTransportFixture) resetEvents() {
	f.collector.events = nil
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
	if payload.ProjectID != fixture.project.ID || payload.ProjectSlug != fixture.project.Slug || payload.TodoID != fixture.todo.ID || payload.LocalID != fixture.todo.LocalID || payload.ActivityReason != reason || payload.CreatedByUserID != fixture.creator.ID || payload.ActorUserID != fixture.actor.ID {
		t.Fatalf("creator request payload=%+v", payload)
	}
	return payload
}

func assertCreatorRequestThenRefresh(t *testing.T, fixture *creatorRequestTransportFixture, reason string) {
	t.Helper()
	events := fixture.collector.events
	if len(events) != 2 {
		t.Fatalf("events=%+v, want request then refresh", events)
	}
	assertCreatorRequestEvent(t, events[0], fixture, reason)
	if events[1].Type != "board.refresh_needed" || events[1].ProjectID != fixture.project.ID {
		t.Fatalf("second event=%+v, want board.refresh_needed", events[1])
	}
	var refresh struct {
		Reason      string `json:"reason"`
		ActorUserID int64  `json:"actorUserId"`
	}
	if err := json.Unmarshal(events[1].Payload, &refresh); err != nil {
		t.Fatalf("decode refresh: %v", err)
	}
	if refresh.Reason != reason || refresh.ActorUserID != fixture.actor.ID {
		t.Fatalf("refresh=%+v, want reason=%q actor=%d", refresh, reason, fixture.actor.ID)
	}
}

func assertCreatorRequestOnly(t *testing.T, fixture *creatorRequestTransportFixture, reason string) {
	t.Helper()
	if len(fixture.collector.events) != 1 {
		t.Fatalf("events=%+v, want one creator request and zero board refresh", fixture.collector.events)
	}
	assertCreatorRequestEvent(t, fixture.collector.events[0], fixture, reason)
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
		assertCreatorRequestThenRefresh(t, fixture, "todo_updated")
	})

	t.Run("modern REST move", func(t *testing.T) {
		fixture.resetEvents()
		response, body := doJSON(t, fixture.client, http.MethodPost,
			fmt.Sprintf("%s/api/board/%s/todos/%d/move", base, fixture.project.Slug, fixture.todo.LocalID),
			map[string]any{"toColumnKey": store.DefaultColumnDoing}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("modern move status=%d body=%s", response.StatusCode, body)
		}
		assertCreatorRequestThenRefresh(t, fixture, "todo_moved")
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
		assertCreatorRequestThenRefresh(t, fixture, "todo_updated")
	})

	t.Run("legacy numeric move", func(t *testing.T) {
		fixture.resetEvents()
		response, body := doJSON(t, fixture.client, http.MethodPost,
			fmt.Sprintf("%s/api/todos/%d/move", base, fixture.todo.ID),
			map[string]any{"toColumnKey": store.DefaultColumnDone}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("legacy move status=%d body=%s", response.StatusCode, body)
		}
		assertCreatorRequestThenRefresh(t, fixture, "todo_moved")
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
		if len(events) != 2 || events[0].Type != "todo.assigned" || events[1].Type != eventbus.TodoCreatorNotificationRequestedEventType {
			t.Fatalf("events=%+v, want todo.assigned then creator request and no direct refresh", events)
		}
		assertCreatorRequestEvent(t, events[1], fixture, "todo_updated")
	})

	projectHub, unsubscribeProject := fixture.server.hub.Subscribe(fixture.project.ID)
	defer unsubscribeProject()
	creatorHub, unsubscribeCreator := fixture.server.hub.SubscribeUser(fixture.creator.ID)
	defer unsubscribeCreator()

	t.Run("MCP update is explicitly bound and remains delivery silent", func(t *testing.T) {
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
		assertCreatorRequestOnly(t, fixture, "todo_updated")
		assertNoCreatorRequestHubDelivery(t, projectHub, creatorHub)
	})

	t.Run("MCP move is explicitly bound and remains delivery silent", func(t *testing.T) {
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
		assertCreatorRequestOnly(t, fixture, "todo_moved")
		assertNoCreatorRequestHubDelivery(t, projectHub, creatorHub)
	})

	t.Run("Agora uses the same bound MCP adapter and remains delivery silent", func(t *testing.T) {
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
		assertCreatorRequestOnly(t, fixture, "todo_updated")
		assertNoCreatorRequestHubDelivery(t, projectHub, creatorHub)
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
		assertNoCreatorRequestHubDelivery(t, projectHub, creatorHub)
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

func assertNoCreatorRequestHubDelivery(t *testing.T, projectHub, creatorHub <-chan []byte) {
	t.Helper()
	select {
	case message := <-projectHub:
		t.Fatalf("creator request leaked to project SSE: %s", message)
	case message := <-creatorHub:
		t.Fatalf("creator request leaked to creator SSE: %s", message)
	case <-time.After(100 * time.Millisecond):
	}
}
