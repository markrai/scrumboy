# GitHub inbound webhooks: M4 operator and safety guide

M4 is a separate, **opt-in** inbound webhook capability. It does not replace M3's explicit **Connect and sync** / **Refresh repositories** actions, and does not create, edit, delete or publish Scrumboy cards.

## Configuration and rollout

The GitHub App must already be configured and Scrumboy must run in Full Mode. With neither `SCRUMBOY_GITHUB_WEBHOOK_SECRET` nor `SCRUMBOY_GITHUB_WEBHOOK_SECRET_FILE` configured, the inbound receiver is **disabled** and returns HTTP 404. The App can still browse repositories without webhooks.

1. Generate a strong random secret, for example `openssl rand -hex 32`. Store the resulting printable value in a host file, outside both the source tree and Scrumboy data/backups. Restrict the host file permissions to the Scrumboy process.
2. In GitHub App settings, enable **Webhook → Active** and paste **the exact same secret** into the Webhook Secret field. A trailing newline in the file is trimmed at startup; internal whitespace is significant.
3. Configure a **publicly reachable HTTPS** endpoint: `https://YOUR-SCRUMBOY-HOST/api/integrations/github/webhook`. GitHub cannot deliver to a private Tailscale-only port or to Vega's localhost HTTP port 9447. Use a protected staging HTTPS host for initial live acceptance.
4. Subscribe to **Installation**, **Installation repositories**, and **Repository** events for M4, and also **Issues** and **Pull request** for M5 notifications. Read-only Issues/Pull requests permissions suffice; no write permissions are required.
5. Choose **one** secret source: set `SCRUMBOY_GITHUB_WEBHOOK_SECRET` to the printable secret for simple self-hosted deployments, **or** mount it read-only and set `SCRUMBOY_GITHUB_WEBHOOK_SECRET_FILE` to the container path for stronger production secret isolation. Setting both fails startup. The App private key (`SCRUMBOY_GITHUB_APP_PRIVATE_KEY_FILE`) is separate. Environment values may be visible through container inspection and must never be printed in diagnostics.
6. Restart the isolated instance, then send a GitHub **ping** and an installation-repository event to verify delivery. Never deploy untested changes directly to a production database.

Example Compose override, not to be committed with real paths or secrets:

```yaml
services:
  scrumboy:
    environment:
      SCRUMBOY_GITHUB_WEBHOOK_SECRET_FILE: /run/secrets/github-webhook-secret
    volumes:
      - /secure/github-webhook-secret:/run/secrets/github-webhook-secret:ro
```

If only the App ID, App private key and slug are configured, webhooks remain **off**. To disable ingestion, remove whichever webhook secret variable is set and restart; disabling the webhook at GitHub also stops new deliveries.

## Security and privacy

- The receiver reads **at most 256 KiB** and validates `X-Hub-Signature-256` with constant-time HMAC-SHA256 over the **raw bytes before JSON decoding**. POST requests missing a valid signature are rejected. Only the exact URL is exempt from the browser-oriented `X-Scrumboy` CSRF header; the secret authenticates it.
- The event type is restricted to `installation`, `installation_repositories` and `repository`; a valid `ping` is acknowledged without storage. Unrelated event types are acknowledged and ignored.
- The durable inbox (migration `092`) holds only event type/action, the connected GitHub installation ID, delivery identifier, SHA-256 payload digest, retry state and timestamps. **It never persists the raw GitHub JSON, webhook secret, installation access token, JWT or private key.**
- Delivery IDs deduplicate resends; the same ID with a different body hash is rejected. Unknown or disconnected installation IDs are acknowledged but not queued. Installation ownership is checked again before reconciliation.
- The inbox caps pending/leased records at **5,000**, processes up to **5** per poll, uses a **45-second lease**, exponential retries capped at **8 attempts** and **30 minutes**, and prunes completed/failed records after approximately **30 days**. Unfinished leases are reclaimable after expiry on restart.
- Webhook processing obtains fresh provider data through the read-only GitHub App adapter. It can **add/update repository metadata**, but it does not delete repositories or card/project references automatically. A complete, user-triggered **Refresh repositories** marks inaccessible repositories unavailable and retains existing project/card references as documented in M3.1. Only explicit unlink/detach/disconnect operations remove those links.
- Signed installation `deleted` or `suspend` events atomically disable the local connection and mark its repositories unavailable without deleting existing references. Re-enable explicitly through the GitHub App UI and a manual **Connect and sync** after restoration.
- GitHub webhooks can feed M5 notification rules **only after signature verification, installation/repository association and explicit rule enablement**. They cannot edit cards, change public-board visibility or modify GitHub issues and pull requests.

