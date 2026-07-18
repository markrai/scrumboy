# OIDC / SSO configuration

Scrumboy supports one configured OpenID Connect provider for human sign-in. This is separate from Scrumboy's OAuth server for MCP clients.

## Configuration

Set all four values and restart Scrumboy:

```sh
SCRUMBOY_OIDC_ISSUER=https://auth.example.com/realms/myrealm
SCRUMBOY_OIDC_CLIENT_ID=scrumboy
SCRUMBOY_OIDC_CLIENT_SECRET=your-client-secret
SCRUMBOY_OIDC_REDIRECT_URL=https://scrumboy.example.com/api/auth/oidc/callback
```

Local login remains enabled by default. To operate as SSO-only at the instance level:

```sh
SCRUMBOY_OIDC_LOCAL_AUTH_DISABLED=true
```

All four OIDC settings are required. The redirect URL must exactly match the provider registration. The issuer is trimmed and normalized without a trailing slash; discovery may accept that issuer's single trailing-slash form, but no unrelated issuer.

## Account authentication methods

A user can have:

- **Local password**: a usable Scrumboy bcrypt password.
- **SSO**: an OIDC identity for the currently configured normalized issuer.
- **Local password + SSO**: both methods on the same account.

An identity saved for an old issuer remains in the database, but does not make SSO appear usable after the configured issuer changes. If the old issuer is configured again, that historical link can become usable again.

The hybrid login screen intentionally shows both **Sign in with your Scrumboy password** and **Continue with SSO**. The visitor has not identified an account yet, and an installation may contain all three account combinations.

When local authentication is disabled, the local form, forgot-password action, reset page, and local/admin reset APIs are unavailable. A stored local password does not become usable until the operator re-enables local authentication.

## Canonical email ownership

`users.email` is the canonical Scrumboy email. Normal OIDC login identifies an existing account only by `(issuer, subject)`; email is not an account join key.

- The first login for a new, non-colliding identity may provision a user using the verified IdP email and display name.
- Later logins update only the linked identity's latest verified `email_at_login`.
- Later logins do not rewrite the canonical Scrumboy email, name, role, ownership, or profile.
- An IdP email change or collision therefore cannot transfer account ownership, and the account's existing local login remains tied to its canonical Scrumboy email.
- If an unlinked identity's normalized email already has a canonical owner, Scrumboy creates no duplicate and performs no implicit link. Sign in locally and use **Connect SSO**.

Changing the canonical Scrumboy email is a separate operation and is not part of OIDC login or linking.

## Connect SSO

A user with a local password can open **Settings → Profile → Connect SSO**. Scrumboy requires:

1. the current Scrumboy password;
2. Scrumboy 2FA when enabled;
3. fresh authentication at the configured provider; and
4. a verified normalized IdP email that exactly matches the canonical Scrumboy email.

The identity must not belong to another user. A historical identity for a different issuer does not block connecting the current provider. Linking is explicit because matching email alone is not sufficient proof of account ownership. Unlinking is not provided.

If an existing session has neither a usable local password nor an identity for the current provider (for example, after an issuer change), Security settings still explain **Connect SSO**, but the flow cannot start from the session alone. Use host-side owner recovery where applicable to establish a local password first; Scrumboy never treats the old session as sufficient linking proof.

## Setting the first Scrumboy password

An SSO-only user linked to the current provider can choose **Settings → Profile → Set Scrumboy password**. A Scrumboy session alone is insufficient. Scrumboy performs a fresh OIDC step-up using `max_age=0` and requires a valid recent `auth_time` claim.

After the callback, Scrumboy issues a single-use five-minute authorization bound to the exact user and Scrumboy session. The new password is sent only in the final JSON POST, is validated by the normal Scrumboy password policy, and uses the normal bcrypt hashing implementation. Roles, projects, ownership, profile, 2FA, sessions, and the OIDC link remain intact.

If the provider omits or returns invalid `auth_time`, the operation fails closed with a provider-compatibility message. Normal SSO login does not require `auth_time`.

When local authentication is disabled, the password can still be established for recovery preparation, but the UI explains that it is unusable until the operator re-enables local login.

