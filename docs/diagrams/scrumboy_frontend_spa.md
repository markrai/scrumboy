# Frontend SPA shell

The browser application is vanilla TypeScript compiled to `web/dist/` and embedded by the Go server. Large feature surfaces such as the Wall are loaded on demand.

```mermaid
flowchart TB
  App[app entry]
  Router[router]
  State[state and URL selectors]
  Views[projects board dashboard archive auth]
  Dialogs[todo settings wall bulk edit]
  Core[SSE push theme preferences]
  I18n[i18n runtime and catalogs]
  API[apiFetch]
  Wall[Wall lazy module]

  App --> Router --> Views
  App --> Core
  Router --> State
  Views --> Dialogs
  Views --> I18n
  Dialogs --> I18n
  Dialogs --> API
  Views --> API
  Views -.->|dynamic import| Wall
```

## Board and story surfaces

```mermaid
flowchart LR
  Board[Board view]
  Todo[New/Edit Todo dialog]
  Details[Details tab]
  Refs[References tab]
  External[todo-external-links]
  GitHub[todo-github-items]
  Wall[Wall dialog]
  Archive[Archive view]

  Board --> Todo
  Todo --> Details
  Todo --> Refs
  Refs --> External
  External --> GitHub
  Board --> Wall
  Board --> Archive
```

The References tab is available on authenticated durable projects. It supports repository references and structured GitHub issue/PR references. GitHub browsing defaults to open pull requests, with repository/type/state selectors and a debounced title filter. New Todo can stage references before the story ID exists and commits them after creation.

Unavailable GitHub repositories retain cached references. The UI marks them unavailable and prevents new links until installation access is restored and repositories are refreshed.

## Settings composition

```mermaid
flowchart TB
  Settings[Settings dialog]
  Global[Customization Profile Users Backup API tokens]
  Project[Workflow Priorities Tags Sprints Calendar Custom fields Sharing Charts]
  GitHub[GitHub instance and project repository management]
  Automations[Automations rules matches notification history audit]

  Settings --> Global
  Settings --> Project
  Settings --> GitHub
  Settings --> Automations
```

GitHub installation discovery is instance-admin oriented; repository association is project-scoped. Automations are readable by project members and mutable only by Maintainers.

## Locale flow

```mermaid
sequenceDiagram
  participant App as app
  participant I18n as initI18n
  participant Boot as BOOTSTRAP_EN_CATALOG
  participant JSON as locale JSON
  participant DOM as document

  App->>I18n: choose saved/browser locale
  Note over Boot: auth and error keys available before fetch
  I18n->>JSON: fetch English and active catalog
  JSON-->>I18n: cached catalog
  I18n->>DOM: set lang/dir and hydrate
  I18n->>DOM: emit I18N_LOCALE_CHANGED on switch
```

Public locale catalogs cover 23 languages; `pseudo` remains localhost/test QA only. RTL direction applies to Arabic, Urdu and Persian.

## Client routes

| Path | View |
|---|---|
| `/` | projects list in Full Mode; Anonymous Mode landing before SPA boot |
| `/dashboard` | dashboard |
| `/{slug}` | board |
| `/{slug}/t/{id}` | board with story open |
| `/{slug}/archive` | archived stories |
| `/auth/reset-password` | reset-password form |

Login/bootstrap/2FA use auth overlays rather than separate routes. Unmatched private-board/archive destinations that resolve to not-found return to the safe application root without weakening existence-hiding behavior.

Theme, density, board filter layout, board sort, VoiceFlow, notifications and Wall canvas preferences are browser/user-level frontend preferences layered over server-authoritative project permissions.
