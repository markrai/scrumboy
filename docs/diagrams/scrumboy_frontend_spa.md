# Frontend SPA shell

Vanilla TypeScript modules compiled to `dist/` and embedded by Go `//go:embed`.

```mermaid
flowchart TB
  App[app.js entry]
  Router[router.ts]
  State[state selectors mutations]
  Views[views projects board dashboard auth notfound]
  Dialogs[dialogs todo settings wall bulk-edit]
  Core[core sse push notifications theme]
  I18n[i18n runtime index.ts]
  Picker[locale-select.ts listbox]
  Tips[field-tooltips.ts]
  Cat[locale catalogs and flag SVGs]
  Events[I18N_LOCALE_CHANGED hydrateI18n]
  ApiErr[apiErrorMessage details.reason]

  App --> Router
  App --> Core
  App --> I18n
  Router --> State
  Router --> Views
  Views --> Dialogs
  Views --> I18n
  Views --> Tips
  Dialogs --> I18n
  Dialogs --> Tips
  I18n --> Cat
  I18n --> Picker
  I18n --> Events
  I18n --> ApiErr
  Events --> Views
  Events --> Dialogs
  Picker --> Cat
```

## Locale flow

```mermaid
sequenceDiagram
  participant App as app.js
  participant I18n as initI18n
  participant Boot as BOOTSTRAP_EN_CATALOG
  participant JSON as dist/i18n/locales
  participant DOM as document

  App->>I18n: detect saved or browser locale
  Note over Boot: auth and errors keys embedded before fetch
  I18n->>JSON: fetch en then active locale catalog
  JSON-->>I18n: cached MessageCatalog
  I18n->>DOM: lang dir translate=no
  I18n->>DOM: I18N_LOCALE_CHANGED on setLocale
```

- `modules/i18n/index.ts` owns locale detection, catalog loading, `t(...)`, `hydrateI18n(...)`, `I18N_LOCALE_CHANGED`, RTL `dir` for `ar`, `ur`, and `fa`, and shared date/number formatting helpers.
- **Public locales** (picker order by speaker count): `en`, `zh`, `hi`, `es`, `ar`, `fr`, `bn`, `pt`, `id`, `ur`, `ru`, `de`, `ja`, `sw`, `vi`, `tr`, `ko`, `fa`, `th`, `it`, `ms`, `pl`, `uk`. **`pseudo`** is supported for localhost/test QA only (not in the public picker).
- Catalogs live in `modules/i18n/locales/*.json`, ship as `dist/i18n/locales/*.json`, and are verified for key parity by `scripts/verify-i18n-locales.mjs` during the web build.
- `BOOTSTRAP_EN_CATALOG` embeds all `auth.*` and `errors.*` keys so sign-in, bootstrap, 2FA, and password-reset copy is available before the full catalog fetch completes.
- **Language picker:** `locale-select.ts` renders a shared accessible listbox (keyboard navigation, click-outside close) with vendored 3x2 SVG flags from `assets/flags/`. Used on the auth topbar and in Settings → Customization.
- On startup the app uses the saved `scrumboy.locale` preference when present; otherwise it normalizes the browser language.
- Locale changes hydrate static `data-i18n-*` DOM in place and trigger targeted re-renders for state-derived copy in views and dialogs that stay open; listeners must detach on dialog close.
- **`index.html`** marks the shell `translate="no"` so browser translation does not double-translate Scrumboy's own i18n UI.

See also [`docs/i18n.md`](../i18n.md) (catalogs, landings, change gates) and [`AGENTS.md`](../../AGENTS.md) (localization rules).

## API error localization

- Backend validation failures may include stable `error.details.reason` (snake_case). `apiErrorMessage()` maps known reasons to catalog keys; `apiErrorMessageOrRaw()` keeps raw backend text for dynamic import/backup diagnostics.
- Field hover hints (`field-tooltips.ts`) resolve tooltip copy through `t(...)` at render time.

## Client routes

| Path | View |
|------|------|
| `/` | projects list (full mode); anonymous server mode serves marketing landing at `/` before SPA boot |
| `/dashboard` | dashboard |
| `/{slug}` | board (`?openTodoId=` opens a todo) |
| `/{slug}/t/{id}` | board with todo open |
| `/auth/reset-password` | password reset form |

Login, bootstrap, and 2FA render as **auth overlays** when unauthenticated (not separate URL routes). Anonymous-mode server routes `/{locale}/` marketing landings in `spa.go` (see `scrumboy_http_routing.md`).

Theme preference defaults to `system` (`theme.ts` / `THEME_SYSTEM`), resolving via `prefers-color-scheme`. Effective dark leaves `data-theme` unset (CSS `:root` dark tokens); light sets `[data-theme="light"]`. Pre-hydration CSS may still look dark. Density via `--ui-scale`. PWA: `sw.js` with version injected at server startup, `manifest.json`.
