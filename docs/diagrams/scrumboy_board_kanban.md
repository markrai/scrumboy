# Board and Kanban UX

Board data flows from slug URL through REST into rendered lanes and drag-drop mutations.

```mermaid
flowchart TB
  URL["URL /{slug}"]
  Router[router.ts]
  Load[loadBoardBySlug]
  API["GET api board slug"]
  Render[board-rendering.ts]
  DnD[drag-drop Sortable]
  Patch["PATCH move update todos"]

  URL --> Router --> Load --> API --> Render
  Render --> DnD
  DnD --> Patch --> API
```

## Workflow columns (per project)

Lanes are **not** hard-coded. Each project stores an ordered list in `project_workflow_columns`: stable `key`, display label, color, sort order, and exactly one `is_done` flag.

```mermaid
flowchart TB
  Proj[Project]
  WF[project_workflow_columns]
  Template[Default template on create]
  Settings[Settings workflow UI add rename reorder recolor]
  DoneLane[One column is_done]
  API[Board JSON columnOrder]
  Lanes[Rendered swimlanes]

  Proj --> WF
  Template --> WF
  Settings --> WF
  WF --> DoneLane
  WF --> API --> Lanes
```

Default template keys (example only; boards can diverge):

```mermaid
flowchart LR
  subgraph example [Default template example]
    B[backlog]
    NS[not_started]
    IP[doing]
    T[testing]
    D["done (is_done)"]
    B --> NS --> IP --> T --> D
  end
```

Projects may add lanes (at most **12** columns; `maxWorkflowColumns` in `internal/store/workflows.go`), rename labels, recolor columns, reorder them, and choose which lane counts as done. Todos reference lanes by `column_key`, not a fixed enum. The default mid-lane display name is “In Progress”; its stable key is `doing` (not `in_progress`).

Lane colors and sprint chips use `styles.css` CSS variables. Sprints filter board scope via `sprintId` query param; tags and search filters apply client-side in `board-filters.ts`. Agile field labels and native `title` hover hints (`field-tooltips.ts`) localize with the active locale.
