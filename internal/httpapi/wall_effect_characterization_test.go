package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

type wallRefreshPayload struct {
	Reason string `json:"reason"`
}

func assertSingleWallRefresh(t *testing.T, fx *wallCharacterizationFixture, reason string) eventbus.Event {
	t.Helper()
	events := fx.collector.snapshot()
	if len(events) != 1 {
		t.Fatalf("events=%+v want exactly one wall refresh", events)
	}
	event := events[0]
	if event.Type != "wall.refresh_needed" || event.ProjectID != fx.project.ID {
		t.Fatalf("event=%+v want wall.refresh_needed for project %d", event, fx.project.ID)
	}
	var payload wallRefreshPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode refresh payload %s: %v", event.Payload, err)
	}
	if payload.Reason != reason {
		t.Fatalf("refresh reason=%q want=%q", payload.Reason, reason)
	}
	var exact map[string]any
	if err := json.Unmarshal(event.Payload, &exact); err != nil {
		t.Fatal(err)
	}
	assertExactJSONKeys(t, exact, "reason")
	return event
}

func mustCreateWallNoteDirect(t *testing.T, fx *wallCharacterizationFixture, text string) store.WallNote {
	t.Helper()
	note, _, err := fx.store.CreateNote(context.Background(), fx.project.ID, store.CreateNoteInput{
		X: 10, Y: 20, Width: 180, Height: 140, Color: "#ffd966", Text: text,
	})
	if err != nil {
		t.Fatalf("direct CreateNote: %v", err)
	}
	return note
}

func mustCreateWallEdgeDirect(t *testing.T, fx *wallCharacterizationFixture, from, to string) store.WallEdge {
	t.Helper()
	edge, _, err := fx.store.CreateEdge(context.Background(), fx.project.ID, from, to)
	if err != nil {
		t.Fatalf("direct CreateEdge: %v", err)
	}
	return edge
}

func TestWallEffectCharacterizationDurableSuccessReasonsAndCardinality(t *testing.T) {
	t.Run("create note", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		fx.collector.reset()
		resp, body := doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/notes"), wallNoteInput("create"), nil)
		assertWallStatus(t, resp, body, http.StatusCreated)
		assertSingleWallRefresh(t, fx, "wall_note_created")
	})

	t.Run("empty patch still writes and publishes", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		note := mustCreateWallNoteDirect(t, fx, "empty patch")
		before, err := fx.store.GetWall(context.Background(), fx.project.ID)
		if err != nil {
			t.Fatal(err)
		}
		fx.collector.reset()
		resp, body := doJSON(t, fx.client, http.MethodPatch, wallMutationURL(fx, "/notes/"+note.ID), map[string]any{
			"ifVersion": note.Version,
		}, nil)
		assertWallStatus(t, resp, body, http.StatusOK)
		assertSingleWallRefresh(t, fx, "wall_note_updated")
		after, err := fx.store.GetWall(context.Background(), fx.project.ID)
		if err != nil {
			t.Fatal(err)
		}
		if after.Version != before.Version+1 || len(after.Notes) != 1 || after.Notes[0].Version != note.Version+1 {
			t.Fatalf("empty patch before=%+v after=%+v", before, after)
		}
	})

	t.Run("delete note", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		note := mustCreateWallNoteDirect(t, fx, "delete")
		fx.collector.reset()
		resp, body := doJSON(t, fx.client, http.MethodDelete, wallMutationURL(fx, "/notes/"+note.ID), nil, nil)
		assertWallStatus(t, resp, body, http.StatusNoContent)
		assertSingleWallRefresh(t, fx, "wall_note_deleted")
	})

	t.Run("replace wall", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		fx.collector.reset()
		resp, body := doJSON(t, fx.client, http.MethodPut, wallMutationURL(fx, ""), map[string]any{
			"notes": []any{wallNoteInput("replace")},
		}, nil)
		assertWallStatus(t, resp, body, http.StatusOK)
		assertSingleWallRefresh(t, fx, "wall_replaced")
	})

	t.Run("create edge", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		a := mustCreateWallNoteDirect(t, fx, "a")
		b := mustCreateWallNoteDirect(t, fx, "b")
		fx.collector.reset()
		resp, body := doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/edges"), map[string]any{
			"from": a.ID, "to": b.ID,
		}, nil)
		assertWallStatus(t, resp, body, http.StatusCreated)
		assertSingleWallRefresh(t, fx, "wall_edge_created")
	})

	t.Run("delete edge", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		a := mustCreateWallNoteDirect(t, fx, "a")
		b := mustCreateWallNoteDirect(t, fx, "b")
		edge := mustCreateWallEdgeDirect(t, fx, a.ID, b.ID)
		fx.collector.reset()
		resp, body := doJSON(t, fx.client, http.MethodDelete, wallMutationURL(fx, "/edges/"+edge.ID), nil, nil)
		assertWallStatus(t, resp, body, http.StatusNoContent)
		assertSingleWallRefresh(t, fx, "wall_edge_deleted")
	})
}

