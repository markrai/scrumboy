package trelloimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func fixedNow() time.Time {
	return time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
}

func TestBuildImportBundle_MinimalBoard(t *testing.T) {
	raw := []byte(`{
		"id":"board-1",
		"name":"Minimal Board",
		"lists":[
			{"id":"list-1","name":"Todo","pos":10,"closed":false},
			{"id":"list-2","name":"Done","pos":20,"closed":false}
		],
		"cards":[
			{"id":"card-1","name":"Ship it","desc":"Hello","idList":"list-1","pos":10,"closed":false,"idLabels":[],"idMembers":[]}
		]
	}`)

	bundle, err := BuildImportBundle(raw, fixedNow())
	if err != nil {
		t.Fatalf("BuildImportBundle: %v", err)
	}
	if len(bundle.Preview.HardErrors) != 0 {
		t.Fatalf("unexpected hard errors: %v", bundle.Preview.HardErrors)
	}
	if bundle.Preview.BoardName != "Minimal Board" {
		t.Fatalf("boardName = %q", bundle.Preview.BoardName)
	}
	if bundle.Preview.DetectedDoneColumn != "Done" {
		t.Fatalf("detected done column = %q", bundle.Preview.DetectedDoneColumn)
	}
	if bundle.ExportData == nil || len(bundle.ExportData.Projects) != 1 {
		t.Fatalf("expected single exported project, got %#v", bundle.ExportData)
	}
	project := bundle.ExportData.Projects[0]
	if project.Name != "Minimal Board" {
		t.Fatalf("project.Name = %q", project.Name)
	}
	if len(project.WorkflowColumns) != 2 {
		t.Fatalf("workflowColumns len = %d", len(project.WorkflowColumns))
	}
	assertValidWorkflowColors(t, project.WorkflowColumns)
	if len(project.Todos) != 1 {
		t.Fatalf("todos len = %d", len(project.Todos))
	}
	if project.Todos[0].Title != "Ship it" {
		t.Fatalf("todo title = %q", project.Todos[0].Title)
	}
	if project.Todos[0].Body != "Hello" {
		t.Fatalf("todo body = %q", project.Todos[0].Body)
	}
}

func TestBuildImportBundle_OneOpenListSynthesizesDoneColumn(t *testing.T) {
	raw := []byte(`{
		"id":"board-1a",
		"name":"Single List Board",
		"lists":[
			{"id":"list-open","name":"Inbox","pos":10,"closed":false},
			{"id":"list-closed","name":"Archive","pos":20,"closed":true}
		],
		"cards":[
			{"id":"card-open","name":"Open card","idList":"list-open","pos":10,"closed":false,"idLabels":[],"idMembers":[]},
			{"id":"card-closed","name":"Closed lane card","idList":"list-closed","pos":20,"closed":false,"idLabels":[],"idMembers":[]}
		]
	}`)

	bundle, err := BuildImportBundle(raw, fixedNow())
	if err != nil {
		t.Fatalf("BuildImportBundle: %v", err)
	}
	if len(bundle.Preview.HardErrors) != 0 {
		t.Fatalf("unexpected hard errors: %v", bundle.Preview.HardErrors)
	}
	if bundle.Preview.DetectedDoneColumn != "Done" {
		t.Fatalf("detected done column = %q", bundle.Preview.DetectedDoneColumn)
	}
	if !strings.Contains(bundle.Preview.DetectedDoneReason, "only one open list") {
		t.Fatalf("detected done reason = %q", bundle.Preview.DetectedDoneReason)
	}
	if !containsText(bundle.Preview.Warnings, "closed Trello lists") {
		t.Fatalf("expected closed-list warning, got %v", bundle.Preview.Warnings)
	}

	project := bundle.ExportData.Projects[0]
	if len(project.WorkflowColumns) != 2 {
		t.Fatalf("expected exactly 2 workflow columns, got %d", len(project.WorkflowColumns))
	}
	assertValidWorkflowColors(t, project.WorkflowColumns)
	if project.WorkflowColumns[0].Name != "Inbox" || project.WorkflowColumns[0].IsDone {
		t.Fatalf("unexpected first column: %+v", project.WorkflowColumns[0])
	}
	if project.WorkflowColumns[1].Key != "done" || project.WorkflowColumns[1].Name != "Done" || !project.WorkflowColumns[1].IsDone {
		t.Fatalf("unexpected synthetic done column: %+v", project.WorkflowColumns[1])
	}

	statusByTitle := map[string]string{}
	for _, todo := range project.Todos {
		statusByTitle[todo.Title] = todo.Status
	}
	if statusByTitle["Open card"] != strings.ToUpper(project.WorkflowColumns[0].Key) {
		t.Fatalf("expected open card in real column %q, got %q", project.WorkflowColumns[0].Key, statusByTitle["Open card"])
	}
	if statusByTitle["[Closed List] Closed lane card"] != "DONE" {
		t.Fatalf("expected closed-list card in synthetic done column, got %q", statusByTitle["[Closed List] Closed lane card"])
	}
}

