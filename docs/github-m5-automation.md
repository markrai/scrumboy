# M5.A: Scrumboy automation rules and dry-run previews

**M5.A status: implemented API foundation; M5.C adds separately gated execution below.** All M5.A routes require an authenticated, durable Full Mode project. This milestone adds rule storage, owner-controlled configuration and a preview endpoint, **not** automatic GitHub processing, notification delivery, card writes or UI automation controls.

## Rule model and boundaries

A rule belongs to exactly one project and one GitHub repository already attached to that project. No new credentials, installation tokens or external GitHub writes are involved in rule management.

| Field | Behavior |
| --- | --- |
| \`id\`, \`projectId\` | Server-generated, immutable IDs |
| \`createdByUserId\` | Creator provenance; becomes null if the creator is deleted |
| \`resourceId\` | Must refer to an **available GitHub repository** attached to the same project and an active connection |
| \`name\` | Required, trimmed, 1–100 characters, no control characters |
| \`eventType\` | Explicit supported event name |
| \`actionType\` | Only \`notify\` is recognized as a **future action proposal**; nothing is dispatched |
| \`enabled\` | Defaults to **false**; opt-in configuration only, **never execution consent** in M5.A |
| \`createdAt\`, \`updatedAt\` | Server timestamps |

Supported event types are \`issue.opened\`, \`issue.closed\`, \`pull_request.opened\`, \`pull_request.closed\` and \`pull_request.merged\`. Rule storage is designed as an integration boundary rather than as a GitHub-specific executor, but provider adapters and new action types must be explicitly reviewed before being added.

Each project may contain at most **50 rules**. Detaching a GitHub repository from a project deletes its dependent automation rules through the database's project/repository foreign key. Deleting a project also deletes its rules. Unknown or unlinked repositories cannot be used as rule targets.

## Authorization

| Operation | Minimum permission |
| --- | --- |
| List rules | Project Viewer |
| Preview a sample event | Project Viewer |
| Create a rule | Project Maintainer |
| Edit/enable/disable a rule | Project Maintainer |
| Delete a rule | Project Maintainer |

Unauthenticated calls are rejected. Users outside a project cannot discover, read, preview, modify or delete its rules. Anonymous and temporary boards have no automation capability, and the public-board API does not expose rule operations. All mutation checks happen inside the store transaction.

## API

All routes use \`/api/board/{slug}/integrations/automation-rules\` and the existing authenticated Scrumboy request/CSRF protections.

**Create a rule (disabled initially):**

\`\`\`http
POST /api/board/my-project/integrations/automation-rules
Content-Type: application/json

{"name":"Review merged PRs","resourceId":123,"eventType":"pull_request.merged","actionType":"notify"}
\`\`\`

Returns \`201\` with the stored rule. Clients cannot activate execution by adding fields to creation requests.

**List rules:** \`GET /api/board/{slug}/integrations/automation-rules\` returns \`{"rules":[...],"executionAvailable":true}\`.

**Update a rule:** \`PATCH /api/board/{slug}/integrations/automation-rules/{ruleId}\` accepts one or more of \`name\`, \`resourceId\`, \`eventType\` and \`enabled\`. For example:

\`\`\`json
{"enabled":true}
\`\`\`

This saves configuration **only**. There is no rule worker, provider-event subscription or execution path in M5.A. The database also has an `execution_approved_at` field that is constrained to `NULL` throughout M5.A. M5.C must explicitly change that schema and require a new user approval before executing saved configurations.

**Delete a rule:** \`DELETE /api/board/{slug}/integrations/automation-rules/{ruleId}\` returns \`204\`.

**Preview a sample event:**

\`\`\`http
POST /api/board/my-project/integrations/automation-rules/45/preview
Content-Type: application/json

{"resourceId":123,"eventType":"pull_request.merged"}
\`\`\`

The response describes a deterministic match against the saved rule. Its \`sampleOnly\` field is always \`true\`, \`executionAvailable\` is \`true\` in the current M5.C implementation, and \`wouldExecute\` remains \`false\`: previews never execute notifications.

\`\`\`json
{
  "ruleId":45,
  "sampleOnly":true,
  "matched":true,
  "enabled":false,
  "actionType":"notify",
  "executionAvailable":true,
  "wouldExecute":false,
  "reason":"rule_disabled"
}
\`\`\`

After enabling a matching rule, \`reason\` changes to \`execution_not_approved\`, while \`wouldExecute\` stays \`false\`. For a different sample event it becomes \`sample_does_not_match\`. If the repository has become unavailable the match is rejected. Preview does not fetch GitHub data, parse webhook payloads, mutate cards, send notifications or write any operational event.

## Safety and next milestones

- **M5.B:** Map authenticated, owner-bound provider events to stable event types, enforce project/repository association and deduplicate delivery. Never evaluate raw unauthenticated webhooks.
- **M5.C:** Add a separately reviewed executor with explicit **new execution approval** at activation time, per-action permissions, idempotency, loop prevention, failure bounds and audit history. **Previously saved \`enabled:true\` rules must not automatically start executing after an upgrade.**
- **M5.D:** Add user-facing rule creation, preview and activation controls to project settings. The current milestone exposes the backend API only.
- **M5.E:** Full Go, frontend, migration, role/tenant, negative security and live staging acceptance. No release of automatic actions until this passes.

M3.3.C's browser/session GitHub connection and cross-tenant qualification remain deferred, not complete. M5.A does not activate that shared-App feature and does not change public-board visibility.

## M5.B: authenticated event matching (read-only)

**Status: code implemented, automated and live acceptance pending.** This phase consumes only GitHub webhook payloads that pass the existing raw-body HMAC-SHA256 verification. A new metadata-only automation inbox is separate from the M4 installation/repository reconciliation inbox. No worker, endpoint or SQL table performs automated actions, notifications or card changes.

### GitHub App event subscriptions and mapping

The Scrumboy Connect App's GitHub settings must subscribe to **Issues** and **Pull request** events, in addition to the existing Installation, Installation repositories and Repository events. The webhook secret remains server-side and all event types share the existing signed webhook endpoint. These new subscriptions must be enabled separately by the operator; configuration or public connection flows are not changed here.

| Verified GitHub event | Normalized M5 event |
| --- | --- |
| \`issues\` with \`opened\` | \`issue.opened\` |
| \`issues\` with \`closed\` | \`issue.closed\` |
| \`pull_request\` with \`opened\` | \`pull_request.opened\` |
| \`pull_request\` with \`closed\` and \`merged: false\` | \`pull_request.closed\` |
| \`pull_request\` with \`closed\` and \`merged: true\` | \`pull_request.merged\` |

One merged pull request produces **only** \`pull_request.merged\`, never a second closed event. Unsupported actions receive the usual accepted response without a rule event. Recognized actions require positive numeric installation, repository and issue/PR identifiers. The top-level and nested PR numbers must agree. A closed PR lacking a \`merged\` Boolean is rejected rather than guessing its meaning.

### Durable inbox and matching guarantees

Migration \`096_add_github_automation_events.sql\` introduces an independent bounded metadata inbox and a project-linked receipt table. Only delivery ID, payload SHA-256 digest, installation ID, repository ID, issue/PR number, event type, timestamps, lease/retry status and error class are stored. Full GitHub event bodies, issue titles/comments, OAuth states, tokens and App secrets are **not stored**. A delivery ID reused with different payload content or across the M4/M5 inboxes is rejected; identical duplicates have no new effect.

The automation inbox allows at most 5,000 pending or leased events and 8 bounded lease attempts, with claim tokens, lease expiration and capped backoff. Completed/dead events and their receipts are pruned after 30 days. Matching an event and committing its completed status happen in **one SQLite transaction**. A failed receipt insert rolls the transaction back and the event remains retriable. A stale lease cannot finalize an event or create a receipt. A replay cannot generate another receipt for the same event/rule.

Only \`enabled:true\` rules whose repository is **still available**, associated with a project and part of an active, matching GitHub installation can produce a receipt. This association is checked at matching time, not only on webhook admission. Disconnected installations, removed repository permissions, deleted rules, detached projects and unrelated tenants produce no match.

The result is a metadata-only record identifying the matched rule and GitHub item number. The rule's \`updated_at\` is copied as version evidence. Rule \`created_by_user_id\` remains historical provenance, **never** authority to evaluate or execute. No GitHub provider API calls are made when matching.

### Read-only inspection

Authenticated members of an authorized project can read a bounded receipt list:

\`\`\`http
GET /api/board/my-project/integrations/automation-matches?limit=30
\`\`\`

The limit defaults to 30 and cannot exceed 100. Responses contain \`matches\` and \`executionAvailable:true\` (the current server capability), with per-match \`wouldExecute:false\` because listing matches is read-only. The endpoint is GET-only and cannot be accessed from unrelated or anonymous boards. It contains only associated local resource IDs, event type, item number, timestamps, action proposal and rule IDs, not raw webhook bodies or private GitHub data.

The existing admin-only webhook status endpoint \`GET /api/admin/integrations/github/webhooks\` also reports an \`automation\` object with pending/leased/completed/dead and receipt counts. Operational errors remain distinguishable from a valid zero-match result, while exact provider errors and payloads are not logged.

### Explicit non-execution gate

M5.A's \`execution_approved_at\` still has a database constraint requiring \`NULL\`. **Even enabled and matched rules are not execution approval.** M5.C must introduce a separately reviewed consent migration and new end-user activation before any notification or card change. Existing \`enabled:true\` rules cannot become executable automatically after an upgrade.

The public shared-App browser/session binding and live multi-tenant staging remain blocked by deferred M3.3.C. M5.B is a backend metadata-processing milestone, **not** general availability.

## M5.C: separately approved, in-app notification execution

**Implementation status: staged; server execution capability is available by default, live staging and final acceptance pending.** Migration 097 adds new durable approval, action and redacted action-audit tables. The legacy `execution_approved_at` column remains NULL and is **not** used as authorization. A new approval record is authoritative; this preserves M5.A's strict no-auto-upgrade rule while allowing an explicit M5.C consent workflow.

The only executable action is `notify`, implemented as a durable **project-scoped in-app notification record**. It does not send email, push notifications or provider API writes, and cannot modify card contents, status, visibility or GitHub resources. The M5.D UI will present the feed; until then it is readable through the project API.

The automation executor is available automatically in a Full Mode deployment with the GitHub webhook service configured. There is **no server-wide automation execution environment switch**. The former `SCRUMBOY_GITHUB_AUTOMATION_EXECUTION_ENABLED` variable is ignored, whether set to `true` or `false`; do not add it to deployment manifests. Execution still requires an enabled rule, separate authenticated Maintainer approval of that exact rule version, a valid repository association, and a verified matching GitHub webhook. Merely installing the GitHub App, enabling a rule or upgrading the server never grants per-rule execution approval.

This behavior is staged in PR #366, which remains unreleased pending M5.E and M3.3.C acceptance. Do not deploy the unqualified branch to general-availability production.

To activate execution, a project Maintainer first creates and enables a rule as before. Enabling `enabled:true` alone is never consent. Next, the Maintainer submits a **separate, authenticated** request:

```http
POST /api/board/my-project/integrations/automation-rules/45/execution-approval
Content-Type: application/json

