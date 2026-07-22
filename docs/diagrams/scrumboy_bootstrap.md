# Application bootstrap

Startup sequence in `cmd/scrumboy/main.go`.

```mermaid
flowchart LR
  Env[config.FromEnv]
  DB[db.Open SQLite]
  Mig[migrate.Apply]
  Key[ResolveStartupEncryptionKey]
  Store[store.New]
  Color[BackfillDominantColors]
  OIDC[oidc.New optional]
  MCP[mcp.New]
  Agora[agora.New]
  Srv[httpapi.NewServer]
  MailQ[newMailQueue always]
  MailW[newMailWorker if SMTPConfigured]
  Listen[http.Server Listen TLS optional]

  Env --> DB --> Mig --> Key --> Store --> Color
  Color --> OIDC
  OIDC --> MCP --> Agora --> Srv --> Listen
  Srv --> MailQ
  MailQ -. when SMTPConfigured .-> MailW
```

`ResolveStartupEncryptionKey` runs after migrations and before `store.New`. If encrypted auth/security data already exists, an invalid or missing `SCRUMBOY_ENCRYPTION_KEY` fails startup. On a fresh database with no encrypted data, an invalid key is logged and ignored (2FA setup and password-reset encryption stay disabled until a valid key is configured).

`httpapi.NewServer` always creates `newMailQueue`. If `SMTPConfigured(host, port, from)`, it builds `mailer.New`, `newMailWorker`, and starts `mWorker.Run(mailCtx)`.

Shutdown (`main.go`): `http.Server.Shutdown` then `srv.Close(closeCtx)`. `Server.Close` seals webhook and mail queues, begins worker shutdown, cancels accept loops, and waits on `webhookDone` / `mailDone` (bounded by context).

## Background work

```mermaid
flowchart TB
  Hourly[Hourly ticker main.go]
  Hourly --> Expire[DeleteExpiredProjects temp boards]
  Hourly --> OAuthClean[DeleteExpiredOAuthArtifacts]
  Hourly --> Wal[PRAGMA wal_checkpoint TRUNCATE]

  Fanout[eventbus fanout consumers]
  Fanout --> SSE[sse bridge to Hub]
  Fanout --> WH[webhook dispatcher queue]
  Fanout --> PushN[push notifier todo assigned]

  WH --> Worker[webhook worker goroutine]
  MailQ2[mail queue]
  MailQ2 --> MailWorker[mail worker goroutine]
```

Hourly order in `main.go`: `DeleteExpiredProjects` → `DeleteExpiredOAuthArtifacts` → WAL checkpoint.

`NewServer` wires the SSE bridge, webhook queue plus worker, mail queue (and mail worker when SMTP is configured), and push notifier into `eventbus.NewFanout` before serving traffic. After `NewServer`, `main.go` calls `st.SetTodoAssignedPublisher(srv.PublishTodoAssigned)` to close the todo-assigned → eventbus loop.

`httpapi.NewServer` also receives feature flags from config: `WallEnabled`, `MarkdownNotesEnabled`, and `MermaidNotesEnabled`.
