# Repository Guidelines

## Project Structure & Module Organization

`accessdump` is a Go CLI for extracting VBA code and Access schema from `.mdb`/`.accdb` files.

- `main.go`: entrypoint.
- `cmd/`: Cobra CLI commands (`extract`, `schema`, `list`, `info`) and flag wiring.
- `internal/mdb/`: low-level Jet/ACE page parsing, schema/table decoding, LVAL chain handling.
- `internal/vba/`: VBA project parsing, MS-OVBA decompression, forensic stream recovery.
- `testdata/`: binary fixtures used by tests (including `testdata/sample.mdb`).
- `docs/`: design notes, including architecture.

Keep parsing logic in `internal/*` packages and keep `cmd/` focused on user-facing behavior and output.

## Build, Test, and Development Commands

- `go build -o accessdump .`: build local binary.
- `go test ./...`: run unit/integration tests.
- `go test -race ./...`: run race detector.
- `go test -coverprofile=coverage.out ./...`: generate coverage profile.
- `just coverage-report`: write `code-coverage-results.md` (total and per package) from `coverage.out`; CI posts it on every PR.
- `go run . extract --verbose testdata/sample.mdb`: run CLI against fixture.
- `just fmt`: format codebase via `treefmt` (gofumpt, gci, prettier).
- `just lint`: run `golangci-lint`.
- `just ci`: run format check, tests, lint, and module tidiness checks.

## Coding Style & Naming Conventions

- Use idiomatic Go and standard `gofmt` formatting.
- Formatting is enforced by `treefmt.toml` (`gofumpt` + `gci` for Go).
- Linting is enforced by `.golangci.yml`; treat warnings as actionable unless explicitly excluded.
- Prefer descriptive package-level APIs; keep binary parsing helpers small and explicit.
- Test files follow Go conventions: `*_test.go`, `TestXxx` function names.

## Testing Guidelines

- Primary framework is Go’s built-in `testing` package.
- Keep tests close to implementation (`internal/mdb/*_test.go`, `internal/vba/*_test.go`).
- Use `testdata/` fixtures for deterministic behavior.
- For broader real-world fixture coverage, set `VBA_FIXTURE_DIR=/path/to/mdbs`.
- Before opening a PR, run at least: `just fmt`, `just lint`, `go test ./...`.

## Commit & Pull Request Guidelines

- Follow Conventional Commit style seen in history (`feat:`, `fix:`, `chore:`, `refactor:`).
- Use imperative, scoped messages, e.g. `fix(vba): validate compressed container header`.
- PRs should include: concise summary, motivation, testing performed, and sample output when CLI behavior changes.
- Link related issues and call out breaking behavior or output format changes explicitly.
