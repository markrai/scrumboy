# Scrumboy Connect central broker: initial qualification service (M3.3.A)

**Status: implemented for trusted operator qualification only. Not a public or self-service GitHub connection.** The centrally operated Scrumboy Connect GitHub App is the end-user product direction; the broker below is its first isolated runtime and authorization boundary.

## Purpose and current capabilities

The standalone `cmd/scrumboy-connect-broker` executable owns the GitHub App RSA PEM on infrastructure operated by Scrumboy. An ordinary self-hosted Scrumboy server must **never** receive this PEM. The broker has no arbitrary GitHub API proxy or token-export endpoint. The only provider operations are read-only GitHub issue/PR lookup and title search:

| Method | Path | Input | Output |
| --- | --- | --- | --- |
| `GET` | `/healthz` | none | `204` |
| `POST` | `/v1/github/items/resolve` | `{"installationId":11,"repositoryId":22,"kind":"issue","number":357}` | `{"item":...}` |
| `POST` | `/v1/github/items/search` | `{"installationId":11,"repositoryId":22,"kind":"pr","query":"bug"}` | `{"items":[...]}` |

The response includes only validated GitHub metadata, not OAuth credentials, installation tokens or secrets. The caller cannot supply a repository name or GitHub API URL.

Every non-health request must pass all gates in order:

1. **mTLS.** Direct TLS 1.3 terminates at the broker, requires a CA-verified client certificate, and checks the exact certificate SHA-256 fingerprint against an operator-provisioned instance identity. Client-provided identity headers are ignored.
2. **Rate bounds and strict input.** JSON requests are bounded at 4 KiB, permit one object only, reject unknown fields, and are subject to an in-memory 120-per-minute instance budget and 3,000-per-minute global budget.
3. **Server-owned grant.** The authenticated instance must have an explicit, unexpired grant keyed by the exact GitHub installation ID and immutable repository ID. The repository name comes only from the grant. Invalid, stale or mismatched grants fail before a GitHub request is dispatched.
4. **Least-privilege provider operation.** The existing `githubapp.Client` mints a GitHub installation token scoped to the exact repository ID with Metadata, Issues and Pull requests **read-only**, and fetches only the bounded result.
5. **Result validation.** Returned issue/PR type, number, title, state and canonical GitHub URL must match the authorized repository and request before anything is sent to the caller. Upstream errors return a generic response without secret-bearing details.

The API must not be exposed directly to untrusted browsers. An eventual Scrumboy instance-to-broker client will call it after local project permission checks, and durable grants must independently bind the user to the authorized GitHub installation.

## Isolated operator staging configuration

The qualification binary requires HTTPS certificate/key, a CA for instance client certificates, a local operator grant file, the numeric GitHub App ID, and a GitHub App RSA PEM. No credential values are baked into the source, Docker image or repository. All private files must remain outside the repository and mounted read-only where possible.

The operator-only grant file format is:

```json
{
  "clients": [
    {
      "instanceId": "vega-test",
      "certificateSha256": "REPLACE_WITH_64_LOWERCASE_HEX_CHARACTERS"
    }
  ],
  "grants": [
    {
      "instanceId": "vega-test",
      "installationId": 111,
      "repositoryId": 222,
      "repository": "example-org/disposable-repo",
      "expiresAt": "2026-11-25T00:00:00Z"
    }
  ]
}
```

These values are **illustrative**. The fingerprint is SHA-256 of the client certificate's DER representation (`openssl x509 -in client.pem -outform DER | openssl dgst -sha256`). Its corresponding private key must remain on the authorized client instance. The operator must provision a real, verified certificate and GitHub repository, use an expiration in the next 30 days, and create a different identity for every instance.

Run the isolated broker directly on the trusted host:

```bash
go run ./cmd/scrumboy-connect-broker \
  -listen 127.0.0.1:9448 \
  -tls-cert /secure/broker.crt \
  -tls-key /secure/broker.key \
  -client-ca /secure/instance-clients-ca.crt \
  -grants /secure/qualification-grants.json \
  -github-app-id 123456 \
  -github-key /secure/scrumboy-connect.pem
```

The listener defaults to localhost. TLS 1.3 and CA-verified client certificates are always required, even on loopback. Never publish the qualification endpoint publicly or configure it as an internet-facing endpoint. Use disposable repositories and test installations for qualification.

Grant snapshots are read **once on startup**. To revoke an instance or repository grant in this first slice, remove it from the operator file and **restart the broker**; the broker also refuses requests after a grant expires. There is no online self-service enrollment, persistent database, operator endpoint for issuing grants, or automatic grant refresh. A real shared service must replace this file with an authoritative revocable, user-bound grant store and account lifecycle management before public availability. Rate limits are single-process and are not yet suitable as a globally coordinated multi-replica budget.

## M3.3.B: user-bound persistent ownership (separate mode)

The broker executable supports two **mutually exclusive** authorization modes:

| Mode | Flags | Access control |
| --- | --- | --- |
| Operator qualification | `-grants /secure/qualification-grants.json` | Existing static expiring instance/repository grant snapshot |
| Durable user-bound | `-client-identities /secure/client-identities.json -binding-db /secure/connect-bindings.sqlite` | Verified mTLS instance identity **plus** persistent, revocable instance/user/installation/repository binding |

The user-bound identities file contains **only** the `clients` array from the operator snapshot example, with no `grants` field. Including operator grants in this file causes startup to fail. The SQLite path must be absolute and the file owner-only (mode 0600). An empty database grants **no** GitHub repository access. The broker creates its ownership schema on first launch and leaves the database on disk between restarts.

A user-bound resolve request supplies `{"userId":42,"installationId":111,"repositoryId":222,"kind":"issue","number":357}`; search uses the same `userId` and `query` instead of `number`. The broker validates the pinned mTLS instance identity, then checks the exact user/install/repo tuple against the SQLite store. User, installation, instance or individual repository revocation is enforced immediately by that store. No static operator-grant fallback is permitted.

Bindings are written only by the **internal** `BindingCoordinator` after `githubconnect.Service.Complete` verifies GitHub identity and installation access and lists user-accessible repositories. It then validates and intersects metadata reported by the Scrumboy Connect App. Authentication and callback wiring to a browser session are not implemented. Merely knowing a local user ID, installation ID or repository URL cannot create a binding. This service **must not be advertised as active** or exposed for ordinary users until the subject/session verification and callback design are qualified.

The temporary bindings expire after 24 hours because no GitHub refresh token is retained in this slice. Continuous private-repository access and automatic reauthorization require a separately reviewed token lifecycle and live revocation strategy.

## Security tests

```bash
go test ./internal/integration/githubbroker ./internal/integration/githubconnect ./internal/integration/githubapp
go test ./... -count=1 -timeout=10m
```

Automated tests cover real TLS 1.3 handshakes with trusted, missing and unpinned client certificates; mTLS fingerprint rejection, spoofed forwarded identity, cross-instance access, cross-installation/repository access, expired/duplicate grants, malformed and oversized payloads, forged provider responses, generic upstream error handling, concurrency-safe budgets, and incomplete installation and repository discovery in the companion OAuth verifier.

**Required next work:** verified Scrumboy user/session callbacks, durable instance/user/install/repository ownership bindings and revocation; a credentialed Scrumboy-to-broker client integrated with the existing project permission checks; and multi-tenant acceptance with two unrelated GitHub accounts. The current operator snapshot is **not** proof of GitHub installation ownership by an end user, so M3.3 and the shared-App release gate remain open.

See [shared-App architecture](github-shared-app.md) and [M3 acceptance](github-m3-qualification.md).
