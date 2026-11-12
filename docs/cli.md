# scrumboy-cli

A command-line client for a Scrumboy server: list, search, create, update and
move todos, view boards, and script against a server from a shell or CI.

It is a thin client over the documented MCP HTTP tool interface
([API.md](../API.md), `POST /mcp`). It shares no code with the server, so it
can be built and released independently of server internals.

## Install

**Homebrew** (macOS and Linux). The repo is its own tap:

```sh
brew tap markrai/scrumboy https://github.com/markrai/scrumboy
brew install markrai/scrumboy/scrumboy-cli            # latest release
brew install --HEAD markrai/scrumboy/scrumboy-cli     # build main from source
```

**Release archive:** download `scrumboy-cli_<version>_<os>_<arch>.tar.gz`
(`.zip` on Windows) from the GitHub release, verify it against
`scrumboy-cli_<version>_checksums.txt`, and put `scrumboy-cli` on your `PATH`.

**From source:**

```sh
go install ./cmd/scrumboy-cli     # or: go build -o scrumboy-cli ./cmd/scrumboy-cli
```

## Authenticate

Create an `sb_` API token in the web UI (or `POST /api/me/tokens`, see
[API.md](../API.md#api-access-tokens-rest)). Use a human-owned token for
personal use and a service-account token for unattended automation or CI.

```sh
echo "$TOKEN" | scrumboy-cli auth login --server https://scrumboy.example.com --with-token
scrumboy-cli auth status
```

`login` verifies the token (`me_get`) before saving it to
the user config directory (`~/.config/scrumboy/config.json` on Linux, `~/Library/Application Support/scrumboy/` on macOS, `%AppData%\scrumboy\` on Windows; mode `0600`). For CI skip `login` and set
`SCRUMBOY_URL` and `SCRUMBOY_TOKEN`. Settings resolve flag > environment >
config file.

| Setting | Flag | Environment |
|---|---|---|
| Server URL | `--server` | `SCRUMBOY_URL` |
| API token | `--token` (prefer `login`: argv is visible to other users) | `SCRUMBOY_TOKEN` |
| Default project | `-p`, `--project` | `SCRUMBOY_PROJECT` |
| Config file | `--config` | `SCRUMBOY_CLI_CONFIG` |

## Use

```sh
scrumboy-cli project list
scrumboy-cli board -p demo
scrumboy-cli todo list -p demo --column doing --assignee me
scrumboy-cli todo list -p demo --tag bug --tag ux --all --json | jq '.[].title'
scrumboy-cli todo get demo/12
scrumboy-cli todo create "Fix login redirect" -p demo --tag bug --priority high --due 2026-12-01
scrumboy-cli todo update demo/12 --assignee me --points 3 --field mr_url=https://example.com/mr/1
scrumboy-cli todo move demo/12 done
scrumboy-cli todo link demo/12 15 --type blocks
scrumboy-cli sprint list -p demo
```

- A todo is `slug/12`, `slug#12`, or `12` together with `-p`.
- `todo update` sends only the flags you pass. `none` clears a value
  (`--priority none`, `--due none`); `--field key=` clears a custom field and
  `--field key:=3` sends a JSON number.
- `board`/`todo list` use `board_get`; `--all` follows per-column pagination.
  `todo search` is a title lookup for link targets, not a filter.
- Destructive commands (`todo delete`, `project delete`) need `--yes` unless
  run interactively.
- `--json` prints the tool's `data` payload (a JSON array for lists) for
  scripting. Errors go to stderr; exit status is `0` ok, `1` failure
  (including API errors), `2` usage.
- Anything without a dedicated command (webhooks, admin, custom fields,
  metrics, ...) is reachable with the raw escape hatch:

  ```sh
  scrumboy-cli tool system_getCapabilities          # lists every tool
  scrumboy-cli tool metrics_getBurndown --input '{"projectSlug":"demo"}'
  ```

## Releasing

Publishing a GitHub Release triggers `.github/workflows/cli-release.yml`,
which builds the CLI from the release tag (the CLI is versioned with the
server), attaches `scrumboy-cli_<version>_<os>_<arch>` archives for
linux/darwin/windows x amd64/arm64, a checksums file, and a rendered Homebrew
formula (`scrumboy-cli.rb`). `workflow_dispatch` with a tag rebuilds the assets
for an existing release.

Each release also opens a PR that updates `Formula/scrumboy-cli.rb`; merge it
and `brew install` serves the new version (the formula installs the prebuilt
archive for your OS and CPU). Until the first release the formula only supports
`--HEAD`. The release workflow smoke-tests the binary natively on Linux, macOS
and Windows. CI runs the CLI tests on all three and uploads cross-built
binaries for every pull request.

Locally: `scripts/build-cli-release.sh <version> <outdir>` and
`scripts/render-homebrew-formula.sh <version> <owner/repo> <checksums.txt>`.
