# Human authentication security

Scrumboy derives human account methods from authoritative records: a valid bcrypt password hash and an identity for the currently configured normalized OIDC issuer. It does not maintain duplicate booleans in the database.

High-risk method changes use short-lived, hashed, single-use server-side state, exact user/session binding, OIDC state/nonce/PKCE validation, fresh `auth_time`, Scrumboy 2FA when configured, independent user and trusted-IP rate limits, and transactional compare-and-set updates. Normal OIDC ownership is always `(issuer, subject)`; matching email never silently links an identity.

Scrumboy 2FA protects local-password login and sensitive method changes. MFA for ordinary SSO login is controlled by the identity provider. Host-side owner recovery is a deliberate break-glass exception: host/database control replaces user proof, existing sessions are revoked, and 2FA configuration remains intact.

See [OIDC / SSO](oidc.md) and [owner disaster recovery](recovery.md).
