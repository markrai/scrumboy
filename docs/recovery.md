# Owner disaster recovery

`recover-owner` is a host-side break-glass command for an existing Scrumboy owner. It works without the OIDC provider and can establish or replace a local password even when the stored hash is null or malformed.

Host and database access is the security boundary. The command intentionally bypasses user-level 2FA, does not clear 2FA configuration, and revokes every Scrumboy session and pending local-login 2FA challenge for the recovered owner.

## Required preparation

Never run recovery concurrently with the active SQLite service.

For what a full `DATA_DIR` restore includes (SQLite, WAL/SHM, wallpapers, encryption key), see the [persistence matrix](diagrams/scrumboy_deployment_ops.md#persistence-matrix).

1. Stop the active Scrumboy service or container.
2. Back up the stopped SQLite database, bind mount, or named volume.
3. Use the same Scrumboy binary/image version as the database, or another schema-compatible version.
4. Run recovery against the same configured database path/volume.
5. Restart Scrumboy. If `SCRUMBOY_OIDC_LOCAL_AUTH_DISABLED=true`, re-enable local authentication before using the recovered password.

The command does **not** apply migrations. It reads `schema_migrations`, rejects schema versions unknown to the binary, requires the OIDC identity-era schema (migration 049 or later), and checks the required auth tables and columns. If the schema is too old, keep the backup, run the normal Scrumboy upgrade path, stop the service again, and rerun recovery. Migration 056 is not required.

## Native binary

Interactive input is hidden and requires confirmation:

```sh
./scrumboy recover-owner --email owner@example.com
```

For deliberate automation, supply one password line through standard input. The password is never accepted in argv:

```sh
printf '%s\n' "$RECOVERY_PASSWORD" | ./scrumboy recover-owner --email owner@example.com --password-stdin
```

## Docker Compose

Stop and back up the configured data directory first. With the repository's `./data:/data` bind mount:

```sh
docker compose stop scrumboy
cp -a ./data ./data.before-owner-recovery
printf '%s\n' "$RECOVERY_PASSWORD" | docker compose run --rm -T scrumboy recover-owner --email owner@example.com --password-stdin
docker compose up -d scrumboy
```

For an interactive hidden prompt, omit `-T` and `--password-stdin`:

```sh
docker compose run --rm scrumboy recover-owner --email owner@example.com
```

## Direct Docker bind mount

```sh
docker stop scrumboy
cp -a /srv/scrumboy/data /srv/scrumboy/data.before-owner-recovery
printf '%s\n' "$RECOVERY_PASSWORD" | docker run --rm -i \
  -v /srv/scrumboy/data:/data \
  ghcr.io/markrai/scrumboy:latest recover-owner --email owner@example.com --password-stdin
docker start scrumboy
```

Use the actual image reference used by the stopped service.

## Docker named volume

Back up the stopped volume using your normal volume-backup procedure, then run the same image with the same volume:

```sh
docker stop scrumboy
docker run --rm -v scrumboy_data:/data -v "$PWD":/backup alpine \
  tar -czf /backup/scrumboy-data-before-recovery.tgz -C /data .
printf '%s\n' "$RECOVERY_PASSWORD" | docker run --rm -i \
  -v scrumboy_data:/data \
  ghcr.io/markrai/scrumboy:latest recover-owner --email owner@example.com --password-stdin
docker start scrumboy
```

## Failures

- Unknown or ambiguous email: no mutation.
- Non-owner target: no mutation.
- Weak password: no mutation.
- Active database lock: stop the service and retry; the command reports an actionable lock error.
- Too-old schema: back up and use the normal upgrade path before retrying.
- Newer/unknown schema: use the same or a newer compatible binary.

Successful output contains no password or authentication secret. Recovery touches only the owner's password hash, sessions, and pending login challenges; it does not change OIDC identities, roles, ownership, projects, API tokens, profile, or 2FA configuration.
