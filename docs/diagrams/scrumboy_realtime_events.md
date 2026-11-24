# Realtime and delivery pipelines

Scrumboy has two different event directions that should not be confused:

1. **Scrumboy-originated events** fan out to browsers and optional outbound delivery.
2. **GitHub-originated events** enter through a signed provider webhook and use separate durable inboxes/workers.

## Scrumboy-originated fanout

```mermaid
flowchart TB
  Mutation[Committed application mutation]
  Pub[Server.PublishEvent]
  Fan[eventbus.Fanout]
  SSE[SSE bridge]
  OutWH[Outbound webhook dispatcher]
  Push[Web Push notifier]
  Mail[Email notification path]

  Mutation --> Pub --> Fan
  Fan --> SSE
  Fan --> OutWH
  Fan --> Push
  Fan --> Mail

  SSE --> ProjectSSE["GET /api/board/{slug}/events"]
  SSE --> UserSSE["GET /api/me/realtime"]
  OutWH --> Queue[best-effort queue or durable delivery table]
  Queue --> Worker[outbound webhook workers]
  Push --> VAPID[VAPID push service]
  Mail --> SMTP[SMTP worker]
```

Prepared todo update/move paths can publish the internal `todo.creator_notification_requested` event. Authorization is rechecked before the private `todo.creator_activity` SSE event or creator email is delivered. Those internal consideration events are not emitted verbatim to browsers or project webhooks.

Wall durable changes produce `wall.refresh_needed`; transient drag/move traffic can use `wall.transient` without a durable store write.

## GitHub-originated inbound flow

```mermaid
flowchart TB
  GitHub[GitHub]
  RX["POST /api/integrations/github/webhook"]
  Verify[Raw-body HMAC verification]
  Split{event family}

  Inbound[(integration_github_inbound_events)]
  AutoEvents[(integration_github_automation_events)]
  Reconcile[Repository reconciliation worker]
  Match[Automation matcher]
  Actions[(integration_automation_actions)]
  Execute[Notification action worker]
  History[Project notification history and audit]

  GitHub --> RX --> Verify --> Split
  Split -->|installation repository lifecycle| Inbound --> Reconcile
  Split -->|issues or pull_request| AutoEvents --> Match --> Actions --> Execute --> History
```

The GitHub path does **not** flow through `eventbus.Fanout`. It has independent HMAC admission, dedupe, bounded leases/retries and maintenance. Raw GitHub JSON is not persisted in the durable inboxes; only bounded metadata and SHA-256 digests are stored.

At automation settlement the worker rechecks the rule version, enable/activation time, project/repository association, installation status, repository availability and lease token. Disable/edit/revocation races therefore cancel stale work instead of creating a notification.

## SSE transport

| Client context | Endpoint | Module |
|---|---|---|
| Authenticated user | `GET /api/me/realtime` | `core/realtime.ts` |
| Board stream | `GET /api/board/{slug}/events` | `board-realtime.ts` |
| Public published board | isolated public event route | public-board client |

Both authenticated board paths use the shared SSE client infrastructure.

## Common internal/wire events

| Event | Typical consumer |
|---|---|
| `board.refresh_needed` | board clients |
| `board.members_updated` | membership UI refresh |
| `todo.assigned` | private user stream and push |
| `todo.creator_notification_requested` | internal candidate only |
| `todo.creator_notification_recipient_authorized` | internal authorized candidate |
| `todo.creator_activity` | private user-channel toast |
| `wall.refresh_needed` | Wall refetch/incremental reconciliation |
| `wall.transient` | ephemeral Wall movement |

GitHub automation matches/actions are durable database records rather than ordinary eventbus wire events.