func TestWallEffectCharacterizationDuplicateEdgePublishesAfterStoreNoOp(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	a := mustCreateWallNoteDirect(t, fx, "a")
	b := mustCreateWallNoteDirect(t, fx, "b")
	edge := mustCreateWallEdgeDirect(t, fx, a.ID, b.ID)
	before, err := fx.store.GetWall(context.Background(), fx.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	fx.collector.reset()

	var duplicate map[string]any
	resp, body := doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/edges"), map[string]any{
		"from": b.ID, "to": a.ID,
	}, &duplicate)
	assertWallStatus(t, resp, body, http.StatusCreated)
	assertExactJSONKeys(t, duplicate, "id", "from", "to")
	if duplicate["id"] != edge.ID || duplicate["from"] != edge.From || duplicate["to"] != edge.To {
		t.Fatalf("duplicate response=%+v want existing edge=%+v", duplicate, edge)
	}
	assertSingleWallRefresh(t, fx, "wall_edge_created")

	after, err := fx.store.GetWall(context.Background(), fx.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != before.Version || after.UpdatedAt != before.UpdatedAt || len(after.Edges) != 1 || after.Edges[0] != edge {
		t.Fatalf("duplicate changed persisted wall: before=%+v after=%+v", before, after)
	}
}

func TestWallEffectCharacterizationNoPublicationOnDurableFailure(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		setup     func(*testing.T, *wallCharacterizationFixture) (string, any)
		method    string
	}{
		{name: "create note", operation: "CreateNote", method: http.MethodPost, setup: func(_ *testing.T, fx *wallCharacterizationFixture) (string, any) {
			return wallMutationURL(fx, "/notes"), wallNoteInput("failure")
		}},
		{name: "patch note", operation: "PatchNote", method: http.MethodPatch, setup: func(t *testing.T, fx *wallCharacterizationFixture) (string, any) {
			note := mustCreateWallNoteDirect(t, fx, "patch failure")
			return wallMutationURL(fx, "/notes/"+note.ID), map[string]any{"ifVersion": note.Version, "text": "x"}
		}},
		{name: "delete note", operation: "DeleteNote", method: http.MethodDelete, setup: func(t *testing.T, fx *wallCharacterizationFixture) (string, any) {
			note := mustCreateWallNoteDirect(t, fx, "delete failure")
			return wallMutationURL(fx, "/notes/"+note.ID), nil
		}},
		{name: "replace wall", operation: "ReplaceWall", method: http.MethodPut, setup: func(_ *testing.T, fx *wallCharacterizationFixture) (string, any) {
			return wallMutationURL(fx, ""), map[string]any{"notes": []any{wallNoteInput("failure")}}
		}},
		{name: "create edge", operation: "CreateEdge", method: http.MethodPost, setup: func(t *testing.T, fx *wallCharacterizationFixture) (string, any) {
			a := mustCreateWallNoteDirect(t, fx, "a")
			b := mustCreateWallNoteDirect(t, fx, "b")
			return wallMutationURL(fx, "/edges"), map[string]any{"from": a.ID, "to": b.ID}
		}},
		{name: "delete edge", operation: "DeleteEdge", method: http.MethodDelete, setup: func(t *testing.T, fx *wallCharacterizationFixture) (string, any) {
			a := mustCreateWallNoteDirect(t, fx, "a")
			b := mustCreateWallNoteDirect(t, fx, "b")
			edge := mustCreateWallEdgeDirect(t, fx, a.ID, b.ID)
			return wallMutationURL(fx, "/edges/"+edge.ID), nil
		}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			fx := newWallCharacterizationFixture(t, true)
			requestURL, input := tt.setup(t, fx)
			fx.spy.setFailure(tt.operation, errors.New("injected "+tt.operation+" failure"))
			fx.collector.reset()
			resp, body := doJSON(t, fx.client, tt.method, requestURL, input, nil)
			assertWallStatus(t, resp, body, http.StatusInternalServerError)
			if events := fx.collector.snapshot(); len(events) != 0 {
				t.Fatalf("failed %s published events=%+v", tt.operation, events)
			}
		})
	}
}

