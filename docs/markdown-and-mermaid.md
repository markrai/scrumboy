# Todo notes Markdown + Mermaid preview (Phase 1 / 1B)

Scrumboy provides a **client-side preview** of Markdown in the todo dialog **Notes** field. Mermaid is a **sub-feature** of that preview. Both are on by default and can be turned off by the operator. Notes remain stored and served as raw Markdown text. The browser handles Markdown and Mermaid rendering, while the server separately parses Markdown image references for asset authorization, lifecycle management, and backup portability.

For operator setup, see [`FAQ.md`](../FAQ.md). This document describes architecture, configuration, and the preview security model.

---

## Scope

| In scope | Out of scope |
|----------|----------------|
| **markdown** / **preview** tabs in the Edit/New Todo dialog (`#todoBody`, `#todoBodyPreview`) | Rendering Markdown on the board, dashboard, or notifications |
| Preview of `todos.body` when the user opens the **preview** tab | Server-side Markdown → HTML conversion |
| Sanitized HTML in the preview pane only | WYSIWYG editing: notes are edited as Markdown text; only Scrumboy images are shown in place ([inline-images.md](inline-images.md)) |
| Optional Mermaid rendering for fenced ` ```mermaid ` blocks in the preview pane only | Rendering Mermaid on cards, notifications, exports, or any server path |
| Raw Markdown persisted on create/patch | Raw HTML, external images, or non-http(s) links in preview |
| Scrumboy-managed images (`/api/assets/{uuid}`) when inline images are enabled ([inline-images.md](inline-images.md)) | Any other image source |

Todo **titles** and card titles on the board are always escaped plain text (see `board-rendering` tests).

---

## Configuration

| Variable | Default | Enabled when |
|----------|---------|--------------|
| `SCRUMBOY_MARKDOWN_NOTES_ENABLED` | on (unset or empty) | unless set to `0`, `false`, `off`, or `no` (trimmed, case-insensitive) |
| `SCRUMBOY_MERMAID_NOTES_ENABLED` | on (unset or empty) | unless set to `0`, `false`, `off`, or `no`, **and** only while Markdown notes are enabled |

`1`, `true`, `on` and `yes` also enable. Any other value disables that feature and logs a startup warning. Parsed in `internal/config/config.go` (`defaultOnFeatureFromEnv`); Mermaid is already off in the parsed config when Markdown is off. The values are passed through `cmd/scrumboy/main.go` into the HTTP server (`ServerOpts.MarkdownNotesEnabled`, `ServerOpts.MermaidNotesEnabled`), and exposed to the SPA as **`markdownNotesEnabled`** and **`mermaidNotesEnabled`** on:

- `GET /api/auth/status` (authenticated and anonymous status payloads in `internal/httpapi/routing_auth.go`)

The UI reads those flags once during auth bootstrap (`modules/router.ts` → `setMarkdownNotesEnabled` / `setMermaidNotesEnabled`). Markdown gates tab visibility in `modules/dialogs/todo.ts` (`markdownNotesPreviewEnabled()`); Mermaid only affects whether fenced Mermaid blocks are upgraded inside the preview pane.

There is **no per-project** or per-user toggle; it is instance-wide.

---

## Data flow

```
User edits #todoBody (textarea), or the visual notes editor (#todoBodyEditor) when inline images are on
        │
        ▼
Create/Patch API ──► todos.body (raw Markdown string, unchanged)
        │
        ▼ (preview tab only, client-side)
renderMarkdownPreviewInto()
        │
        ├─► markdown-it.render()  (html: false)
        └─► DOMPurify + link policy + DOM cleanup
                │
                ├─► (optional) detect Mermaid placeholders
                ├─► lazy-load /vendor/mermaid.min.js on first Mermaid preview use
                └─► mermaid.run() in preview-only hosts (strict mode)
                │
                ▼
        #todoBodyPreview innerHTML (ephemeral, not saved)
