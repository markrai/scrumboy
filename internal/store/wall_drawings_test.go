package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func wallStroke(id string, x float64) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"id":"%s","tool":"pen","color":"#112233","size":4,"opacity":1,"points":[[%g,-50000,0.5],[%g,10,0.7]],"createdAt":1}`, id, x, x+5))
}

func TestWallDrawingApplyBumpsVersionAndPreservesNotes(t *testing.T) {
	st, _, ctx, p := setupDrawingStore(t)
	note, wall, err := st.CreateNote(ctx, p.ID, CreateNoteInput{X: 1, Y: 2, Color: "#ffd966", Text: "n"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.ApplyWallDrawingOps(ctx, p.ID, 7, WallDrawingOps{BaseVersion: wall.Version, Add: []json.RawMessage{wallStroke("a", -100000)}})
	if err != nil || !res.Applied {
		t.Fatalf("apply err=%v applied=%v", err, res.Applied)
	}
	if res.Wall.Version != wall.Version+1 {
		t.Fatalf("version %d -> %d", wall.Version, res.Wall.Version)
	}
	live := LiveWallDrawings(res.Wall.Drawings)
	if len(live) != 1 || live[0].By == nil || *live[0].By != 7 {
		t.Fatalf("live=%+v", live)
	}
	if _, _, err := st.PatchNote(ctx, p.ID, note.ID, PatchNoteInput{Text: ptrString("edited")}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetWall(ctx, p.ID)
	if err != nil || len(LiveWallDrawings(got.Drawings)) != 1 || got.Notes[0].Text != "edited" {
		t.Fatalf("note mutation lost drawings: %+v err=%v", got.Drawings, err)
	}
}

func ptrString(s string) *string { return &s }

func TestWallDrawingThreeWayStaleCheck(t *testing.T) {
	st, _, ctx, p := setupDrawingStore(t)
	first, err := st.ApplyWallDrawingOps(ctx, p.ID, 1, WallDrawingOps{BaseVersion: 0, Add: []json.RawMessage{wallStroke("a", 0)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"}); err != nil {
		t.Fatal(err)
	}
	retry, err := st.ApplyWallDrawingOps(ctx, p.ID, 1, WallDrawingOps{BaseVersion: 0, Add: []json.RawMessage{wallStroke("a", 0)}})
	if err != nil || retry.Applied {
		t.Fatalf("identical stale retry: applied=%v err=%v", retry.Applied, err)
	}
	if len(LiveWallDrawings(retry.Wall.Drawings)) != 1 {
		t.Fatal("retry duplicated a stroke")
	}
	_, err = st.ApplyWallDrawingOps(ctx, p.ID, 1, WallDrawingOps{BaseVersion: first.Wall.Version, Add: []json.RawMessage{wallStroke("b", 0)}})
	if !errors.Is(err, ErrConflict) || ErrorReason(err) != ReasonWallDrawingVersionConflict {
		t.Fatalf("stale different err=%v", err)
	}
	current, _ := st.GetWall(ctx, p.ID)
	rebased, err := st.ApplyWallDrawingOps(ctx, p.ID, 1, WallDrawingOps{BaseVersion: current.Version, Add: []json.RawMessage{wallStroke("b", 0)}})
	if err != nil || !rebased.Applied || len(LiveWallDrawings(rebased.Wall.Drawings)) != 2 {
		t.Fatalf("rebased retry err=%v", err)
	}
}

func TestWallDrawingEraseTombstoneWinsOverLateAdd(t *testing.T) {
	st, _, ctx, p := setupDrawingStore(t)
	r, err := st.ApplyWallDrawingOps(ctx, p.ID, 1, WallDrawingOps{Add: []json.RawMessage{wallStroke("a", 0)}})
	if err != nil {
		t.Fatal(err)
	}
	r, err = st.ApplyWallDrawingOps(ctx, p.ID, 2, WallDrawingOps{BaseVersion: r.Wall.Version, Erase: []string{"a"}})
	if err != nil || len(LiveWallDrawings(r.Wall.Drawings)) != 0 {
		t.Fatalf("erase err=%v", err)
	}
	late, err := st.ApplyWallDrawingOps(ctx, p.ID, 1, WallDrawingOps{BaseVersion: 0, Add: []json.RawMessage{wallStroke("a", 0)}})
	if err != nil || late.Applied || len(LiveWallDrawings(late.Wall.Drawings)) != 0 {
		t.Fatalf("late add resurrected stroke: applied=%v err=%v", late.Applied, err)
	}
	again, err := st.ApplyWallDrawingOps(ctx, p.ID, 2, WallDrawingOps{BaseVersion: 0, Erase: []string{"a"}})
	if err != nil || again.Applied {
		t.Fatalf("erase retry err=%v applied=%v", err, again.Applied)
	}
}

func TestWallDrawingValidationAndLimits(t *testing.T) {
	st, _, ctx, p := setupDrawingStore(t)
	cases := []json.RawMessage{
		json.RawMessage(`{"id":"x","tool":"pen","color":"url(#a)","size":4,"opacity":1,"points":[[0,0,0.5]],"createdAt":1}`),
		json.RawMessage(`{"id":"x","tool":"pen","color":"#112233","size":4,"opacity":1,"points":[[100001,0,0.5]],"createdAt":1}`),
		json.RawMessage(`{"id":"x","tool":"eraser","color":"#112233","size":4,"opacity":1,"points":[[0,0,0.5]],"createdAt":1}`),
		json.RawMessage(`{"id":"x","tool":"pen","color":"#112233","size":4,"opacity":1,"points":[[0,0,0.5]],"createdAt":1,"deleted":true}`),
		json.RawMessage(`{"id":"<x>","tool":"pen","color":"#112233","size":4,"opacity":1,"points":[[0,0,0.5]],"createdAt":1}`),
	}
	for i, c := range cases {
		if _, err := st.ApplyWallDrawingOps(ctx, p.ID, 1, WallDrawingOps{Add: []json.RawMessage{c}}); ErrorReason(err) != ReasonInvalidDrawing {
			t.Fatalf("case %d err=%v", i, err)
		}
	}
	version := int64(0)
	for batch := 0; batch < MaxWallDrawingStrokes/MaxWallDrawingOpsPerCommit; batch++ {
		var adds []json.RawMessage
		for i := 0; i < MaxWallDrawingOpsPerCommit; i++ {
			adds = append(adds, wallStroke(fmt.Sprintf("s%d_%d", batch, i), float64(i)))
		}
		r, err := st.ApplyWallDrawingOps(ctx, p.ID, 1, WallDrawingOps{BaseVersion: version, Add: adds})
		if err != nil {
			t.Fatal(err)
		}
		version = r.Wall.Version
	}
	_, err := st.ApplyWallDrawingOps(ctx, p.ID, 1, WallDrawingOps{BaseVersion: version, Add: []json.RawMessage{wallStroke("over", 0)}})
	if ErrorReason(err) != ReasonDrawingLimit {
		t.Fatalf("over limit err=%v", err)
	}
}

func TestWallDrawingBackupRoundTrip(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	if _, err := st.ApplyWallDrawingOps(ctx, p.ID, 1, WallDrawingOps{Add: []json.RawMessage{wallStroke("a", 3), wallStroke("b", 9)}}); err != nil {
		t.Fatal(err)
	}
	r, _ := st.GetWall(ctx, p.ID)
	if _, err := st.ApplyWallDrawingOps(ctx, p.ID, 1, WallDrawingOps{BaseVersion: r.Version, Erase: []string{"b"}}); err != nil {
		t.Fatal(err)
	}
	data := exportArchive(t, st, ctx)
	if data.Projects[0].Wall == nil || len(data.Projects[0].Wall.Drawings) != 1 || !ExportHasDrawings(data) {
		t.Fatalf("wall export=%+v", data.Projects[0].Wall)
	}
	if _, err := st.ImportProjectsWithAssets(ctx, data, ModeFull, "copy", &ImportAssetPlan{}); err != nil {
		t.Fatal(err)
	}
	var copyID int64
	if err := db.QueryRow(`SELECT id FROM projects WHERE id != ? ORDER BY id DESC LIMIT 1`, p.ID).Scan(&copyID); err != nil {
		t.Fatal(err)
	}
	copied, err := st.GetWall(ctx, copyID)
	if err != nil {
		t.Fatal(err)
	}
	live := LiveWallDrawings(copied.Drawings)
	if len(live) != 1 || live[0].ID != "a" || live[0].By != nil {
		t.Fatalf("copied drawings=%+v", live)
	}
	legacy := exportArchive(t, st, ctx)
	legacy.Version = "1.3"
	if _, err := st.ImportProjectsWithAssets(ctx, legacy, ModeFull, "copy", &ImportAssetPlan{}); err == nil {
		t.Fatal("1.3 archive with wall drawings accepted")
	}
}
