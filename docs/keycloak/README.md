# Keycloak realm (local development)

This directory contains a **realm export** for running Scrumboy against a local Keycloak instance.

## File

- [`realm-scrumboy-local.json`](realm-scrumboy-local.json) — realm **`scrumboy`**, public OIDC client **`scrumboy`**, test user **`test`** / **`test`**, redirect URIs for Scrumboy on **localhost:8080**.

## Import

Stop Keycloak, then (paths may vary):

```sh
/opt/keycloak/bin/kc.sh import --file /path/to/realm-scrumboy-local.json
```

Or copy the file into Keycloak’s import directory and start with `--import-realm` per [Keycloak import/export](https://www.keycloak.org/server/importExport).

After import, the **issuer** (no `/auth` prefix on modern Keycloak) is:

```text
http://<keycloak-host>:<port>/realms/scrumboy
```

Example if Keycloak listens on **8180**:

```sh
SCRUMBOY_OIDC_ISSUER=http://localhost:8180/realms/scrumboy
SCRUMBOY_OIDC_CLIENT_ID=scrumboy
SCRUMBOY_OIDC_CLIENT_SECRET=dev-public-client-placeholder
SCRUMBOY_OIDC_REDIRECT_URL=http://localhost:8080/api/auth/oidc/callback
```

Scrumboy’s config requires **all four** variables and a **non-empty** `SCRUMBOY_OIDC_CLIENT_SECRET` string. For this **public** Keycloak client, the token endpoint uses **PKCE**; use any non-empty placeholder secret in Scrumboy’s env (Keycloak ignores it for public clients).

Ensure the **redirect URL** matches what is registered in the client (**`…/*`** in the realm file covers `/api/auth/oidc/callback`).

## Test user

| Field    | Value            |
|----------|------------------|
| Username | `test`           |
| Password | `test`           |
| Email    | `test@example.com` |

`emailVerified` is **true** so Scrumboy accepts the ID token (`email_verified` requirement).

## Security note

This realm is for **local development** only (`sslRequired: none`, known test password). Do not import as-is into production.

For more on Scrumboy OIDC behavior, see [OIDC.md](../OIDC.md).
