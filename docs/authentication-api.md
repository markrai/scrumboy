# Human authentication API

These routes authenticate Scrumboy users. They are unrelated to the OAuth server used by MCP clients.

`GET /api/auth/status`, `GET /api/me`, authentication responses, and `GET /api/admin/users` expose `hasLocalPassword` and `oidcLinked`. `oidcLinked` means linked to the currently configured normalized issuer; historical links for other issuers are intentionally not reported as usable. No hash, subject, token, nonce, verifier, or recovery proof is exposed.

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
