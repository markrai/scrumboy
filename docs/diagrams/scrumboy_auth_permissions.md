# Authentication and permissions

Scrumboy separates **instance-wide system roles** from **per-project roles**. HTTP establishes identity; transaction-authoritative store methods decide access.

```mermaid
flowchart TB
  Cred[Credential]
  Cookie[session cookie]
  Token["sb_ API token"]
  OAuth[OAuth access token on canonical MCP]
  OIDC[OIDC login]
  Ctx[requestContext actor]
  Handler[HTTP MCP handler]
  Store[store authorization]
  Result[allow 403 or existence-hiding 404]

  Cred --> Cookie --> Ctx
  Cred --> Token --> Ctx
  Cred --> OAuth --> Ctx
  Cred --> OIDC --> Ctx
  Ctx --> Handler --> Store --> Result
```

## System roles

| Role | Instance authority |
|---|---|
| Owner | user administration, role assignment, owner-only sensitive setup such as guided GitHub App enrollment |
| Admin | instance administration allowed by admin APIs, including GitHub installation management |
| User | no instance-wide administration |
| Guest | assigned regular boards are read-only; temporary-board collaboration by shared link remains possible; cannot create/import/claim boards |

System roles **never grant project membership**.

## Project roles

| Role | Project authority |
|---|---|
| Viewer | read project/story state and retained integration references |
| Contributor | limited project mutations, including allowed reference/tag operations; body edit only when assigned under normal story rules |
| Maintainer | project configuration, members, full story management, repository association and automation rule management |

Temporary/anonymous boards use separate link/expiry semantics and do not expose durable-project integration surfaces.

## GitHub authority composition

```mermaid
flowchart LR
  OwnerAdmin[Owner or Admin]
  Maintainer[Project Maintainer]
  Contributor[Project Contributor]
  Viewer[Project Viewer]

  OwnerAdmin --> Install[Discover connect refresh disconnect GitHub installation]
  Maintainer --> Repo[Attach or explicitly unlink repository from project]
  Maintainer --> Rules[Create edit enable disable automation rule]
  Contributor --> Refs[Link refresh remove story GitHub refs]
  Viewer --> Read[Read retained refs rules matches history]

  Install --> ProjectCheck[Project-scoped operations still require membership]
  Repo --> ProjectCheck
  Rules --> ProjectCheck
  Refs --> ProjectCheck
  Read --> ProjectCheck
```

An instance Owner/Admin cannot bypass project membership merely because they control the GitHub App. Conversely, project membership never exposes App private keys, webhook secrets or installation tokens.

## Login paths

```mermaid
flowchart LR
  Local[Email password]
  TOTP[2FA]
  OIDCFlow[OIDC authorization code]
  Bootstrap[First-user bootstrap]
  Identity[issuer subject lookup]
  Existing[existing linked user]
  Provision[provision verified unowned email]
  Connect[explicit Connect SSO]
  StepUp[sensitive provider step-up]
  Grant[first-password grant]
  Password[write first local password]
  Session[session]

  Local --> TOTP --> Session
  OIDCFlow --> Identity
  Identity --> Existing --> Session
  Identity --> Provision --> Session
  Connect --> OIDCFlow
  StepUp --> Grant --> Password
  Bootstrap --> Session
```

Normal OIDC login never silently links by email and never rewrites the canonical user email. Sensitive local-password establishment for an SSO-only account uses a separate step-up and short-lived session-bound grant.

See [roles-and-permissions.md](../roles-and-permissions.md) for the complete matrix.