func TestBuildImportBundle_CardInClosedExistingListImportsIntoDoneColumn(t *testing.T) {
	raw := []byte(`{
		"id":"board-2",
		"name":"Closed List Board",
		"lists":[
			{"id":"list-open","name":"Doing","pos":10,"closed":false},
			{"id":"list-done","name":"Done","pos":20,"closed":false},
			{"id":"list-closed","name":"Old Lane","pos":30,"closed":true}
		],
		"cards":[
			{"id":"card-open","name":"Open card","idList":"list-open","pos":10,"closed":false,"idLabels":[],"idMembers":[]},
			{"id":"card-closed-list","name":"Closed list card","idList":"list-closed","pos":20,"closed":false,"idLabels":[],"idMembers":[]}
		]
	}`)

	bundle, err := BuildImportBundle(raw, fixedNow())
	if err != nil {
		t.Fatalf("BuildImportBundle: %v", err)
	}
	if len(bundle.Preview.HardErrors) != 0 {
		t.Fatalf("unexpected hard errors: %v", bundle.Preview.HardErrors)
	}
	if !containsText(bundle.Preview.Warnings, "closed Trello lists") {
		t.Fatalf("expected closed-list warning, got %v", bundle.Preview.Warnings)
	}

	project := bundle.ExportData.Projects[0]
	var closedListTodoTitle string
	var closedListTodoStatus string
	var openCardTitle string
	for _, todo := range project.Todos {
		if strings.Contains(todo.Title, "Closed list card") {
			closedListTodoTitle = todo.Title
			closedListTodoStatus = todo.Status
		}
		if todo.Title == "Open card" {
			openCardTitle = todo.Title
		}
	}
	if openCardTitle != "Open card" {
		t.Fatalf("expected open-list card to import normally, got %q", openCardTitle)
	}
	if !strings.HasPrefix(closedListTodoTitle, "[Closed List] ") {
		t.Fatalf("expected closed-list marker, got %q", closedListTodoTitle)
	}
	if closedListTodoStatus != "DONE" {
		t.Fatalf("expected closed-list card to land in done column, got %q", closedListTodoStatus)
	}
}

func TestBuildImportBundle_CardReferencingMissingListIsHardError(t *testing.T) {
	raw := []byte(`{
		"id":"board-3",
		"name":"Broken Board",
		"lists":[
			{"id":"list-open","name":"Doing","pos":10,"closed":false}
		],
		"cards":[
			{"id":"card-missing","name":"Broken card","idList":"missing-list","pos":10,"closed":false,"idLabels":[],"idMembers":[]}
		]
	}`)

	bundle, err := BuildImportBundle(raw, fixedNow())
	if err != nil {
		t.Fatalf("BuildImportBundle: %v", err)
	}
	if !containsText(bundle.Preview.HardErrors, "missing Trello list") {
		t.Fatalf("expected missing-list hard error, got %v", bundle.Preview.HardErrors)
	}
	if bundle.ExportData != nil {
		t.Fatalf("expected no export data on hard error")
	}
}

