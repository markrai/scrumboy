# Remote MCP/OAuth release acceptance

This is the release gate for Scrumboy's remote MCP OAuth boundary. Run it against the exact release candidate deployed to Vega. Do not record client secrets, authorization codes, access or refresh tokens, session cookies, or PKCE verifiers.

Production authentication remains provider-neutral. Vega uses Keycloak as its upstream OIDC provider for this acceptance run; Keycloak is not Scrumboy's MCP authorization server and its access tokens are not MCP credentials.

## Evidence record

Record before testing:

- Scrumboy commit and version:
- Vega public origin:
- Cursor version:
- Claude Code version:
- Keycloak version:
- Keycloak realm:
- Keycloak OIDC client type and standard authorization-code-flow settings:
- Keycloak issuer URL:
- Scrumboy OIDC callback: `https://<vega-host>/api/auth/oidc/callback`
- Sanitized reverse-proxy configuration:
- `SCRUMBOY_PUBLIC_BASE_URL` value:
- Whether `SCRUMBOY_TRUST_PROXY` is enabled and which forwarding headers the proxy overwrites:

Cursor and Claude dynamically register their redirect URIs with Scrumboy's `/oauth/register` endpoint. Do not add those client callbacks to Keycloak. Keycloak knows only Scrumboy's OIDC callback above.

## Deployment preparation

1. Back up Vega's SQLite database and record the recoverable backup location.
2. Deploy the release candidate and migration 057.
3. Confirm users and OAuth client registrations remain; confirm pre-migration codes and tokens have been invalidated.
4. Set `SCRUMBOY_PUBLIC_BASE_URL=https://<vega-host>`.
5. Configure the existing provider-neutral `SCRUMBOY_OIDC_*` values from Keycloak discovery and client configuration.
6. Enable `SCRUMBOY_TRUST_PROXY` only when the trusted proxy strips or overwrites client-supplied `X-Forwarded-For`, `X-Forwarded-Proto`, `X-Forwarded-Host`, and `CF-Visitor` values as applicable.

## Discovery and transport checks

Verify the exact public values, saving sanitized headers and JSON:

1. `GET /.well-known/oauth-protected-resource/mcp/rpc` returns:

   ```json
   {
     "resource": "https://<vega-host>/mcp/rpc",
     "authorization_servers": ["https://<vega-host>"]
   }
   ```

2. `GET /.well-known/oauth-protected-resource` returns the same resource identity as a compatibility alias.
3. `GET /.well-known/oauth-authorization-server` has issuer `https://<vega-host>` and `protected_resources: ["https://<vega-host>/mcp/rpc"]`.
4. An unauthenticated request to `/mcp/rpc` returns an empty 401 and:

   ```text
   WWW-Authenticate: Bearer resource_metadata="https://<vega-host>/.well-known/oauth-protected-resource/mcp/rpc"
   ```

5. An invalid Bearer returns an empty 401 with `error="invalid_token"` and the same metadata URL.
6. A cross-origin `Origin` value returns empty 403 without `WWW-Authenticate`.
7. An authenticated GET returns empty 405 with `Allow: POST`.
8. A valid notification returns empty 202. No response issues `MCP-Session-Id`.

## Cursor browser OAuth

1. Clear any old Scrumboy OAuth credentials from the client.
2. Configure `https://<vega-host>/mcp/rpc`, not `/mcp`.
3. Confirm the initial request receives the path-derived protected-resource challenge.
4. Complete DCR and record only the registered redirect URI shape. Current releases may use `http://localhost:8787/callback`; older releases may use `cursor://anysphere.cursor-mcp/oauth/callback` or multiple URIs.
5. Complete Scrumboy authorization. When Scrumboy needs human authentication, follow its standard OIDC redirect to Keycloak.
6. Confirm Keycloak returns only to `https://<vega-host>/api/auth/oidc/callback`, then Scrumboy resumes consent.
7. Approve consent, complete the Scrumboy code exchange, and invoke `projects.list` successfully.

If this exact Cursor build registers multiple redirect URIs or requires a private-use scheme that current DCR policy rejects, stop the release. Handle normalized multi-URI storage, exact selected-URI matching, and private-use-scheme policy in a separate prerequisite change; do not silently discard registration values in P1B.

## Claude Code browser OAuth

1. Clear old Scrumboy OAuth credentials.
2. Configure:

   ```sh
   claude mcp add --transport http scrumboy https://<vega-host>/mcp/rpc
   ```

3. Confirm the unauthenticated request receives the canonical challenge.
4. Complete browser OAuth through Scrumboy's `/oauth/*` endpoints and Keycloak-backed human login.
5. Invoke `projects.list` successfully.

## Compatibility and negative checks

1. Invoke a `/mcp/rpc` tool with a valid Scrumboy session cookie.
2. Invoke a `/mcp/rpc` tool with a valid static `sb_…` API token.
3. Verify legacy `GET /mcp` capability/bootstrap JSON with a cookie and static token.
4. Verify legacy `POST /mcp` still accepts `{ "tool": "…", "input": {} }` with a cookie and static token.
5. Present a valid `/mcp/rpc` OAuth access token to `/mcp`; expect the legacy 401 authentication envelope and no OAuth challenge.
6. Present the same OAuth access token to `/agora/v1/*`; expect 401 and no OAuth challenge.
7. Verify revoked, expired, wrong-resource, and unbound OAuth artifacts are rejected identically on `/mcp/rpc`.
8. Confirm authorize, token, refresh, stored grants/tokens, metadata, and challenges consistently identify exactly `https://<vega-host>/mcp/rpc`.
9. Review Scrumboy, reverse-proxy, and Keycloak logs for secrets or bearer credentials. Record only sanitized evidence.

## Result

- Date/time and operator:
- Cursor result and redirect URI shape:
- Claude Code result and redirect URI shape:
- Cookie/static/legacy result:
- Negative-resource tests:
- Log review result:
- Release accepted or blocked:
- Follow-up issue links:

Authentik remains an interoperability target for another contributor, but it is not a substitute for Vega/Keycloak acceptance. Do not claim live Authentik validation unless a real environment was tested and its version, issuer, callback, client configuration, and sanitized evidence were reviewed.
