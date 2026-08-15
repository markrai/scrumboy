# Realtime events pipeline

Most domain events are published by prepared application services or HTTP adapters after successful store operations (`emitRefreshNeeded`, `emitMembersUpdated`, `emitWallRefreshNeeded`, `emitWallTransient` → `Server.PublishEvent`). Store mutations do not generally publish. Exception: todo assignee changes call `store.TodoAssignedFunc` set via `SetTodoAssignedPublisher(srv.PublishTodoAssigned)` in `main.go`.

Prepared REST, legacy numeric REST, and MCP todo update/move services may also publish `todo.creator_notification_requested` after a committed mutation. This is an internal request to consider the historical creator later, not delivery or authorization. It is excluded from webhooks and has no SSE, email, Web Push, or frontend consumer. MCP still publishes no `board.refresh_needed`; its internal creator request is a separate, explicit application capability.

```mermaid
flowchart TB
  Handler[HTTP adapters after store success]
  Prepared[Prepared todo update and move services]
  CreatorRequest[Internal creator consideration request]
  Emit["emitRefreshNeeded emitMembersUpdated emitWall"]
  Pub[Server.PublishEvent]
  Fan[eventbus.Fanout]

  Handler --> Emit --> Pub --> Fan
  Prepared --> CreatorRequest --> Pub

  AssignStore[store assignee change]
  CB["TodoAssignedFunc PublishTodoAssigned"]
  AssignStore --> CB --> Pub

  Transient["emitWallTransient no DB write"]
  Transient --> Pub

  Fan --> Bridge[SSE bridge]
  Fan --> WHDisp[webhook dispatcher]
  Fan --> PushN[push notifier]

  Bridge --> Hub[Hub project and user channels]
  Hub --> BoardSSE["GET /api/board/slug/events"]
  Hub --> UserSSE["GET /api/me/realtime"]
  BoardSSE --> BoardClient[board-realtime.ts unauthenticated board stream]
  UserSSE --> LoggedClient[core/realtime.ts authenticated merged stream]
  BoardClient --> Refresh[orchestration/board-refresh.ts]
  LoggedClient --> Refresh

  WHDisp --> Queue[webhook queue]
  Queue --> Worker[webhook worker]
  PushN --> VAPID[Web Push VAPID]
```

## SSE transport

| Client context | Endpoint | Module |
|----------------|----------|--------|
| Authenticated user | `GET /api/me/realtime` | `core/realtime.ts` (merged user + accessible projects) |
| Unauthenticated board client | `GET /api/board/{slug}/events` | `board-realtime.ts` (per-board; also temp/share-style boards in full mode) |

Both paths share `sse-client.ts` for the EventSource connection.

## Common event types

| Event | Typical consumer |
|-------|------------------|
| `board.refresh_needed` | SSE to browsers on that project |
| `board.members_updated` | SSE plus membership UI refresh |
| `todo.assigned` | Push notification to assignee; also on merged user stream |
| `todo.creator_notification_requested` | Internal consideration only; excluded from SSE, email, push, and webhooks |
| `wall.refresh_needed` | Wall canvas full refetch |
| `wall.transient` | Ephemeral drag/move only (`emitWallTransient`); not a durable store mutation; SSE wire only |
