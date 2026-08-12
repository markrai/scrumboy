package trelloimport

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"scrumboy/internal/store"
	"scrumboy/internal/version"
)

const (
	maxWorkflowColumns = 12
	maxWorkflowKeyLen  = 32
	rankStep           = int64(1000)
)

type Board struct {
	ID           string                  `json:"id"`
	Name         string                  `json:"name"`
	Desc         string                  `json:"desc"`
	URL          string                  `json:"url"`
	ShortURL     string                  `json:"shortUrl"`
	Closed       bool                    `json:"closed"`
	Lists        []List                  `json:"lists"`
	Cards        []Card                  `json:"cards"`
	Labels       []Label                 `json:"labels"`
	Members      []Member                `json:"members"`
	Checklists   []Checklist             `json:"checklists"`
	Actions      []Action                `json:"actions"`
	CustomFields []CustomFieldDefinition `json:"customFields"`
}

type List struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Pos    float64 `json:"pos"`
	Closed bool    `json:"closed"`
}

type Card struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Desc             string            `json:"desc"`
	IDList           string            `json:"idList"`
	Pos              float64           `json:"pos"`
	Closed           bool              `json:"closed"`
	Due              *string           `json:"due"`
	Start            *string           `json:"start"`
	IDLabels         []string          `json:"idLabels"`
	IDMembers        []string          `json:"idMembers"`
	IDChecklists     []string          `json:"idChecklists"`
	URL              string            `json:"url"`
	ShortURL         string            `json:"shortUrl"`
	Attachments      []Attachment      `json:"attachments"`
	CustomFieldItems []CustomFieldItem `json:"customFieldItems"`
	Labels           []Label           `json:"labels"`
}

type Label struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type Member struct {
	ID       string `json:"id"`
	FullName string `json:"fullName"`
	Username string `json:"username"`
}

type Checklist struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	IDCard     string      `json:"idCard"`
	CheckItems []CheckItem `json:"checkItems"`
}

type CheckItem struct {
	Name  string  `json:"name"`
	State string  `json:"state"`
	Pos   float64 `json:"pos"`
}

type Action struct {
	ID              string     `json:"id"`
	Type            string     `json:"type"`
	Date            string     `json:"date"`
	IDMemberCreator string     `json:"idMemberCreator"`
	Data            ActionData `json:"data"`
	MemberCreator   *Member    `json:"memberCreator"`
}

type ActionData struct {
	Text string        `json:"text"`
	Card ActionRefCard `json:"card"`
	List ActionRefList `json:"list"`
}

type ActionRefCard struct {
	ID string `json:"id"`
}

type ActionRefList struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Attachment struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	URL   string   `json:"url"`
	Bytes *float64 `json:"bytes"`
}

type CustomFieldDefinition struct {
	ID      string              `json:"id"`
	Name    string              `json:"name"`
	Type    string              `json:"type"`
	Options []CustomFieldOption `json:"options"`
}

type CustomFieldOption struct {
	ID    string           `json:"id"`
	Color string           `json:"color"`
	Value CustomFieldValue `json:"value"`
}

type CustomFieldItem struct {
	IDCustomField string           `json:"idCustomField"`
	IDValue       string           `json:"idValue"`
	Value         CustomFieldValue `json:"value"`
}

type CustomFieldValue struct {
	Text    string `json:"text"`
	Number  string `json:"number"`
	Date    string `json:"date"`
	Checked string `json:"checked"`
}

type Preview struct {
	BoardName          string   `json:"boardName"`
	OpenLists          int      `json:"openLists"`
	ClosedLists        int      `json:"closedLists"`
	Cards              int      `json:"cards"`
	ArchivedCards      int      `json:"archivedCards"`
	Labels             int      `json:"labels"`
	MembersReferenced  int      `json:"membersReferenced"`
	Checklists         int      `json:"checklists"`
	ChecklistItems     int      `json:"checklistItems"`
	CommentCardActions int      `json:"commentCardActions"`
	Attachments        int      `json:"attachments"`
	CustomFieldItems   int      `json:"customFieldItems"`
	DetectedDoneColumn string   `json:"detectedDoneColumn"`
	DetectedDoneReason string   `json:"detectedDoneReason"`
	HardErrors         []string `json:"hardErrors"`
	Warnings           []string `json:"warnings"`
}

