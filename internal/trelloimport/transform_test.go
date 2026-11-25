package trelloimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

func TestBuildImportBundlePreservesUnicodeLabelNames(t *testing.T) {
	raw := []byte(`{
		"id":"unicode-board","name":"Unicode Board",
		"lists":[{"id":"todo","name":"Todo","pos":10,"closed":false}],
		"labels":[
			{"id":"cn","name":"erp系统流程","color":"green"},
			{"id":"jp","name":"つながるひろが","color":"blue"},
			{"id":"accent","name":"Café","color":"red"}
		],
		"cards":[{"id":"card","name":"Unicode labels","idList":"todo","pos":10,"closed":false,"idLabels":["cn","jp","accent"],"idMembers":[]}]
	}`)
	bundle, err := BuildImportBundle(raw, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Preview.HardErrors) != 0 || bundle.ExportData == nil {
		t.Fatalf("Unicode Trello preview = %+v", bundle.Preview)
	}
	project := bundle.ExportData.Projects[0]
	for _, name := range []string{"erp系统流程", "つながるひろが", "Café"} {
		if !containsTagName(project.Tags, name) {
			t.Fatalf("missing Unicode label %q in %+v", name, project.Tags)
		}
	}
	if len(project.Todos) != 1 || len(project.Todos[0].Tags) != 3 {
		t.Fatalf("Unicode card tags = %+v", project.Todos)
	}
}

func TestBuildImportBundleMakesLabelNamesValidTags(t *testing.T) {
	raw := []byte(`{
		"id":"punct-board","name":"Punctuation Board",
		"lists":[{"id":"todo","name":"Todo","pos":10,"closed":false}],
		"labels":[
			{"id":"a","name":"Won't fix","color":"red"},
			{"id":"b","name":"Bug/Fix","color":"blue"},
			{"id":"c","name":"v2.0","color":"green"},
			{"id":"d","name":"","color":"green"},
			{"id":"e","name":"","color":"green"}
		],
		"cards":[{"id":"card","name":"Labels","idList":"todo","pos":10,"closed":false,"idLabels":["a","b","c","d","e"],"idMembers":[]}]
	}`)
	bundle, err := BuildImportBundle(raw, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range bundle.ExportData.Projects[0].Tags {
		if store.CanonicalizeTag(tag.Name) == "" {
			t.Fatalf("label imported as invalid tag %q", tag.Name)
		}
	}
	if len(bundle.ExportData.Projects[0].Todos[0].Tags) != 5 {
		t.Fatalf("card tags = %+v", bundle.ExportData.Projects[0].Todos[0].Tags)
	}
}

func TestLabelTagNameSanitization(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "slash", input: "Bug/Fix", want: "Bug Fix"},
		{name: "ASCII apostrophe", input: "Won't fix", want: "Wont fix"},
		{name: "typographic apostrophe", input: "Won’t fix", want: "Wont fix"},
		{name: "period", input: "v2.0", want: "v2 0"},
		{name: "trailing punctuation", input: "urgent!", want: "urgent"},
		{name: "mixed punctuation", input: "Q&A (client)", want: "Q A client"},
		{name: "empty", input: "", want: ""},
		{name: "whitespace", input: " \t\u2003 ", want: ""},
		{name: "hyphens", input: "---", want: ""},
		{name: "punctuation", input: "!@#$", want: ""},
		{name: "emoji", input: "🚀", want: ""},
		{name: "standalone mark", input: "\u0301", want: ""},
		{name: "mark after separator", input: "a-\u0301b", want: "a-b"},
		{name: "format character", input: "alpha\u200dbeta", want: "alpha beta"},
		{name: "valid combining mark", input: "Cafe\u0301", want: "Cafe\u0301"},
		{name: "Chinese", input: "erp系统流程", want: "erp系统流程"},
		{name: "Japanese", input: "つながるひろが", want: "つながるひろが"},
		{name: "Arabic", input: "إصلاح خطأ", want: "إصلاح خطأ"},
		{name: "Devanagari", input: "नमस्ते दुनिया", want: "नमस्ते दुनिया"},
		{name: "accented Latin", input: "Café", want: "Café"},
		{name: "exact limit", input: strings.Repeat("界", store.MaxTagRunes), want: strings.Repeat("界", store.MaxTagRunes)},
		{name: "over limit", input: strings.Repeat("界", store.MaxTagRunes+1), want: strings.Repeat("界", store.MaxTagRunes)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := labelTagName(tc.input); got != tc.want {
				t.Fatalf("labelTagName(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if tc.want != "" && store.CanonicalizeTag(tc.want) == "" {
				t.Fatalf("expected output is not canonicalizable: %q", tc.want)
			}
		})
	}

	rawLongButCanonicalShort := "Alpha" + strings.Repeat(" ", store.MaxTagRunes+10) + "Beta"
	if got := labelTagName(rawLongButCanonicalShort); got != rawLongButCanonicalShort {
		t.Fatalf("valid raw name was rewritten: %q", got)
	}
}

