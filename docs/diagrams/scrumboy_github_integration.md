# GitHub integration architecture

Scrumboy v3.43.0 uses an **instance-owned GitHub App**. The self-hosted operator owns the App and its credentials; there is no Scrumboy.com relay, shared production App, or central broker in the data path.

```mermaid
flowchart TB
  Operator[Instance Owner or Admin]
  Member[Project member]
  GitHub[GitHub App and REST API]
  Delivery[GitHub signed webhook delivery]

  subgraph instance [Self-hosted Scrumboy instance]
    Settings[Settings GitHub]
    Manifest[githubmanifest service]
    Creds[githubcredentials store]
    Provider[githubapp provider]
    AdminAPI[Admin integration routes]
    ProjectAPI[Project integration and References routes]
    Receiver[githubwebhook HMAC receiver]
    Reconcile[Repository reconciliation worker]
    Match[Automation matching worker]
    Execute[Notification action worker]
    Store[store authorization and integration state]
    DB[(SQLite)]
    Bundle["DATA_DIR/.scrumboy-github-app/credentials.json"]
  end

  Operator --> Settings --> AdminAPI
  Settings --> Manifest
  Manifest -->|App Manifest registration| GitHub
  GitHub -->|one-time callback code| Manifest
  Manifest --> Creds --> Bundle
  Creds --> Provider

  AdminAPI --> Provider --> GitHub
  AdminAPI --> Store
  Member --> ProjectAPI --> Store
  ProjectAPI --> Provider

  Delivery -->|POST /api/integrations/github/webhook| Receiver
  Receiver -->|verified repository lifecycle event| Reconcile
  Receiver -->|verified issue or PR event| Match
  Reconcile --> Provider
  Reconcile --> Store
  Match --> Store
  Match --> Execute --> Store
  Store --> DB
```

## Authority split

```mermaid
flowchart LR
  OA[Instance Owner or Admin]
  M[Project Maintainer]
  C[Project Contributor]
  V[Project Viewer]

  OA --> A1[Create or configure instance GitHub App]
  OA --> A2[Discover and connect installations]
  OA --> A3[Refresh or disconnect installation]

  M --> P1[Attach or unlink repositories from project]
  M --> P2[Create edit enable disable automation rules]

  C --> R1[Link refresh remove story GitHub references]
  V --> R2[Read retained references and automation history]

  A2 --> Scope[Project membership still required]
  P1 --> Scope
  R1 --> Scope
  R2 --> Scope
```

System privilege does not grant project membership. An Owner/Admin who is not a member of a project cannot use project-scoped GitHub APIs merely because they administer the instance.

## Repository and reference lifecycle

```mermaid
stateDiagram-v2
  [*] --> Available
  Available --> Unavailable: complete refresh no longer sees repository
  Unavailable --> Available: same immutable GitHub repository ID restored
  Available --> Removed: explicit project unlink or connection disconnect
  Unavailable --> Removed: explicit project unlink or connection disconnect

  note right of Unavailable
    Keep resource ID
    Keep last-known metadata
    Keep project association
    Keep existing story references
    Refuse new links
  end note

  note right of Removed
    Explicit local action may remove
    project associations and references
  end note
```

A failed, partial, unauthorized, or truncated provider scan is **not** treated as an empty installation and does not destructively reconcile repositories.

## Signed inbound event path

```mermaid
sequenceDiagram
  participant GH as GitHub
  participant RX as HMAC receiver
  participant DB as SQLite inboxes
  participant P as GitHub provider
  participant W as Workers
  participant N as In-app notification history

  GH->>RX: raw body + X-Hub-Signature-256 + delivery ID
  RX->>RX: verify HMAC before JSON parsing
  RX->>DB: admit metadata + SHA-256 digest, dedupe delivery ID
  RX-->>GH: 202 Accepted
  W->>DB: lease due event
  W->>P: re-read installation or repository when needed
  P-->>W: least-privilege provider state
  W->>DB: reconcile repository metadata or match enabled rule
  W->>DB: lease action and recheck rule version, activation time, repository and installation
  W->>N: create project-scoped notify record
  W->>DB: append action audit transition
```

Only the `notify` action executes in v3.43.0. It creates an in-app history record. It does not edit a Scrumboy story, write to GitHub, publish a board, send email, or generate Web Push.

## Credential boundary

Guided App setup stores one protected bundle under `DATA_DIR/.scrumboy-github-app/credentials.json`. POSIX mode and Windows DACL privacy are validated on load. Manual App configuration instead reads an operator-managed PEM path. SQLite never stores the App private key, webhook secret, installation access tokens, or JWTs.

See [GitHub App integration](../github-app.md), [GitHub inbound webhooks](../github-m4-webhooks.md), and [GitHub notification automations](../github-m5-automation.md).