func TestWallEffectCharacterizationNoPublicationBeforeSuccessfulMutation(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	_, viewer := fx.createUser(t, store.RoleViewer)

	tests := []struct {
		name   string
		client *http.Client
		method string
		url    string
		body   string
		status int
	}{
		{name: "authorization", client: viewer, method: http.MethodPost, url: wallMutationURL(fx, "/notes"), body: "{", status: http.StatusForbidden},
		{name: "parse", client: fx.client, method: http.MethodPost, url: wallMutationURL(fx, "/notes"), body: "{", status: http.StatusBadRequest},
		{name: "validation", client: fx.client, method: http.MethodPost, url: wallMutationURL(fx, "/notes"), body: `{"color":"bad"}`, status: http.StatusBadRequest},
		{name: "target", client: fx.client, method: http.MethodDelete, url: wallMutationURL(fx, "/notes/missing"), status: http.StatusNotFound},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			fx.collector.reset()
			resp, body := doWallRawJSON(t, tt.client, tt.method, tt.url, tt.body, true, nil)
			assertWallStatus(t, resp, body, tt.status)
			if events := fx.collector.snapshot(); len(events) != 0 {
				t.Fatalf("%s failure published events=%+v", tt.name, events)
			}
		})
	}

	note := mustCreateWallNoteDirect(t, fx, "conflict")
	if _, _, err := fx.store.PatchNote(context.Background(), fx.project.ID, note.ID, store.PatchNoteInput{IfVersion: note.Version}); err != nil {
		t.Fatal(err)
	}
	fx.collector.reset()
	resp, body := doJSON(t, fx.client, http.MethodPatch, wallMutationURL(fx, "/notes/"+note.ID), map[string]any{
		"ifVersion": note.Version, "text": "stale",
	}, nil)
	assertWallStatus(t, resp, body, http.StatusConflict)
	if events := fx.collector.snapshot(); len(events) != 0 {
		t.Fatalf("conflict published events=%+v", events)
	}
}

func TestWallEffectCharacterizationTransientIsUnpersistedAttributedPublication(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	before, err := fx.store.GetWall(context.Background(), fx.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	fx.spy.resetCalls()
	fx.collector.reset()
	noteID := "  unresolved cross-project-looking note  "
	resp, body := doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/transient"), map[string]any{
		"noteId": noteID, "x": -11.25, "y": 22.5,
	}, nil)
	assertWallStatus(t, resp, body, http.StatusNoContent)
	events := fx.collector.snapshot()
	if len(events) != 1 || events[0].Type != "wall.transient" || events[0].ProjectID != fx.project.ID {
		t.Fatalf("transient events=%+v", events)
	}
	var payload struct {
		NoteID string  `json:"noteId"`
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
		By     int64   `json:"by"`
	}
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.NoteID != noteID || payload.X != -11.25 || payload.Y != 22.5 || payload.By != fx.owner.ID {
		t.Fatalf("transient payload=%+v", payload)
	}
	var exact map[string]any
	if err := json.Unmarshal(events[0].Payload, &exact); err != nil {
		t.Fatal(err)
	}
	assertExactJSONKeys(t, exact, "noteId", "x", "y", "by")
	for _, operation := range []string{"GetWall", "CreateNote", "PatchNote", "DeleteNote", "ReplaceWall", "CreateEdge", "DeleteEdge"} {
		if got := fx.spy.callCount(operation); got != 0 {
			t.Fatalf("transient %s calls=%d want=0", operation, got)
		}
	}
	after, err := fx.store.GetWall(context.Background(), fx.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Notes) != len(before.Notes) || len(after.Edges) != len(before.Edges) || after.Version != before.Version || after.UpdatedAt != before.UpdatedAt {
		t.Fatalf("transient persisted state: before=%+v after=%+v", before, after)
	}
}

