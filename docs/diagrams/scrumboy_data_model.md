# Data model and persistence

SQLite is the authoritative store for board, identity, permissions, integration metadata and durable work queues. File-backed state under `DATA_DIR` holds uploaded images, wallpapers, and the optional guided GitHub credential bundle.

```mermaid
flowchart TB
  SQL[(SQLite)]
  Store[internal/store]
  Files["DATA_DIR files"]

  subgraph core [Projects and stories]
    Projects[projects project_members workflow columns]
    Todos[todos tags links assignees sprints priorities]
    Archive[todo archived_at due_at custom fields]
    Public[public-board preferences]
  end

  subgraph visual [Visual content]
    Wall[wall notes edges story placements wall drawings]
    Notes[story drawings and notes ink]
    Assets[project_assets and todo_assets]
  end

  subgraph identity [Identity and authorization]
    Users[users sessions API tokens service accounts]
    OIDC[OIDC identities mobile handoff]
    OAuth[OAuth clients codes access refresh]
    Prefs[user_preferences org_settings]
    Audit[audit_events instance audit]
  end

  subgraph integrations [Delivery and integrations]
    OutWH[webhooks durable webhook deliveries]
    GHConn[integration connections resources project resources]
    GHRefs[integration todo resources and GitHub items]
    GHInbox[GitHub inbound and automation event inboxes]
    Auto[integration automation rules matches actions audit]
  end

  subgraph agenda [Agenda and calendar]
    Cal[project calendar sources snapshots agenda settings]
  end

  Store --> SQL
  SQL --> core
  SQL --> visual
  SQL --> identity
  SQL --> integrations
  SQL --> agenda
  Files --> Assets
```

## Integration relationships

```mermaid
erDiagram
  PROJECTS ||--o{ INTEGRATION_PROJECT_RESOURCES : allows
  INTEGRATION_CONNECTIONS ||--o{ INTEGRATION_RESOURCES : owns
  INTEGRATION_RESOURCES ||--o{ INTEGRATION_PROJECT_RESOURCES : attached_to
  TODOS ||--o{ INTEGRATION_TODO_RESOURCES : references
  INTEGRATION_RESOURCES ||--o{ INTEGRATION_TODO_RESOURCES : repository_ref
  TODOS ||--o{ INTEGRATION_TODO_GITHUB_ITEMS : references
  INTEGRATION_RESOURCES ||--o{ INTEGRATION_TODO_GITHUB_ITEMS : scopes
  PROJECTS ||--o{ INTEGRATION_AUTOMATION_RULES : configures
  INTEGRATION_RESOURCES ||--o{ INTEGRATION_AUTOMATION_RULES : targets
  INTEGRATION_GITHUB_AUTOMATION_EVENTS ||--o{ INTEGRATION_GITHUB_AUTOMATION_MATCHES : produces
  INTEGRATION_AUTOMATION_RULES ||--o{ INTEGRATION_GITHUB_AUTOMATION_MATCHES : matches
  INTEGRATION_GITHUB_AUTOMATION_EVENTS ||--o{ INTEGRATION_AUTOMATION_ACTIONS : queues
  INTEGRATION_AUTOMATION_ACTIONS ||--o{ INTEGRATION_AUTOMATION_ACTION_AUDIT : transitions
```

`integration_resources.availability` is independent of repository identity. A complete GitHub scan can mark a repository `unavailable` while keeping its stable provider ID, project associations and existing story references.

The inbound GitHub tables store delivery metadata and payload digests, not raw webhook JSON or provider credentials. Guided App private keys and webhook secrets are outside SQLite in the protected credential bundle.

## Migration pipeline

```mermaid
sequenceDiagram
  participant Main as cmd/scrumboy
  participant Mig as migrate.Apply
  participant DB as SQLite

  Main->>Mig: open DB and enumerate embedded NNN migrations
  Mig->>DB: ensure schema_migrations
  loop each unapplied migration in numeric order
    Mig->>DB: apply transactionally
    Mig->>DB: record migration filename
  end
  Main->>Main: initialize store and runtime services
```

As of v3.43.0 the embedded schema reaches **migration 100**. Important later groups include:

| Range | Major additions |
|------|------------------|
| 065-070 | Agenda/calendar sources, snapshots and mobile OIDC handoff |
| 071-079 | Story archival, service-token groundwork, Wall story placement, public boards, due dates, inline assets, custom fields |
| 080-086 | Service accounts, instance auditing, webhook provenance and bounded durable outbound delivery |
| 087-089 | Story drawings, Wall drawings and Notes ink |
| 090-091 | Guest system-role membership restrictions and temporary-board semantics |
| 092-096 | Provider-neutral integration foundation, story repository links, GitHub inbound inbox, resource availability, structured GitHub issue/PR references |
| 097-100 | Automation rules, GitHub automation events/matches, durable actions/audit, single-toggle enable semantics |

Migration discovery has no hard-coded upper bound. PR #366 retains alias handling for earlier staging-only integration migration filenames so a previously exercised test database does not replay the renumbered 092-100 series.

## Authorization facts

- Store methods, not only HTTP handlers, enforce system/project role boundaries.
- System roles and project roles are separate.
- Historical attribution fields such as `created_by_user_id` do not grant access.
- Public-board reads use isolated projections and do not expose private integration management surfaces.
- Service-account provenance and Guest role state do not silently expand project authority.
