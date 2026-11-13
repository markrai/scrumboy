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

Two ways to sign in, both verified with `me_get` before anything is saved.

**API token** (scripts, CI, service accounts). Create an `sb_` token in the web
UI (or `POST /api/me/tokens`, see [API.md](../API.md#api-access-tokens-rest)).
Use a human-owned token for personal use and a service-account token for
unattended automation.

```sh
echo "$TOKEN" | scrumboy-cli auth login --server https://scrumboy.example.com --with-token
```

**OAuth** (interactive, no token to copy around). Opens your browser, you sign
in and approve, and the CLI keeps the session alive with a refresh token:

```sh
scrumboy-cli auth login --oauth --server https://scrumboy.example.com
scrumboy-cli auth login --oauth --no-browser --server ...   # prints the URL; paste the redirect back
```

It uses OAuth 2.1 as described in [oauth.md](oauth.md): discovery, dynamic
client registration, authorization code with PKCE (S256) and a loopback
redirect on `127.0.0.1`. Refresh tokens rotate on every use, so the CLI saves
the new one before using it; `auth logout` revokes the session on the server
(and warns if the server does not confirm, in which case the session stays
valid until it expires).
OAuth tokens are only accepted by the server's `/mcp/rpc` endpoint, so the CLI
switches to that transport automatically (a bearer token that does not start
with `sb_`, passed by flag or environment, is treated as an OAuth token too).

```sh
scrumboy-cli auth status      # who you are, and which kind of credential
scrumboy-cli auth logout
```

Credentials are saved in the user config directory (`~/.config/scrumboy/config.json`
on Linux, `~/Library/Application Support/scrumboy/` on macOS, `%AppData%\scrumboy\`
on Windows), readable only by you: mode `0600` on Linux and macOS, and on
Windows an access list that grants only your account (plus SYSTEM and
Administrators), also for a `--config` path outside your profile. For CI skip
`login` and set `SCRUMBOY_URL` and `SCRUMBOY_TOKEN`. Settings resolve flag >
environment > config file.

The CLI never follows HTTP redirects, so a credential is only ever sent to the
URL you configured: if the server answers with a redirect (for example plain
`http://` to `https://`), the command fails and names the target to use with
`--server` instead.

| Setting | Flag | Environment |
|---|---|---|
| Server URL | `--server` | `SCRUMBOY_URL` |
| API token / bearer token | `--token` (prefer `login`: argv is visible to other users) | `SCRUMBOY_TOKEN` |
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
scrumboy-cli sprint create "Sprint 4" --start 2026-12-01 --end 2026-12-14 -p demo
scrumboy-cli member add 7 --role contributor -p demo
scrumboy-cli webhook create https://example.com/hook --event todo.created -p demo
scrumboy-cli dashboard todos
scrumboy-cli metrics burndown -p demo
```

Commands (run `scrumboy-cli <command> -h` for flags):

| Command | Subcommands |
|---|---|
| `auth` | `login`, `logout`, `status` |
| `project` | `list`, `create`, `update`, `delete` |
| `board` | show a project's board |
| `todo` | `list`, `get`, `create`, `update`, `move`, `delete`, `search`, `archive`, `restore`, `links`, `link`, `unlink` |
| `sprint` | `list`, `active`, `get`, `create`, `update`, `activate`, `close`, `delete` |
| `tag` | `list`, `mine`, `color`, `delete` |
| `member` | `list`, `available`, `add`, `role`, `remove` |
| `column`, `priority` | `list`, `add`, `update`, `delete` |
| `field` (custom fields) | `list`, `add`, `delete` |
| `webhook` | `list`, `create`, `delete` |
| `dashboard` | `summary`, `todos` |
| `metrics` | `burndown`, `backlog` |
| `admin` | `users`, `service-account`, `service-token` (owner/admin role) |
| `tool` | any server tool by name |

- A todo is `slug/12`, `slug#12`, or `12` together with `-p`.
- `todo update` sends only the flags you pass. `none` clears a value
  (`--priority none`, `--due none`); an empty value is rejected rather than
  clearing. `--field key=` clears a custom field and `--field key:=3` sends a
  JSON number.
- `board`/`todo list` use `board_get`; `--all` follows per-column pagination.
  `todo search` is a title lookup for link targets, not a filter.
- Destructive commands (anything named `delete`, `remove` or `revoke`) need
  `--yes` unless run interactively.
- `--json` prints the tool's `data` payload (a JSON array for lists) for
  scripting. Errors go to stderr; exit status is `0` ok, `1` failure
  (including API errors), `2` usage.
- Every server tool is reachable, including any added later, with the raw
  escape hatch:

  ```sh
  scrumboy-cli tool system_getCapabilities          # lists every tool
  scrumboy-cli tool metrics_getBurndown --input '{"projectSlug":"demo"}'
  ```

Not covered, because the server exposes them only over its browser (REST)
API: the archived-stories listing, inline image upload, `.sbz` backup/restore,
public-board publication, and API token management (`/api/me/tokens` needs a
session cookie).

## Releasing

Publishing a GitHub Release triggers `.github/workflows/cli-release.yml`,
which builds the CLI from the release tag (the CLI is versioned with the
server) as `scrumboy-cli_<version>_<os>_<arch>` archives for
linux/darwin/windows x amd64/arm64, smoke-tests every binary natively, and only
then attaches the archives, a checksums file and a rendered Homebrew formula
(`scrumboy-cli.rb`) to the release. `workflow_dispatch` with a tag does the same
for an existing release (also a draft).

The archives are reproducible (the same commit gives the same bytes), and
`scripts/publish-cli-release.sh` never replaces a published asset: a re-run
skips assets that are already there with the same checksum and fails, without
uploading anything, if any differs. To really replace an asset, delete it on
the release first. With GitHub's immutable releases enabled, assets can only be
added while the release is a draft, so the run fails for an already published
release that lacks them.

For a published, non-prerelease release the workflow then opens a PR that
updates `Formula/scrumboy-cli.rb`; merge it and `brew install` serves the new
version (the formula installs the prebuilt archive for your OS and CPU). Until
the first release the formula only supports `--HEAD`. Opening that PR needs
**Settings > Actions > General > Allow GitHub Actions to create and approve
pull requests**; without it the release assets are still published and only
the formula job fails (re-run it after enabling the setting). A PR opened by
the workflow token does not start CI on its own; close and reopen it to run the
checks. CI runs the CLI tests on
Linux, macOS and Windows and uploads cross-built binaries for every pull
request.

Locally (GNU tar and Info-ZIP `zip`, as on Linux):
`scripts/build-cli-release.sh <version> <outdir>` and
`scripts/render-homebrew-formula.sh <version> <owner/repo> <checksums.txt>`.
`bash scripts/test-publish-cli-release.sh` (needs `jq`; also run by CI) checks
the publication rules against a stub `gh`.
