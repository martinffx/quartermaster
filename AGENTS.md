# AGENTS.md

## What this is

`quartermaster` is a small Go library for short PostgreSQL transactions with sqlc (`sql_package: "pgx/v5"`)
and a `*pgxpool.Pool`. It exists because Go 1.27 allows generic methods: `Transactor[Q].RunTx[R]` takes a
`func(Q) (R, error)` and returns `(R, error)`, a typed result from the closure. `RunTx` begins a
transaction, hands the closure Queries bound to it, and commits or rolls back. The transaction is an
argument. It is never stored in `context.Context`, and nested calls never join. The README "Design" section
has the reasoning; read it before proposing API changes.

## Commands

`make help` lists everything. Tests start a real Postgres in a container, so Docker must be running.

- `make test`: `go test -race -count=2 ./...`
- `make check`: lint, tidy and generate drift checks, `govulncheck`, tests. This is what CI runs, so run it
  before committing.
- `make generate`: regenerate the committed sqlc code.
- `make fmt`, `make lint`

## Layout

- `quartermaster.go`: the whole library. The package doc holds the semantics (Errors, Rollback, Nested
  transactions).
- `internal/testdb`: sqlc output the library tests run against.
- `internal/pgtest`: starts the Postgres container; `Unique` makes test names and keys.
- `examples/payments`: the worked example (idempotency key and outbox in one transaction, two short
  transactions around a network call).

## Rules

- Keep the public API small. No driver abstraction, no transaction in `ctx`, no joining, no support for
  other query layers.
- `RunTx` and `RunTxOpts` must keep returning the closure's typed result. That is the point of the library.
- `fn`'s error is returned unwrapped. Begin and commit errors keep the `quartermaster: begin: ` and
  `quartermaster: commit: ` prefixes and wrap the cause with `%w`.
- Generated sqlc code is committed. Edit `schema.sql` or `query.sql`, then `make generate`. Never edit
  `*.sql.go` by hand.
- `examples/payments` teaches one pattern. Don't turn it into a production payments service; known gaps are
  deliberate.
- Tests in a package share one database. Build names, IDs and keys with `pgtest.Unique(t, ...)`, never assert
  absolute counts of shared data, and use `t.Context()`.
- Every exported identifier needs a doc comment (revive enforces it). When behavior changes, update the
  package doc and the README "Semantics" section together.
- `//nolint` needs a reason after it.

## Commits and releases

Use [Conventional Commits](https://www.conventionalcommits.org): `feat`, `fix`, `docs`, `test`, `build`,
`ci`, `chore`, with `!` for breaking changes. release-please reads them to choose the next version and write
the changelog; while the module is pre-1.0, a breaking change bumps the minor version. Merging the release PR
tags the release.
