# OIDC / SSO Configuration

Scrumboy supports OpenID Connect (OIDC) for single sign-on. After a successful OIDC login the user gets a normal `scrumboy_session` cookie — no JWTs are exposed to the browser.

Compatible providers: Keycloak, Auth0, Authentik, Zitadel, Azure AD (external tenant), Google Workspace (when configured as a standard OIDC client), and any provider with standard OIDC Discovery.

---

## Quick Start

Set all four required environment variables and restart the server.

```sh
SCRUMBOY_OIDC_ISSUER=https://auth.example.com/realms/myrealm
SCRUMBOY_OIDC_CLIENT_ID=scrumboy
SCRUMBOY_OIDC_CLIENT_SECRET=your-client-secret
SCRUMBOY_OIDC_REDIRECT_URL=https://scrumboy.example.com/api/auth/oidc/callback
```

**Localhost example** (e.g. Keycloak running locally):

```sh
SCRUMBOY_OIDC_ISSUER=http://localhost:8180/realms/dev
SCRUMBOY_OIDC_CLIENT_ID=scrumboy-local
SCRUMBOY_OIDC_CLIENT_SECRET=secret
SCRUMBOY_OIDC_REDIRECT_URL=http://localhost:8080/api/auth/oidc/callback
```

Requirements:

- All four variables must be set. Any missing variable disables OIDC entirely.
- The redirect URL must be registered at the provider exactly as written here.
- Restart the server after changing any of these values.

If configured correctly, a **Continue with SSO** button appears on the login screen (and on first-time setup when local password bootstrap is also available).

OIDC is disabled by default. Local password login remains available unless you also set:

```sh
SCRUMBOY_OIDC_LOCAL_AUTH_DISABLED=true
```

---

## How It Works

1. User clicks **Continue with SSO** on the login screen.
2. Browser is redirected to the provider's authorization endpoint.
3. User authenticates at the provider.
4. Provider redirects back to `/api/auth/oidc/callback` with an authorization code.
5. Scrumboy exchanges the code for tokens server-side, validates the ID token, and creates a session.
6. Browser receives a `scrumboy_session` cookie and is redirected to the original page.

All token handling happens on the server. The browser never sees access tokens or ID tokens.

---

## Requirements & Constraints

**Email**

- The ID token must include an `email` claim (non-empty).
- `email_verified` must be `true` (boolean or string). If absent or false, login is denied.
- Email is stored normalized to lowercase.
- On **first** OIDC login for a given `(issuer, subject)`, `users.email` is set from the token. On later logins, Scrumboy does **not** sync email or display name from the IdP (matches current server behavior).

**Identity**

- Identity is tracked by `(issuer, subject)` — the provider's `iss` + `sub` claims.
- Email is not used as a join key. Changing your email at the provider does not break the link; the row in Scrumboy keeps the email from first login until you change it in-app or via admin.
- There is no account linking between local password accounts and OIDC accounts.

**First user / ownership**

- If no users exist when an OIDC login succeeds, that user becomes the instance owner — but only if the token's issuer matches `SCRUMBOY_OIDC_ISSUER` exactly.
- If a local password account was created first via bootstrap, subsequent OIDC users are created as normal users.

**Display name**

- Set from the `name` claim at account creation.
- Falls back to `preferred_username`, then to the last segment of `sub`.
- Not updated on subsequent logins.

**Scopes requested**

- `openid email profile` — fixed, not configurable.

---

## Configuration Notes

**Issuer normalization**

Scrumboy strips trailing slashes and whitespace from `SCRUMBOY_OIDC_ISSUER` at startup. Set the issuer without a trailing slash to avoid confusion:

```sh
# Correct
SCRUMBOY_OIDC_ISSUER=https://auth.example.com/realms/myrealm

# Avoid (will be normalized, but don't rely on it)
SCRUMBOY_OIDC_ISSUER=https://auth.example.com/realms/myrealm/
```

The issuer in the ID token's `iss` claim must match the normalized value character-for-character.

**Redirect URL**

- Must be an absolute URL.
- Must match the redirect URI registered at the provider exactly — including scheme, host, port, and path.
- Path is always `/api/auth/oidc/callback`.
- Do not derive it from request headers. Set it explicitly.