## Password recovery responsibilities

- **Local-only**: Scrumboy reset email or an owner-generated Scrumboy reset link resets the local password.
- **SSO-only**: credential recovery belongs to the identity provider. Scrumboy sends no local reset email and administrators are not offered an IdP-password reset action.
- **Dual authentication**: Scrumboy reset changes only the local password. It neither changes the provider password nor removes any OIDC identity.

Public reset requests stay enumeration-safe. Passwordless and unknown accounts receive the same generic response and timing; no token or Scrumboy reset mail is produced. A completed local reset revokes Scrumboy sessions and pending local-login 2FA challenges.

## Provider outage and owner recovery

Existing Scrumboy sessions can continue until they expire or are revoked because they are local sessions. New SSO logins and sensitive OIDC reauthentication cannot complete while the provider is unavailable.

Scrumboy warns at startup when no owner has an effective login method and warns separately when owners depend exclusively on the configured provider. Effective methods are:

```text
effectiveLocal = localAuthEnabled && hasLocalPassword
effectiveSSO = oidcEnabled && identityForConfiguredIssuer
```

The calculation treats null, empty, and malformed password hashes as unusable and treats old-issuer identities as historical only. Startup is not blocked. See [owner disaster recovery](recovery.md) before an outage occurs.

## Security details

- Authorization Code flow with PKCE S256, issuer/audience verification, state, and nonce.
- OIDC state is random, stored only as a hash, expiring, and single-use. Sensitive state is bound to purpose, initiating user, and the exact hashed Scrumboy session.
- Ordinary **Continue with SSO** uses a top-level redirect GET so identity-provider `SameSite=Lax` session cookies continue to support seamless SSO. Its state, nonce, and PKCE challenge are standard authorization-request query parameters on the provider URL; the PKCE verifier remains server-side.
- Sensitive Set Password and Connect SSO requests use form POST, keeping their state, nonce, and PKCE challenge out of Scrumboy-controlled redirect URLs.
- Sensitive operations use `max_age=0` without also requiring `prompt=login`, and validate `auth_time` with a small clock-skew allowance.
- Callback authorization codes and state may necessarily be present in the callback URL. Scrumboy does not log or echo them. Passwords, first-password grants, session tokens, PKCE verifiers, TOTP values, and recovery codes are never put in URLs or application logs. Sensitive-flow nonces are form fields; the ordinary-login nonce is present only in the provider authorization URL.
- Return paths are restricted to safe local paths.
- Scrumboy 2FA protects local-password sign-in and sensitive authentication-method changes. MFA for normal SSO sign-in is the configured provider's responsibility.
- Sensitive current-password and second-factor attempts are independently limited per user and per trusted client IP. TOTP and recovery-code attempts also share an aggregate limit.

The configured provider must accept form-POST authorization requests for sensitive method changes and support `max_age=0` plus a valid `auth_time` claim. Ordinary SSO login uses redirect GET and does not require `auth_time`.

The `first_password_grants` table may be present in full SQLite backups. Grant values are hashed, short-lived, bound to a session, excluded from Scrumboy logical project exports, unusable after expiration or session deletion, and cleaned up opportunistically.

## Troubleshooting

- **SSO button missing**: `/api/auth/status` must report `oidcEnabled: true`; verify all four settings and restart.
- **503 when continuing with SSO**: discovery is lazy and the provider is unreachable or its advertised issuer does not match.
- **`oidc_error=email`**: a verified email claim is missing.
- **`oidc_error=link_required`**: the identity is unlinked and its email collides with a canonical Scrumboy account. Sign in locally and use Connect SSO.
- **`oidc_error=auth_time`**: the provider did not honor the sensitive reauthentication contract. Confirm support for `max_age=0` and a valid `auth_time` claim.
- **`oidc_error=identity_mismatch` or `session_changed`**: the provider identity or Scrumboy session changed during a sensitive operation; start again from Settings.

## Deliberate limitations

There is no canonical-email change flow, unlinking, IdP logout, multiple simultaneous OIDC providers, role/group claim mapping, provider 2FA policy enforcement, 2FA-reset recovery command, userinfo use, or refresh-token use.
