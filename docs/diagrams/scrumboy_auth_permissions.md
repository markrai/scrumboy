# Authentication and permissions

Two-layer model: HTTP establishes actor identity; `store` enforces system and project roles.

```mermaid
flowchart TB
  Cred[Credential]
  Cred --> Cookie[scrumboy_session cookie]
  Cred --> Token[API token sb_* header]
  Cred --> OIDC[OIDC callback flow]

  Cookie --> Ctx[requestContext user id in ctx]
  Token --> Ctx
  OIDC --> Ctx

  Ctx --> Handler[HTTP handler]
  Handler --> StoreCheck[store CheckProjectRole etc]
  StoreCheck --> Allow[allow or 403]
```

## Login paths

```mermaid
flowchart LR
  Local[Email password]
  TOTP[2FA TOTP step]
  OIDCFlow[OIDC authorization code flow]
  Bootstrap[Bootstrap first user]
  Identity[Lookup issuer and subject]
  Existing[Existing linked user]
  Provision[Provision only when verified email is unowned]
  Connect[Explicit Connect SSO from authenticated settings]
  SetPassword[Set first Scrumboy password from settings]
  StepUp[Sensitive OIDC step-up]
  Grant[Session-bound first-password grant]
  LocalPW[Write first local password]

  Local --> TOTP
  OIDCFlow --> Identity
  Identity --> Existing
  Identity --> Provision
  Connect --> OIDCFlow
  SetPassword --> StepUp
  StepUp --> Grant
  Grant --> LocalPW
  Bootstrap --> Session[CreateSession cookie]
  TOTP --> Session
  Existing --> Session
  Provision --> Session
```

Normal OIDC login never links by matching email and never rewrites the canonical `users.email`. A local user connects a new identity explicitly after current-password confirmation, Scrumboy 2FA when enabled, and fresh provider authentication. An SSO-only user sets a first local password only after a separate provider step-up and a short-lived session-bound grant.

- **Full mode:** sessions, users, API tokens, admin routes
- **Anonymous mode:** `requestContext` ignores cookies; temp boards have no owner until claimed
- **2FA:** requires `SCRUMBOY_ENCRYPTION_KEY` once encrypted auth/security data exists (TOTP and/or password-reset tokens)
- **Pre-auth locale picker:** auth shell (sign-in, bootstrap, 2FA, password reset) exposes the shared public locale listbox in the topbar; copy comes from the i18n bootstrap catalog until the full locale JSON loads.
- **Post-login redirect:** bootstrap, login, and 2FA completion redirect via sanitized `next` from auth state (strips stale OIDC query noise).

See `docs/roles-and-permissions.md` for role matrix detail.
