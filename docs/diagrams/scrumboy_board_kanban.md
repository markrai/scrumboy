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

## Priority tiers (per project)

Each board has ordered definitions in `project_priorities`: stable `key`,
display `name`, `#RRGGBB` color, and `position`. Todos store an optional
project-local `priority_key`; cards render the matching tier but board ordering
and filters do not use priority in this release.

```mermaid
flowchart LR
  Settings[Settings priorities UI]
  Tiers[project_priorities]
  BoardRead[Initial board priorityOrder]
  Todo[todos priority_key]
  Badge[Todo picker and board badge]

  Settings --> Tiers --> BoardRead --> Badge
  Tiers --> Todo --> Badge
```

Maintainers create, rename, recolor, and delete tiers. A project may have at
most 12 and must keep one; an assigned tier cannot be deleted. Readers can use
`GET /api/board/{slug}/priorities`. Initial slug and legacy board projections
own `priorityOrder` through the application board-read service, while lane
pagination intentionally does not repeat project definitions.

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

Lane colors and sprint chips use `styles.css` CSS variables. The server filters board scope via `sprintId` / `tag` / `search` / `assignee` query params on `GET /api/board/{slug}` (and lane pagination); the REST `sprintId` query value is a project-local sprint number, and filtering itself is implemented in the store. MCP `board_get.sprintId` intentionally uses the distinct stored sprint row ID returned by `sprints_list`. `board-filters.ts` currently manages only sprint, tag, and search chips, URL params, and reload, so assignee filtering remains API/MCP-only and the SPA does not yet preserve `?assignee=`. On durable projects, the `tag` filter resolves through the same canonical-name grouping as chip listings (`TagGroupKey`). Agile field labels and native `title` hover hints (`field-tooltips.ts`) localize with the active locale.