type Bundle struct {
	Preview                     Preview
	ExportData                  *store.ExportData
	ProjectImportMetadata       string
	TodoImportMetadataByLocalID map[int64]string
}

type projectImportMetadata struct {
	Source              string   `json:"source"`
	TrelloBoardID       string   `json:"trelloBoardId,omitempty"`
	TrelloBoardURL      string   `json:"trelloBoardUrl,omitempty"`
	TrelloBoardShortURL string   `json:"trelloBoardShortUrl,omitempty"`
	TrelloBoardDesc     string   `json:"trelloBoardDesc,omitempty"`
	TrelloBoardClosed   bool     `json:"trelloBoardClosed"`
	ImportedAt          string   `json:"importedAt"`
	SourceListCount     int      `json:"sourceListCount"`
	SourceCardCount     int      `json:"sourceCardCount"`
	SourceActionCount   int      `json:"sourceActionCount"`
	Warnings            []string `json:"warnings,omitempty"`
}

type todoImportMetadata struct {
	Source             string                    `json:"source"`
	TrelloCardID       string                    `json:"trelloCardId,omitempty"`
	TrelloCardURL      string                    `json:"trelloCardUrl,omitempty"`
	TrelloCardShortURL string                    `json:"trelloCardShortUrl,omitempty"`
	TrelloListID       string                    `json:"trelloListId,omitempty"`
	TrelloListName     string                    `json:"trelloListName,omitempty"`
	TrelloListClosed   bool                      `json:"trelloListClosed"`
	TrelloMemberIDs    []string                  `json:"trelloMemberIds,omitempty"`
	TrelloLabelIDs     []string                  `json:"trelloLabelIds,omitempty"`
	TrelloDue          *string                   `json:"trelloDue,omitempty"`
	TrelloStart        *string                   `json:"trelloStart,omitempty"`
	TrelloClosed       bool                      `json:"trelloClosed"`
	Attachments        []attachmentMetadata      `json:"attachments,omitempty"`
	CustomFieldItems   []customFieldItemMetadata `json:"customFieldItems,omitempty"`
}

type attachmentMetadata struct {
	ID    string   `json:"id,omitempty"`
	Name  string   `json:"name,omitempty"`
	URL   string   `json:"url,omitempty"`
	Bytes *float64 `json:"bytes,omitempty"`
}

type customFieldItemMetadata struct {
	CustomFieldID   string           `json:"customFieldId,omitempty"`
	CustomFieldName string           `json:"customFieldName,omitempty"`
	CustomFieldType string           `json:"customFieldType,omitempty"`
	IDValue         string           `json:"idValue,omitempty"`
	Value           CustomFieldValue `json:"value"`
}

func ParseBoardJSON(raw []byte) (*Board, error) {
	var board Board
	if err := json.Unmarshal(raw, &board); err != nil {
		return nil, err
	}
	if board.Lists == nil {
		board.Lists = []List{}
	}
	if board.Cards == nil {
		board.Cards = []Card{}
	}
	if board.Labels == nil {
		board.Labels = []Label{}
	}
	if board.Members == nil {
		board.Members = []Member{}
	}
	if board.Checklists == nil {
		board.Checklists = []Checklist{}
	}
	if board.Actions == nil {
		board.Actions = []Action{}
	}
	if board.CustomFields == nil {
		board.CustomFields = []CustomFieldDefinition{}
	}
	for i := range board.Cards {
		if board.Cards[i].IDLabels == nil {
			board.Cards[i].IDLabels = []string{}
		}
		if board.Cards[i].IDMembers == nil {
			board.Cards[i].IDMembers = []string{}
		}
		if board.Cards[i].IDChecklists == nil {
			board.Cards[i].IDChecklists = []string{}
		}
		if board.Cards[i].Attachments == nil {
			board.Cards[i].Attachments = []Attachment{}
		}
		if board.Cards[i].CustomFieldItems == nil {
			board.Cards[i].CustomFieldItems = []CustomFieldItem{}
		}
		if board.Cards[i].Labels == nil {
			board.Cards[i].Labels = []Label{}
		}
	}
	for i := range board.Checklists {
		if board.Checklists[i].CheckItems == nil {
			board.Checklists[i].CheckItems = []CheckItem{}
		}
	}
	for i := range board.CustomFields {
		if board.CustomFields[i].Options == nil {
			board.CustomFields[i].Options = []CustomFieldOption{}
		}
	}
	return &board, nil
}

