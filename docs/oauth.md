# OAuth 2.1 for MCP Clients

Updated: 2026-07-16

Scrumboy's MCP endpoint (`/mcp`, `/mcp/rpc`) supports OAuth 2.1 for clients that implement automatic discovery, PKCE, and Dynamic Client Registration (DCR) — for example Claude Code's `claude mcp add --transport http` flow. This is an alternative to manually minting a static API token via `POST /api/me/tokens`; no manual client registration is needed — the client self-registers and the user approves access through a normal browser consent screen. **No environment variables are required for a direct-TLS or loopback/localhost deployment.** When `SCRUMBOY_TRUST_PROXY=1`, OAuth issuer discovery requires either `SCRUMBOY_PUBLIC_BASE_URL` or a proxy-provided `X-Forwarded-Host` together with a forwarded HTTPS indication. See [Issuer / discovery origin](#issuer--discovery-origin) below.

Compatible clients: any MCP client that speaks HTTP OAuth discovery (RFC 8414 / RFC 9728), PKCE (RFC 7636, S256 only), and Dynamic Client Registration (RFC 7591). Claude Code is the primary target; any other MCP-over-HTTP client with the same OAuth support works identically.

---

## Quick Start

For direct-TLS and loopback/localhost deployments, point an MCP client at your Scrumboy instance's `/mcp` endpoint:

```sh
claude mcp add --transport http scrumboy https://scrumboy.example.com/mcp
```

The client will:

1. Fetch `/.well-known/oauth-protected-resource` and `/.well-known/oauth-authorization-server` to discover the endpoints below.
2. `POST /oauth/register` to self-register as a public client (no `client_secret`).
3. Open a browser to `/oauth/authorize` with a PKCE `code_challenge`.
4. Prompt you to log in (if not already) and approve access.
5. Exchange the returned code for an access token at `/oauth/token`.

Static Bearer API tokens (`docs/mcp.md`) remain fully supported and unaffected — this is an additional way to obtain a Bearer credential, not a replacement.

---

## How It Works

1. MCP client discovers endpoints via `GET /.well-known/oauth-authorization-server` and `GET /.well-known/oauth-protected-resource`.
2. Client registers itself via `POST /oauth/register` (RFC 7591) and receives a `client_id` (no secret — public client).
3. Client redirects the user's browser to `GET /oauth/authorize` with `response_type=code`, its `client_id`, `redirect_uri`, and a PKCE `code_challenge` (S256).
4. If the user has no active Scrumboy session, a login form is shown inline. Once logged in, a consent screen ("Approve access for `<client name>`?") is shown.
5. On approval, Scrumboy redirects back to the client's `redirect_uri` with a single-use authorization code.
6. Client exchanges the code (plus its `code_verifier`) for an access token and refresh token at `POST /oauth/token`.
7. Client sends the access token as `Authorization: Bearer <token>` on `/mcp` or `/mcp/rpc` requests, exactly like a static API token.

---

## Requirements & Constraints

**Client type**

- Public clients only (no `client_secret`, `token_endpoint_auth_method: "none"`). This matches how Claude Code and most MCP clients register.
- One `redirect_uri` per client, fixed at registration time.

**PKCE**

- Required on every authorization request. Only `S256` is accepted; `plain` is rejected.

**Consent**

- Single fixed scope ("read and manage projects, todos, sprints, and tags"); there is no granular per-scope consent screen.
- The user approving consent must already have (or create, via the normal login form shown inline) a Scrumboy session. Accounts with 2FA enabled must log in at the main app first — the inline login form on the consent page does not handle a 2FA challenge.
- Because `client_name` is unauthenticated, self-registered metadata (any client can call itself "Claude Code" or anything else), the consent screen also shows the actual `redirect_uri` destination the code will be sent to, not just the name, so a user has something to check before approving.
- The consent form POST requires `Origin` (falling back to `Referer`) to match this server's own origin, rejecting the request otherwise. `SameSite=Lax` on the session cookie alone isn't sufficient: "site" for SameSite purposes is the registrable domain, not this exact origin, so a form auto-submitted from any sibling subdomain sharing that cookie's Domain would otherwise still carry it into this endpoint.
- The login, consent, and error HTML pages all send `Cache-Control: no-store`, `Content-Security-Policy: frame-ancestors 'none'; base-uri 'none'`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, and `X-Content-Type-Options: nosniff`, so the Approve button can't be framed for a clickjacking-style attack and a shared/cached browser never retains a copy of these pages.

**Dynamic Client Registration abuse resistance**

- `POST /oauth/register` is rate-limited (shares `authRateLimit`, 10/min per IP; the IP key honors `X-Forwarded-For` only when `SCRUMBOY_TRUST_PROXY=1`, otherwise `RemoteAddr` — see `SCRUMBOY_TRUST_PROXY` in the README — so a client can't spoof a fresh bucket per request) since it's otherwise an unauthenticated, unbounded way to create `oauth_clients` rows.
- `POST /oauth/register` requires `Content-Type: application/json` strictly, rejecting any other value. This isn't just input validation: a cross-origin browser request with e.g. `Content-Type: text/plain` is a CORS "simple request" that needs no preflight, so without this check a hostile webpage could get many unwitting visitors' browsers to each register a client from their own IP — defeating the per-IP rate limit above by distributing registration load across real, distinct addresses instead of one attacker IP.
- `redirect_uris` must contain exactly one entry (extras are rejected, not silently dropped), which must be a well-formed absolute `http`/`https` URL with a valid host and optional numeric port in the range 1–65535, no userinfo, and no fragment delimiter. `https` is allowed for any host; plain `http` is allowed only for loopback (`localhost`, `127.0.0.0/8`, `::1` — not RFC1918/LAN addresses), per RFC 8252, for native/CLI clients. This is a structural sanity check only — it does not make a registered client trustworthy; exact-match comparison against the registered value is still what prevents redirect-target tampering during the authorize/token flow.

**Mode**

- Available only in full mode (`SCRUMBOY_MODE=full`). All `/oauth/*` and `/.well-known/oauth-*` endpoints return `404` in anonymous mode.

**Issuer / discovery origin**

- <a name="issuer--discovery-origin"></a>**Issuer / discovery origin.** The `issuer`/`resource` values in the two discovery documents, and the absolute endpoint URLs built from them, are chosen in order: (1) `SCRUMBOY_PUBLIC_BASE_URL`, used verbatim, same as the password-reset link origin; (2) direct TLS, where TLS supplies the `https` scheme and the request's `Host` is used only after strict authority and port validation; (3) when `SCRUMBOY_TRUST_PROXY=1`, a validated forwarded origin — `X-Forwarded-Proto` (or `CF-Visitor`) must indicate `https` and the proxy must provide an explicit, valid `X-Forwarded-Host`; (4) a validated loopback request host (`localhost`, `127.0.0.0/8`, `::1`) over plain `http`, since that traffic never leaves the machine. The proxy branch never falls back to the backend-facing request `Host`. When `SCRUMBOY_TRUST_PROXY=1`, OAuth issuer discovery requires either `SCRUMBOY_PUBLIC_BASE_URL` or a proxy-provided `X-Forwarded-Host` together with a forwarded HTTPS indication. A proxy that sends only `X-Forwarded-Proto` receives `503 server_error`. If no ladder branch applies, discovery fails closed with the same controlled 503 rather than guessing an issuer.

**Token lifetimes**

- Authorization codes: 60 seconds, single-use.
- Access tokens: 1 hour.
- Refresh tokens: 30 days (matches the existing session TTL), rotated on every use.
- Consumed/expired codes and revoked/expired tokens are swept hourly by the same background job that expires temporary boards (`DeleteExpiredOAuthArtifacts` in `cmd/scrumboy/main.go`) — nothing else deletes these rows, only marks them consumed/revoked.

---

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/.well-known/oauth-protected-resource` | RFC 9728 — advertises this MCP resource's authorization server. |
| `GET` | `/.well-known/oauth-authorization-server` | RFC 8414 — advertises the endpoints below. |
| `POST` | `/oauth/register` | RFC 7591 — self-service client registration. |
| `GET`/`POST` | `/oauth/authorize` | RFC 6749 §3.1 — login/consent, then issues an authorization code. |
| `POST` | `/oauth/token` | RFC 6749 §3.2 — exchanges a code or refresh token for an access token. |
| `POST` | `/oauth/revoke` | RFC 7009 — revokes an access or refresh token. |

`/oauth/*` is deliberately outside `/api/*`: it does not require the `X-Scrumboy: 1` CSRF header that `/api/*` writes require. The consent form at `/oauth/authorize` instead combines `SameSite=Lax` session-cookie semantics with a canonical `Origin` check (falling back to `Referer`). A submission whose browser origin does not match the OAuth issuer is rejected.

---

## Example: token exchange

```sh
curl -X POST https://scrumboy.example.com/oauth/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "grant_type=authorization_code" \
  --data-urlencode "code=$CODE" \
  --data-urlencode "redirect_uri=$REDIRECT_URI" \
  --data-urlencode "client_id=$CLIENT_ID" \
  --data-urlencode "code_verifier=$CODE_VERIFIER"
```

```json
{
  "access_token": "...",
  "token_type": "Bearer",
  "expires_in": 3600,
  "refresh_token": "..."
}
```

Then use it exactly like a static API token:

```sh
curl -X POST https://scrumboy.example.com/mcp/rpc \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"projects.list","arguments":{}}}'
```

---

## Error Handling

`/oauth/token` and `/oauth/register` return the flat RFC 6749 §5.2 / RFC 7591 §3.2.2 error shape (not Scrumboy's usual `{"error":{"code":...}}` API envelope):

```json
{"error": "invalid_grant", "error_description": "the authorization code is invalid, expired, or already used"}
```

Codes used: **`invalid_request`**, **`invalid_client`**, **`invalid_grant`**, **`unsupported_grant_type`**, **`access_denied`**, **`unsupported_response_type`**, **`invalid_redirect_uri`**, **`invalid_client_metadata`**.

`/oauth/authorize` failures either redirect to the client's `redirect_uri` with `error`/`error_description`/`state` query params (once `redirect_uri` itself is verified against the registered client), or — if `redirect_uri` cannot be verified — render a plain error page instead of redirecting, to avoid an open-redirect.

`/oauth/revoke` always returns `200`, whether or not the presented token existed (RFC 7009 §2.2) — this is intentional and prevents the endpoint from being used to probe for token existence.

---

## Not Implemented

The following are explicitly out of scope in the current version:

- **Confidential clients / client secrets** — public clients (PKCE) only.
- **Multiple redirect URIs per client** — one per client, fixed at registration.
- **Granular per-scope consent** — a single fixed scope.
- **Refresh-token reuse-detection cascade** — a reused (already-rotated-away-from) refresh token is rejected, but reuse does not revoke the rest of that token family.
- **Revocation cascade** — explicitly revoking a refresh token via `/oauth/revoke` does not also revoke access tokens already issued alongside it; those remain valid until their own (1 hour) expiry. Access and refresh tokens aren't linked by a shared grant/family id in the current schema, so revoking one can't look up the other.
- **Admin UI for listing/revoking registered OAuth clients** — inspect or clean up via direct database access if ever needed.
- **JWT access tokens / JWKS endpoint** — tokens are opaque and validated by direct database lookup, matching how static API tokens already work.
- **Inline 2FA during the consent-page login form** — accounts with 2FA enabled must log in at the main app first, then reopen the authorization link.
