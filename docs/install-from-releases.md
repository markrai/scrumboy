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

## macOS

### Download

Both architectures are published on [GitHub Releases](https://github.com/markrai/scrumboy/releases):

- Apple Silicon: `scrumboy-<tag>-darwin-arm64.tar.gz`
- Intel: `scrumboy-<tag>-darwin-amd64.tar.gz`

Beside each archive:

- `scrumboy-<tag>-darwin-<arch>.tar.gz.sha256`
- `scrumboy-<tag>-darwin-<arch>.tar.gz.intoto.jsonl`

Replace `<tag>` with the release tag and `<arch>` with `arm64` or `amd64`.

### Minimum version

These binaries require **macOS 12 Monterey or later** (the current Go 1.26 Darwin deployment floor). A newer GitHub Actions runner used to produce the build does not raise that runtime minimum.

### Integrity and provenance

The `.sha256` file verifies archive integrity. The attestation verifies signed build provenance and expected repository identity.

Apple Silicon example:

```bash
shasum -a 256 -c scrumboy-<tag>-darwin-arm64.tar.gz.sha256
gh attestation verify scrumboy-<tag>-darwin-arm64.tar.gz -R markrai/scrumboy
```

Intel users: substitute `darwin-amd64` for `darwin-arm64`.

### Install and run

Extract and run from a dedicated writable folder:

```bash
tar -xzf scrumboy-<tag>-darwin-arm64.tar.gz
./scrumboy
```

The process starts a local Scrumboy server. Open [http://localhost:8080](http://localhost:8080) after it starts.

### Runtime data

Default instance data is under `./data` relative to the working directory (including `./data/app.db` and related runtime files). Set `DATA_DIR` to store the database and uploads elsewhere. See [environment-variables.md](environment-variables.md#data_dir--sqlite_path).

### Signing and notarization

Current macOS release binaries are **not** Apple-signed and are **not** notarized. GitHub attestation verifies build provenance; it is not Apple code signing.

### Troubleshooting: Gatekeeper / quarantine

If macOS blocks launch of a binary downloaded from GitHub Releases **after** you have verified the checksum and attestation, clear quarantine for that file only:

```bash
xattr -d com.apple.quarantine ./scrumboy
```

Do not disable Gatekeeper or other system-wide protections.
