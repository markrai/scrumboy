# M5.A: Scrumboy automation rules and dry-run previews

**Status: implemented API foundation; no automation is executed.** All M5.A routes require an authenticated, durable Full Mode project. This milestone adds rule storage, owner-controlled configuration and a preview endpoint, **not** automatic GitHub processing, notification delivery, card writes or UI automation controls.

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

**List rules:** \`GET /api/board/{slug}/integrations/automation-rules\` returns \`{"rules":[...],"executionAvailable":false}\`.

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

The response describes a deterministic match against the saved rule. Its \`sampleOnly\` field is always \`true\`, and \`executionAvailable\` and \`wouldExecute\` are always \`false\`.

\`\`\`json
{
  "ruleId":45,
  "sampleOnly":true,
  "matched":true,
  "enabled":false,
  "actionType":"notify",
  "executionAvailable":false,
  "wouldExecute":false,
  "reason":"rule_disabled"
}
\`\`\`

After enabling a matching rule, \`reason\` changes to \`execution_not_implemented\`, while \`wouldExecute\` stays \`false\`. For a different sample event it becomes \`sample_does_not_match\`. If the repository has become unavailable the match is rejected. Preview does not fetch GitHub data, parse webhook payloads, mutate cards, send notifications or write any operational event.

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

The limit defaults to 30 and cannot exceed 100. Responses contain \`matches\` and \`executionAvailable:false\`, with per-match \`wouldExecute:false\`. The endpoint is GET-only and cannot be accessed from unrelated or anonymous boards. It contains only associated local resource IDs, event type, item number, timestamps, action proposal and rule IDs, not raw webhook bodies or private GitHub data.

The existing admin-only webhook status endpoint \`GET /api/admin/integrations/github/webhooks\` also reports an \`automation\` object with pending/leased/completed/dead and receipt counts. Operational errors remain distinguishable from a valid zero-match result, while exact provider errors and payloads are not logged.

### Explicit non-execution gate

M5.A's \`execution_approved_at\` still has a database constraint requiring \`NULL\`. **Even enabled and matched rules are not execution approval.** M5.C must introduce a separately reviewed consent migration and new end-user activation before any notification or card change. Existing \`enabled:true\` rules cannot become executable automatically after an upgrade.

The public shared-App browser/session binding and live multi-tenant staging remain blocked by deferred M3.3.C. M5.B is a backend metadata-processing milestone, **not** general availability.
