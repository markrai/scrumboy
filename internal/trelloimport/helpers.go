package trelloimport

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"scrumboy/internal/store"
)

const syntheticDoneListID = "__trello_import_done__"

var trelloWorkflowColors = []string{
	"#94a3b8",
	"#f59e0b",
	"#22c55e",
	"#3b82f6",
	"#8b5cf6",
	"#ec4899",
	"#14b8a6",
	"#f97316",
	"#84cc16",
	"#06b6d4",
	"#a855f7",
}

func sortLists(lists []List) {
	sort.SliceStable(lists, func(i, j int) bool {
		if lists[i].Pos != lists[j].Pos {
			return lists[i].Pos < lists[j].Pos
		}
		return lists[i].ID < lists[j].ID
	})
}

func detectDoneList(openLists []List) (List, string) {
	for i := len(openLists) - 1; i >= 0; i-- {
		if isCommonDoneListName(openLists[i].Name) {
			return openLists[i], "Matched a common done list name."
		}
	}
	if len(openLists) == 0 {
		return List{}, ""
	}
	return openLists[len(openLists)-1], "Used the rightmost open list because no common done list name matched."
}

func synthesizedDoneList() (List, string) {
	return List{
			ID:   syntheticDoneListID,
			Name: "Done",
			Pos:  1_000_000,
		},
		"Synthesized a Done column because the Trello board has only one open list."
}

func isCommonDoneListName(name string) bool {
	normalized := normalizeDoneName(name)
	switch normalized {
	case "done", "complete", "completed", "shipped", "finished":
		return true
	default:
		return false
	}
}

