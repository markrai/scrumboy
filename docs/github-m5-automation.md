# M5.A: Scrumboy automation rules and dry-run previews

**M5.A status: historical API foundation; current M5.C–E behavior is described below.** All M5.A routes require an authenticated, durable Full Mode project. This milestone adds rule storage, owner-controlled configuration and a preview endpoint, **not** automatic GitHub processing, notification delivery, card writes or UI automation controls.

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

This enables the rule for actual execution in the current M5.C–E implementation. M5.A was a non-executing foundation with no worker or provider event subscription. The database also has an `execution_approved_at` field that is constrained to `NULL` throughout M5.A. The later implementation authorizes execution when a Maintainer enables a rule; the obsolete M5.C approval table is removed by migration 100.

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

After enabling a matching rule, \`reason\` becomes \`ready_for_new_events\`, while \`wouldExecute\` stays \`false\` because previews never execute. For a different sample event it becomes \`sample_does_not_match\`. If the repository has become unavailable the match is rejected. Preview does not fetch GitHub data, parse webhook payloads, mutate cards, send notifications or write any operational event.

## Safety and milestone progression

- **M5.B (implemented):** Normalize signed GitHub events, enforce installation/repository/project association and deduplicate delivery. Never evaluate unauthenticated webhook bodies.
- **M5.C (implemented):** Only rules enabled by a Maintainer before event admission may enqueue durable in-app notifications. There is **no separate execution approval**; idempotency, rule-version checks, bounded failure recovery and audit history apply. Previously received events never execute retroactively.
- **M5.D (implemented):** Project Settings → Automations supports rule creation, sample-only preview, Enable/Disable, recent matches and in-app notification history. Live deployment acceptance remains pending.
- **M5.E:** Full Go, frontend, migration, role/tenant, negative security and live staging acceptance. No release of automatic actions until this passes.

The instance-owned GitHub App is currently administrator-managed; automated App creation and independent-instance qualification remain separate work. M5.A does not change public-board visibility.

## M5.B: authenticated event matching (read-only)

**Status: code implemented, automated and live acceptance pending.** This phase consumes only GitHub webhook payloads that pass the existing raw-body HMAC-SHA256 verification. A new metadata-only automation inbox is separate from the M4 installation/repository reconciliation inbox. No worker, endpoint or SQL table performs automated actions, notifications or card changes.

### GitHub App event subscriptions and mapping

The instance-owned GitHub App's settings must subscribe to **Issues** and **Pull request** events, in addition to the existing Installation, Installation repositories and Repository events. The webhook secret remains server-side and all event types share the existing signed webhook endpoint. These new subscriptions must be enabled separately by the operator; configuration or public connection flows are not changed here.

| Verified GitHub event | Normalized M5 event |
| --- | --- |
| \`issues\` with \`opened\` | \`issue.opened\` |
| \`issues\` with \`closed\` | \`issue.closed\` |
| \`pull_request\` with \`opened\` | \`pull_request.opened\` |
| \`pull_request\` with \`closed\` and \`merged: false\` | \`pull_request.closed\` |
| \`pull_request\` with \`closed\` and \`merged: true\` | \`pull_request.merged\` |

One merged pull request produces **only** \`pull_request.merged\`, never a second closed event. Unsupported actions receive the usual accepted response without a rule event. Recognized actions require positive numeric installation, repository and issue/PR identifiers. The top-level and nested PR numbers must agree. A closed PR lacking a \`merged\` Boolean is rejected rather than guessing its meaning.

### Durable inbox and matching guarantees

Migration \`098_add_github_automation_events.sql\` introduces an independent bounded metadata inbox and a project-linked receipt table. Only delivery ID, payload SHA-256 digest, installation ID, repository ID, issue/PR number, event type, timestamps, lease/retry status and error class are stored. Full GitHub event bodies, issue titles/comments, OAuth states, tokens and App secrets are **not stored**. A delivery ID reused with different payload content or across the M4/M5 inboxes is rejected; identical duplicates have no new effect.

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

M5.A's \`execution_approved_at\` still has a database constraint requiring \`NULL\`. **M5.A/B were deliberately non-executing.** The current implementation authorizes future notifications when a Maintainer enables a rule; migration 100 ensures events admitted before the upgrade are not backfilled.

Independent-instance isolation and live signed-delivery testing remain pending. M5.B is a backend metadata-processing milestone, **not** general availability.

## M5.C–E: enabling a rule authorizes in-app notifications

**Implementation status: staged in PR #366; automated and live acceptance still required.** Only `notify` actions are executable. They create durable project-scoped in-app history records, not email, push, browser pop-ups, card mutations, or GitHub writes.

There is **no separate execution approval**, confirmation dialog, or operator environment switch. A project Maintainer creates a disabled rule and turns it on with the **Enable** button. The authenticated `PATCH /api/board/{slug}/integrations/automation-rules/{id}` request with `{"enabled":true}` authorizes execution for future matching GitHub events. **Disable** stops execution and invalidates pending actions. Editing an enabled rule starts a fresh rule version; actions from older versions are cancelled at settlement, and only events received after the new version can produce new notifications. The obsolete `/execution-approval` API returns 404. The legacy `SCRUMBOY_GITHUB_AUTOMATION_EXECUTION_ENABLED` environment variable has no effect.

Migration 100 removes the M5.C execution-approval table/triggers and renames the action authorization column to `activation_version`. It adds `execution_since` to the rule model. Existing enabled rules become executable for **future events only** after upgrade; already-admitted events and old queued actions cannot suddenly activate. The legacy `integration_automation_rules.execution_approved_at` field remains inert as constrained by the M5.A migration, with no permission implications. Both the rule's exact version and activation time are checked against the event admission time. These checks occur in the same durable transaction that records a match and queues a unique action.

A webhook event is eligible only when it passed raw-body HMAC verification, maps to a supported GitHub issue or pull-request event, belongs to an active installation with an available repository linked to the project, and matches an enabled rule that existed before the event was admitted. Events are deduplicated by delivery ID and digest, and one event/rule pair can produce at most one notification. Every notification is a durable action with a bounded lease, retry limit, and redacted audit history. At settlement the worker rechecks the exact enabled rule version, project/repository association, installation status, event timing and the lease token. A disable, edit, delete, repository disconnect or installation revocation causes cancellation, not a notification.

The project rule list and preview endpoints retain `executionAvailable:true` for API compatibility. Preview always returns `sampleOnly:true` and `wouldExecute:false`. A matching enabled rule reports `reason:"ready_for_new_events"`; a disabled one reports `reason:"rule_disabled"`. Preview **never** creates a GitHub event or in-app notification, and it does not verify delivery to this particular deployment.

### Readiness diagnostics (S1)

Settings → GitHub (instance Owner/Admin) distinguishes App configured, webhook receiver disabled, missing installation, awaiting a signed delivery, and a signed event received since the process started. Project members see reduced status in Settings → Automations without the administrator webhook URL or installation counts. `lastVerifiedAt` resets on restart; a verified ping does not prove that Issues or Pull request subscriptions are enabled. See [M4 operator guide](github-m4-webhooks.md).

### Diagnosing a real GitHub issue with no notification

1. Confirm the installed GitHub App is subscribed to **Issues** events and that its configured webhook destination reaches the exact Scrumboy deployment being tested. Each instance-owned GitHub App must have a reachable webhook destination; a private-only Tailscale address cannot receive direct GitHub deliveries.
2. Create a **new** issue after the rule was enabled. Previously received webhook events are never backfilled.
3. Open **Settings → Automations**, refresh, and check **Recent matches**. If empty, first investigate GitHub webhook delivery, installation/repository ownership, and rule event matching. A successful preview is not evidence that a live webhook arrived.
4. If the match exists, check **Notification history** and the action's **Audit details**. Completed means an in-app history record was created. Failed or cancelled actions expose bounded status/reason details. This milestone does not produce desktop/push alerts or a notification-center badge.

Admin webhook status, inbox counts and redacted server logs help distinguish an absent delivery from a processing failure. Verify in GitHub's App webhook delivery diagnostics as well. Do not interpret an empty notification history as proof that a rule is disabled.

### Project settings and permissions

Project Viewers, Contributors and Maintainers may view rules, previews, matches, notification history, and redacted action audits. Only Maintainers can create, enable, disable, edit or delete rules. New rules start disabled. Neither temporary boards nor anonymous public-board readers have automation access. Public-board publication choices remain user-controlled.

The tab calls only project-scoped authenticated APIs: `/integrations/resources`, `/integrations/automation-rules`, `/integrations/automation-rules/{id}/preview`, `/integrations/automation-matches`, `/integrations/automation-actions`, and `/integrations/automation-actions/{id}/audit`. Audits are append-only transition records; previews are strictly read-only.

**Remaining release gates:** Complete M5.E two-tenant live webhook and browser qualification, replay/revocation/restart/retry exercises, failure-path tests and final CI. Independent-instance isolation and the planned App Manifest wizard are tracked in #371; live webhook acceptance remains a rollout blocker. PR #366 stays open and non-draft until these gates pass.