func TestPreferredLabelTagNameSanitizesColorFallbacks(t *testing.T) {
	tests := []struct {
		name  string
		label Label
		want  string
	}{
		{name: "green", label: Label{Color: "green"}, want: "Trello green label"},
		{name: "duplicate capable green", label: Label{Color: "green"}, want: "Trello green label"},
		{name: "green light", label: Label{Color: "green_light"}, want: "Trello green light label"},
		{name: "blue dark", label: Label{Color: "blue_dark"}, want: "Trello blue dark label"},
		{name: "red light", label: Label{Color: "red_light"}, want: "Trello red light label"},
		{name: "purple dark", label: Label{Color: "purple_dark"}, want: "Trello purple dark label"},
		{name: "missing", label: Label{}, want: "Untitled Trello label"},
		{name: "none", label: Label{Color: "none"}, want: "Untitled Trello label"},
		{name: "unknown", label: Label{Color: "chartreuse"}, want: "Trello chartreuse label"},
		{name: "malformed", label: Label{Color: "!!!"}, want: "Trello label"},
		{name: "format characters", label: Label{Color: "red\u200dlight"}, want: "Trello red light label"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, originalValid := preferredLabelTagName(tc.label)
			if originalValid {
				t.Fatal("fallback unexpectedly treated as an original valid name")
			}
			if got != tc.want {
				t.Fatalf("preferredLabelTagName(%+v) = %q, want %q", tc.label, got, tc.want)
			}
			if store.CanonicalizeTag(got) == "" {
				t.Fatalf("fallback is not canonicalizable: %q", got)
			}
		})
	}

	longFallback, originalValid := preferredLabelTagName(Label{Color: strings.Repeat("x", 100)})
	if originalValid || store.CanonicalizeTag(longFallback) == "" || len([]rune(store.CanonicalizeTag(longFallback))) != store.MaxTagRunes {
		t.Fatalf("long fallback = %q, valid original=%v", longFallback, originalValid)
	}
}

