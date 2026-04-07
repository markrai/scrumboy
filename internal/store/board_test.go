package store

import (
	"context"
	"sort"
	"testing"
)

func TestGetBoard_SearchMatchesTitle(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Login feature", Body: ""}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo Login feature: %v", err)
	}
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "User login page", Body: ""}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo User login page: %v", err)
	}
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Dashboard", Body: ""}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo Dashboard: %v", err)
	}

	pc, _ := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	_, _, _, cols, err := st.GetBoard(ctx, &pc, "", "login", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}

	// Should match "Login feature" and "User login page"
	totalTodos := len(cols[DefaultColumnBacklog])
	if totalTodos != 2 {
		t.Fatalf("expected 2 todos, got %d", totalTodos)
	}
}

func TestGetBoard_SearchMatchesBody(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Feature A", Body: "User authentication required"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo Feature A: %v", err)
	}
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Feature B", Body: "Main dashboard page"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo Feature B: %v", err)
	}

	pc, _ := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	_, _, _, cols, err := st.GetBoard(ctx, &pc, "", "authentication", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}

	// Should match "Feature A" via body
	totalTodos := len(cols[DefaultColumnBacklog])
	if totalTodos != 1 {
		t.Fatalf("expected 1 todo, got %d", totalTodos)
	}
}

func TestGetBoard_SearchCaseInsensitive(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Login feature", Body: ""}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	pc, _ := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	_, _, _, cols, err := st.GetBoard(ctx, &pc, "", "LOGIN", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}

	// Should match "Login feature" (case-insensitive)
	totalTodos := len(cols[DefaultColumnBacklog])
	if totalTodos != 1 {
		t.Fatalf("expected 1 todo, got %d", totalTodos)
	}
}

func TestGetBoard_SearchSubstringOnly(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Login feature", Body: ""}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	// "log" should match "Login" (substring)
	pc, _ := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	_, _, _, cols, err := st.GetBoard(ctx, &pc, "", "log", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	totalTodos := len(cols[DefaultColumnBacklog])
	if totalTodos != 1 {
		t.Fatalf("expected 1 todo for 'log', got %d", totalTodos)
	}

	// "log in" should NOT match "Login" (no tokenization)
	_, _, _, cols, err = st.GetBoard(ctx, &pc, "", "log in", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	totalTodos = len(cols[DefaultColumnBacklog])
	if totalTodos != 0 {
		t.Fatalf("expected 0 todos for 'log in', got %d", totalTodos)
	}
}

func TestGetBoard_TagAndSearchAND(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create user for tag ownership
	user, err := st.BootstrapUser(ctx, "user@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, user.ID)

	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create todos with different tags and titles
	todo1, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Login feature", Body: "", Tags: []string{"bug"}}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo 1: %v", err)
	}
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Login page", Body: "", Tags: []string{"feature"}}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo 2: %v", err)
	}
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Dashboard", Body: "", Tags: []string{"bug"}}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo 3: %v", err)
	}

	// Search for "login" with tag "bug" - should only match todo1
	pc, _ := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	_, _, _, cols, err := st.GetBoard(ctx, &pc, "bug", "login", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}

	totalTodos := len(cols[DefaultColumnBacklog])
	if totalTodos != 1 {
		t.Fatalf("expected 1 todo (tag=bug AND search=login), got %d", totalTodos)
	}

	// Verify it's the right todo
	if cols[DefaultColumnBacklog][0].ID != todo1.ID {
		t.Fatalf("expected todo ID %d, got %d", todo1.ID, cols[DefaultColumnBacklog][0].ID)
	}
}

func TestParseLaneCursor(t *testing.T) {
	tests := []struct {
		in       string
		wantRank int64
		wantID   int64
	}{
		{"", 0, 0},
		{"invalid", 0, 0},
		{"2000:42", 2000, 42},
		{"0:0", 0, 0},
		{"100:999", 100, 999},
	}
	for _, tt := range tests {
		gotRank, gotID := ParseLaneCursor(tt.in)
		if gotRank != tt.wantRank || gotID != tt.wantID {
			t.Errorf("ParseLaneCursor(%q) = (%d, %d), want (%d, %d)", tt.in, gotRank, gotID, tt.wantRank, tt.wantID)
		}
	}
}