func BuildImportBundle(raw []byte, now time.Time) (*Bundle, error) {
	board, err := ParseBoardJSON(raw)
	if err != nil {
		return nil, err
	}
	return TransformBoard(board, now.UTC())
}

func TransformBoard(board *Board, now time.Time) (*Bundle, error) {
	if board == nil {
		return &Bundle{
			Preview: Preview{
				HardErrors: []string{"Missing Trello board data."},
				Warnings:   defaultWarnings(),
			},
		}, nil
	}

	preview := Preview{
		BoardName:  strings.TrimSpace(board.Name),
		Cards:      len(board.Cards),
		Labels:     len(board.Labels),
		Checklists: len(board.Checklists),
		Warnings:   defaultWarnings(),
		HardErrors: []string{},
	}

	listsByID := make(map[string]List, len(board.Lists))
	openLists := make([]List, 0, len(board.Lists))
	closedLists := make([]List, 0, len(board.Lists))
	for _, list := range board.Lists {
		listID := strings.TrimSpace(list.ID)
		if listID == "" {
			preview.HardErrors = append(preview.HardErrors, "A Trello list is missing its id.")
			continue
		}
		if _, exists := listsByID[listID]; exists {
			preview.HardErrors = append(preview.HardErrors, "Duplicate Trello list id: "+listID)
			continue
		}
		list.ID = listID
		listsByID[listID] = list
		if list.Closed {
			closedLists = append(closedLists, list)
		} else {
			openLists = append(openLists, list)
		}
	}
	sortLists(openLists)
	sortLists(closedLists)
	preview.OpenLists = len(openLists)
	preview.ClosedLists = len(closedLists)

	if preview.BoardName == "" {
		preview.HardErrors = append(preview.HardErrors, "Trello board is missing its name.")
	}
	if len(openLists) == 0 {
		preview.HardErrors = append(preview.HardErrors, "Trello board has no open lists to import.")
	}
	if len(openLists) > maxWorkflowColumns {
		preview.HardErrors = append(preview.HardErrors, "Trello boards with more than 12 open lists cannot be imported as-is.")
	}

	labelByID := make(map[string]Label, len(board.Labels))
	for _, label := range board.Labels {
		labelID := strings.TrimSpace(label.ID)
		if labelID == "" {
			continue
		}
		if _, exists := labelByID[labelID]; exists {
			preview.HardErrors = append(preview.HardErrors, "Duplicate Trello label id: "+labelID)
			continue
		}
		label.ID = labelID
		labelByID[labelID] = label
	}
	for _, card := range board.Cards {
		for _, label := range card.Labels {
			labelID := strings.TrimSpace(label.ID)
			if labelID == "" {
				continue
			}
			if _, exists := labelByID[labelID]; !exists {
				label.ID = labelID
				labelByID[labelID] = label
			}
		}
	}

	memberByID := make(map[string]Member, len(board.Members))
	for _, member := range board.Members {
		memberID := strings.TrimSpace(member.ID)
		if memberID == "" {
			continue
		}
		member.ID = memberID
		memberByID[memberID] = member
	}

	customFieldByID := make(map[string]CustomFieldDefinition, len(board.CustomFields))
	customFieldOptionText := make(map[string]string)
	for _, field := range board.CustomFields {
		fieldID := strings.TrimSpace(field.ID)
		if fieldID == "" {
			continue
		}
		field.ID = fieldID
		customFieldByID[fieldID] = field
		for _, option := range field.Options {
			optionID := strings.TrimSpace(option.ID)
			if optionID == "" {
				continue
			}
			customFieldOptionText[optionID] = firstNonEmpty(
				strings.TrimSpace(option.Value.Text),
				strings.TrimSpace(option.Value.Number),
				strings.TrimSpace(option.Value.Date),
				strings.TrimSpace(option.Value.Checked),
			)
		}
	}

	checklistsByCardID := make(map[string][]Checklist)
	checklistByID := make(map[string]Checklist, len(board.Checklists))
	for _, checklist := range board.Checklists {
		checklistID := strings.TrimSpace(checklist.ID)
		if checklistID == "" {
			continue
		}
		checklist.ID = checklistID
		checklistByID[checklistID] = checklist
		cardID := strings.TrimSpace(checklist.IDCard)
		if cardID == "" {
			preview.HardErrors = append(preview.HardErrors, "Checklist "+checklistID+" is missing its card reference.")
			continue
		}
		checklistsByCardID[cardID] = append(checklistsByCardID[cardID], checklist)
		preview.ChecklistItems += len(checklist.CheckItems)
	}

	cardsByID := make(map[string]Card, len(board.Cards))
	for _, card := range board.Cards {
		cardID := strings.TrimSpace(card.ID)
		if cardID == "" {
			preview.HardErrors = append(preview.HardErrors, "A Trello card is missing its id.")
			continue
		}
		if _, exists := cardsByID[cardID]; exists {
			preview.HardErrors = append(preview.HardErrors, "Duplicate Trello card id: "+cardID)
			continue
		}
		card.ID = cardID
		card.IDList = strings.TrimSpace(card.IDList)
		cardsByID[cardID] = card
		if card.Closed {
			preview.ArchivedCards++
		}
		preview.Attachments += len(card.Attachments)
		preview.CustomFieldItems += len(card.CustomFieldItems)
	}

	for _, checklist := range board.Checklists {
		cardID := strings.TrimSpace(checklist.IDCard)
		if cardID == "" {
			continue
		}
		if _, exists := cardsByID[cardID]; !exists {
			preview.HardErrors = append(preview.HardErrors, "Checklist "+strings.TrimSpace(checklist.ID)+" references missing Trello card "+cardID+".")
		}
	}

	referencedMembers := make(map[string]struct{})
	commentsByCardID := make(map[string][]Action)
	for _, action := range board.Actions {
		if strings.TrimSpace(action.IDMemberCreator) != "" {
			referencedMembers[strings.TrimSpace(action.IDMemberCreator)] = struct{}{}
		}
		if action.MemberCreator != nil && strings.TrimSpace(action.MemberCreator.ID) != "" {
			referencedMembers[strings.TrimSpace(action.MemberCreator.ID)] = struct{}{}
			if _, exists := memberByID[strings.TrimSpace(action.MemberCreator.ID)]; !exists {
				memberByID[strings.TrimSpace(action.MemberCreator.ID)] = *action.MemberCreator
			}
		}
		if action.Type != "commentCard" {
			continue
		}
		cardID := strings.TrimSpace(action.Data.Card.ID)
		if cardID == "" {
			continue
		}
		commentsByCardID[cardID] = append(commentsByCardID[cardID], action)
		preview.CommentCardActions++
	}
	for cardID := range commentsByCardID {
		sort.SliceStable(commentsByCardID[cardID], func(i, j int) bool {
			a := commentsByCardID[cardID][i]
			b := commentsByCardID[cardID][j]
			if a.Date == b.Date {
				return a.ID < b.ID
			}
			return a.Date < b.Date
		})
	}

	for _, card := range board.Cards {
		for _, memberID := range card.IDMembers {
			memberID = strings.TrimSpace(memberID)
			if memberID == "" {
				continue
			}
			referencedMembers[memberID] = struct{}{}
		}
	}
	preview.MembersReferenced = len(referencedMembers)

	workflowLists := append([]List(nil), openLists...)
	doneList, doneReason := detectDoneList(openLists)
	if len(openLists) == 1 {
		doneList, doneReason = synthesizedDoneList()
		workflowLists = append(workflowLists, doneList)
	}
	if doneList.ID != "" {
		preview.DetectedDoneColumn = doneList.Name
		preview.DetectedDoneReason = doneReason
	}

	workflowColumns := make([]store.WorkflowColumnExport, 0, len(workflowLists))
	listToColumnKey := make(map[string]string, len(workflowLists))
	usedColumnKeys := make(map[string]struct{}, len(workflowLists))
	if len(openLists) == 1 {
		usedColumnKeys["done"] = struct{}{}
	}
	for idx, list := range workflowLists {
		key := ""
		if list.ID == syntheticDoneListID {
			key = "done"
		}
		baseKey := workflowKeyFromName(list.Name)
		if key == "" {
			var err error
			key, err = uniqueWorkflowKey(baseKey, usedColumnKeys)
			if err != nil {
				preview.HardErrors = append(preview.HardErrors, "Could not generate a unique workflow key for Trello list "+quotedName(list.Name)+".")
				continue
			}
		}
		usedColumnKeys[key] = struct{}{}
		listToColumnKey[list.ID] = key
		workflowColumns = append(workflowColumns, store.WorkflowColumnExport{
			Key:      key,
			Name:     strings.TrimSpace(list.Name),
			Color:    workflowColumnColor(idx, list.ID == doneList.ID),
			Position: idx,
			IsDone:   list.ID == doneList.ID,
		})
	}

	if len(preview.HardErrors) > 0 {
		return &Bundle{Preview: dedupePreview(preview)}, nil
	}

	labelNameByID, tagExports := buildTagExports(labelByID)
	closedListCardCount := 0
	todoMetadataByLocalID := make(map[int64]string, len(board.Cards))
	type convertedCard struct {
		card            Card
		sourceList      List
		targetColumnKey string
		title           string
		body            string
		tagNames        []string
	}
	convertedCards := make([]convertedCard, 0, len(cardsByID))
	for _, card := range board.Cards {
		cardID := strings.TrimSpace(card.ID)
		if cardID == "" {
			continue
		}
		list, listExists := listsByID[strings.TrimSpace(card.IDList)]
		if !listExists {
			preview.HardErrors = append(preview.HardErrors, "Card "+cardID+" references missing Trello list "+strings.TrimSpace(card.IDList)+".")
			continue
		}
		targetColumnKey := listToColumnKey[list.ID]
		if list.Closed {
			targetColumnKey = listToColumnKey[doneList.ID]
			closedListCardCount++
		}

		tagNames := make([]string, 0, len(card.IDLabels))
		seenTagNames := map[string]struct{}{}
		for _, labelID := range card.IDLabels {
			labelID = strings.TrimSpace(labelID)
			if labelID == "" {
				continue
			}
			if _, ok := labelByID[labelID]; !ok {
				preview.HardErrors = append(preview.HardErrors, "Card "+cardID+" references missing Trello label "+labelID+".")
				continue
			}
			tagName := labelNameByID[labelID]
			if _, seen := seenTagNames[tagName]; seen {
				continue
			}
			seenTagNames[tagName] = struct{}{}
			tagNames = append(tagNames, tagName)
		}

		title := strings.TrimSpace(card.Name)
		if title == "" {
			title = "Untitled Trello card"
		}
		if list.Closed {
			title = "[Closed List] " + title
		}
		if card.Closed {
			title = "[Archived] " + title
		}

		body := buildTodoBody(card, list, memberByID, checklistsByCardID, commentsByCardID, customFieldByID, customFieldOptionText)
		convertedCards = append(convertedCards, convertedCard{
			card:            card,
			sourceList:      list,
			targetColumnKey: targetColumnKey,
			title:           title,
			body:            body,
			tagNames:        tagNames,
		})
	}

	if len(preview.HardErrors) > 0 {
		return &Bundle{Preview: dedupePreview(preview)}, nil
	}

	if closedListCardCount > 0 {
		preview.Warnings = append(preview.Warnings, "Cards from closed Trello lists will be imported into the detected done column and marked because Scrumboy cannot preserve closed Trello lanes.")
	}
	if board.Closed {
		preview.Warnings = append(preview.Warnings, "This Trello board is closed. The closed state is preserved only as import metadata in this MVP.")
	}

	sort.SliceStable(convertedCards, func(i, j int) bool {
		if convertedCards[i].sourceList.Pos != convertedCards[j].sourceList.Pos {
			return convertedCards[i].sourceList.Pos < convertedCards[j].sourceList.Pos
		}
		if convertedCards[i].card.Pos != convertedCards[j].card.Pos {
			return convertedCards[i].card.Pos < convertedCards[j].card.Pos
		}
		return convertedCards[i].card.ID < convertedCards[j].card.ID
	})

	columnRanks := make(map[string]int64, len(workflowColumns))
	todoExports := make([]store.TodoExport, 0, len(convertedCards))
	for idx, converted := range convertedCards {
		localID := int64(idx + 1)
		columnRanks[converted.targetColumnKey] += rankStep
		metadata := todoImportMetadata{
			Source:             "trello",
			TrelloCardID:       converted.card.ID,
			TrelloCardURL:      strings.TrimSpace(converted.card.URL),
			TrelloCardShortURL: strings.TrimSpace(converted.card.ShortURL),
			TrelloListID:       converted.sourceList.ID,
			TrelloListName:     strings.TrimSpace(converted.sourceList.Name),
			TrelloListClosed:   converted.sourceList.Closed,
			TrelloMemberIDs:    dedupeNonEmptyStrings(converted.card.IDMembers),
			TrelloLabelIDs:     dedupeNonEmptyStrings(converted.card.IDLabels),
			TrelloDue:          converted.card.Due,
			TrelloStart:        converted.card.Start,
			TrelloClosed:       converted.card.Closed,
			Attachments:        attachmentMetadataList(converted.card.Attachments),
			CustomFieldItems:   customFieldItemMetadataList(converted.card.CustomFieldItems, customFieldByID),
		}
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			return nil, err
		}
		todoMetadataByLocalID[localID] = string(metadataJSON)
		todoExports = append(todoExports, store.TodoExport{
			LocalID:            localID,
			Title:              converted.title,
			Body:               converted.body,
			Status:             strings.ToUpper(converted.targetColumnKey),
			Rank:               columnRanks[converted.targetColumnKey],
			Tags:               converted.tagNames,
			CreatedAt:          now,
			UpdatedAt:          now,
			PriorityKeyPresent: true,
		})
	}

	projectMetadataJSON, err := json.Marshal(projectImportMetadata{
		Source:              "trello",
		TrelloBoardID:       strings.TrimSpace(board.ID),
		TrelloBoardURL:      strings.TrimSpace(board.URL),
		TrelloBoardShortURL: strings.TrimSpace(board.ShortURL),
		TrelloBoardDesc:     strings.TrimSpace(board.Desc),
		TrelloBoardClosed:   board.Closed,
		ImportedAt:          now.Format(time.RFC3339),
		SourceListCount:     len(board.Lists),
		SourceCardCount:     len(board.Cards),
		SourceActionCount:   len(board.Actions),
		Warnings:            dedupeStrings(preview.Warnings),
	})
	if err != nil {
		return nil, err
	}

	return &Bundle{
		Preview: dedupePreview(preview),
		ExportData: &store.ExportData{
			Version:    version.ExportFormatVersion,
			ExportedAt: now,
			Mode:       store.ModeFull.String(),
			Scope:      "project",
			Projects: []store.ProjectExport{
				{
					Name:                 strings.TrimSpace(board.Name),
					CreatedAt:            now,
					UpdatedAt:            now,
					WorkflowColumns:      workflowColumns,
					PriorityTiers:        []store.PriorityTierExport{},
					PriorityTiersPresent: true,
					Todos:                todoExports,
					Tags:                 tagExports,
				},
			},
		},
		ProjectImportMetadata:       string(projectMetadataJSON),
		TodoImportMetadataByLocalID: todoMetadataByLocalID,
	}, nil
}

func defaultWarnings() []string {
	return []string{
		"Trello single-board JSON may include only the latest 1,000 actions, so older comments may already be missing from the source export.",
		"Attachments will be imported as links only. Files are not downloaded in this MVP.",
		"Trello members will not become Scrumboy assignees automatically in this MVP.",
		"Due and start dates will be preserved in the todo body and import metadata, not as native Scrumboy date fields.",
		"Custom fields will be preserved as text and import metadata, not as structured or queryable Scrumboy fields.",
		"Archived cards will be imported with an archived marker.",
		"Trello boards with more than 12 open lists cannot be imported as-is.",
	}
}

func dedupePreview(preview Preview) Preview {
	preview.HardErrors = dedupeStrings(preview.HardErrors)
	preview.Warnings = dedupeStrings(preview.Warnings)
	return preview
}
