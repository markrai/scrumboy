# HTTP request routing

Top-level dispatch in `internal/httpapi/server.go` keeps a few protocol surfaces outside the normal `/api/*` router.

```mermaid
flowchart TD
  Req[Incoming request]
  Req --> Health{healthz?}
  Health -->|yes| HZ[health handler]
  Health -->|no| Agora{agora v1?}
  Agora -->|yes| AgH[Agora handler]
  Agora -->|no| MCP{mcp prefix?}
  MCP -->|yes| McpH[MCP handler]
  MCP -->|no| PRM{OAuth protected-resource metadata?}
  PRM -->|yes| OAuthPR[protected-resource metadata]
  PRM -->|no| ASMeta{OAuth AS metadata?}
  ASMeta -->|yes| OAuthAS[authorization-server metadata]
  ASMeta -->|no| OAuthP{oauth prefix?}
  OAuthP -->|yes| OAuthH[OAuth handlers]
  OAuthP -->|no| GHCB{GitHub Manifest callback?}
  GHCB -->|yes| Manifest[owner-bound Manifest callback]
  GHCB -->|no| GHWH{GitHub webhook exact path?}
  GHWH -->|yes| Receiver[HMAC receiver or 404 when disabled]
  GHWH -->|no| API{api prefix?}
  API -->|yes| ApiH[handleAPI]
  API -->|no| SpaH[SPA static and client routes]
```

Dispatch order is significant. The GitHub callback and webhook endpoint are intercepted **before** the generic API router. The webhook receiver therefore does not use browser-session CSRF semantics; it authenticates GitHub with `X-Hub-Signature-256` over the raw request body.

## API namespaces

```mermaid
flowchart LR
  API["/api"]
  API --> Public[public]
  API --> Projects[projects]
  API --> Board[board]
  API --> Assets[assets]
  API --> Todos[todos]
  API --> Auth[auth]
  API --> Me[me]
  API --> Backup[backup]
  API --> Import[import]
  API --> User[user]
  API --> Admin[admin]
  API --> Version[version]
  API --> Tags[tags]
  API --> Dashboard[dashboard]
  API --> OutWebhooks[webhooks]
  API --> Push[push]

  Board --> Integrations["/{slug}/integrations"]
  Integrations --> Resources[resources]
  Integrations --> Browse["github/browse"]
  Integrations --> Rules[automation-rules]
  Integrations --> Matches[automation-matches]
  Integrations --> Actions[automation-actions]

  Todos --> TodoGH["/{localId}/github-items"]
  Admin --> AdminGH["integrations/github"]
```

Important GitHub routes include:

| Surface | Route | Authority |
|---|---|---|
| Guided registration start | `POST /api/admin/integrations/github/manifest/start` | instance Owner |
| Manifest callback | `/api/integrations/github/manifest/callback` | one-time Owner-bound state + GitHub code |
| Installation management | `/api/admin/integrations/github/*` | Owner/Admin |
| Signed provider webhook | `POST /api/integrations/github/webhook` | GitHub HMAC, not browser auth |
| Project repository association | `/api/board/{slug}/integrations/resources` | project-scoped |
| Issue/PR browse | `/api/board/{slug}/integrations/github/browse` | authenticated project member |
| Story GitHub refs | `/api/board/{slug}/todos/{localId}/github-items` | project-scoped |
| Automation rules/history | `/api/board/{slug}/integrations/automation-*` | project-scoped |

Normal state-changing `/api/*` browser requests require `X-Scrumboy: 1` except the documented auth exceptions. Public-board reads use the isolated `/api/public/*` namespace and do not expose private integration surfaces.

## SPA paths

- `/`: projects list in Full Mode; Anonymous Mode can serve the marketing landing
- `/dashboard`: personal dashboard
- `/{slug}`: canonical project board
- `/{slug}/t/{localId}`: story deep link
- `/{slug}/archive`: archived-story listing
- `/anon`: create temporary anonymous board and redirect
- `/temp`: redirect to `/anon`
- `/{locale}/`: localized Anonymous Mode marketing landing
- `/p/{id}`: legacy project redirect
- `/auth/reset-password`: reset-password form
- `sw.js`: version-injected service worker

See `scrumboy_github_integration.md` for the provider-specific flow.