func TestWallEffectCharacterizationBlankTransientIsExactSilentNoOp(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	before, err := fx.store.GetWall(context.Background(), fx.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	fx.spy.resetCalls()
	fx.collector.reset()
	resp, body := doWallRawJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/transient"), `{"noteId":"   ","x":1,"y":2}`, true, nil)
	assertWallStatus(t, resp, body, http.StatusBadRequest)
	assertWallError(t, body, "VALIDATION_ERROR", "noteId required", map[string]any{
		"field": "noteId", "reason": "note_id_required",
	})
	if events := fx.collector.snapshot(); len(events) != 0 {
		t.Fatalf("blank transient published events=%+v", events)
	}
	for _, operation := range []string{"GetWall", "CreateNote", "PatchNote", "DeleteNote", "ReplaceWall", "CreateEdge", "DeleteEdge"} {
		if got := fx.spy.callCount(operation); got != 0 {
			t.Fatalf("blank transient %s calls=%d want=0", operation, got)
		}
	}
	after, err := fx.store.GetWall(context.Background(), fx.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Notes) != len(before.Notes) || len(after.Edges) != len(before.Edges) || after.Version != before.Version || after.UpdatedAt != before.UpdatedAt {
		t.Fatalf("blank transient persisted state: before=%+v after=%+v", before, after)
	}
}

type wallEffectContextMarker struct{}

func TestWallEffectCharacterizationPublicationUsesExactRawRequestContext(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	token, expiresAt, err := fx.store.CreateSession(context.Background(), fx.owner.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name       string
		body       string
		wantStatus int
		invoke     func(*httptest.ResponseRecorder, *http.Request)
	}{
		{
			name: "durable refresh", body: `{"x":10,"y":20,"width":180,"height":140,"color":"#ffd966","text":"context"}`,
			wantStatus: http.StatusCreated,
			invoke: func(w *httptest.ResponseRecorder, r *http.Request) {
				fx.server.handleWallCreateNote(w, r, fx.project.ID)
			},
		},
		{
			name: "transient publication", body: `{"noteId":"context-note","x":1,"y":2}`,
			wantStatus: http.StatusNoContent,
			invoke: func(w *httptest.ResponseRecorder, r *http.Request) {
				fx.server.handleWallTransient(w, r, fx.project.ID)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			deadline := time.Now().Add(time.Minute)
			marked := context.WithValue(context.Background(), wallEffectContextMarker{}, tt.name)
			rawContext, cancel := context.WithDeadline(marked, deadline)
			defer cancel()
			req := httptest.NewRequest(http.MethodPost, wallMutationURL(fx, "/direct"), bytes.NewBufferString(tt.body)).WithContext(rawContext)
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: token, Expires: expiresAt})
			recorder := httptest.NewRecorder()
			fx.collector.reset()

			tt.invoke(recorder, req)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, tt.wantStatus, recorder.Body.Bytes())
			}
			contexts := fx.collector.contextSnapshot()
			if len(contexts) != 1 || contexts[0] != rawContext {
				t.Fatalf("published contexts=%v want exact raw request context", contexts)
			}
			if contexts[0].Value(wallEffectContextMarker{}) != tt.name {
				t.Fatalf("published context lost request marker")
			}
			gotDeadline, ok := contexts[0].Deadline()
			if !ok || !gotDeadline.Equal(deadline) {
				t.Fatalf("published deadline=%v,%v want=%v", gotDeadline, ok, deadline)
			}
			if _, ok := store.UserIDFromContext(contexts[0]); ok {
				t.Fatal("raw publication context unexpectedly contains the enriched actor")
			}
		})
	}
}

