package httpapi

import (
	publicboardapp "scrumboy/internal/application/publicboard"
	"scrumboy/internal/store"
)

type publicAccessJSON struct {
	Kind     string `json:"kind"`
	ReadOnly bool   `json:"readOnly"`
}

type publicProjectJSON struct {
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	DominantColor  string `json:"dominantColor"`
	EstimationMode string `json:"estimationMode"`
	SprintsEnabled bool   `json:"sprintsEnabled"`
}

type publicWorkflowJSON struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	IsDone   bool   `json:"isDone"`
	Position int    `json:"position"`
}

type publicPriorityJSON struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Position int    `json:"position"`
}

type publicTagJSON struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	ActiveCount int    `json:"activeCount,omitempty"`
}

type publicTodoJSON struct {
	LocalID          int64           `json:"localId"`
	Title            string          `json:"title"`
	Body             string          `json:"body"`
	ColumnKey        string          `json:"columnKey"`
	EstimationPoints *int            `json:"estimationPoints,omitempty"`
	PriorityKey      *string         `json:"priorityKey,omitempty"`
	SprintNumber     *int64          `json:"sprintNumber,omitempty"`
	Tags             []publicTagJSON `json:"tags"`
}

type publicLaneMetaJSON struct {
	HasMore    bool    `json:"hasMore"`
	NextCursor *string `json:"nextCursor"`
	TotalCount int     `json:"totalCount"`
}

type publicBoardJSON struct {
	Access      publicAccessJSON              `json:"access"`
	Project     publicProjectJSON             `json:"project"`
	Workflow    []publicWorkflowJSON          `json:"workflow"`
	Priorities  []publicPriorityJSON          `json:"priorities"`
	Tags        []publicTagJSON               `json:"tags"`
	Columns     map[string][]publicTodoJSON   `json:"columns"`
	ColumnsMeta map[string]publicLaneMetaJSON `json:"columnsMeta"`
}

type publicLaneJSON struct {
	Items      []publicTodoJSON `json:"items"`
	HasMore    bool             `json:"hasMore"`
	NextCursor *string          `json:"nextCursor"`
	TotalCount int              `json:"totalCount"`
}

type publicTodoDetailJSON struct {
	Todo publicTodoJSON `json:"todo"`
}

type publicTodoLinkJSON struct {
	Direction string `json:"direction"`
	LocalID   int64  `json:"localId"`
	Title     string `json:"title"`
}

type publicTodoLinksJSON struct {
	Links []publicTodoLinkJSON `json:"links"`
}

type publicSprintJSON struct {
	Number int64  `json:"number"`
	Name   string `json:"name"`
	State  string `json:"state"`
}

type publicSprintsJSON struct {
	Sprints []publicSprintJSON `json:"sprints"`
}

func publicTodoToJSON(todo store.PublicTodoProjection) publicTodoJSON {
	tags := make([]publicTagJSON, 0, len(todo.Tags))
	for _, tag := range todo.Tags {
		tags = append(tags, publicTagJSON{Name: tag.Name, Color: tag.Color})
	}
	return publicTodoJSON{
		LocalID: todo.LocalID, Title: todo.Title, Body: todo.Body, ColumnKey: todo.ColumnKey,
		EstimationPoints: todo.EstimationPoints, PriorityKey: todo.PriorityKey,
		SprintNumber: todo.SprintNumber, Tags: tags,
	}
}

func publicTodosToJSON(todos []store.PublicTodoProjection) []publicTodoJSON {
	out := make([]publicTodoJSON, 0, len(todos))
	for _, todo := range todos {
		out = append(out, publicTodoToJSON(todo))
	}
	return out
}

func publicSnapshotToJSON(snapshot publicboardapp.SnapshotResult) publicBoardJSON {
	workflow := make([]publicWorkflowJSON, 0, len(snapshot.Workflow))
	for _, column := range snapshot.Workflow {
		workflow = append(workflow, publicWorkflowJSON{
			Key: column.Key, Name: column.Name, Color: column.Color,
			IsDone: column.IsDone, Position: column.Position,
		})
	}
	priorities := make([]publicPriorityJSON, 0, len(snapshot.Priorities))
	for _, priority := range snapshot.Priorities {
		priorities = append(priorities, publicPriorityJSON{
			Key: priority.Key, Name: priority.Name, Color: priority.Color, Position: priority.Position,
		})
	}
	tags := make([]publicTagJSON, 0, len(snapshot.Tags))
	for _, tag := range snapshot.Tags {
		tags = append(tags, publicTagJSON{Name: tag.Name, Color: tag.Color, ActiveCount: tag.ActiveCount})
	}
	columns := make(map[string][]publicTodoJSON, len(snapshot.Columns))
	for key, todos := range snapshot.Columns {
		columns[key] = publicTodosToJSON(todos)
	}
	meta := make(map[string]publicLaneMetaJSON, len(snapshot.ColumnsMeta))
	for key, lane := range snapshot.ColumnsMeta {
		meta[key] = publicLaneMetaJSON{HasMore: lane.HasMore, NextCursor: lane.NextCursor, TotalCount: lane.TotalCount}
	}
	return publicBoardJSON{
		Access: publicAccessJSON{Kind: "public", ReadOnly: true},
		Project: publicProjectJSON{
			Slug: snapshot.Project.Slug, Name: snapshot.Project.Name,
			DominantColor: snapshot.Project.DominantColor, EstimationMode: snapshot.Project.EstimationMode,
			SprintsEnabled: snapshot.Project.SprintsEnabled,
		},
		Workflow: workflow, Priorities: priorities, Tags: tags, Columns: columns, ColumnsMeta: meta,
	}
}

func publicLaneToJSON(lane publicboardapp.LaneResult) publicLaneJSON {
	return publicLaneJSON{Items: publicTodosToJSON(lane.Items), HasMore: lane.HasMore, NextCursor: lane.NextCursor, TotalCount: lane.TotalCount}
}

func publicLinksToJSON(links []store.PublicTodoLinkProjection) publicTodoLinksJSON {
	out := make([]publicTodoLinkJSON, 0, len(links))
	for _, link := range links {
		out = append(out, publicTodoLinkJSON{Direction: link.Direction, LocalID: link.LocalID, Title: link.Title})
	}
	return publicTodoLinksJSON{Links: out}
}

func publicSprintsToJSON(sprints []store.PublicSprintProjection) publicSprintsJSON {
	out := make([]publicSprintJSON, 0, len(sprints))
	for _, sprint := range sprints {
		out = append(out, publicSprintJSON{Number: sprint.Number, Name: sprint.Name, State: sprint.State})
	}
	return publicSprintsJSON{Sprints: out}
}