func TestListTodosForBoardLane_Pagination(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create 5 todos in BACKLOG
	for i := 0; i < 5; i++ {
		_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Todo", Body: ""}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
	}

	// First page: limit 2
	items, nextCursor, hasMore, err := st.ListTodosForBoardLane(ctx, p.ID, DefaultColumnBacklog, 2, 0, 0, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("ListTodosForBoardLane: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
	if !hasMore {
		t.Error("expected hasMore true")
	}
	if nextCursor == "" {
		t.Error("expected non-empty nextCursor")
	}

	// Parse cursor and fetch next page
	afterRank, afterID := ParseLaneCursor(nextCursor)
	items2, nextCursor2, hasMore2, err := st.ListTodosForBoardLane(ctx, p.ID, DefaultColumnBacklog, 2, afterRank, afterID, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("ListTodosForBoardLane page 2: %v", err)
	}
	if len(items2) != 2 {
		t.Errorf("expected 2 items on page 2, got %d", len(items2))
	}
	if !hasMore2 {
		t.Error("expected hasMore true on page 2")
	}

	// Third page
	afterRank2, afterID2 := ParseLaneCursor(nextCursor2)
	items3, _, hasMore3, err := st.ListTodosForBoardLane(ctx, p.ID, DefaultColumnBacklog, 2, afterRank2, afterID2, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("ListTodosForBoardLane page 3: %v", err)
	}
	if len(items3) != 1 {
		t.Errorf("expected 1 item on page 3, got %d", len(items3))
	}
	if hasMore3 {
		t.Error("expected hasMore false on last page")
	}

	// Verify no duplicates across pages
	seen := make(map[int64]bool)
	for _, it := range append(append(items, items2...), items3...) {
		if seen[it.ID] {
			t.Errorf("duplicate todo ID %d", it.ID)
		}
		seen[it.ID] = true
	}
}

// isAfter returns true iff first argument is strictly after second in ORDER BY rank ASC, id ASC
// (same total order as ListTodosForBoardLane / getHiddenLaneBoundaryLocalId).
func isAfter(a, b Todo) bool {
	if a.Rank != b.Rank {
		return a.Rank > b.Rank
	}
	return a.ID > b.ID
}

func TestListTodosForBoardLane_PaginationBoundaryContract(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	for i := 0; i < 5; i++ {
		_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Todo", Body: ""}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
	}

	const pageSize = 2
	items1, cursor1, hasMore1, err := st.ListTodosForBoardLane(ctx, p.ID, DefaultColumnBacklog, pageSize, 0, 0, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(items1) != pageSize || !hasMore1 || cursor1 == "" {
		t.Fatalf("page1: len=%d hasMore=%v cursor empty=%v", len(items1), hasMore1, cursor1 == "")
	}
	A := items1[len(items1)-1]

	afterRank, afterID := ParseLaneCursor(cursor1)
	items2, cursor2, _, err := st.ListTodosForBoardLane(ctx, p.ID, DefaultColumnBacklog, pageSize, afterRank, afterID, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(items2) < 1 {
		t.Fatalf("page2 empty")
	}
	B := items2[0]
	if !isAfter(B, A) {
		t.Fatalf("expected page2[0] strictly after page1[last]: A=(rank=%d id=%d) B=(rank=%d id=%d)", A.Rank, A.ID, B.Rank, B.ID)
	}

	// Drag/drop invariant: limit=1 with afterCursor == page1's nextCursor returns exactly page2[0].
	probe, probeCursor, probeHasMore, err := st.ListTodosForBoardLane(ctx, p.ID, DefaultColumnBacklog, 1, afterRank, afterID, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("probe limit=1: %v", err)
	}
	if len(probe) != 1 {
		t.Fatalf("probe: expected 1 item, got %d", len(probe))
	}
	if probe[0].ID != B.ID {
		t.Fatalf("limit=1 after page1 cursor: got id=%d want page2[0] id=%d", probe[0].ID, B.ID)
	}
	_ = probeCursor
	_ = probeHasMore

	// Walk remaining pages: union should be 5 distinct ids, no gaps in sequence when merged by order
	var all []Todo
	all = append(all, items1...)
	all = append(all, items2...)
	afterRank2, afterID2 := ParseLaneCursor(cursor2)
	items3, _, _, err := st.ListTodosForBoardLane(ctx, p.ID, DefaultColumnBacklog, pageSize, afterRank2, afterID2, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	all = append(all, items3...)
	seen := make(map[int64]struct{}, len(all))
	for _, it := range all {
		if _, ok := seen[it.ID]; ok {
			t.Fatalf("duplicate id %d across pages", it.ID)
		}
		seen[it.ID] = struct{}{}
	}
	if len(seen) != 5 {
		t.Fatalf("expected 5 unique todos, got %d", len(seen))
	}
}

func TestListTodosForBoardLane_TagFilterPaginationInvariants(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "tagpage@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, user.ID)

	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	for i := 0; i < 5; i++ {
		_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Todo", Body: "", Tags: []string{"bug"}}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
	}

	const pageSize = 2
	tag := "bug"
	var all []Todo
	afterRank, afterID := int64(0), int64(0)
	var prevLast *Todo
	for page := 0; page < 5; page++ {
		items, nextCur, hasMore, err := st.ListTodosForBoardLane(ctx, p.ID, DefaultColumnBacklog, pageSize, afterRank, afterID, tag, "", SprintFilter{Mode: "none"})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(items) == 0 {
			break
		}
		if prevLast != nil {
			if !isAfter(items[0], *prevLast) {
				t.Fatalf("page boundary: first item not after previous page last")
			}
		}
		for i := range items {
			if i > 0 && !isAfter(items[i], items[i-1]) {
				t.Fatalf("page %d: items not ordered by rank,id", page)
			}
			got := append([]string(nil), items[i].Tags...)
			sort.Strings(got)
			if len(got) != 1 || got[0] != "bug" {
				t.Fatalf("todo %d: tags %v want [bug]", items[i].ID, items[i].Tags)
			}
		}
		all = append(all, items...)
		prevLast = &items[len(items)-1]
		if !hasMore {
			break
		}
		if nextCur == "" {
			t.Fatalf("page %d: hasMore but empty cursor", page)
		}
		afterRank, afterID = ParseLaneCursor(nextCur)
	}
	seen := make(map[int64]struct{})
	for _, it := range all {
		if _, ok := seen[it.ID]; ok {
			t.Fatalf("duplicate id %d", it.ID)
		}
		seen[it.ID] = struct{}{}
	}
	if len(seen) != 5 {
		t.Fatalf("expected 5 tagged todos across pages, got %d", len(seen))
	}
}

func TestListTodosForBoardLane_TagFilter(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "u@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, user.ID)

	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "A", Body: "", Tags: []string{"bug"}}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "B", Body: "", Tags: []string{"feature"}}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	items, _, hasMore, err := st.ListTodosForBoardLane(ctx, p.ID, DefaultColumnBacklog, 10, 0, 0, "bug", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("ListTodosForBoardLane: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 item with tag=bug, got %d", len(items))
	}
	if hasMore {
		t.Error("expected hasMore false")
	}
}

func TestGetBoardPaged_ReturnsColumnsMeta(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	for i := 0; i < 25; i++ {
		_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Todo", Body: ""}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
	}

	pc, _ := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	_, _, _, cols, meta, err := st.GetBoardPaged(ctx, &pc, "", "", SprintFilter{Mode: "none"}, 10)
	if err != nil {
		t.Fatalf("GetBoardPaged: %v", err)
	}
	if meta == nil {
		t.Fatal("expected non-nil columnsMeta")
	}

	// BACKLOG should have 25 todos, we get 10, hasMore true
	if len(cols[DefaultColumnBacklog]) != 10 {
		t.Errorf("expected 10 items in BACKLOG, got %d", len(cols[DefaultColumnBacklog]))
	}
	if !meta[DefaultColumnBacklog].HasMore {
		t.Error("expected BACKLOG hasMore true")
	}
	if meta[DefaultColumnBacklog].NextCursor == "" {
		t.Error("expected non-empty nextCursor for BACKLOG")
	}
	if meta[DefaultColumnBacklog].TotalCount != 25 {
		t.Errorf("expected BACKLOG totalCount 25, got %d", meta[DefaultColumnBacklog].TotalCount)
	}
}
