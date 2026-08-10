package httpapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func TestDisabledSprintHistoricalAndLiveReadContract(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	st := wireTodoUpdatePublisher(t, ts)
	client := newCookieClient(t)
	owner := bootstrapUserClient(t, client, ts.URL, "Owner", "disabled-read-contract@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	project, ownerCtx := createTodoUpdateProject(t, st, ownerID, "Disabled historical reads")
	now := time.Now().UTC()
	active, err := st.CreateSprint(ownerCtx, project.ID, "Retained active history", now.Add(-48*time.Hour), now.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	if err := st.ActivateSprint(ownerCtx, project.ID, active.ID); err != nil {
		t.Fatalf("ActivateSprint: %v", err)
	}
	todo, err := st.CreateTodo(ownerCtx, project.ID, store.CreateTodoInput{
		Title: "Dormant sprint todo", ColumnKey: store.DefaultColumnDoing, SprintID: &active.ID,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if err := st.UpdateProjectSprintsEnabled(ownerCtx, project.ID, ownerID, false); err != nil {
		t.Fatalf("disable sprints: %v", err)
	}

	activeURL := fmt.Sprintf("%s/api/board/%s/sprints/active", ts.URL, project.Slug)
	resp, body := doJSON(t, client, http.MethodGet, activeURL, nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled active sprint status=%d body=%s, want 404", resp.StatusCode, body)
	}

	var filterError apiErrorEnvelope
	resp, body = doJSON(t, client, http.MethodGet, fmt.Sprintf("%s/api/board/%s?sprintId=%d", ts.URL, project.Slug, active.ID), nil, &filterError)
	if resp.StatusCode != http.StatusBadRequest || filterError.Error.Code != "VALIDATION_ERROR" || filterError.Error.Details["reason"] != "sprints_disabled" {
		t.Fatalf("disabled board filter status=%d envelope=%+v body=%s", resp.StatusCode, filterError, body)
	}

	var liveBoard map[string]any
	resp, body = doJSON(t, client, http.MethodGet, fmt.Sprintf("%s/api/board/%s", ts.URL, project.Slug), nil, &liveBoard)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disabled live board status=%d body=%s", resp.StatusCode, body)
	}
	columns, ok := liveBoard["columns"].(map[string]any)
	if !ok {
		t.Fatalf("disabled live board columns=%T %+v", liveBoard["columns"], liveBoard["columns"])
	}
	foundTodo := false
	for _, rawColumn := range columns {
		for _, rawTodo := range rawColumn.([]any) {
			projected := rawTodo.(map[string]any)
			if int64(projected["localId"].(float64)) != todo.LocalID {
				continue
			}
			foundTodo = true
			if _, exposed := projected["sprintId"]; exposed {
				t.Fatalf("disabled live board exposed dormant sprint assignment: %+v", projected)
			}
		}
	}
	if !foundTodo {
		t.Fatalf("disabled live board did not return todo localId=%d", todo.LocalID)
	}

	var listResponse map[string]any
	resp, body = doJSON(t, client, http.MethodGet, fmt.Sprintf("%s/api/board/%s/sprints", ts.URL, project.Slug), nil, &listResponse)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("historical sprint list status=%d body=%s", resp.StatusCode, body)
	}
	sprints, ok := listResponse["sprints"].([]any)
	if !ok || len(sprints) != 1 {
		t.Fatalf("historical sprint list=%+v", listResponse)
	}
	listed := sprints[0].(map[string]any)
	if int64(listed["id"].(float64)) != active.ID || listed["state"] != store.SprintStateActive || int64(listed["todoCount"].(float64)) != 1 {
		t.Fatalf("historical sprint list entry=%+v", listed)
	}

	var detail map[string]any
	resp, body = doJSON(t, client, http.MethodGet, fmt.Sprintf("%s/api/board/%s/sprints/%d", ts.URL, project.Slug, active.ID), nil, &detail)
	if resp.StatusCode != http.StatusOK || int64(detail["id"].(float64)) != active.ID || detail["state"] != store.SprintStateActive {
		t.Fatalf("historical sprint detail status=%d detail=%+v body=%s", resp.StatusCode, detail, body)
	}

	var sprintBurndown []map[string]any
	resp, body = doJSON(t, client, http.MethodGet, fmt.Sprintf("%s/api/board/%s/sprints/%d/burndown", ts.URL, project.Slug, active.ID), nil, &sprintBurndown)
	if resp.StatusCode != http.StatusOK || len(sprintBurndown) == 0 {
		t.Fatalf("historical sprint burndown status=%d points=%+v body=%s", resp.StatusCode, sprintBurndown, body)
	}

	var projectBurndown []map[string]any
	resp, body = doJSON(t, client, http.MethodGet, fmt.Sprintf("%s/api/board/%s/burndown", ts.URL, project.Slug), nil, &projectBurndown)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disabled project burndown status=%d body=%s", resp.StatusCode, body)
	}

	stored, err := st.GetSprintByID(ownerCtx, active.ID)
	if err != nil || stored.State != store.SprintStateActive || stored.ClosedAt != nil {
		t.Fatalf("disabled reads mutated retained sprint: %+v err=%v", stored, err)
	}
	if err := st.UpdateProjectSprintsEnabled(ownerCtx, project.ID, ownerID, true); err != nil {
		t.Fatalf("re-enable sprints: %v", err)
	}
	var effective map[string]any
	resp, body = doJSON(t, client, http.MethodGet, activeURL, nil, &effective)
	if resp.StatusCode != http.StatusOK || int64(effective["id"].(float64)) != active.ID || effective["state"] != store.SprintStateActive {
		t.Fatalf("re-enabled active sprint status=%d sprint=%+v body=%s", resp.StatusCode, effective, body)
	}
}