```

`modules/dialogs/todo-submit.ts` passes `body` through unchanged in create and patch payloads. Tests in `todo-submit.test.ts` assert Markdown is not converted to HTML before send.

---

## Frontend

### DOM (`internal/httpapi/web/index.html`)

- **Label row:** `Notes` label and `#todoBodyToggle` (tab list: `#todoBodyWriteTab`, `#todoBodyPreviewTab`).
- **Editor:** `#todoBody` (textarea), `#todoBodyEditor` (visual notes editor) and `#todoBodyPreview` (preview `div`) inside `.todo-notes-editor`.
- **Visual notes editor** (`modules/dialogs/todo-notes-editor.ts`, used when inline images are enabled and `/vendor/codemirror.min.js` loaded): a CodeMirror 6 Markdown source editor. Image references found by `modules/note-images.ts` (only where markdown-it, with the preview's configuration, also sees an image) are replaced by atomic image widgets built in code; their files come from `ServerTransport.acquireResource()` and are released when no widget shows them (after 30 s) and immediately when the dialog closes. The note text is the editor document, so the saved body is exactly the loaded text until it is edited. Otherwise the textarea is used, unchanged.
- Tabs are hidden when `markdownNotesEnabled` is false (`hidden` on `#todoBodyToggle`).

### Todo dialog (`modules/dialogs/todo.ts`)

- **`todoNotesMode`:** `"markdown"` | `"preview"`.
- **`syncTodoNotesModeUI()`:** toggles `hidden` on the editor in use (textarea or visual editor) vs preview, `is-active` / `aria-pressed` on tabs.
- **`getTodoNotesValue()`:** the body to save, from whichever editor is in use (`app.js` submit and the close guard use it).
- **`renderTodoNotesPreview()`:** calls `renderMarkdownPreviewInto`; on vendor/render failure, shows a toast and falls back to markdown mode.
- **`input` listener:** re-renders preview live while the preview tab is active; stale async Mermaid renders are cancelled by container-scoped render epochs.

### Preview module (`modules/markdown-preview.ts`)

Exported API:

- `renderMarkdownToSafeHtml(markdown: string): string`
- `renderMarkdownPreviewInto(container: HTMLElement, markdown: string, options?: { mermaidEnabled?: boolean }): Promise<void>`

Empty or whitespace-only notes set `todo-markdown-preview--empty` and clear the container (placeholder via CSS `::before`).

### Styles (`styles.css`)

Preview typography and colors are scoped under `#todoDialog .todo-markdown-preview` (headings, lists, blockquote, code, `pre`, links, `hr`).

### State

- `current._markdownNotesEnabled` in `modules/state/state.ts`, set from auth status only (not from board payloads).
- `current._mermaidNotesEnabled` in `modules/state/state.ts`, also set from auth status only.

---

## Rendering pipeline

### 1. markdown-it

Loaded from `/vendor/markdown-it.min.js` (global `window.markdownit`). Pinned dependency: `markdown-it@14.3.2` in `internal/httpapi/web/package.json`, copied by `scripts/sync-vendor.mjs`.

Instance options (`getMarkdownRenderer()`):

| Option | Value | Rationale |
|--------|-------|-----------|
| `html` | `false` | Disables raw HTML blocks in source Markdown |
| `breaks` | `true` | Single newlines become `<br>` |
| `linkify` | `false` | Bare URLs are not auto-linked (explicit `[text](url)` only) |

**Custom rule:** `renderer.rules.image` rewrites image tokens to **escaped plain text** (`![alt](url)`) so no `<img>` is emitted.

**Inline images** (only when `inlineImagesEnabled`): an image whose destination is exactly `/api/assets/{lowercase uuid v4}` becomes a random-nonce placeholder inside `<code>`. After DOMPurify and the post-sanitize pass, code creates the `<img>` element (alt and title set as DOM properties) and loads it through `ServerTransport.acquireResource()`, so browsers and the Capacitor app share one path; every acquisition is released on re-render, tab switch and dialog close. Every other image destination keeps the escaped-text rendering. The DOMPurify allowlist and the purge pass are unchanged, so Markdown can never produce an `<img>` itself. The server applies the same rule (`internal/assetrefs`, goldmark) to decide which assets a note embeds; `internal/assetrefs/testdata/fixtures.json` is a shared parity corpus tested in Go and vitest.

### 2. DOMPurify

Second pass via `/vendor/purify.min.js` (`dompurify@3.4.17`):

- **`ALLOWED_TAGS`:** `a`, `blockquote`, `br`, `code`, `em`, `h1`–`h6`, `hr`, `li`, `ol`, `p`, `pre`, `strong`, `ul`
- **`ALLOWED_ATTR`:** `href`, `rel`, `target` only
- ARIA and `data-*` attributes disallowed

### 3. Post-sanitize DOM pass

On a detached `<template>`:

1. Remove any `img`, `iframe`, `object`, `embed`, `script`, `svg` that survived parsing.
2. For each `<a>`:
   - **`isSafeLinkHref`:** allow empty-scheme-relative paths (`/`, `./`, `../`, `#`, `?`); allow `http`/`https` only for explicit schemes; reject `//`, `javascript:`, `data:`, `mailto:`, `tel:`, etc.
   - **External** (`http://` / `https://`): set `target="_blank"` and `rel="noopener noreferrer"`.
   - **Unsafe href:** replace anchor with text node (link text only).

### 4. Mermaid (optional, preview-only)

When `mermaidEnabled` is true and the Markdown contains fenced Mermaid blocks:

1. `renderer.rules.fence` emits opaque placeholders for ` ```mermaid ` blocks while keeping non-Mermaid fences as normal code blocks.
2. After sanitized HTML is placed into the preview container, placeholders are replaced with local Mermaid hosts.
3. `/vendor/mermaid.min.js` is lazy-loaded on demand.
4. Mermaid initializes with preview theme tokens (`theme: "base"` + `themeVariables` from `--panel2`, `--text`, `--accent`, `--border`) so diagram backgrounds match the preview pane in light and dark mode. Re-initializes when the effective theme changes.
5. All user-authored Mermaid directive blocks in the `%%{...}%%` form are stripped before render so site security settings remain authoritative.
6. Preview-side limits cap Mermaid work per note: only the first 4 Mermaid fences render, each fence is capped at 4000 characters, and the total Mermaid preview budget is capped at 8000 characters.
7. Limit overages and diagram syntax failures do **not** tear down preview mode; the preview shows a local warning/error block with the original source.
8. Changing app theme while the todo preview is open re-renders diagrams via the same preview pipeline (render epochs prevent stale async work from committing).
9. Optional yes/no-style branch **label backgrounds** only: `/mermaid-semantic-edges.json` (override via `$DATA_DIR/mermaid-semantic-edges.json`; see `data/mermaid-semantic-edges.json.example`). Applied after render via DOM; does not modify diagram source or connector lines.

---

## Supported Markdown (preview)

Verified in `modules/markdown-preview.test.ts`:

- ATX headings `#` … `######`
- `**bold**`, `*italic*`
- Bullet and ordered lists
- Blockquotes (`>`)
- Inline `` `code` `` and fenced code blocks
- Thematic breaks (`---`, `***`, `___`) on their own line (CommonMark rules; blank lines often required)
- `[label](https://…)` and safe same-origin relative links

**Not rendered as HTML in preview:**

- `![alt](url)` → escaped literal
- Raw HTML in source → escaped in output
- Dangerous or non-web link schemes → plain text
- Protocol-relative URLs (`//host/...`)
- Mermaid directives (`%%{...}%%`) are ignored for rendering; site-level Mermaid config wins

---

## Security notes

- **XSS surface** is limited to the preview `div` in the todo dialog; content is never written back to the server as HTML.
- **CSP / trust:** preview depends on vendored `markdown-it` and DOMPurify; `npm test` in `internal/httpapi/web` runs `verify-vendor.mjs` before Vitest.
- **Link exfiltration:** only `http`/`https` external navigation; `noopener noreferrer` on external tabs.
- **No Markdown on titles** avoids XSS or layout surprises on shared boards and SSE-driven card updates.
- **Mermaid isolation:** in Mermaid `11.17.2`, the preview uses `securityLevel: "strict"` (Mermaid's secure default), which encodes HTML in diagram text and renders inline SVG. Diagram output is never passed through the general Markdown allow-list as arbitrary SVG/HTML. Inline (rather than sandboxed-iframe) rendering is what lets the semantic label coloring recolor matched label backgrounds via the DOM after render.
- **Mermaid scope:** diagrams only render in the todo dialog preview, never on the board or server.

---

## Build, vendor, and PWA

| Asset | Role |
|-------|------|
| `/vendor/markdown-it.min.js` | Parser (eager script in `index.html`) |
| `/vendor/purify.min.js` | Sanitizer (eager script in `index.html`) |
| `/vendor/mermaid.min.js` | Mermaid runtime (lazy-loaded only when Mermaid preview is needed) |
| `/dist/markdown-preview.js` | Compiled module (TypeScript → `dist/`) |

Service worker (`sw.js`) precaches the eager Markdown assets and `dist/markdown-preview.js`. Mermaid is **not** install-time precached. It is fetched lazily, and because `/vendor/mermaid.min.js` is handled by the service worker's same-origin cache-first asset branch, a successful first fetch while the service worker is active makes it available for later offline reuse from cache.

Manual verification for offline reuse:

- Load a note that contains a Mermaid fence while online and wait for the preview to render once.
- Disconnect or simulate offline mode, then reopen the same note's preview.
- Expected result: the preview can still resolve `/vendor/mermaid.min.js` from cache; if the runtime was never fetched successfully before going offline, Mermaid preview is not guaranteed.

Regenerate vendor files after dependency bumps:

```bash
cd internal/httpapi/web
npm run sync:vendor
npm run verify:vendor
```

---

## Tests

| Location | Covers |
|----------|--------|
| `internal/config/config_markdown_enabled_test.go` | Markdown + Mermaid env parsing |
| `internal/httpapi/routing_auth_markdown_test.go` | `markdownNotesEnabled` and `mermaidNotesEnabled` on auth status |
| `modules/markdown-preview.test.ts` | Supported subset, links, HTML/images neutralization, Mermaid fences, placeholder spoofing regression, full directive stripping, preview-size limits, async rerender cancellation |
| `modules/dialogs/todo-markdown-preview.test.ts` | Dialog gating, preview vs textarea, raw body on save, preview resource release |
| `modules/markdown-preview-images.test.ts` | Inline images: canonical-only rendering, placeholder spoofing, sanitizer unchanged, acquire/release lifecycle, stale renders, stored display widths, Go/markdown-it parity fixtures |
| `modules/note-images.test.ts` | Width suffix grammar; which references the visual editor shows (code, escapes, containers, tables); parity with the Go extractor fixtures |
| `modules/dialogs/todo-notes-editor.test.ts` | Visual editor with real CodeMirror: exact text round trip (CRLF, LF, lone CR and mixed line endings kept through edits, including randomized edits; Unicode), selection, delete, undo/redo, keyboard resize, upload reservations and order, selected text replaced only on upload success (failure, cancellation, concurrent uploads, edits during upload), read-only, resource release |
| `modules/dialogs/todo-submit.test.ts` | API payloads keep raw Markdown / raw note bodies |
| `modules/views/board-rendering.test.ts` | Card titles do not render note Markdown or Mermaid |

---

## Key source files

| Area | Path |
|------|------|
| Env / config | `internal/config/config.go` |
| Server flag | `internal/httpapi/server.go`, `cmd/scrumboy/main.go` |
| Auth JSON | `internal/httpapi/routing_auth.go` |
| Preview core | `internal/httpapi/web/modules/markdown-preview.ts` |
| Todo UI | `internal/httpapi/web/modules/dialogs/todo.ts` |
| Markup | `internal/httpapi/web/index.html` |
| Vendor sync | `internal/httpapi/web/scripts/sync-vendor.mjs` |