func TestBuildTagExportsAllocatesUniqueCanonicalNames(t *testing.T) {
	labels := map[string]Label{
		"bug-blue":        {ID: "bug-blue", Name: "Bug", Color: "blue"},
		"bug-red":         {ID: "bug-red", Name: "Bug", Color: "red"},
		"bug-two":         {ID: "bug-two", Name: "Bug 2", Color: "green"},
		"punctuation":     {ID: "punctuation", Name: "Bug/Fix", Color: "orange"},
		"spaced":          {ID: "spaced", Name: "Bug Fix", Color: "yellow"},
		"hyphenated":      {ID: "hyphenated", Name: "bug-fix", Color: "purple"},
		"nfc":             {ID: "nfc", Name: "Café", Color: "lime"},
		"nfd":             {ID: "nfd", Name: "Cafe\u0301", Color: "pink"},
		"fallback-named":  {ID: "fallback-named", Name: "Trello green light label", Color: "black"},
		"fallback-one":    {ID: "fallback-one", Color: "green_light"},
		"fallback-two":    {ID: "fallback-two", Color: "green_light"},
		"long-one":        {ID: "long-one", Name: strings.Repeat("a", store.MaxTagRunes) + "x", Color: "sky"},
		"long-two":        {ID: "long-two", Name: strings.Repeat("a", store.MaxTagRunes) + "y", Color: "blue_dark"},
		"long-valid-one":  {ID: "long-valid-one", Name: strings.Repeat("界", store.MaxTagRunes), Color: "yellow_dark"},
		"long-valid-two":  {ID: "long-valid-two", Name: strings.Repeat("界", store.MaxTagRunes), Color: "orange_dark"},
		"case-upper":      {ID: "case-upper", Name: "RELEASE", Color: "red_light"},
		"case-lower":      {ID: "case-lower", Name: "release", Color: "blue_light"},
		"raw-long-valid":  {ID: "raw-long-valid", Name: "Alpha" + strings.Repeat(" ", 40) + "Beta", Color: "red_dark"},
		"standalone-mark": {ID: "standalone-mark", Name: "\u0301", Color: "purple_dark"},
	}

	nameByID, tags, adjusted, err := buildTagExports(labels)
	if err != nil {
		t.Fatal(err)
	}
	if len(nameByID) != len(labels) || len(tags) != len(labels) {
		t.Fatalf("labels=%d names=%d tags=%d", len(labels), len(nameByID), len(tags))
	}
	if adjusted == 0 {
		t.Fatal("expected transformed labels to be counted")
	}
	if nameByID["bug-two"] != "Bug 2" {
		t.Fatalf("genuine suffix name was not preserved: %q", nameByID["bug-two"])
	}
	if store.CanonicalizeTag(nameByID["bug-red"]) == "bug-2" || store.CanonicalizeTag(nameByID["bug-blue"]) == "bug-2" {
		t.Fatalf("generated duplicate stole genuine Bug 2: %+v", nameByID)
	}
	if nameByID["fallback-named"] != "Trello green light label" {
		t.Fatalf("genuine fallback-shaped name was not preserved: %q", nameByID["fallback-named"])
	}
	if nameByID["raw-long-valid"] != labels["raw-long-valid"].Name {
		t.Fatalf("raw long but canonically valid name was rewritten: %q", nameByID["raw-long-valid"])
	}

	tagByCanonical := make(map[string]store.TagExport, len(tags))
	for _, tag := range tags {
		canonical := store.CanonicalizeTag(tag.Name)
		if canonical == "" {
			t.Fatalf("invalid exported tag %q", tag.Name)
		}
		if previous, exists := tagByCanonical[canonical]; exists {
			t.Fatalf("canonical collision %q between %+v and %+v", canonical, previous, tag)
		}
		tagByCanonical[canonical] = tag
	}
	for id, label := range labels {
		canonical := store.CanonicalizeTag(nameByID[id])
		tag, exists := tagByCanonical[canonical]
		if !exists {
			t.Fatalf("label %q has no exported tag for %q", id, canonical)
		}
		wantColor := trelloLabelColorHex(label.Color)
		if !reflect.DeepEqual(tag.Color, wantColor) {
			t.Fatalf("label %q color = %v, want %v", id, tag.Color, wantColor)
		}
	}

	reordered := make(map[string]Label, len(labels))
	ids := make([]string, 0, len(labels))
	for id := range labels {
		ids = append(ids, id)
	}
	for i := len(ids) - 1; i >= 0; i-- {
		reordered[ids[i]] = labels[ids[i]]
	}
	secondNames, secondTags, secondAdjusted, err := buildTagExports(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(nameByID, secondNames) || !reflect.DeepEqual(tags, secondTags) || adjusted != secondAdjusted {
		t.Fatalf("map iteration changed output:\nfirst=%+v\nsecond=%+v", nameByID, secondNames)
	}
}

func TestBuildTagExportsSuffixesAcrossDigitBoundary(t *testing.T) {
	labels := make(map[string]Label)
	for i := 1; i <= 12; i++ {
		id := "repeat-" + strconv.Itoa(i)
		labels[id] = Label{ID: id, Name: "Repeat"}
	}
	nameByID, tags, adjusted, err := buildTagExports(labels)
	if err != nil {
		t.Fatal(err)
	}
	if len(nameByID) != 12 || len(tags) != 12 || adjusted != 11 {
		t.Fatalf("names=%d tags=%d adjusted=%d", len(nameByID), len(tags), adjusted)
	}
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		canonical := store.CanonicalizeTag(tag.Name)
		if canonical == "" {
			t.Fatalf("invalid tag %q", tag.Name)
		}
		seen[canonical] = struct{}{}
	}
	for _, want := range []string{"repeat", "repeat-9", "repeat-10", "repeat-11", "repeat-12"} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("missing %q in %v", want, seen)
		}
	}
}

