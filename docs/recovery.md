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

## Instance backup and restore

An instance backup is the SQLite database plus the files Scrumboy keeps beside it: `DATA_DIR/user-wallpapers/` and, when [inline images](inline-images.md) are used, `DATA_DIR/assets/`. The database and `assets/` must come from the **same** backup.

Copying a live database and then copying `assets/` is **not** consistent. Project deletion, temporary-board expiry, image cleanup and Replace imports delete database rows and then remove the matching files. A file can vanish between the database copy and the file copy, and restoring that pair gives broken images.

### Offline (recommended)

1. Stop Scrumboy (`docker compose stop`, or stop the service).
2. Copy `SQLITE_PATH` together with any `-wal`/`-shm` files, and `DATA_DIR/assets/`. Copying all of `DATA_DIR` is simplest and also covers wallpapers.
3. Start Scrumboy.

### Online

1. Create the file `DATA_DIR/assets/.removal-hold` (`touch`, or `New-Item` in PowerShell).
2. Wait at least 5 seconds.
3. Take a consistent SQLite copy: `sqlite3 "$SQLITE_PATH" ".backup '/backup/app.db'"` or `sqlite3 "$SQLITE_PATH" "VACUUM INTO '/backup/app.db'"`. Never copy a live database file directly.
4. Copy `DATA_DIR/assets/`, excluding `.removal-hold`.
5. Delete `.removal-hold`.

While the hold file exists, uploads, saves and deletions keep working, but no image file is deleted. Disk use can grow by the deleted images until the hold is removed and the next cleanup runs, so keep the hold short. The Docker image may not include the `sqlite3` CLI: run it from the host or a sidecar container, or use the offline procedure.

### Restore

1. Stop Scrumboy.
2. Put the database and `assets/` from the same backup in place.
3. Make sure no `.removal-hold` file was restored (delete it if present).
4. Start Scrumboy.

Every image row then has its file. Extra files left from newer activity are removed automatically after about 24 hours. Never restore a database and an `assets/` directory from different backups.
