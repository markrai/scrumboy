# Outbound webhooks

Scrumboy can **POST JSON** to HTTPS or HTTP URLs you register when board events occur. Use webhooks for **server-side integrations** — your script, gateway, queue worker, or automation service.

Webhooks are **not**:

- In-app or browser notifications (Settings / Notification API)
- Background **Web Push** (VAPID / PWA)
- Live board updates — those continue over **SSE** as usual

When an event fires, Scrumboy delivers a JSON body asynchronously. Delivery is best-effort inside the Scrumboy process; it is not a durable external message queue.

---

## Availability and permissions

| Constraint | Behavior |
| ---------- | -------- |
| **Mode** | **Full mode only.** In anonymous mode, webhook HTTP routes return **404**. |
| **Who** | Project **Maintainers** may create subscriptions for a project they maintain. MCP can make the Maintainer or an explicit service-account member the owner. |
| **UI** | **API only** — there is no Settings screen for webhooks. |
| **Auth** | REST uses the existing session-cookie contract. MCP accepts its existing authenticated session, API-token, or OAuth principal. |
| **CSRF** | Mutating calls need header `X-Scrumboy: 1`, like other mutating `/api/*` requests. |

`GET /api/webhooks` remains an owner-only compatibility view. REST create remains self-owned and retains its existing body and response. MCP listing lets an owner inspect only its own subscriptions even after membership removal. REST and MCP deletion let an owner remove its own subscription; a current project Maintainer may also remove a service-owned subscription on that project, but not another human's subscription.

Delivery authorization is continuous, not a one-time grant at creation. A human-owned subscription is eligible only while its owner remains a Maintainer (or valid legacy Owner). A service-owned subscription is eligible while its owner remains an explicit Viewer, Contributor, or Maintainer. The same predicate is applied before enqueue and immediately before every send attempt. The registering actor's later role and the service owner's API-token state do not authorize delivery.

---

## Creating and managing webhooks

| Method | Path | Purpose |
| ------ | ---- | ------- |
| `POST` | `/api/webhooks` | Create a subscription |
| `GET` | `/api/webhooks` | List your subscriptions |
| `DELETE` | `/api/webhooks/{id}` | Delete one of your subscriptions |

**Create body (JSON):**

| Field | Required | Notes |
| ----- | -------- | ----- |
| `projectId` | Yes | Project you maintain |
| `url` | Yes | Absolute `http` or `https` URL with a host |
| `events` | Yes | 1–50 non-empty event type strings (each ≤ 100 characters) |
| `secret` | No | Shared secret for HMAC signing (omitted from list/create **response** JSON) |

Limits enforced by the server include a maximum of **20** webhooks per project.

Authorization, the project limit, insertion, and registration provenance are checked on the same transaction. The registrar is stored separately from the owner; deleting a distinct registrar clears that provenance reference without deleting the subscription.

Create responses return `id`, `projectId`, `url`, `events`, and `createdAt` — never the secret.

### MCP management

The exact MCP tool names are `webhooks_create`, `webhooks_list`, and `webhooks_delete`; there are no dotted aliases.

| Tool | Inputs | Authorization and output |
| --- | --- | --- |
| `webhooks_create` | `projectSlug`, `url`, `events`, optional write-only `secret`, optional `serviceAccountUserId` | Current project Maintainer. Omitting the owner makes the authenticated actor the owner. An explicit owner must be a genuine service account with an explicit Viewer-or-higher membership on the same project. Returns `webhook` metadata without the secret. |
| `webhooks_list` | `projectSlug`, optional `limit` (default 20, max 100), optional `cursor` | Project Maintainers receive the project management view; other subscription owners receive only their own subscriptions, including after membership removal. Returns owner classification, nullable registrar provenance, URL, events, and creation time without the secret. |
| `webhooks_delete` | `projectSlug`, `webhookId` | The owner, or a current project Maintainer when the owner is a service account. Repeated deletion returns `NOT_FOUND`. Returns only deletion status and identifiers. |

Example delegated registration:

```json
{"tool":"webhooks_create","input":{"projectSlug":"release","url":"https://example.com/scrumboy-hook","events":["todo.assigned"],"secret":"write-only","serviceAccountUserId":42}}
```

The safe result identifies the subscription owner with numeric and stable user IDs, `ownerIsService`, and registrar provenance. Neither MCP transport returns `secret`.

