# Scrumboy architecture diagrams

Mermaid diagrams for the Scrumboy monolith (Go server + embedded TypeScript SPA + SQLite).

**Interactive viewer:** open [`index.html`](index.html) via a local HTTP server (browser cannot load markdown from `file://`).

**Easiest (Windows):** double-click [`serve-diagrams.bat`](serve-diagrams.bat) in **this** folder. It runs [`serve.py`](serve.py), which always serves from the `docs/diagrams` directory next to the script (not whatever folder your terminal was in).

Or manually from the repository root:

```powershell
cd docs/diagrams
python serve.py
```

```sh
cd docs/diagrams
python serve.py
```

Then open **http://127.0.0.1:8775/** (the script may open it for you).

### Sanity check before serving

```powershell
cd docs/diagrams
dir
```

You should see **`index.html`**, **`catalog.json`**, **`serve.py`**, and the `scrumboy_*.md` diagram sources.

| If `dir` shows… | You are… |
|-----------------|----------|
| Only `README.md` | **Wrong folder** - open `docs/diagrams` inside the Scrumboy repo |
| `index.html` + `catalog.json` + many `scrumboy_*.md` | Correct - run `python serve.py` here |

### Diagram catalog (single source of truth)

The viewer loads [`catalog.json`](catalog.json) over HTTP. That file lists every architecture diagram (`file`, `title`, `desc`, `category`). Do **not** maintain a second file list in this README or hardcode a `CATALOG` object in `index.html`.

**To add a diagram:**

1. Add `scrumboy_<name>.md` in this folder.
2. Register it in [`catalog.json`](catalog.json) under the appropriate category.
3. Run `node docs/scripts/verify-docs.mjs` from the repo root.

### Yes/no branch label colors

The viewer applies semantic edge coloring via `mermaid-semantic-edges.js` + `mermaid-semantic-edges.json`: paired branch labels (`yes`/`no`, `true`/`false`, `pass`/`fail`) get green/red **label backgrounds** after render.

**Divergence (intentional for the docs viewer):** this helper is **not** kept in lockstep with `internal/httpapi/web/modules/mermaid-semantic-edges.ts`. The SPA module only recolors label backgrounds. The docs helper adds extra **layout** behavior (foreignObject/`labelBkg` sizing and min widths, border-radius, paint retries aimed at Mermaid HTML labels). Treat config pair/color defaults as shared; treat paint/layout code as viewer-specific. Do not assume “keep in sync” when changing either side.

### Viewer CDN dependencies and trust model

`index.html` loads Mermaid from jsDelivr pinned to **11.16.0** (aligned with `internal/httpapi/web/package.json`) and Marked pinned to **18.0.7**. A network connection is required for those CDN assets unless you vendor them yourself.

Markdown prose from the repository is treated as **trusted content**: `marked.parse(prose)` is assigned to the DOM without an extra sanitizer. Do not point the viewer at untrusted markdown.