func TestBuildImportBundle_FailsWhenTooManyOpenLists(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"id":"board-4","name":"Too Many","lists":[`)
	for i := 0; i < 13; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"id":"list-`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`","name":"List `)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`","pos":`)
		b.WriteString(strconv.Itoa((i + 1) * 10))
		b.WriteString(`,"closed":false}`)
	}
	b.WriteString(`],"cards":[]}`)

	bundle, err := BuildImportBundle([]byte(b.String()), fixedNow())
	if err != nil {
		t.Fatalf("BuildImportBundle: %v", err)
	}
	if !containsText(bundle.Preview.HardErrors, "more than 12 open lists") {
		t.Fatalf("expected max-list hard error, got %v", bundle.Preview.HardErrors)
	}
}

func TestBuildImportBundle_ComprehensiveFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "real_board_fixture.json"))
	if err != nil {
		t.Fatalf("ReadFile fixture: %v", err)
	}

	bundle, err := BuildImportBundle(raw, fixedNow())
	if err != nil {
		t.Fatalf("BuildImportBundle: %v", err)
	}
	if len(bundle.Preview.HardErrors) != 0 {
		t.Fatalf("unexpected hard errors: %v", bundle.Preview.HardErrors)
	}
	if bundle.Preview.BoardName != "Fixture Board" {
		t.Fatalf("boardName = %q", bundle.Preview.BoardName)
	}
	if bundle.Preview.Checklists != 1 || bundle.Preview.ChecklistItems != 2 {
		t.Fatalf("unexpected checklist counts: %+v", bundle.Preview)
	}
	if bundle.Preview.CommentCardActions != 1 {
		t.Fatalf("commentCardActions = %d", bundle.Preview.CommentCardActions)
	}
	if bundle.Preview.Attachments != 2 {
		t.Fatalf("attachments = %d", bundle.Preview.Attachments)
	}
	if bundle.Preview.CustomFieldItems != 2 {
		t.Fatalf("customFieldItems = %d", bundle.Preview.CustomFieldItems)
	}
	if bundle.Preview.MembersReferenced != 2 {
		t.Fatalf("membersReferenced = %d", bundle.Preview.MembersReferenced)
	}

	project := bundle.ExportData.Projects[0]
	assertValidWorkflowColors(t, project.WorkflowColumns)
	if len(project.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(project.Tags))
	}
	if project.Tags[0].Name != "Feature" && project.Tags[1].Name != "Feature" {
		t.Fatalf("expected Feature tag, got %+v", project.Tags)
	}
	if !containsTagName(project.Tags, "Trello green label") {
		t.Fatalf("expected fallback label name, got %+v", project.Tags)
	}

	var openTodoBody string
	var archivedTodo *store.TodoExport
	for _, todo := range project.Todos {
		switch {
		case todo.Title == "Real open card":
			openTodoBody = todo.Body
		case strings.Contains(todo.Title, "Archived from closed list"):
			archivedTodo = &todo
		}
	}
	if !strings.Contains(openTodoBody, "## Trello dates") {
		t.Fatalf("expected Trello dates section, body=%q", openTodoBody)
	}
	if !strings.Contains(openTodoBody, "## Trello members") {
		t.Fatalf("expected Trello members section, body=%q", openTodoBody)
	}
	if !strings.Contains(openTodoBody, "## Trello checklists") || !strings.Contains(openTodoBody, "[x] Done item") || !strings.Contains(openTodoBody, "[ ] Todo item") {
		t.Fatalf("expected checklist rendering, body=%q", openTodoBody)
	}
	if !strings.Contains(openTodoBody, "## Imported Trello comments") || !strings.Contains(openTodoBody, "Latest comment") {
		t.Fatalf("expected comments section, body=%q", openTodoBody)
	}
	if !strings.Contains(openTodoBody, "## Trello attachments") || !strings.Contains(openTodoBody, "https://example.com/spec") {
		t.Fatalf("expected attachments section, body=%q", openTodoBody)
	}
	if !strings.Contains(openTodoBody, "## Trello custom fields") || !strings.Contains(openTodoBody, "Priority: High") || !strings.Contains(openTodoBody, "Estimate: 8") {
		t.Fatalf("expected custom fields section, body=%q", openTodoBody)
	}
	if archivedTodo == nil || !strings.HasPrefix(archivedTodo.Title, "[Closed List] ") {
		t.Fatalf("expected only the closed-list marker, got %+v", archivedTodo)
	}
	if archivedTodo.ArchivedAt == nil || !archivedTodo.ArchivedAtPresent {
		t.Fatalf("expected closed Trello card to use first-class archival, got %+v", archivedTodo)
	}

	var projectMeta map[string]any
	if err := json.Unmarshal([]byte(bundle.ProjectImportMetadata), &projectMeta); err != nil {
		t.Fatalf("Unmarshal project metadata: %v", err)
	}
	if projectMeta["source"] != "trello" {
		t.Fatalf("project metadata source = %#v", projectMeta["source"])
	}
	if projectMeta["trelloBoardDesc"] != "Fixture desc" {
		t.Fatalf("project metadata desc = %#v", projectMeta["trelloBoardDesc"])
	}

	var todoMeta map[string]any
	if err := json.Unmarshal([]byte(bundle.TodoImportMetadataByLocalID[1]), &todoMeta); err != nil {
		t.Fatalf("Unmarshal todo metadata: %v", err)
	}
	if todoMeta["trelloCardId"] != "card1" {
		t.Fatalf("todo metadata card id = %#v", todoMeta["trelloCardId"])
	}
	attachments, ok := todoMeta["attachments"].([]any)
	if !ok || len(attachments) != 2 {
		t.Fatalf("todo metadata attachments = %#v", todoMeta["attachments"])
	}
	customFieldItems, ok := todoMeta["customFieldItems"].([]any)
	if !ok || len(customFieldItems) != 2 {
		t.Fatalf("todo metadata customFieldItems = %#v", todoMeta["customFieldItems"])
	}
}

