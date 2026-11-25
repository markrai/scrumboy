# Board and Kanban UX

Board state is loaded by slug, filtered and sorted server-side, then rendered into project-defined workflow lanes. Active-board reads exclude archived stories.

```mermaid
flowchart TB
  URL["/{slug} + query filters"]
  Router[router]
  Load[loadBoardBySlug]
  API["GET /api/board/{slug}"]
  Store[store board query]
  Render[board rendering]
  DnD[Sortable drag and drop]
  Patch[move/update REST]

  URL --> Router --> Load --> API --> Store
  Store --> API --> Render
  Render --> DnD --> Patch --> Store
```

## Board query state

```mermaid
flowchart LR
  Tags[ordered tag filters]
  Search[text search]
  Sprint[sprint filter]
  Assignee[assignee filter]
  Priority[priority filter]
  Sort["sort: manual newest oldest due"]
  Query[board/lane SQL]
  Active["archived_at IS NULL"]

  Tags --> Query
  Search --> Query
  Sprint --> Query
  Assignee --> Query
  Priority --> Query
  Sort --> Query
  Active --> Query
```

The SPA preserves filter/sort state in the board URL. Large boards can fall back to per-lane pagination using cursors that match the selected sort order. Due-date sorting places undated stories after dated ones.

## Priority tiers

Each durable project owns ordered `project_priorities` definitions with stable keys, names, colors and positions. Todos hold an optional project-local `priority_key`.

```mermaid
flowchart LR
  Settings[Settings Priorities]
  Tiers[project_priorities]
  Todo[todos.priority_key]
  Board[board priority definitions]
  Filter[priority filter]
  Badge[story/card priority UI]

  Settings --> Tiers
  Tiers --> Todo
  Tiers --> Board
  Board --> Filter
  Todo --> Badge
```

Maintainers manage priority tiers and assign/clear story priorities. Viewers can read the definitions and filter the board by a specific tier or no priority.

## Workflow columns

```mermaid
flowchart TB
  Project[Project]
  Columns[project_workflow_columns]
  Settings[Settings Workflow]
  Done[exactly one is_done column]
  Todos[todos.column_key]
  Board[columnOrder and lanes]

  Project --> Columns
  Settings --> Columns
  Columns --> Done
  Columns --> Todos
  Columns --> Board
```

Projects can add, rename, recolor and reorder up to the supported workflow-column limit, and choose the lane that counts as done. Todos reference stable column keys rather than a hard-coded status enum.

## Story state beyond lanes

```mermaid
flowchart TB
  Todo[Story Todo]
  Due[due_at reminders and due sort]
  Custom[typed custom fields]
  Archive[archived_at]
  Notes[Markdown images drawings Notes ink]
  Refs[story links and GitHub References]
  Wall[optional Wall placement]
  Tags[tags sprint assignee priority estimation]

  Todo --> Due
  Todo --> Custom
  Todo --> Archive
  Todo --> Notes
  Todo --> Refs
  Todo --> Wall
  Todo --> Tags
```

Archive/restore removes a story from ordinary board queries without hard-deleting it. The archive has its own listing route. GitHub repository and issue/PR references are separate from title/body/workflow state and do not automatically move or edit a story.

See `scrumboy_github_integration.md` for external-reference authority and `scrumboy_wall_canvas.md` for Wall placements.