func TestTransformBoardLabelAssociationsAndWarnings(t *testing.T) {
	board := &Board{
		ID:    "labels",
		Name:  "Labels",
		Lists: []List{{ID: "todo", Name: "Todo", Pos: 10}},
		Labels: []Label{
			{ID: "punctuation", Name: "Bug/Fix", Color: "blue"},
			{ID: "bug-one", Name: "Bug", Color: "red"},
			{ID: "bug-two", Name: "Bug", Color: "green"},
		},
		Cards: []Card{
			{ID: "one", Name: "One", IDList: "todo", Pos: 10, IDLabels: []string{"punctuation", "bug-one", "punctuation"}},
			{ID: "two", Name: "Two", IDList: "todo", Pos: 20, IDLabels: []string{"bug-two"}},
		},
	}
	bundle, err := TransformBoard(board, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	if bundle.ExportData == nil || len(bundle.Preview.HardErrors) != 0 {
		t.Fatalf("unexpected preview: %+v", bundle.Preview)
	}
	var adjustmentWarning string
	for _, warning := range bundle.Preview.Warnings {
		if strings.Contains(warning, "label names will be adjusted") {
			adjustmentWarning = warning
		}
	}
	if !strings.Contains(adjustmentWarning, "(2 adjusted)") {
		t.Fatalf("adjustment warning = %q", adjustmentWarning)
	}
	if !strings.Contains(bundle.ProjectImportMetadata, adjustmentWarning) {
		t.Fatalf("project metadata omitted warning: %s", bundle.ProjectImportMetadata)
	}
	project := bundle.ExportData.Projects[0]
	if len(project.Tags) != 3 || len(project.Todos) != 2 {
		t.Fatalf("project = %+v", project)
	}
	if len(project.Todos[0].Tags) != 2 {
		t.Fatalf("duplicate source reference created duplicate card tags: %+v", project.Todos[0].Tags)
	}
	if store.CanonicalizeTag(project.Todos[0].Tags[0]) == store.CanonicalizeTag(project.Todos[0].Tags[1]) {
		t.Fatalf("distinct card labels collapsed: %+v", project.Todos[0].Tags)
	}

	validOnly := &Board{
		ID:     "valid-labels",
		Name:   "Valid Labels",
		Lists:  []List{{ID: "todo", Name: "Todo", Pos: 10}},
		Labels: []Label{{ID: "unicode", Name: "नमस्ते दुनिया"}, {ID: "whitespace", Name: "Alpha   Beta"}},
	}
	validBundle, err := TransformBoard(validOnly, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	for _, warning := range validBundle.Preview.Warnings {
		if strings.Contains(warning, "label names will be adjusted") {
			t.Fatalf("valid labels produced adjustment warning: %q", warning)
		}
	}
	validTags := validBundle.ExportData.Projects[0].Tags
	if !containsTagName(validTags, "नमस्ते दुनिया") || !containsTagName(validTags, "Alpha   Beta") {
		t.Fatalf("valid display names were not preserved: %+v", validBundle.ExportData.Projects[0].Tags)
	}
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