Registration authority and delivery authority remain separate. A service Viewer or Contributor cannot register a webhook, including for itself; a service Maintainer can self-register. System Owner/Admin roles do not bypass project membership or Maintainer authority.

### Example create

```bash
curl -b cookies.txt -X POST http://localhost:8080/api/webhooks \
  -H "Content-Type: application/json" \
  -H "X-Scrumboy: 1" \
  -d '{"projectId":1,"url":"https://example.com/scrumboy-hook","events":["todo.assigned"],"secret":"optional-shared-secret"}'
```

---

## Event subscriptions

Subscribe to **specific** event type strings (for example `todo.assigned`), or use `*` to match **all** event types that Scrumboy delivers as webhooks.

Matching is exact string equality, or `*`:

- A subscription to `todo.assigned` receives only that type.
- A subscription to `*` receives every delivered type for that project.

Some internal routing events (for example `board.refresh_needed` and creator-notification internal events) are **never** sent as webhooks. The set of deliverable types can grow as Scrumboy adds features.

Event names are **not** validated against a fixed allowlist at create time. Subscribing to a type that is never emitted, or that is not used yet, is harmless — those entries simply never match.

---

## Todo lifecycle events

Besides `todo.assigned`, Scrumboy delivers four todo events. They are published from the store **after the mutation commits**, so a failed or rejected mutation never emits one, and the REST API and MCP tools behave identically. Temporary (anonymous) boards do not emit them.

| Type | When | Extra payload fields |
| ---- | ---- | -------------------- |
| `todo.created` | A todo was created | none |
| `todo.updated` | Title, body, sprint, estimation, priority, due date, or tags actually changed | `changedFields` (subset of `title`, `body`, `sprint`, `estimation`, `priority`, `due`, `tags`) |
| `todo.moved` | The todo changed column (a reorder within the same column is not delivered) | `fromColumnKey`, `toColumnKey`, `fromColumnName`, `toColumnName` |
| `todo.deleted` | A todo was deleted | none (fields describe its last state) |
| `todo.due_soon` | A todo entered its reminder window (see below) | `dueAt` (RFC 3339 UTC), `leadHours` |

Common payload fields: `projectId`, `projectSlug`, `todoId`, `localId`, `title`, `columnKey`, `assigneeUserId`, `assigneeStableUserId`, `actorUserId`, `actorStableUserId`. The body of the todo is never included.