When an App is created using the guided GitHub App Manifest flow, the generated webhook secret is saved automatically in the private `DATA_DIR/.scrumboy-github-app` bundle and the receiver is activated without restarting Scrumboy. The manual webhook secret variables are unnecessary for that App and must not conflict with a saved guided secret. A public HTTPS route is still required for GitHub-originated event delivery.

## Monitoring and failure handling

The authenticated Owner/Admin `GET /api/admin/integrations/github` returns a redacted `readiness` object with `state`, `message`, `appConfigured`, `webhookConfigured`, `externalReachability: "unverified"`, `subscriptions: "unverified"`, `activeInstallations`, and `webhookPath`. If `SCRUMBOY_PUBLIC_BASE_URL` is a valid pathless HTTPS origin, the server also returns `webhookUrl`. No secret, key, or private filesystem path is exposed. The project-authorized `GET /api/board/{slug}/integrations/automation-rules` provides a reduced `readiness` without administrator URLs or installation counts.

A correctly HMAC-authenticated, syntactically valid GitHub event (including `ping`) updates process-local `lastVerifiedAt`. This timestamp **resets on restart** and does not prove current reachability, accurate event subscriptions, repository authorization, or a matching automation rule. GitHub's delivery history is authoritative for diagnosing missing deliveries.

Use these HTTP responses when debugging: an unsigned JSON POST to the enabled route returns **401**; a missing receiver returns **404**; an HTML **200** means a proxy or subpath reached the frontend rather than the webhook handler. The exact route is `/api/integrations/github/webhook`, without the PWA `/scrumboy` subpath.


Authenticated instance Owners/Admins can query:

`GET /api/admin/integrations/github/webhooks`

When enabled, the response has `configured: true` and four counters: `pending`, `leased`, `completed`, `dead`, plus a maximum of 20 recent `deadDeliveries`. With the feature disabled it returns `{"configured":false}`. Each dead-delivery record contains the numerical internal ID, delivery ID, installation ID, event name/action, retry count and a generic error class. No secret or inbound payload is returned.

Authenticated Owners/Admins can manually requeue a dead delivery with `POST /api/admin/integrations/github/webhooks/{id}/retry` and the normal `X-Scrumboy: 1` header. This resets the retry budget and schedules a fresh read-only reconciliation. The mutation rechecks Owner/Admin permissions at the database transaction boundary; it does not let the operator fabricate or modify the original webhook payload.

Transient GitHub API errors or incomplete discovery are retried. Exhausted deliveries become `dead` and remain visible in the counts until retention cleanup. A failed scan must never be converted into an empty repository list or a cascade deletion of existing card references. Investigate the provider and secret configuration before retrying; repeated manual requeues should not be used to mask permanent authorization failures.

## Acceptance checklist

- [ ] Full Mode and App configured; secret absent means endpoint 404.
- [ ] Valid signed delivery is accepted without session-cookie auth or `X-Scrumboy`; missing/forged signature is rejected.
- [ ] Oversized, malformed and unrelated events fail safely; no secrets appear in logs or responses.
- [ ] Duplicate delivery is idempotent; same ID with different verified payload is rejected.
- [ ] Unknown installation is ignored; disconnected/suspended installation cannot update resources.
- [ ] Durable records survive restart; abandoned leases are reclaimable; retry exhaustion is bounded.
- [ ] Rename or newly authorized repository updates metadata without rewriting user card content.
- [ ] Removed/revoked repository after webhook leaves card references intact for explicit operator review.
- [ ] Owner/Admin monitoring and manual requeue endpoints are permission checked.
- [ ] Docker and migration tests, Go and frontend suites, CodeQL and GitHub required checks pass.
- [ ] GitHub staging HTTPS ping and repository event are received and verified with real GitHub delivery logs.

See [M3 qualification](github-m3-qualification.md) for the earlier manual repository-to-card test gate.