func TestWallEffectCharacterizationSSEWireProjection(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	channel, unsubscribe := fx.server.hub.Subscribe(fx.project.ID)
	defer unsubscribe()

	resp, body := doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/notes"), wallNoteInput("sse"), nil)
	assertWallStatus(t, resp, body, http.StatusCreated)
	select {
	case message := <-channel:
		var wire map[string]any
		if err := json.Unmarshal(message, &wire); err != nil {
			t.Fatal(err)
		}
		assertExactJSONKeys(t, wire, "id", "type", "projectId", "reason")
		if wire["type"] != "wall.refresh_needed" || wire["projectId"] != float64(fx.project.ID) || wire["reason"] != "wall_note_created" {
			t.Fatalf("refresh wire=%+v", wire)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for wall refresh SSE")
	}

	resp, body = doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/transient"), map[string]any{
		"noteId": "sse-note", "x": 1.25, "y": -2.5,
	}, nil)
	assertWallStatus(t, resp, body, http.StatusNoContent)
	select {
	case message := <-channel:
		var wire map[string]any
		if err := json.Unmarshal(message, &wire); err != nil {
			t.Fatal(err)
		}
		assertExactJSONKeys(t, wire, "id", "type", "projectId", "payload")
		payload := wire["payload"].(map[string]any)
		assertExactJSONKeys(t, payload, "noteId", "x", "y", "by")
		if wire["type"] != "wall.transient" || wire["projectId"] != float64(fx.project.ID) || payload["by"] != float64(fx.owner.ID) {
			t.Fatalf("transient wire=%+v", wire)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for wall transient SSE")
	}
}

func TestWallEffectCharacterizationWebhookExactAndWildcardVisibility(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	ownerCtx := store.WithUserID(context.Background(), fx.owner.ID)
	for _, hook := range []store.CreateWebhookInput{
		{ProjectID: fx.project.ID, URL: "https://example.com/refresh", Events: []string{"wall.refresh_needed"}},
		{ProjectID: fx.project.ID, URL: "https://example.com/transient", Events: []string{"wall.transient"}},
		{ProjectID: fx.project.ID, URL: "https://example.com/wildcard", Events: []string{"*"}},
	} {
		if _, err := fx.store.CreateWebhook(ownerCtx, fx.owner.ID, hook); err != nil {
			t.Fatalf("create webhook: %v", err)
		}
	}
	queue := newWebhookQueue(discardLogger())
	dispatcher := newWebhookDispatcher(fx.spy, queue, discardLogger())
	fx.server.fanout = eventbus.NewFanout(newSSEBridge(fx.server.hub, nil), dispatcher)

	resp, body := doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/notes"), wallNoteInput("webhook"), nil)
	assertWallStatus(t, resp, body, http.StatusCreated)
	refresh := waitForWallWebhookDeliveries(t, queue, 2)
	assertWallWebhookTypes(t, refresh, "wall.refresh_needed")

	resp, body = doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/transient"), map[string]any{
		"noteId": "webhook-note", "x": 3, "y": 4,
	}, nil)
	assertWallStatus(t, resp, body, http.StatusNoContent)
	transient := waitForWallWebhookDeliveries(t, queue, 2)
	assertWallWebhookTypes(t, transient, "wall.transient")
}

func waitForWallWebhookDeliveries(t *testing.T, queue *webhookQueue, want int) []webhookDelivery {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	var deliveries []webhookDelivery
	for len(deliveries) < want {
		deliveries = append(deliveries, queue.Drain()...)
		if len(deliveries) >= want {
			break
		}
		select {
		case <-queue.Wait():
		case <-deadline.C:
			t.Fatalf("webhook deliveries=%+v want=%d", deliveries, want)
		}
	}
	return deliveries
}

func assertWallWebhookTypes(t *testing.T, deliveries []webhookDelivery, wantType string) {
	t.Helper()
	if len(deliveries) != 2 {
		t.Fatalf("deliveries=%+v want exact and wildcard", deliveries)
	}
	urls := make([]string, 0, len(deliveries))
	for _, delivery := range deliveries {
		if delivery.EventType != wantType {
			t.Fatalf("delivery=%+v want type=%s", delivery, wantType)
		}
		urls = append(urls, delivery.URL)
	}
	sort.Strings(urls)
	wantSpecific := "https://example.com/refresh"
	if wantType == "wall.transient" {
		wantSpecific = "https://example.com/transient"
	}
	wantURLs := []string{wantSpecific, "https://example.com/wildcard"}
	sort.Strings(wantURLs)
	if urls[0] != wantURLs[0] || urls[1] != wantURLs[1] {
		t.Fatalf("delivery URLs=%v want=%v", urls, wantURLs)
	}
}
