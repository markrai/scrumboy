# Wall canvas (Scrumbaby)

The Wall is enabled by default for durable projects and can be disabled instance-wide with `SCRUMBOY_WALL_ENABLED=0`. It is reachable from both desktop and mobile board topbars. Temporary/anonymous boards do not expose the Wall.

```mermaid
flowchart TB
  Board[Board topbar or Send to Wall]
  Lazy[dynamic import wall.ts]
  Wall[Wall dialog]
  API[wall-api.ts]
  Routes[routing_board_wall.go]
  App[application/wall mutation layer]
  Store[store wall state]
  SSE[wall-realtime SSE]
  Draw[wall-drawing controller]
  DB[(SQLite)]

  Board --> Lazy --> Wall
  Wall --> API --> Routes --> App --> Store --> DB
  SSE --> Wall
  Wall --> Draw --> API
```

The Wall document combines sticky notes, pinned canonical stories, edges, viewport state in the browser, and a persistent drawing layer.

## Content model

```mermaid
flowchart LR
  Project[Durable project]
  Notes[Wall notes]
  Stories[Wall story placements]
  Edges[Edges between note/story endpoints]
  Ink[Wall drawings in world coordinates]

  Project --> Notes
  Project --> Stories
  Project --> Edges
  Project --> Ink
```

Pinned stories are references to canonical Todos, not copies. Moving a story on the Wall changes only its Wall coordinates. Opening it uses the normal story dialog; deleting the Todo removes its placement.

## Frontend composition

```mermaid
flowchart TB
  Wall[wall.ts lifecycle]
  VP[wall-viewport and navigation]
  Render[wall-rendering]
  Drag[wall-drag-controller]
  Select[wall-selection]
  Edit[wall-edit-controller]
  Context[note/story context menus]
  Drawing[wall-drawing + shared drawing engine]
  Realtime[wall-realtime]
  Mode[select/pan/draw modes]

  Wall --> VP
  Wall --> Render
  Wall --> Drag
  Wall --> Select
  Wall --> Edit
  Wall --> Context
  Wall --> Drawing
  Wall --> Realtime
  Wall --> Mode
```

Select and Pan are persistent browser interaction modes. Draw mode temporarily takes over pointer input while retaining the same transformed world-coordinate canvas.

## Persistent drawing flow

```mermaid
sequenceDiagram
  participant U as User pointer
  participant D as Wall drawing controller
  participant Q as pending/ack queue
  participant API as Wall drawing endpoint
  participant DB as SQLite
  participant SSE as collaborators

  U->>D: pen/highlighter/eraser strokes
  D->>Q: enqueue mutation
  Q->>API: save bounded drawing update
  API->>DB: persist project Wall drawing
  API-->>Q: acknowledge
  API->>SSE: wall refresh signal
  SSE-->>D: reconcile latest Wall document
```

If drawing work is not safely settled when the Wall closes, the UI warns before discarding unsaved state. Late responses from a previously opened Wall are ignored by lifecycle/current-instance guards.

Viewers can open the Wall read-only. Contributors and Maintainers can mutate Wall content under project authorization. The Wall and its drawing controls remain lazy-loaded so users who never open it do not pay the frontend cost.
