# Provider-neutral integration architecture (M1–M3)

M1 introduces a provider-neutral, database-backed foundation for connecting external systems to Scrumboy projects. No new HTTP routes, web UI, credentials, GitHub App setup, inbound webhooks, synchronization, or card modifications ship in this milestone. The store methods are internal building blocks for later milestones.

## Shared GitHub App product direction

The end-user GitHub experience is to install **one centrally operated Scrumboy Connect App** on repositories the user controls or administers. End users **do not register their own GitHub Apps**. The App ID/slug are public, while the private key and App-issued installation credentials are retained by a trusted service under Scrumboy's operational control.

This design requires distinct boundaries for GitHub installation ownership, Scrumboy account/tenant identity, project authorization, and self-hosted server authentication. App-global installation discovery must not be exposed as a user-owned installation list. The present trusted instance-administrator-managed connection is a **temporary implementation**, not the multi-tenant shared-App design. See [shared-App architecture and release gate](../github-shared-app.md). No user-bound broker or verified self-service installation flow is implemented yet.

## Ownership and authority

- Connections are instance-managed. Only a current non-service system Owner or Admin may register, list, rename, disable, or delete them and their external resources. System privilege does not grant project membership.
- External resources belong to one connection and have stable provider-native identifiers. The provider and resource kind are constrained tokens, not hard-coded GitHub enums.
- A project Maintainer may attach and detach existing resources from that project. Project Viewers and Contributors can read the project's associations, but cannot modify them; nonmembers cannot enumerate them.
- Associations are scoped to durable projects, including projects their owners deliberately make publicly viewable. This milestone does not impose a GitHub-versus-Scrumboy visibility policy. Temporary and anonymous boards have no integration association surface.
- Connections are not user credentials. The model deliberately does not persist PATs, private keys, installation tokens, or webhook secrets. Credential management belongs to M3, behind its own encryption and lifecycle design.

## Schema and identity

- `integration_connections`: instance-level provider and provider account identity (`provider`, `external_account_id`), label, status and timestamps. The unique provider/account pair is a stable installation identity for one connection.
- `integration_resources`: resource kind and provider-native `external_id`, name, absolute HTTP(S) URL, timestamps. Unique within (`connection_id`, `kind`, `external_id`), independent of mutable repository names and links.
- `integration_project_resources`: many-to-many association from a Scrumboy project to a resource. There is no special treatment of public durable projects; existing Scrumboy board visibility remains the user's decision.

Project and connection deletion cascade through their associations; connection deletion also cascades resource records. Project deletion never mutates an external provider. M1 intentionally does not introduce issue/PR objects or card links, which will be modeled in M2 without altering the existing `todos` table.

## Provider boundary

The integration core stores opaque provider-native IDs and a minimal resource reference. In M3, adapters will handle provider API calls, authentication and installation discovery, without embedding GitHub-specific ID fields in generic tables. M4 will introduce a separate inbound event inbox, deduplication and reconciliation. M5 introduces user-configurable automations. None of those later systems should turn a Scrumboy board's public-view choice into a GitHub-visibility restriction.

## Validation

`go test ./internal/store ./internal/migrate` should cover schema migrations, transaction-authoritative role checks, duplicate identity handling, project isolation, input validation, and cascade lifecycle. M1 has no live network dependency.

## M2: card-to-resource links

Migration 091 creates `integration_todo_resources` with card-deletion and project-resource-detachment cascade cleanup. A card can reference at most 50 external resources. References use stable resource IDs, do not modify the card, and remain separate from story-to-story links.

REST: `GET /api/board/{slug}/integrations/resources` lists a project's available resources. `GET/POST /api/board/{slug}/todos/{localId}/integrations` lists or links resources, where POST accepts `{"resourceId":123}`. `DELETE /api/board/{slug}/todos/{localId}/integrations/{resourceId}` unlinks a resource. MCP mirrors these operations with `integrations_resourcesList`, `todos_externalLinksList`, `todos_externalLinkAdd`, and `todos_externalLinkRemove`.

Only authenticated project members with Viewer or higher can read external references. Contributors or higher can add and remove card references. Association of a resource to a project still requires Maintainer or higher; registration of new resources still requires instance Owner/Admin. Expiring boards are excluded. Public board publication does not grant integration access. Provider credentials and event processing are deferred to M3/M4.

## M3: GitHub App adapter and installation lifecycle

GitHub is the first provider adapter behind the generic `internal/integration.Provider` boundary. Only the operator holds the GitHub App ID and RSA private key, and the key remains outside the database and repository. The adapter signs short-lived app JWTs, mints least-privilege installation tokens, caches them only in memory, refreshes them before expiry and invalidates cached credentials on provider denial or local disconnect. Requests always target GitHub's fixed API origin and never use a URL supplied by a connected repository.

In Full Mode, authenticated instance Owners/Admins can list GitHub App installations, connect one through a verified installation ID, synchronize its repositories and disconnect a locally managed connection. Discovery must complete successfully before an atomic SQLite reconciliation changes any registered repository. A repository's immutable GitHub ID is the stable identity. Rename updates the display name and URL without severing linked cards. Repositories removed from the App's authorized installation scope are marked unavailable after a complete scan, retaining their original resource identity, last-known URL, project associations and card references. New card links or project associations to unavailable resources are refused. Restored access reactivates the same resource ID. Failed, truncated, or unauthorized scans make no SQLite changes. A provider outage or revocation is not automatically interpreted as a zero-repository installation.

Explicit local disconnect first previews the counts of affected repository resources, project associations and card references, then requires confirmation with a matching impact snapshot checked in the deletion transaction. The Settings > GitHub tab provides the admin workflow. Only project Maintainers can attach and detach registered resources to their project, and only Contributors and above can link them to cards. Owner/Admin system role does not imply project membership. Project and Scrumboy public-board visibility are independent of GitHub's repository visibility. M3 performs no issue sync, GitHub mutations, provider webhooks, or autonomous card updates.

See [GitHub App operator setup](../github-app.md) for installation instructions, constraints and API reference.
