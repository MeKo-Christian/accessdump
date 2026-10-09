# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

## [v0.5.0]

### Added
- Saved queries: the SQL is rebuilt from `MSysQueries` for select, make-table,
  append, update, delete, crosstab, union, pass-through (including bulk) and
  data-definition queries, plus the embedded `~sq_` record and row sources of
  forms, reports and controls. A query that cannot be rebuilt is reported as
  `unsupported` with a reason instead of partial SQL.
- `extract.Queries(path, logger)` library API, and the `accessdump queries
  [--summary|--json]` command.
- `PWD=`/`Password=` values are redacted in connect strings and in SQL,
  including braced values with escaped `}}`.

### Changed
- `schema` uses the query type: only parameterless selects become
  `CREATE VIEW`, everything else stays a comment.
- Lint: `exhaustruct_v5` disabled (renamed in golangci-lint 2.14). Local
  `just lint` now needs golangci-lint 2.14 or newer.

### Fixed
- Rows that Jet moved to another page (overflow rows) were skipped. This
  affected every table: VBA extraction now finds modules it missed before,
  and databases whose VBA project was unreadable no longer fall back to the
  forensic scan, which could report leftover modules of deleted code.
- Jet 4 text stored with Unicode compression (`FF FE`) is decoded instead of
  being read as UCS-2, which garbled umlauts.
- Jet 3 query expressions are decoded as Jet 3 text.

## [v0.4.0]

### Added
- `extract` package: public Go library API for extracting VBA modules programmatically.
  Import `github.com/MeKo-Christian/accessdump/extract` and call `extract.Extract(path, logger)`.
- Jet3 parser coverage for table definition, row decoding, and MEMO/LVAL traversal.
- Jet3 VBA extraction regression tests:
  - synthetic 2K forensic scanner path
  - legacy fixture end-to-end extraction checks
- CLI diagnostics helpers for layout classification and actionable error hints.

### Changed
- `info` command now reports `pageSize`, `layoutClass`, and `layoutHint`.
- `extract` and `schema` command error output now includes troubleshooting hints and probe context when available.
- `extract` command now reports partial-recovery and warning counts per file.
- CI unit workflow now includes explicit Jet3 synthetic regression tests and optional legacy-fixture tests when available.

### Documentation
- Updated support matrix and limitations in `README.md` to reflect current Jet3 status.
- Updated `docs/ARCHITECTURE.md` to describe 2K/4K layout detection and current parser scope.
- Expanded `docs/fixtures.md` with regression command matrix.
