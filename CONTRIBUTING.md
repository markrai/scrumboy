# Contributing to Scrumboy

Thank you for considering contributing to Scrumboy. This document explains how to get started.

## Before you begin

Contributions use the **Developer Certificate of Origin (DCO)**. Sign off each commit with Git’s `-s` flag so the commit message includes a `Signed-off-by` line with your name and email.

Example:

```bash
git commit -s -m "Fix board filter chip styling"
```

You do **not** need to sign a separate CLA, email a form, or use any other signing service. The `-s` on your commits is enough. Pull requests are checked by the [DCO workflow](.github/workflows/dco.yml); every commit in the PR must be signed off.

By contributing, you certify that you have the right to submit your work under the project license (see [LICENSE](LICENSE)).

## Development setup



### Fork and clone

1. Fork the Scrumboy repository on GitHub.
2. Clone your fork locally:
  ```bash
   git clone https://github.com/YOUR_USERNAME/scrumboy.git
   cd scrumboy
  ```



### Feature branches

Create a branch for your work:

```bash
git checkout -b feature/your-feature-name
```

Use descriptive branch names (e.g. `fix/login-redirect`, `feat/sprint-filter`).

## Building and testing



### Run locally

```bash
go run ./cmd/scrumboy
```

The server starts on `:8080` by default. Data is stored in `./data` unless overridden by env vars (see `internal/config/config.go`).

### Build

```bash
go build ./cmd/scrumboy
```



### Frontend (TypeScript)

The web UI lives in `internal/httpapi/web`. It requires Node.js `^20.19.0`, `^22.13.0`, or `>=24.0.0`; npm `11.6.1` is the canonical package manager version used to maintain the lockfile. Build it with:

```bash
cd internal/httpapi/web
npm install
npm run build
```

The output goes to `web/dist` and is embedded by the Go server at build time. The Docker build and CI run this step before building the binary.

### Tests

```bash
go test ./...
```



### Docker

```bash
docker compose up --build
```

Binds `127.0.0.1:8080:8080` and uses the config in `docker-compose.yml`.

## Code style

- **Go:** Follow standard `gofmt` formatting. Run `go fmt ./...` before committing.
- **TypeScript:** Use consistent formatting; the project uses TypeScript in `internal/httpapi/web`.
- Keep changes focused and avoid unrelated edits.



## Pull request guidelines

1. **DCO:** Every commit must include `Signed-off-by` (use `git commit -s`, or amend with `git commit --amend -s --no-edit` if you forgot). You do not need any separate agreement or signature beyond that.
2. **Tests:** Run `go test ./...` and ensure all tests pass. Major new functionality should include automated tests.
3. **Build:** Ensure `go build ./cmd/scrumboy` succeeds. If you change the frontend, run `npm run build` in `internal/httpapi/web` and include the built output.
4. **Description:** Provide a clear description of the change and why it is needed.
5. **Scope:** One logical change per PR when possible.
6. **Documentation impact:** See below when the change touches contracts that docs describe.



### Documentation impact

Before merge/release, if the change touches **migrations**, **public routes**, **env vars**, **persistence files**, **auth methods**, or **frontend dependency versions cited in docs**:

1. Update the affected docs/diagrams (and `docs/diagrams/catalog.json` if adding a diagram), **or**
2. Record **“no documentation impact”** with a one-line reason in the PR description.

When you change docs or the diagram catalog, run:

```powershell
node docs/scripts/verify-docs.mjs
```

Docs index: `[docs/README.md](docs/README.md)`.

## Questions

If you have questions, open an issue at [https://github.com/markrai/scrumboy/issues](https://github.com/markrai/scrumboy/issues) or email the maintainer at [markraidc@gmail.com](mailto:markraidc@gmail.com)