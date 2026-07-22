# Scrumboy documentation

Index of contributor and operator docs under `docs/`. Verify claims against current `main` or the release tag when touching migrations, routes, or env contracts—do not treat hand-maintained calendar dates on individual pages as freshness signals.

**Parity check** (diagram catalog, MCP tool names, Mermaid/markdown pins, internal links):

```powershell
node docs/scripts/verify-docs.mjs
```

See also [CONTRIBUTING.md](../CONTRIBUTING.md) (documentation-impact gate).

| Field | Meaning |
|-------|---------|
| **Audience** | Who the page is for |
| **Source of truth** | Code package, script, or env contract to trust over prose |
| **Status** | `current` = maintained narrative; `checklist` = manual/procedural |

---

## Operating / deployment

| Doc | Audience | Source of truth | Status |
|-----|----------|-----------------|--------|
| [recovery.md](recovery.md) | Operators / owners | `cmd/scrumboy` recover-owner; `DATA_DIR` | current |
| [smtp.md](smtp.md) | Operators | `internal/mailer`, SMTP env vars | current |
| [vapid.md](vapid.md) | Operators | `prepareWebPushConfiguration` / push status | current |
| [pwa.md](pwa.md) | Operators / UX | `sw.js`, push client, Compose env | current |
| [oidc.md](oidc.md) | Operators | OIDC env + auth handlers | current |
| [keycloak/readme.md](keycloak/readme.md) | Operators (dev IdP) | Keycloak fixture under `docs/keycloak/` | current |

Persistence restore matrix (SQLite, wallpapers, encryption key, Mermaid override): [diagrams/scrumboy_deployment_ops.md](diagrams/scrumboy_deployment_ops.md#persistence-matrix).

---

## User features

| Doc | Audience | Source of truth | Status |
|-----|----------|-----------------|--------|
| [wall.md](wall.md) | Users / contributors | wall modules + API | current |
| [voiceflow.md](voiceflow.md) | Users / contributors | VoiceFlow parser / UI | current |
| [markdown-and-mermaid.md](markdown-and-mermaid.md) | Users / contributors | `internal/httpapi/web` markdown/mermaid deps | current |

---

## Security / permissions

| Doc | Audience | Source of truth | Status |
|-----|----------|-----------------|--------|
| [security.md](security.md) | Operators / contributors | auth, crypto, workflows, scanners | current |
| [roles-and-permissions.md](roles-and-permissions.md) | Contributors / operators | authz / project roles | current |
| [audit-trail.md](audit-trail.md) | Operators / security | `audit_events` / insert path | current |

---

## Integrations

| Doc | Audience | Source of truth | Status |
|-----|----------|-----------------|--------|
| [mcp.md](mcp.md) | Agents / integrators | `internal/mcp` `implementedTools()` | current |
| [oauth.md](oauth.md) | Integrators | `internal/oauth` | current |
| [agoragentic.md](agoragentic.md) | Agents | Agora manifest + MCP bridge | current |
| [authentication-api.md](authentication-api.md) | Integrators | `/api/auth/*` handlers | current |
| [mcp-oauth-acceptance.md](mcp-oauth-acceptance.md) | Release / QA | acceptance evidence checklist | checklist |

---

## Architecture

| Doc | Audience | Source of truth | Status |
|-----|----------|-----------------|--------|
| [diagrams/](diagrams/) | Contributors | `docs/diagrams/catalog.json` + Mermaid sources | current |
| [i18n.md](i18n.md) | Contributors | `modules/i18n/*`, `AGENTS.md` (rules) | current |

Viewer: `cd docs/diagrams` then `python serve.py` (or `serve-diagrams.bat`), open `http://127.0.0.1:8775/`.

---

## Manual checks

| Doc | Audience | Source of truth | Status |
|-----|----------|-----------------|--------|
| [wall-viewport-manual-checklist.md](wall-viewport-manual-checklist.md) | QA / contributors | wall viewport UI behavior | checklist |

---

## Related root docs

- [README.md](../README.md) — install, config, feature overview
- [API.md](../API.md) — HTTP/MCP API reference
- [FAQ.md](../FAQ.md) — common operator questions
- [SECURITY.md](../SECURITY.md) — vulnerability disclosure policy
- [docs/security.md](security.md) — technical security architecture and practices
- [CONTRIBUTING.md](../CONTRIBUTING.md) — build, DCO, docs-impact gate
- [AGENTS.md](../AGENTS.md) — i18n localization rules (SoT for copy/plumbing constraints)
