# Install from GitHub Releases

Download a published Scrumboy binary from [GitHub Releases](https://github.com/markrai/scrumboy/releases), verify it optionally, and run it as a local server.

## Windows

### Download

From a Scrumboy release, download:

- `scrumboy-<tag>-windows-amd64.exe`

Published beside it:

- `scrumboy-<tag>-windows-amd64.exe.sha256`
- `scrumboy-<tag>-windows-amd64.exe.intoto.jsonl`

Replace `<tag>` with the release tag (for example `v1.2.3`).

### Integrity and provenance

The `.sha256` file checks that the downloaded executable matches the published checksum (file integrity).

The attestation/provenance bundle verifies that the artifact was produced with signed build provenance for the expected Scrumboy repository identity:

```bash
gh attestation verify scrumboy-<tag>-windows-amd64.exe -R markrai/scrumboy
```

### Running

1. Put the executable in a dedicated writable folder, for example `%USERPROFILE%\Scrumboy`.
2. Run `scrumboy-<tag>-windows-amd64.exe` from that folder.
3. The process starts a local Scrumboy server.
4. Open [http://localhost:8080](http://localhost:8080).

### Runtime data

With neither `DATA_DIR` nor `SQLITE_PATH` set, Scrumboy stores instance data under `./data` relative to the process working directory (typically the dedicated folder you run from). That directory holds SQLite (`./data/app.db`, plus any WAL/SHM sidecars) and file-backed uploads such as wallpapers.

Set `DATA_DIR` if you want the database and uploads in a different directory. See [environment-variables.md](environment-variables.md#data_dir--sqlite_path).