- **Stable ids:** integer user ids are kept for compatibility, but SQLite can reuse them after a user is deleted. Key any external account link on `actorStableUserId` / `assigneeStableUserId` (the same value as `stableUserId` from the MCP `me_get` tool).
- **No duplicates:** a no-op update or same-column reorder emits nothing, and an assignee-only change is delivered as `todo.assigned` only (not also as `todo.updated`). Creating a todo that already has an assignee emits both `todo.created` and `todo.assigned`.
- **Not guaranteed:** delivery stays best-effort (in-memory queue); see [Delivery and retry behavior](#delivery-and-retry-behavior).
- Internal events such as `board.refresh_needed` are still never delivered.
- **`todo.due_soon`** is emitted by a background scheduler, not by a mutation: once per due date, when a todo's `dueAt` falls within the configured lead time (default 24 hours; `GET/PUT/DELETE /api/admin/settings/due-reminder`, `0` disables). Changing `dueAt` starts a new cycle. Todos in a done column, archived todos, temporary boards, and todos whose due date had already passed when first seen never emit it. Delivery is at most once per due date; a reminder missed during downtime is not replayed. Changing only `dueAt` also reports `due` in `todo.updated`'s `changedFields`.

---

## Payloads and idempotency

Each delivery is an HTTP `POST` with `Content-Type: application/json; charset=utf-8` and a JSON body shaped like:

```json
{
  "id": "<event-id>",
  "type": "<event-type>",
  "timestamp": "<RFC3339 time>",
  "projectId": 1,
  "payload": { }
}
```

Additional request headers:

| Header | Meaning |
| ------ | ------- |
| `X-Scrumboy-Event` | Event type string |
| `X-Scrumboy-Delivery` | Same value as body `id` (delivery / event id) |
| `X-Scrumboy-Signature` | Present only when a secret is configured (see below) |

Treat body `id` (and `X-Scrumboy-Delivery`) as an **idempotency key**. Scrumboy may retry the same delivery after transient failures; receivers should ignore duplicates safely.

---

## Request signing

If you set an optional `secret` on the webhook, Scrumboy signs the **raw JSON body bytes** with **HMAC-SHA256** and sends:

```http
X-Scrumboy-Signature: sha256=<hex>
```

To verify:

1. Read the raw request body (before JSON parsing that might re-serialize).
2. Compute `HMAC-SHA256(secret, body)`.
3. Hex-encode the digest and compare to the value after `sha256=` using a constant-time compare.

If `secret` is omitted or empty, no signature header is sent.

For broader deployment and secret-handling guidance, see [security.md](security.md#webhooks).

---

## Destination security

Webhook destinations must be publicly routable **`http`** or **`https`** URLs (scheme and host required).

At **create** time, Scrumboy rejects obvious unsafe literal hosts (for example `localhost` and literal private IPs). At **delivery** time it resolves the hostname, refuses unsafe addresses, and dials a vetted IP directly.

Refused destinations include (among others):

- Loopback addresses
- Private / LAN ranges (including RFC1918 and IPv6 ULA)
- Link-local addresses (including common cloud metadata addresses such as `169.254.169.254`)
- Multicast, unspecified, and additional special-purpose ranges (CGNAT shared space, documentation nets, and related blocked nets)

**Dial-time enforcement** also applies to URLs already stored before a security upgrade: a previously saved private or metadata URL will not be delivered once the current SSRF controls are in place.

Additional delivery rules:

- **Redirects are not followed.** A 3xx response fails the delivery (permanently — no retry as a successful redirect chase).
- The webhook HTTP client sets **`Proxy: nil`**, so environment `HTTP_PROXY` / `HTTPS_PROXY` settings are **ignored**. That prevents an ambient proxy from forwarding traffic to an address that failed Scrumboy’s destination checks.

These controls reduce SSRF risk from webhook URLs. They also mean **LAN-only or Docker-internal receivers will not receive deliveries** unless exposed on a publicly routable address.

---

## Delivery and retry behavior

Delivery is **asynchronous** and **best-effort**:

1. After an event is published, a dispatcher matches project subscriptions and enqueues work (without blocking SSE).
2. Immediately before each send attempt, the worker reloads the subscription and verifies the same human-Maintainer or service-Viewer-or-higher predicate used before enqueue. Missing or unauthorized records are dropped. Authorization lookup failures fail closed and make no HTTP request.
3. An in-process worker POSTs to the currently stored subscription URL (10s HTTP timeout).
4. **Success** is any HTTP status in **2xx**.
5. **Retries:** up to **three** attempts with fixed backoff (immediate, then ~100ms, then ~400ms) for transient failures (network errors, non-2xx responses that are not classified permanent, and temporary authorization-lookup errors). Authorization is rechecked before every retry.
6. **Permanent failures** (revoked/deleted subscriptions, forbidden destination / SSRF refusal, or redirect) are **not** retried beyond the failing attempt.
7. The queue is **in-memory** (bounded capacity). On process shutdown the queue is sealed and drained against a deadline; some queued items may still be lost if the process exits. Scrumboy is **not** a durable external queue — design your receiver accordingly and use event `id` for idempotency.

Because authorization is revalidated at send time, an event queued while a subscription was eligible is dropped if membership, account, or subscription state is revoked before the HTTP request begins. Scrumboy does not attempt to cancel a request that was already in flight when revocation occurred.

For a delegated service subscription, removing the service owner makes the row inert; re-adding it resumes only future events. Deleting the service owner or project cascades the subscription away. Removing or deleting a distinct registrar leaves it in place, and revoking the service owner's API token does not affect signing-secret delivery. Missed events are not replayed.

---

## Related documentation

| Doc | Role |
| --- | ---- |
| [security.md](security.md#webhooks) | Security architecture summary for webhook destinations and signing |
| [diagrams/scrumboy_realtime_events.md](diagrams/scrumboy_realtime_events.md) | How events fan out to SSE, webhooks, and other consumers |
| [notifications.md](notifications.md) | Email notifications (separate from webhooks) |
| [vapid.md](vapid.md) / [pwa.md](pwa.md) | Web Push / PWA (separate from webhooks) |