func TestBuildImportBundle_MalformedJSON(t *testing.T) {
	if _, err := BuildImportBundle([]byte(`{"id":`), fixedNow()); err == nil {
		t.Fatal("expected JSON parse error")
	}
}

func containsText(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func assertValidWorkflowColors(t *testing.T, columns []store.WorkflowColumnExport) {
	t.Helper()
	hexColor := regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	for _, column := range columns {
		if column.Color == "" {
			t.Fatalf("workflow column %q has empty color", column.Key)
		}
		if !hexColor.MatchString(column.Color) {
			t.Fatalf("workflow column %q has invalid color %q", column.Key, column.Color)
		}
	}
}

func TestTrelloLabelColorHex(t *testing.T) {
	cases := map[string]string{
		"green":       "#61BD4F",
		"red":         "#EB5A46",
		"green_light": "#BAF3DB",
		"green_dark":  "#4BCE97",
		"blue_light":  "#CCE0FF",
		"pink_dark":   "#E774BB",
	}
	for input, want := range cases {
		got := trelloLabelColorHex(input)
		if got == nil || *got != want {
			t.Fatalf("trelloLabelColorHex(%q) = %v, want %q", input, got, want)
		}
	}
	if got := trelloLabelColorHex(""); got != nil {
		t.Fatalf("trelloLabelColorHex(empty) = %v, want nil", *got)
	}
	if got := trelloLabelColorHex("unknown"); got != nil {
		t.Fatalf("trelloLabelColorHex(unknown) = %v, want nil", *got)
	}
}

func containsTagName(tags []store.TagExport, want string) bool {
	for _, tag := range tags {
		if tag.Name == want {
			return true
		}
	}
	return false
}

// TestBuildImportBundle_CardClosureIsFirstClassArchival pins the post-A1 Trello contract:
// a closed card becomes an archived Scrumboy story rather than a card with an "[Archived]"
// title marker, and card closure stays a separate axis from list closure. The preview
// warning must describe what actually happens, including the import-time approximation
// Trello forces on us (its export carries no per-card archive timestamp).
func TestBuildImportBundle_CardClosureIsFirstClassArchival(t *testing.T) {
	now := fixedNow()
	raw := []byte(`{
		"id":"board-closure",
		"name":"Closure Axes",
		"lists":[
			{"id":"list-open","name":"Doing","pos":10,"closed":false},
			{"id":"list-done","name":"Done","pos":20,"closed":false},
			{"id":"list-closed","name":"Retired","pos":30,"closed":true}
		],
		"cards":[
			{"id":"card-open","name":"Open card","idList":"list-open","pos":10,"closed":false,"idLabels":[],"idMembers":[]},
			{"id":"card-closed","name":"Closed card","idList":"list-open","pos":20,"closed":true,"idLabels":[],"idMembers":[]},
			{"id":"card-in-closed-list","name":"Card in closed list","idList":"list-closed","pos":30,"closed":false,"idLabels":[],"idMembers":[]}
		]
	}`)
	bundle, err := BuildImportBundle(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	byTitle := make(map[string]store.TodoExport)
	for _, todo := range bundle.ExportData.Projects[0].Todos {
		byTitle[todo.Title] = todo
	}

	// An open card in an open list is never archived, and always declares the field
	// so a 1.2 export represents its active state explicitly.
	open, ok := byTitle["Open card"]
	if !ok {
		t.Fatalf("open card missing; titles=%v", byTitle)
	}
	if open.ArchivedAt != nil || !open.ArchivedAtPresent {
		t.Fatalf("open card archivedAt=%v present=%v want nil/true", open.ArchivedAt, open.ArchivedAtPresent)
	}

	// Card closure: first-class archival, title untouched.
	closed, ok := byTitle["Closed card"]
	if !ok {
		t.Fatalf("closed card missing; titles=%v", byTitle)
	}
	if closed.ArchivedAt == nil || *closed.ArchivedAt != now.UnixMilli() {
		t.Fatalf("closed card archivedAt=%v want %d", closed.ArchivedAt, now.UnixMilli())
	}
	if !strings.Contains(closed.Body, "- Archived in Trello: true") {
		t.Fatalf("closed card body lost its archival note: %q", closed.Body)
	}

	// List closure is the other axis: title marker, remapped column, no archival.
	inClosedList, ok := byTitle["[Closed List] Card in closed list"]
	if !ok {
		t.Fatalf("closed-list card missing; titles=%v", byTitle)
	}
	if inClosedList.ArchivedAt != nil {
		t.Fatalf("a closed list must not archive its open cards: %+v", inClosedList)
	}

	for title := range byTitle {
		if strings.Contains(title, "[Archived]") {
			t.Fatalf("the legacy [Archived] title marker is back: %q", title)
		}
	}

	var warning string
	for _, w := range bundle.Preview.Warnings {
		if strings.Contains(w, "Archived Trello cards") {
			warning = w
		}
		if strings.Contains(w, "archived marker") {
			t.Fatalf("stale warning still advertises a title marker: %q", w)
		}
	}
	if !strings.Contains(warning, "not cards with a title marker") || !strings.Contains(warning, "import time") {
		t.Fatalf("archival warning does not describe the shipped behaviour: %q", warning)
	}
}