**Behind a reverse proxy**

OIDC works behind nginx, Caddy, Traefik, etc. as long as:

- `SCRUMBOY_OIDC_REDIRECT_URL` reflects the public-facing URL (not the internal address).
- The proxy forwards `X-Forwarded-Proto: https` if terminating TLS — Scrumboy uses this to set `Secure` on the session cookie.

**Discovery**

Scrumboy fetches the provider's discovery document (`{issuer}/.well-known/openid-configuration`) on the first login attempt, not at startup. The app starts normally even if the provider is unreachable; OIDC is unavailable until discovery succeeds.

---

## Troubleshooting

### `oidcEnabled: false` in `/api/auth/status`

- One or more of the four required env vars is missing or empty.
- The server has not been restarted since the vars were set.

Check:

```sh
curl http://localhost:8080/api/auth/status | jq .oidcEnabled
```

### `503` on the SSO button click

`oidcEnabled` only means the four env vars are set — discovery runs on the first click. The SSO button can appear even when discovery has not succeeded yet; clicking it returns **503** until the provider is reachable and discovery succeeds.

Discovery failed. Possible causes:

- Provider is unreachable from the Scrumboy server.
- `SCRUMBOY_OIDC_ISSUER` points to the wrong URL (wrong realm, wrong host).
- The issuer field in the discovery document does not match `SCRUMBOY_OIDC_ISSUER` after normalization.

Check the server logs for a line like:

```
oidc: discovery/login error: oidc discovery failed for "...": ...
```

### Redirect loop or "invalid redirect URI" at the provider

- `SCRUMBOY_OIDC_REDIRECT_URL` does not match the redirect URI configured at the provider.
- Common mismatches: `http` vs `https`, missing port, trailing slash.

### Login fails after returning from the provider (`oidc_error=token`)

The ID token was rejected. Common causes:

- Token audience (`aud`) does not match `SCRUMBOY_OIDC_CLIENT_ID`.
- Issuer in the token does not match `SCRUMBOY_OIDC_ISSUER` (after normalization).
- Token has expired in transit (clock skew between server and provider).
- Wrong `client_secret`.

Check server logs for `oidc: callback error: token`.

### Login fails with `oidc_error=email`

- Provider did not include an `email` claim.
- `email_verified` is `false` or absent.
- Enable email verification in the provider, or check whether the client/scope configuration returns the email claim.

### Login fails with `oidc_error=token` and email conflict in logs

A different user already has that email address (registered via local password login). Account linking is not supported. The server logs will contain:

```
oidc login aborted: email already in use by existing user (OIDC identity not linked)
```

Options: delete the local account via admin UI, or use a different email at the provider.

### SSO button not showing

`GET /api/auth/status` must return `"oidcEnabled": true`. If it does not, see the first troubleshooting entry above. If it does, hard-refresh the browser (the SPA caches auth status for the page lifetime).

---

## Security Notes

- Uses Authorization Code flow with PKCE (S256). The code verifier and state are stored server-side in memory with a 10-minute TTL.
- State and nonce are validated on every callback. Replayed or expired state values are rejected.
- `return_to` (post-login redirect) is validated strictly: must be a path-only relative URL, no `//`, no `://`, no `..` segments, no fragments.
- Session cookies are `HttpOnly`, `SameSite=Lax`, and `Secure` when served over HTTPS.
- The client secret never leaves the server.

---

## Not Implemented

The following are explicitly out of scope in the current version:

- **IdP logout** — clicking logout ends the Scrumboy session only; the provider session remains active.
- **Role or group claim mapping** — all OIDC users are created as `user` role (except the first user who becomes `owner`).
- **Account linking** — an existing local-password account cannot be linked to an OIDC identity.
- **Multiple OIDC providers** — only one provider can be configured.
- **Userinfo endpoint** — claims are taken from the ID token only.
- **Refresh token usage** — sessions use the existing Scrumboy session TTL (30 days), not provider token lifetimes.
- **Domain allowlists or invitation-only provisioning** — any user the provider authenticates can log in.
