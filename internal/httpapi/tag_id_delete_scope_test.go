package httpapi

import (
	"net/http"
	"strconv"
	"testing"
)

func TestAnonymousBoardTagIDDeleteRejectsForeignBoard(t *testing.T) {
	fx := newTagMutationRESTFixture(t)
	attackerClient := newCookieClient(t)
	victimClient := newCookieClient(t)
	attackerSlug := createAnonBoardViaHTTP(t, attackerClient, fx.ts)
	victimSlug := createAnonBoardViaHTTP(t, victimClient, fx.ts)
	createTodoAPI(t, victimClient, fx.ts, victimSlug, "victim task", "victimsecret")
	victimProjectID := projectIDBySlug(t, fx.db, victimSlug)
	var tagID int64
	if err := fx.db.QueryRow(`SELECT id FROM tags WHERE project_id = ? AND name = ?`, victimProjectID, "victimsecret").Scan(&tagID); err != nil {
		t.Fatal(err)
	}
	fx.resetEvents()
	resp, body := doJSON(t, attackerClient, http.MethodDelete,
		fx.ts+"/api/board/"+attackerSlug+"/tags/id/"+strconv.FormatInt(tagID, 10), nil, nil)
	assertTagMutationRESTStatus(t, resp, body, http.StatusNotFound)
	if len(fx.collector.events) != 0 {
		t.Fatalf("foreign deletion published refresh events: %+v", fx.collector.events)
	}
	var count int
	if err := fx.db.QueryRow(`SELECT COUNT(*) FROM tags WHERE id = ?`, tagID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("victim tag count after rejection = %d, err = %v", count, err)
	}
	if err := fx.db.QueryRow(`SELECT COUNT(*) FROM todo_tags WHERE tag_id = ?`, tagID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("victim task associations after rejection = %d, err = %v", count, err)
	}
	resp, body = doJSON(t, victimClient, http.MethodDelete,
		fx.ts+"/api/board/"+victimSlug+"/tags/id/"+strconv.FormatInt(tagID, 10), nil, nil)
	assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
	if err := fx.db.QueryRow(`SELECT COUNT(*) FROM tags WHERE id = ?`, tagID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("victim tag count after own delete = %d, err = %v", count, err)
	}
}