func normalizeDoneName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func workflowKeyFromName(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	key = strings.Join(strings.Fields(key), "_")
	var b strings.Builder
	lastUnderscore := false
	for _, r := range key {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastUnderscore = false
		case r == '_':
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	key = strings.Trim(b.String(), "_")
	if len(key) > maxWorkflowKeyLen {
		key = strings.Trim(key[:maxWorkflowKeyLen], "_")
	}
	if key == "" {
		return "lane"
	}
	return key
}

func uniqueWorkflowKey(baseKey string, used map[string]struct{}) (string, error) {
	if _, exists := used[baseKey]; !exists && isValidWorkflowKey(baseKey) {
		return baseKey, nil
	}
	for i := 2; i <= 1000; i++ {
		suffix := fmt.Sprintf("_%d", i)
		candidate := baseKey
		if len(candidate)+len(suffix) > maxWorkflowKeyLen {
			candidate = strings.Trim(candidate[:maxWorkflowKeyLen-len(suffix)], "_")
		}
		if candidate == "" {
			candidate = "lane"
		}
		candidate += suffix
		if _, exists := used[candidate]; exists || !isValidWorkflowKey(candidate) {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("could not generate unique workflow key")
}

func isValidWorkflowKey(key string) bool {
	if key == "" || len(key) > maxWorkflowKeyLen {
		return false
	}
	for i, r := range key {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
			if i == 0 || i == len(key)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func workflowColumnColor(position int, isDone bool) string {
	if isDone {
		return "#ef4444"
	}
	return trelloWorkflowColors[position%len(trelloWorkflowColors)]
}

func buildTagExports(labelByID map[string]Label) (map[string]string, []store.TagExport) {
	type labelEntry struct {
		ID    string
		Label Label
	}
	entries := make([]labelEntry, 0, len(labelByID))
	for id, label := range labelByID {
		entries = append(entries, labelEntry{ID: id, Label: label})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(entries[i].Label.Name))
		right := strings.ToLower(strings.TrimSpace(entries[j].Label.Name))
		if left != right {
			return left < right
		}
		if entries[i].Label.Color != entries[j].Label.Color {
			return entries[i].Label.Color < entries[j].Label.Color
		}
		return entries[i].ID < entries[j].ID
	})

	usedNames := map[string]int{}
	labelNameByID := make(map[string]string, len(entries))
	tagExports := make([]store.TagExport, 0, len(entries))
	for _, entry := range entries {
		baseName := labelTagName(entry.Label.Name)
		if baseName == "" {
			if strings.TrimSpace(entry.Label.Color) != "" && strings.TrimSpace(entry.Label.Color) != "none" {
				baseName = "Trello " + strings.ToLower(strings.TrimSpace(entry.Label.Color)) + " label"
			} else {
				baseName = "Untitled Trello label"
			}
		}
		finalName := baseName
		key := store.CanonicalizeTag(baseName)
		usedNames[key]++
		if n := usedNames[key]; n > 1 {
			suffix := fmt.Sprintf(" %d", n)
			finalName = truncateRunes(baseName, tagNameMaxRunes-len(suffix)) + suffix
		}
		labelNameByID[entry.ID] = finalName
		color := trelloLabelColorHex(entry.Label.Color)
		tagExports = append(tagExports, store.TagExport{Name: finalName, Color: color})
	}
	return labelNameByID, tagExports
}

// tagNameMaxRunes matches the store's tag length limit.
const tagNameMaxRunes = 32

// labelTagName turns a Trello label name into a valid tag name: tags allow only
// letters, numbers, spaces and hyphens, so "Won't fix" becomes "Wont fix" and
// "Bug/Fix" becomes "Bug Fix".
func labelTagName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '\'' || r == '’':
		case unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return truncateRunes(strings.Join(strings.Fields(b.String()), " "), tagNameMaxRunes)
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return strings.TrimRight(string(runes[:n]), " -")
}

func trelloLabelColorHex(color string) *string {
	switch strings.ToLower(strings.TrimSpace(color)) {
	case "green":
		return stringPtr("#61BD4F")
	case "green_light":
		return stringPtr("#BAF3DB")
	case "green_dark":
		return stringPtr("#4BCE97")
	case "yellow":
		return stringPtr("#F2D600")
	case "yellow_light":
		return stringPtr("#FCE29C")
	case "yellow_dark":
		return stringPtr("#E2B203")
	case "orange":
		return stringPtr("#FF9F1A")
	case "orange_light":
		return stringPtr("#FDD0B5")
	case "orange_dark":
		return stringPtr("#F38A3F")
	case "red":
		return stringPtr("#EB5A46")
	case "red_light":
		return stringPtr("#FFD5D2")
	case "red_dark":
		return stringPtr("#F87168")
	case "purple":
		return stringPtr("#C377E0")
	case "purple_light":
		return stringPtr("#DFC0FF")
	case "purple_dark":
		return stringPtr("#9F8FEF")
	case "blue":
		return stringPtr("#0079BF")
	case "blue_light":
		return stringPtr("#CCE0FF")
	case "blue_dark":
		return stringPtr("#579DFF")
	case "sky":
		return stringPtr("#00C2E0")
	case "sky_light":
		return stringPtr("#C6EDFB")
	case "sky_dark":
		return stringPtr("#6CC3E0")
	case "lime":
		return stringPtr("#51E898")
	case "lime_light":
		return stringPtr("#D3F1A7")
	case "lime_dark":
		return stringPtr("#94C748")
	case "pink":
		return stringPtr("#FF78CB")
	case "pink_light":
		return stringPtr("#FDD0EC")
	case "pink_dark":
		return stringPtr("#E774BB")
	case "black":
		return stringPtr("#344563")
	case "black_light":
		return stringPtr("#8590A2")
	case "black_dark":
		return stringPtr("#626F86")
	default:
		return nil
	}
}

func buildTodoBody(
	card Card,
	list List,
	memberByID map[string]Member,
	checklistsByCardID map[string][]Checklist,
	commentsByCardID map[string][]Action,
	customFieldByID map[string]CustomFieldDefinition,
	customFieldOptionText map[string]string,
) string {
	sections := make([]string, 0, 7)
	desc := strings.TrimSpace(card.Desc)
	if desc != "" {
		sections = append(sections, desc)
	}

	dateLines := make([]string, 0, 2)
	if card.Start != nil && strings.TrimSpace(*card.Start) != "" {
		dateLines = append(dateLines, "- Start: "+strings.TrimSpace(*card.Start))
	}
	if card.Due != nil && strings.TrimSpace(*card.Due) != "" {
		dateLines = append(dateLines, "- Due: "+strings.TrimSpace(*card.Due))
	}
	appendBodySection(&sections, "## Trello dates", dateLines)

	memberLines := make([]string, 0, len(card.IDMembers))
	for _, memberID := range dedupeNonEmptyStrings(card.IDMembers) {
		memberLines = append(memberLines, "- "+memberDisplay(memberByID[memberID], memberID))
	}
	appendBodySection(&sections, "## Trello members", memberLines)

	checklistLines := make([]string, 0)
	for _, checklist := range orderedChecklistsForCard(card, checklistsByCardID[card.ID]) {
		checklistLines = append(checklistLines, "### "+fallbackChecklistName(checklist.Name))
		items := append([]CheckItem(nil), checklist.CheckItems...)
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Pos != items[j].Pos {
				return items[i].Pos < items[j].Pos
			}
			return items[i].Name < items[j].Name
		})
		for _, item := range items {
			box := "[ ]"
			if strings.EqualFold(strings.TrimSpace(item.State), "complete") {
				box = "[x]"
			}
			checklistLines = append(checklistLines, "- "+box+" "+strings.TrimSpace(item.Name))
		}
	}
	appendBodySection(&sections, "## Trello checklists", checklistLines)

	commentLines := make([]string, 0, len(commentsByCardID[card.ID]))
	for _, comment := range commentsByCardID[card.ID] {
		text := strings.TrimSpace(comment.Data.Text)
		if text == "" {
			continue
		}
		author := commentAuthorDisplay(comment, memberByID)
		when := strings.TrimSpace(comment.Date)
		if when != "" {
			commentLines = append(commentLines, "- "+when+" | "+author+": "+text)
			continue
		}
		commentLines = append(commentLines, "- "+author+": "+text)
	}
	appendBodySection(&sections, "## Imported Trello comments", commentLines)

	attachmentLines := make([]string, 0, len(card.Attachments))
	for _, attachment := range card.Attachments {
		name := strings.TrimSpace(attachment.Name)
		url := strings.TrimSpace(attachment.URL)
		switch {
		case name != "" && url != "":
			attachmentLines = append(attachmentLines, fmt.Sprintf("- [%s](%s)", name, url))
		case url != "":
			attachmentLines = append(attachmentLines, "- "+url)
		case name != "":
			attachmentLines = append(attachmentLines, "- "+name)
		}
	}
	appendBodySection(&sections, "## Trello attachments", attachmentLines)

	customFieldLines := make([]string, 0, len(card.CustomFieldItems))
	for _, item := range card.CustomFieldItems {
		field := customFieldByID[strings.TrimSpace(item.IDCustomField)]
		fieldName := strings.TrimSpace(field.Name)
		if fieldName == "" {
			fieldName = "Custom field " + strings.TrimSpace(item.IDCustomField)
		}
		value := customFieldDisplayValue(item, field, customFieldOptionText)
		customFieldLines = append(customFieldLines, "- "+fieldName+": "+value)
	}
	appendBodySection(&sections, "## Trello custom fields", customFieldLines)

	importNotes := make([]string, 0, 2)
	if list.Closed {
		importNotes = append(importNotes, "- Original Trello list: "+fallbackChecklistName(list.Name)+" (closed in Trello)")
	}
	if card.Closed {
		importNotes = append(importNotes, "- Archived in Trello: true")
	}
	appendBodySection(&sections, "## Trello import notes", importNotes)

	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func orderedChecklistsForCard(card Card, checklists []Checklist) []Checklist {
	if len(checklists) == 0 {
		return nil
	}
	if len(card.IDChecklists) == 0 {
		out := append([]Checklist(nil), checklists...)
		sort.SliceStable(out, func(i, j int) bool {
			left := strings.TrimSpace(out[i].Name)
			right := strings.TrimSpace(out[j].Name)
			if left != right {
				return left < right
			}
			return out[i].ID < out[j].ID
		})
		return out
	}
	byID := make(map[string]Checklist, len(checklists))
	for _, checklist := range checklists {
		byID[checklist.ID] = checklist
	}
	out := make([]Checklist, 0, len(checklists))
	seen := make(map[string]struct{}, len(checklists))
	for _, checklistID := range card.IDChecklists {
		checklistID = strings.TrimSpace(checklistID)
		if checklistID == "" {
			continue
		}
		checklist, ok := byID[checklistID]
		if !ok {
			continue
		}
		out = append(out, checklist)
		seen[checklistID] = struct{}{}
	}
	for _, checklist := range checklists {
		if _, ok := seen[checklist.ID]; ok {
			continue
		}
		out = append(out, checklist)
	}
	return out
}

func customFieldDisplayValue(item CustomFieldItem, field CustomFieldDefinition, optionText map[string]string) string {
	if idValue := strings.TrimSpace(item.IDValue); idValue != "" {
		if text := strings.TrimSpace(optionText[idValue]); text != "" {
			return text
		}
		return idValue
	}
	value := firstNonEmpty(
		strings.TrimSpace(item.Value.Text),
		strings.TrimSpace(item.Value.Number),
		strings.TrimSpace(item.Value.Date),
		strings.TrimSpace(item.Value.Checked),
	)
	if value != "" {
		return value
	}
	if strings.TrimSpace(field.Type) != "" {
		return "(empty " + strings.TrimSpace(field.Type) + ")"
	}
	return "(empty)"
}

func appendBodySection(sections *[]string, heading string, lines []string) {
	if len(lines) == 0 {
		return
	}
	block := heading + "\n" + strings.Join(lines, "\n")
	*sections = append(*sections, block)
}

func memberDisplay(member Member, fallbackID string) string {
	fullName := strings.TrimSpace(member.FullName)
	username := strings.TrimSpace(member.Username)
	switch {
	case fullName != "" && username != "":
		return fullName + " (@" + username + ")"
	case fullName != "":
		return fullName
	case username != "":
		return "@" + username
	case strings.TrimSpace(fallbackID) != "":
		return strings.TrimSpace(fallbackID)
	default:
		return "Unknown member"
	}
}

func commentAuthorDisplay(action Action, memberByID map[string]Member) string {
	if action.MemberCreator != nil {
		return memberDisplay(*action.MemberCreator, strings.TrimSpace(action.IDMemberCreator))
	}
	if member, ok := memberByID[strings.TrimSpace(action.IDMemberCreator)]; ok {
		return memberDisplay(member, strings.TrimSpace(action.IDMemberCreator))
	}
	if strings.TrimSpace(action.IDMemberCreator) != "" {
		return strings.TrimSpace(action.IDMemberCreator)
	}
	return "Unknown author"
}

func fallbackChecklistName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Untitled checklist"
	}
	return name
}

