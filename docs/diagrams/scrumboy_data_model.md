# Data model and persistence

SQLite is the primary store for board, auth, and domain state. Uploaded wallpapers are file-backed under `DATA_DIR/user-wallpapers/` (preference JSON stays in SQLite). `internal/store` owns domain rules; `internal/migrate` applies numbered SQL files discovered from the embedded migrations tree (no fixed upper bound).

```mermaid
flowchart TB
  SQL[(SQLite)]
  Files["user-wallpapers JPEGs"]
  Store[internal/store]
  HTTP[httpapi wallpaper routes]

  subgraph domains [Store domains]
    Proj[projects memberships]
    Todo[todos links tags]
    Board[board lanes workflows]
    Sprint[sprints burndown]
    AuthDom[users sessions api tokens oidc]
    OAuthDom[oauth_clients codes access refresh]
    FirstPwd[first_password_grants]
    WallDom[wall notes edges]
    Audit[audit trail]
    WHook[webhook subscriptions]
    PushDom[push subscriptions]
  end

  Store --> SQL
  Store --> domains
  HTTP --> Files
  HTTP --> SQL
```

Wallpaper preference (`user_preferences` key `wallpaper`) records mode/color/revision in SQLite; the normalized JPEG for image mode lives only on disk at `DATA_DIR/user-wallpapers/<user-id>.jpg`.

OAuth authorization codes and access/refresh tokens (after migration 057) require a non-empty `resource` (canonical MCP audience `<origin>/mcp/rpc`). `oauth_clients` has no resource column. `first_password_grants` references `users(id)` and `sessions(token_hash)`. Expired OAuth artifacts are cleaned by hourly `store.DeleteExpiredOAuthArtifacts`.

## Migration pipeline

```mermaid
sequenceDiagram
  participant Main as main.go
  participant Mig as migrate.Apply
  participant DB as SQLite

  Main->>Mig: open db path from DATA_DIR
  Mig->>DB: ensure schema_migrations table
  Note over Mig: knownVersions reads embedded migrations sql sorted no fixed upper bound
  loop each discovered NNN sql not in schema_migrations
    Mig->>DB: applyOne version
  end
  Main->>Main: store.New with optional 2FA encryption key
```

As of this tree the highest embedded file is `057_bind_oauth_tokens_to_mcp_resource.sql` (OAuth AS in 055, first-password grants in 056, resource binding in 057). New files under `internal/migrate/migrations/` are applied automatically; do not document a frozen upper bound.

Authorization checks live in store methods (`CheckProjectRole`, system roles), not only in HTTP handlers.
