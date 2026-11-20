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
