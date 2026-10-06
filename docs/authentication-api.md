# Human authentication API

These routes authenticate Scrumboy users. They are unrelated to the OAuth server used by MCP clients.

`GET /api/auth/status`, `GET /api/me`, authentication responses, and `GET /api/admin/users` expose `hasLocalPassword` and `oidcLinked`. `oidcLinked` means linked to the currently configured normalized issuer; historical links for other issuers are intentionally not reported as usable. No hash, subject, token, nonce, verifier, or recovery proof is exposed.

When OIDC is configured in full mode, `/api/auth/status` also reports `oidcEnabled` and `mobileOidcEnabled` (both true together today). Anonymous mode and OIDC-disabled installs omit usable mobile OIDC routes.

## Browser OIDC login

- `GET /api/auth/oidc/login?return_to=/...`: browser/PWA redirect into the IdP. Unchanged by mobile handoff.
- `GET /api/auth/oidc/callback`: existing HTTPS provider callback. Browser flows still create a normal session cookie and redirect to a safe local `return_to`. Mobile-marked flows instead mint a one-time handoff and redirect to `com.markrai.scrumboy://oidc/callback`.

## Android native OIDC handoff

Unauthenticated JSON routes used only by the packaged Android shell. They require `Content-Type: application/json` and follow ordinary `/api` `X-Scrumboy: 1` conventions on the client. They are unavailable in anonymous mode and when OIDC is not configured.

- `POST /api/auth/oidc/mobile/start`: body `{"codeChallenge":"...","codeChallengeMethod":"S256","returnTo":"/..."}`. Returns `{"authorizationUrl":"...","flowState":"..."}`. The challenge is the app-held S256 proof; the raw verifier never leaves the device.
- `POST /api/auth/oidc/mobile/exchange`: body `{"code":"...","state":"...","verifier":"..."}`. On success sets the normal `scrumboy_session` cookie and returns `{"returnTo":"/..."}` only. The session credential is never returned in JSON.

Handoff codes are opaque, stored only as hashes, short-lived, state-bound, user-bound, and consumed at most once. Wrong verifier/state, expiry, and replay all fail closed without authenticating.

## Sensitive OIDC method changes

- `POST /api/auth/oidc/set-password/start`: authenticated + CSRF protected. Returns `authorizationEndpoint` and `authorizationParameters` for a browser form POST.
- `GET /api/auth/oidc/set-password/status`: reports whether the exact session holds a live first-password grant.
- `POST /api/auth/oidc/set-password`: body `{"newPassword":"...","twoFactorCode":"..."}`. The second factor is required only when Scrumboy 2FA is enabled.
- `POST /api/auth/oidc/link/start`: body `{"currentPassword":"...","twoFactorCode":"...","returnTo":"/..."}`. Returns form-POST authorization data.

Sensitive callbacks require `max_age=0`, valid recent `auth_time`, state, nonce, PKCE, exact user/session binding, and matching identity invariants. The first-password authorization is delivered only through a five-minute, HttpOnly, SameSite=Strict, path-scoped cookie.

## Password reset

- `POST /api/auth/request-password-reset` always uses a generic public response. Only accounts with a valid local password can cause a token and mail to be generated.
- `POST /api/auth/reset-password` resets only the local password and revokes sessions plus pending local-login 2FA challenges.
- `POST /api/admin/users/{id}/password-reset` generates only a Scrumboy-local reset link and is unavailable for users without a valid local password.

All local login/reset routes are unavailable when local authentication is disabled. See [OIDC](oidc.md), [SMTP/reset](smtp.md), and [recovery](recovery.md).
