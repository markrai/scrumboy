# Integration foundation (M1)

M1 introduces a provider-neutral, database-backed foundation for connecting external systems to Scrumboy projects. No new HTTP routes, web UI, credentials, GitHub App setup, inbound webhooks, synchronization, or card modifications ship in this milestone. The store methods are internal building blocks for later milestones.

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