{"confirm":"APPROVE_NOTIFY_EXECUTION","ruleUpdatedAtMs":1795000000000}
```

`ruleUpdatedAtMs` must be the exact millisecond timestamp represented by the rule's `updatedAt` field. Stale configurations fail with a conflict, disabled rules and unavailable repository associations are rejected. Only a project Maintainer can approve or revoke; CSRF/session protection matches normal project rules. The worker also rechecks that the approver is still a project Maintainer before completing any pending action. A newly approved response exposes `executionApprovedAt`. `DELETE` on the same path immediately revokes the approval without requiring any server-wide setting. **Any rule PATCH also revokes approval**, including a disable, rename, event change or repository change. A later reapproval creates a new monotonic approval identity so old queued actions cannot revive.

Matching still produces **read-only receipts** for enabled rules as in M5.B. An action job is queued *in the same transaction* only when a current approval exists for that exact rule version, and both the rule version and approval predate **webhook admission**. Old receipts or pre-consent events are not backfilled. The queue is capped at 5,000 pending or leased jobs, with unique event/rule keys, short leases, up to eight bounded attempts, and capped retry backoff. Failures are redacted into bounded error classes and audit transitions. The webhook cannot generate new notifications from a re-delivery of the same event.

When a leased notification is completed, the worker rechecks rule enablement, exact rule version, approval identity, project-resource attachment, repository availability and active GitHub installation **inside the completion transaction**. A revoked grant, deleted or modified rule, disconnected installation or unavailable resource causes a terminal `cancelled` record rather than notification execution. The only execution side effect is the atomic transition into a completed in-app notification, avoiding third-party delivery ambiguity. The executor has no card or GitHub write path, so it cannot create an event-action feedback loop.

Project members can read the bounded notification and action history at `GET /api/board/{slug}/integrations/automation-actions?limit=30` (maximum 100), including status, attempt count and redacted error class. The admin GitHub webhook status now includes per-status action counts. Audit transitions are durable while the underlying GitHub event remains retained; the existing M5.B 30-day event retention cascades into completed/dead action history. No raw webhook payload, provider token or private content is added to the action tables.

**Acceptance still required before release:** verify Go/migration/HTTP tests and security CI, explicit consent and stale-version failures, lease races, retry exhaustion, rule disable/revocation mid-flight, legacy environment setting non-interference, two unrelated tenants, real GitHub staging webhooks and app revocation. M3.3.C remains an unwaived public release blocker. This milestone does not merge PR #366.

## M5.D: project Automations settings

**Implementation status: UI and regression tests added; latest CI and live staging acceptance required.** Open a durable project board, then open **Settings → Automations**. This is a board-level tab, not a global GitHub App administration page. Authenticated Viewers, Contributors and Maintainers can inspect rules, sample-only previews, recent matches and notification history. Only project Maintainers can create, edit, enable, disable, delete, approve or revoke rules. Temporary boards and public read-only visitors never receive the tab or rule metadata requests. All API methods independently enforce project membership and role authority.

The tab fetches only server-authoritative, project-scoped endpoints:

| View | Endpoint | Authorization |
| --- | --- | --- |
| Linked repositories | `GET /api/board/{slug}/integrations/resources` | Project member |
| Rules | `GET /api/board/{slug}/integrations/automation-rules` | Project member |
| Sample-only preview | `POST /api/board/{slug}/integrations/automation-rules/{id}/preview` | Project member |
| Matched events | `GET /api/board/{slug}/integrations/automation-matches?limit=30` | Project member |
| In-app notification history | `GET /api/board/{slug}/integrations/automation-actions?limit=30` | Project member |
| Notification audit transitions | `GET /api/board/{slug}/integrations/automation-actions/{id}/audit?limit=30` | Project member |

Rule changes continue using the M5.A POST/PATCH/DELETE API. New rules are always disabled, with one supported `notify` action and explicit supported GitHub event types. Enabling a rule **only allows event matching**. There is no server execution flag or execution-unavailable notice. For each enabled rule, the Maintainer must separately confirm execution in a dialog, and the browser sends the exact current rule timestamp and required `APPROVE_NOTIFY_EXECUTION` marker through the M5.C approval route. Editing a rule revokes that approval, as does disablement and explicit revocation. The UI always reloads authoritative server state after a write or uncertain failure; it never treats optimistic state as approval.

Previews show explicit sample-only, non-executing results. A preview does not authorize execution and cannot trigger a notification. The history page distinguishes completed **in-app records** from email, push or GitHub actions, and includes bounded redacted transition details (`queued`, `claimed`, `completed`, `retry`, `dead`, `cancelled`). Neither automation matching nor the interface changes card content, board visibility or GitHub resources. User-controlled public-board publication decisions are unchanged.

**Remaining release gates:** full Go/frontend/migration/security CI at the final SHA, browser interaction tests in the isolated staging environment, live two-tenant signed webhook and repository revocation exercises, and completion of the deferred M3.3.C public shared-App authorization gate. Do not deploy the unfinished M5 feature in general-availability production until M5.E and those gates pass. Keep cumulative PR #366 open/non-draft.
