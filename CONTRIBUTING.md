# Contributing to Scrumboy

Thank you for considering contributing to Scrumboy. This document explains how to get started.

## Before you begin

**You must sign the [Contributor License Agreement (CLA)](CLA.md) before your contributions can be accepted.** By submitting a pull request, you agree to the terms in [CLA.md](CLA.md).

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

The web UI lives in `internal/httpapi/web`. Build it with:

```bash
cd internal/httpapi/web
npm install
npx tsc
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

1. **CLA:** Ensure you have agreed to the [CLA](CLA.md). Your first PR serves as your signature.
2. **Tests:** Run `go test ./...` and ensure all tests pass.
3. **Build:** Ensure `go build ./cmd/scrumboy` succeeds. If you change the frontend, run `npx tsc` in `internal/httpapi/web` and include the built output.
4. **Description:** Provide a clear description of the change and why it is needed.
5. **Scope:** One logical change per PR when possible.

## Questions

If you have questions, open an issue in the repository.