func attachmentMetadataList(attachments []Attachment) []attachmentMetadata {
	out := make([]attachmentMetadata, 0, len(attachments))
	for _, attachment := range attachments {
		out = append(out, attachmentMetadata{
			ID:    strings.TrimSpace(attachment.ID),
			Name:  strings.TrimSpace(attachment.Name),
			URL:   strings.TrimSpace(attachment.URL),
			Bytes: attachment.Bytes,
		})
	}
	return out
}

func customFieldItemMetadataList(items []CustomFieldItem, customFieldByID map[string]CustomFieldDefinition) []customFieldItemMetadata {
	out := make([]customFieldItemMetadata, 0, len(items))
	for _, item := range items {
		field := customFieldByID[strings.TrimSpace(item.IDCustomField)]
		out = append(out, customFieldItemMetadata{
			CustomFieldID:   strings.TrimSpace(item.IDCustomField),
			CustomFieldName: strings.TrimSpace(field.Name),
			CustomFieldType: strings.TrimSpace(field.Type),
			IDValue:         strings.TrimSpace(item.IDValue),
			Value:           item.Value,
		})
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func dedupeNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func quotedName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return `"(unnamed)"`
	}
	return strconvQuote(name)
}

func strconvQuote(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func stringPtr(v string) *string {
	return &v
}
