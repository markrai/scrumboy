# Application bootstrap

Startup in `cmd/scrumboy/main.go` builds persistence and security prerequisites before opening the HTTP listener.

```mermaid
flowchart LR
  Env[config.FromEnv]
  DB[db.Open SQLite]
  Mig[migrate.Apply]
  Key[ResolveStartupEncryptionKey]
  Store[store.New]
  Assets[asset stores and janitor]
  OIDC[optional OIDC]
  PublicOrigin[publicorigin resolver]
  GitHub[prepareGitHub]
  MCP[mcp.New]
  Server[httpapi.NewServer]
  Publishers[wire lifecycle publishers]
  Listen[HTTP or HTTPS listen]

  Env --> DB --> Mig --> Key --> Store
  Store --> Assets
  Store --> OIDC
  OIDC --> PublicOrigin
  PublicOrigin --> GitHub
  GitHub --> MCP --> Server --> Publishers --> Listen
```

`prepareGitHub` has three paths:

```mermaid
flowchart TB
  Start[prepareGitHub]
  Manual{manual App ID or PEM configured?}
  Saved{protected guided bundle exists?}
  Origin{Full Mode and canonical HTTPS public origin?}

  Start --> Manual
  Manual -->|yes| ManualProvider[load operator PEM and optional manual webhook secret]
  Manual -->|no| Saved
  Saved -->|yes| Restore[validate bundle permissions and restore provider plus receiver]
  Saved -->|no| Origin
  Origin -->|yes| Manifest[create Owner-only Manifest enrollment service]
  Origin -->|no| Disabled[GitHub integration not configured]

  Manifest --> Save[callback saves protected bundle]
  Save --> Hot[Server.ActivateGitHub hot activation]
```

Manual App settings take precedence over a saved guided bundle without rewriting it. Guided credentials must pass platform privacy validation before startup continues.

## Server workers

```mermaid
flowchart TB
  Server[httpapi.NewServer]
  Fan[eventbus fanout]
  OutWH[best-effort outbound webhook worker]
  DurableWH[durable outbound webhook worker]
  MailT[transactional mail worker]
  MailN[notification mail worker]
  Push[push notifier]
  GH[GitHub inbound service]

  Server --> Fan
  Fan --> OutWH
  Server --> DurableWH
  Server --> MailT
  Server --> MailN
  Fan --> Push
  Server -.->|when GitHub receiver configured| GH
  GH --> GHRecon[repository reconciliation loop]
  GH --> GHMatch[automation matching loop]
  GH --> GHExec[notification action loop]
```

The GitHub service owns its own bounded polling/lease loop and is not the same worker as Scrumboy's outbound project-webhook dispatcher.

## Periodic maintenance

The main hourly maintenance covers temporary-project expiry, OAuth artifact cleanup and WAL checkpointing. Other subsystems have their own bounded maintenance, including asset cleanup, durable outbound webhook maintenance, and GitHub inbound/automation retention.

## Shutdown

`http.Server.Shutdown` stops new HTTP work. `Server.Close` then seals/drains outbound delivery queues, stops mail workers, cancels the GitHub inbound service, and waits on worker completion subject to the shutdown context. The server marks GitHub runtime activation closed so a late Manifest callback cannot start a new provider worker during shutdown.
