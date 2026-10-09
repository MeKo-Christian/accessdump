# accessdump

Dump Microsoft Access databases (`.mdb` / `.accdb`) to plain text on Linux — no Windows, no Wine, no ODBC drivers required.

Extracts two things from an Access file:

- **VBA source code** — every module as a `.bas` or `.cls` file, ready for version control or code review
- **Database schema** — table definitions, foreign-key relationships, and saved queries as SQL DDL and Markdown

## What it does

Access databases are binary files built on the Jet/ACE page format. `accessdump` reads those pages directly and recovers:

| What                               | Where it comes from                                  |
| ---------------------------------- | ---------------------------------------------------- |
| VBA module source (`.bas`, `.cls`) | Compressed binary streams in the VBA project storage |
| Table columns, types, constraints  | `MSysObjects` / column definition pages              |
| Foreign-key relationships          | `MSysRelationships` system table                     |
| Saved queries                      | `MSysQueries` system table                           |

It handles two classes of database when extracting VBA:

| Situation                                                      | How it's handled                                             |
| -------------------------------------------------------------- | ------------------------------------------------------------ |
| Standard VBA project (`MSysAccessStorage` intact)              | Reads PROJECT + dir streams, extracts each module            |
| Stripped project (system tables removed but page data on disk) | Two-pass LVAL chain scanner recovers orphaned module streams |
| No VBA at all (pure data database)                             | Reports `modules=0`, no error                                |

## Installation

```sh
go install github.com/MeKo-Christian/accessdump@latest
```

Or build from source:

```sh
git clone https://github.com/MeKo-Christian/accessdump
cd accessdump
go build -o accessdump .
```

**Requires Go 1.22 or later.**

## Usage

### Extract VBA modules

```sh
# Single file — writes to vba-output/<dbname>/
accessdump extract MyDatabase.mdb

# Multiple files
accessdump extract *.mdb

# Recurse into a directory
accessdump extract --recursive /path/to/databases/

# Write all modules into a single flat directory
accessdump extract --flat --output-dir ./src MyDatabase.mdb

# Skip duplicate databases (by file hash)
accessdump extract --dedupe *.mdb

# Stop on first error
accessdump extract --strict *.mdb

# Show recovery details
accessdump extract --verbose MyDatabase.mdb
```

### Extract database schema

```sh
# Single file — writes <dbname>.schema.sql and <dbname>.schema.md
accessdump schema MyDatabase.mdb

# Multiple files / recursive
accessdump schema *.mdb
accessdump schema --recursive /path/to/databases/

# Flat output (no per-database subdirectory)
accessdump schema --flat --output-dir ./schema MyDatabase.mdb

# Skip duplicates, stop on first error
accessdump schema --dedupe --strict *.mdb
```

Output per database:

```
vba-output/
└── MyDatabase/
    ├── MyDatabase.schema.sql   ← CREATE TABLE / ALTER TABLE / CREATE VIEW statements
    └── MyDatabase.schema.md    ← human-readable table and relationship reference
```

The `.schema.sql` file contains:

- `CREATE TABLE` with column names, SQL types, `NOT NULL`, and `AUTOINCREMENT`
- `ALTER TABLE … ADD CONSTRAINT … FOREIGN KEY` for every relationship, with `ON UPDATE`/`ON DELETE CASCADE` where applicable
- `CREATE VIEW` for SELECT queries without parameters; all other queries (action, union, crosstab, parameter queries) are preserved as SQL comments

### Extract saved queries

```sh
# SQL of every saved query
accessdump queries MyDatabase.mdb

# Counts per query type, plus every query that could not be reconstructed
accessdump queries --summary MyDatabase.mdb

# Everything as JSON
accessdump queries --json MyDatabase.mdb
```

