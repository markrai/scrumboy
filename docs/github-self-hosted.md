# M3.3.D: Self-hosted Scrumboy Connect broker transport and instance credentials

**Status: transport and operator-managed identity lifecycle implemented; end-to-end user connection not enabled.** The missing M3.3.C browser-session/authorization integration remains a **release blocker**. Deferring live acceptance for M3.3.C is not evidence that its security boundary was implemented or qualified.

## Trust model

A self-hosted Scrumboy instance uses `internal/integration/githubclient` to call the centrally operated Scrumboy Connect broker. The client offers only fixed read-only issue and pull-request lookup/search plus bound OAuth begin/complete and disconnect operations. It does not accept arbitrary GitHub URLs or API paths and does not expose or receive installation access tokens.

- **Client instance:** owns only its instance-specific mTLS private key, local Scrumboy user sessions and project authorization. The TLS client verifies the broker certificate against a configured trust root and requires TLS 1.3. Redirections, plaintext HTTP, excessive responses, untrusted broker metadata and arbitrary provider operations are refused.
- **Central broker:** owns the GitHub App private key and OAuth client secret. Every request requires a CA-verified, broker-registered client certificate plus the existing instance/user/installation/repository ownership grant. User and repository authorization remains authoritative on the broker; knowing an installation ID or a local user ID is not sufficient.
- **Operator provisioned instance registry:** the broker SQLite database stores a SHA-256 fingerprint of each approved **public client certificate**, its instance identifier, expiry and revocation status. It never stores the client certificate's private key. The operator must confirm which self-hosted server they are registering out of band. Merely possessing a public certificate does not create an active registration.

**M3.3.C is still needed:** the instance-to-broker client must be invoked exclusively by local Scrumboy server handlers that have authenticated the user, checked the project and verified the associated installation/repository. The current client library does **not** authenticate browser sessions. Do not wire it directly to anonymous browser input, expose arbitrary `userId` values, or enable ordinary-user Connect GitHub yet.

## Broker deployment: persistent and revocable mTLS identities

Use the same centrally operated App credentials and the user-bound broker flags described in [the broker guide](github-broker.md). For online-revocable mTLS identities, **omit** `-client-identities` and set `-dynamic-clients`. This is a mutually exclusive mode; it does not read legacy operator grants.

`-binding-db /secure/connect-bindings.sqlite` must be an absolute path owned by the broker operator. New databases begin with no approved clients and no GitHub grants. A client certificate must be issued from the broker's trusted client CA and kept on its individual Scrumboy host. Its fingerprint must be registered by the broker operator.

Example operator-side certificate registration:

```bash
go run ./cmd/scrumboy-connect-instances \
  -action register \
  -binding-db /secure/connect-bindings.sqlite \
  -instance vega-scrumboy-test \
  -certificate /secure/vega-client-public.pem
```

This records the **public certificate only**, not the associated private key. The certificate must permit client authentication, have an active validity period and expire within 90 days. At most two active certificates per instance permit short overlap during rotation.

Revoke one client certificate without deleting all user bindings:

```bash
go run ./cmd/scrumboy-connect-instances \
  -action revoke \
  -binding-db /secure/connect-bindings.sqlite \
  -instance vega-scrumboy-test \
  -certificate /secure/vega-client-public.pem
```

For a complete instance disconnect, atomically revoke all its registered certificates and all its persisted user/installation grants:

```bash
go run ./cmd/scrumboy-connect-instances \
  -action disconnect \
  -binding-db /secure/connect-bindings.sqlite \
  -instance vega-scrumboy-test
```

These operations take effect for **subsequent broker requests**, including requests on already established TLS connections. A revoked certificate is never silently re-registered; rotate using a new certificate and independently authorize the instance. The broker checks the SQLite registry on every request, and the shared GitHub App private key stays solely on the broker host. Provision and operate the broker behind a dedicated HTTPS origin; use a test CA and disposable GitHub installation for staging.

The command is **operator-only**. It is not a self-service instance-enrollment authority. Future fully public operation requires instance registration tied to verified ownership, revocable user/instance enrollment, coordinated rate limits and safe broker service operations.

## Self-hosted client configuration

Create a TLS keypair for the individual Scrumboy instance, have its public client certificate signed by the trusted broker CA, then configure its server-only runtime to load that keypair and the broker's server CA. The application passes those credentials to:

```go
client, err := githubclient.New(githubclient.Config{
    Endpoint: "https://connect.example.net",
    Certificate: certificate,
    ServerRoots: serverRoots,
})
```

The hostname is illustrative. The implementation does not hardcode a public broker URL and must not be treated as a hosted service endpoint. No PEM from the **GitHub App** belongs on a self-hosted machine.

The authenticated server is responsible for supplying the *actual local user ID* and authorizing the Scrumboy project before calling `Resolve`, `Search`, `Begin`, `Complete` or `Revoke`. The new client package is **not yet wired to those authenticated routes**, by design.

## Security and qualification status

Automated tests cover CA-verified TLS 1.3, certificate-present client authentication, broker certificate mismatch, HTTP redirect refusal, method and payload constraints, bounded response handling, durable certificate rotation and revocation, cross-instance isolation and full disconnect. No secrets or installation tokens are stored in Scrumboy's browser or SQLite.

**Deferred, not waived:** M3.3.C's session-bound callback and cross-tenant end-to-end acceptance have not happened. They must pass before enabling public shared-App access. Also outstanding: authenticated self-service instance enrollment, distributed broker rate limits, complete GitHub revocation observation, durable credentials/rotation for the App service and live hosted plus self-hosted validation.
