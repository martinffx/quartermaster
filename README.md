# quartermaster

[![CI](https://github.com/martinffx/quartermaster/actions/workflows/ci.yml/badge.svg)](https://github.com/martinffx/quartermaster/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/martinffx/quartermaster.svg)](https://pkg.go.dev/github.com/martinffx/quartermaster)

Short Postgres transactions for [sqlc](https://sqlc.dev) and
[pgx/v5](https://github.com/jackc/pgx). The transaction is handed to your closure as an
argument. It never goes in `context.Context`.

> The quartermaster issues the kit, knows exactly who has it, and makes sure it comes back.

```go
txr := quartermaster.New(pool, db.New(pool)) // pool is a *pgxpool.Pool

payment, err := txr.RunTx(ctx, func(q *db.Queries) (Payment, error) {
    if err := q.InsertPayment(ctx, params); err != nil {
        return Payment{}, err
    }
    return payment, q.InsertOutbox(ctx, event)
})
```

`RunTx` begins a transaction, calls your function with `db.Queries` bound to it (via sqlc's
generated `WithTx`), and commits if you return `nil`. An error, a panic or a canceled context rolls
it back. Whatever your closure returns comes back out, generically.

It is deliberately small and deliberately narrow: **sqlc output with `sql_package: "pgx/v5"`, on a
`*pgxpool.Pool`.** There's no driver abstraction and no support for other query layers.

## Why

A transaction is a critical section. Keep it short, block on nothing outside the database, and
give it one owner. Most Go transaction helpers do the opposite: they put the open transaction in
`ctx` and quietly join nested calls, so nothing at the call site tells you whether you're inside
one. A network call slips in, the pool drains the next time that service is slow, and the
rollback erases a debit for an order that already exists at the broker.

Read the post this came from: *When did we forget transactions are critical sections?*

The name is the idea. A transaction is kit issued from the stores, not something left lying around
the camp:

- **Issued to a named holder.** `q` goes to exactly one closure, never into `ctx`.
- **Signed out as briefly as possible.** No network calls while you hold it.
- **Always comes back.** It's returned on every path: commit, error, panic or cancel.
- **Nobody draws twice on one chit.** Nested calls get their own issue.

## Semantics

- `fn`'s error is returned unwrapped, so `errors.Is` works. Begin and commit errors are wrapped as
  `quartermaster: begin: ...` and `quartermaster: commit: ...`.
- A panic in `fn` rolls back and propagates. There is no recover.
- A commit error doesn't always mean "rolled back". If the context is canceled while `COMMIT` is on
  the wire, the server may or may not have committed. Check the state before retrying a write that
  isn't idempotent.
- The rollback uses `context.WithoutCancel`, so a canceled request still gets a clean `ROLLBACK` and
  the connection goes back to the pool instead of being closed. The rollback is bounded so a wedged
  server can't block `RunTx` forever: `WithRollbackTimeout(d)` if set, otherwise the time left on
  the context's deadline, otherwise 5 seconds.
- **No joining.** Calling `RunTx` inside `RunTx` opens a second, independent transaction that
  commits or rolls back on its own. If an invariant really spans two repositories, write the
  method that owns it. Nesting has a cost: it holds a **second pooled connection**, so enough
  concurrent nesting can exhaust the pool. If the inner transaction touches rows the outer one has
  locked, it waits on the outer transaction while the outer waits in Go. Postgres's deadlock
  detector can't see that, so only a context deadline or `lock_timeout` ends it. Pass a context with
  a deadline.
- `WithTxOptions` sets default isolation/access mode. `RunTxOpts` overrides it per call. Under
  `Serializable` or `RepeatableRead`, Postgres can fail a transaction with `40001`; retrying the
  whole closure is up to you.

## External calls

Don't keep the transaction open around a network call. Store pending, make the call, store the
result: two short transactions and no connection held in between. See
[`examples/payments`](examples/payments), which also shows an idempotency key and an outbox write
committing together, and tests that no connection is held during `Submit`.

## Alternatives

| Library | How the transaction reaches your code |
|---|---|
| [Thiht/transactor](https://github.com/Thiht/transactor), [go-transaction-manager](https://github.com/avito-tech/go-transaction-manager), [pgxtx](https://github.com/virp/pgxtx), [pgxatomic](https://github.com/ysomad/pgxatomic) | `context.Context`, nested calls join |
| [metalfm/transactor](https://github.com/metalfm/transactor) | typed argument, closure returns only `error` |
| `pgx.BeginFunc` | raw `pgx.Tx`, no sqlc binding |

## Development

`make help` lists the targets. Tests run against a real Postgres in a container (needs Docker):
`make test`. `make check` runs everything CI runs. Generated sqlc code is committed. Regenerate with
`make generate`; sqlc is run through `go run` and is not a dependency of this module.

CI runs lint, `go mod tidy` and `go generate` drift checks, the tests with `-race`, and
`govulncheck` on every pull request. Releases use
[release-please](https://github.com/googleapis/release-please): merge commits follow
[Conventional Commits](https://www.conventionalcommits.org), a release PR collects the changelog and
the next version, and merging it tags the release.

## License

MIT