Access does not store the SQL text of a saved query; it stores the query broken
into rows of `MSysQueries` and rebuilds the SQL when it is shown. `accessdump`
rebuilds it the same way, for select, make-table, append, update, delete,
crosstab, union, pass-through and data-definition queries. The row layout and
rebuilding rules follow [Jackcess](https://jackcess.sourceforge.io/)
(Apache License 2.0); see `internal/mdb/query.go`.

- Embedded queries are included: the `~sq_` record and row sources Access keeps
  for SQL typed directly into a form (`~sq_f<Form>`), a report
  (`~sq_r<Report>`) or a control (`~sq_c<Form>~sq_c<Control>`,
  `~sq_d<Report>~sq_d<Control>`). Each comes with its owner.
- Pass-through queries come with their connect string. `PWD=` and `Password=`
  values are replaced by `***`, in the connect string and in the SQL.
- A query that cannot be rebuilt is listed with status `unsupported` and the
  reason, never with partial SQL.

From Go:

```go
queries, err := extract.Queries("MyDatabase.mdb", nil)
for _, q := range queries {
    if q.SQLStatus != extract.SQLStatusFound {
        fmt.Printf("%s: %s (%s)\n", q.Name, q.SQLStatus, q.Reason)
        continue
    }
    fmt.Printf("-- %s (%s)\n%s\n", q.Name, q.Type, q.SQL)
}
```

The SQL is semantically what Access shows in SQL view; brackets, parentheses
and line breaks can differ. `testdata/queries/AccessdumpQueries.bas` builds a
fixture with one query per type and exports Access's own SQL for comparison;
see [Development](#development).

### List modules without extracting

```sh
accessdump list MyDatabase.mdb

# JSON output
accessdump list --json MyDatabase.mdb
```

Example output:

```
file: MyDatabase.mdb modules: 5
MODULE                         TYPE       STREAM                         BYTES    PARTIAL
Module1                        standard   Module1                        4821     false
Sheet1                         document   Sheet1                         1203     false
ThisWorkbook                   document   ThisWorkbook                    542     false
```

### Database metadata

```sh
# Basic info
accessdump info MyDatabase.mdb

# Show VBA storage tree
accessdump info --tree MyDatabase.mdb

# Forensic scan — useful when standard extraction fails
accessdump info --forensic MyDatabase.mdb
```

## Output

By default, all output goes into `vba-output/<database-name>/`:

```
vba-output/
└── MyDatabase/
    ├── Module1.bas              ← VBA modules
    ├── UserForm1.cls
    ├── Sheet1.cls
    ├── MyDatabase.schema.sql    ← schema (schema command)
    └── MyDatabase.schema.md
```

Use `--output-dir` to specify the root directory, and `--flat` to skip the per-database subdirectory.

## Supported formats

| Format               | Extension | Engine / Layout                                  | Status                                                                                                              |
| -------------------- | --------- | ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------- |
| Access 97            | `.mdb`    | Jet 3.x (`jet3-2k`)                              | Partial: parser paths, memo/LVAL, and VBA extraction are implemented; broader fixture coverage is still in progress |
| Legacy header `.mdb` | `.mdb`    | non-standard Jet header + 4K pages (`legacy-4k`) | Supported via layout probing                                                                                        |
| Access 2000 – 2003   | `.mdb`    | Jet 4.0 (`jet4-4k`)                              | Supported                                                                                                           |
| Access 2007 – 2019+  | `.accdb`  | ACE (`jet4-4k`)                                  | Supported                                                                                                           |

## Current limitations

- Encrypted or password-protected Access databases are not supported yet.
- `jet3-2k` real-world fixture coverage in CI is still limited; synthetic Jet3 tests are always run, while legacy fixture tests are optional.
- Corrupted/truncated databases are best-effort and may produce partial VBA recovery.

## How forensic VBA recovery works

When a database's `MSysAccessStorage` system table is missing or its VBA module references have been stripped, `accessdump` falls back to a two-pass LVAL page scan:

1. **Pass 1** — reads every data page and builds a map of all LVAL chain next-pointers, identifying which records are chain _starts_ vs. _continuations_.
2. **Pass 2** — for each chain start whose content byte pattern suggests a VBA module stream, reads up to 256 KB and scans for a valid MS-OVBA `CompressedContainer`. If decompression succeeds and the output contains `Attribute VB_Name`, the module is recovered.

Duplicate module names (multiple on-disk copies) are deduplicated; only the first found copy is kept.

## Development

```sh
# Run tests
go test ./...

# Jet3 synthetic regression subset
go test ./internal/mdb -run 'TestProbeClassifiesSyntheticJet32K|TestOpenJet3Uses2048PageSize|TestReadTableDefJet3Synthetic|TestReadRowsJet3Synthetic|TestResolveMemoJet3'
go test ./internal/vba -run 'TestScanOrphanedLvalModules_Jet3LayoutSynthetic'

# Optional local legacy fixture regression (if fixture exists)
go test ./internal/vba -run TestExtractAllModulesFromLegacyFixture
go test ./cmd -run TestLoadSchema_legacyFixture

# Run with verbose output on a test file
go run . extract --verbose testdata/sample.mdb
go run . schema testdata/sample.mdb
```

#### Comparing queries with Access

`TestQueriesMatchAccess` compares the rebuilt SQL with what Access shows. It
needs a database plus an export of `CurrentDb.QueryDefs(...).SQL`, made with
the VBA module `testdata/queries/AccessdumpQueries.bas` on Windows:

1. Create an empty Access 2002-2003 `.mdb`, import the module, run
   `BuildFixture` in the Immediate window. It creates tables, one query per
   query type, a form, a report and a combo box with SQL sources, and writes
   `<database>.expected.txt`.
2. Copy both files to `testdata/queries/fixture.mdb` and
   `testdata/queries/fixture.mdb.expected.txt`.

Without these files the test is skipped. To check a production database, run
`ExportQuerySQL` in it instead, and point the test at it:

```sh
ACCESSDUMP_COMPARE_MDB=/path/to/Frontend.mdb go test ./extract -run TestQueriesMatchAccess -v
```

Test fixtures live in `testdata/`. Set `VBA_FIXTURE_DIR` to point at a directory of real `.mdb` files for integration testing against production data.

## License

MIT
