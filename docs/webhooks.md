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
| **Who** | Project **maintainers** may create subscriptions for a project they maintain. |
| **UI** | **API only** — there is no Settings screen for webhooks. |
| **Auth** | Session cookie required (same as other authenticated `/api/*` routes). |
| **CSRF** | Mutating calls need header `X-Scrumboy: 1`, like other mutating `/api/*` requests. |

`GET /api/webhooks` lists webhooks owned by the **current user** (not every webhook on every project). `DELETE` removes a webhook only if it belongs to that user.

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

Create responses return `id`, `projectId`, `url`, `events`, and `createdAt` — never the secret.

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
2. An in-process worker POSTs to your URL (10s HTTP timeout).
3. **Success** is any HTTP status in **2xx**.
4. **Retries:** up to **three** attempts with fixed backoff (immediate, then ~100ms, then ~400ms) for transient failures (network errors, non-2xx responses that are not classified permanent).
5. **Permanent failures** (forbidden destination / SSRF refusal, or redirect) are **not** retried beyond the failing attempt.
6. The queue is **in-memory** (bounded capacity). On process shutdown the queue is sealed and drained against a deadline; some queued items may still be lost if the process exits. Scrumboy is **not** a durable external queue — design your receiver accordingly and use event `id` for idempotency.

---

## Related documentation

| Doc | Role |
| --- | ---- |
| [security.md](security.md#webhooks) | Security architecture summary for webhook destinations and signing |
| [diagrams/scrumboy_realtime_events.md](diagrams/scrumboy_realtime_events.md) | How events fan out to SSE, webhooks, and other consumers |
| [notifications.md](notifications.md) | Email notifications (separate from webhooks) |
| [vapid.md](vapid.md) / [pwa.md](pwa.md) | Web Push / PWA (separate from webhooks) |
