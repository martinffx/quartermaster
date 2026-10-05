# quartermaster

[![CI](https://github.com/martinffx/quartermaster/actions/workflows/ci.yml/badge.svg)](https://github.com/martinffx/quartermaster/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/martinffx/quartermaster.svg)](https://pkg.go.dev/github.com/martinffx/quartermaster)

> I can do more damage on my laptop in pajamas before my first cup of tea than you can in a year in the field.
> — Q, *Skyfall* (2012)

A typed transaction closure for [sqlc](https://sqlc.dev) and [pgx/v5](https://github.com/jackc/pgx),
built on Go 1.27 generic methods. **Requires Go 1.27+.**

Before 1.27 a method couldn't have its own type parameters, so a `Transactor` could only run
`func(Q) error`, and results left the closure through captured variables. `RunTx[R]` returns
whatever your closure returns:

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
it back.

It is deliberately narrow: sqlc output with `sql_package: "pgx/v5"`, on a `*pgxpool.Pool`. No driver
abstraction and no other query layers.

## Design

A DB transaction is a critical section: keep it short, block on nothing outside the database, and
give it one owner. Many popular `Transactor` helpers do the opposite. They put the open transaction
in `ctx` and quietly join nested calls, so nothing at the call site tells you whether you're inside
one, transactions end up spanning services, and a network call slips in.

Read the post this came from: [When did we forget transactions are critical sections?](https://www.martinrichards.me/post/when_did_we_forget_transactions_are_critical_sections/)

- **Issued to a named holder.** `q` goes to exactly one closure, never into `ctx`.
- **Signed out as briefly as possible.** No network calls while you hold it.
- **Always comes back.** It's returned on every path: commit, error, panic or cancel.
- **Nobody draws twice on one chit.** Nested calls get their own issue.

## Semantics

- `fn`'s error is returned unwrapped, so `errors.Is` works.
- A panic in `fn` rolls back and propagates. There is no recover.
- **No joining.** A nested `RunTx` opens a second, independent transaction on a second connection.
- `Config.TxOptions` sets the default isolation level; `RunTxOpts` overrides it per call.

Commit errors, the rollback timeout and the cost of nesting are in the
[package documentation](https://pkg.go.dev/github.com/martinffx/quartermaster).

## External calls

Don't keep the transaction open around a network call. Store pending, make the call, store the
result. [`examples/payments`](examples/payments) shows it, along with an idempotency key and an
outbox write committing together.

## Alternatives

`metalfm/transactor` is the closest design, a typed argument, but its closure can only return `error`.

| Library | How the transaction reaches your code |
|---|---|
| [Thiht/transactor](https://github.com/Thiht/transactor), [go-transaction-manager](https://github.com/avito-tech/go-transaction-manager), [pgxtx](https://github.com/virp/pgxtx), [pgxatomic](https://github.com/ysomad/pgxatomic) | `context.Context`, nested calls join |
| [metalfm/transactor](https://github.com/metalfm/transactor) | typed argument, closure returns only `error` |
| `pgx.BeginFunc` | raw `pgx.Tx`, no sqlc binding |

## Development

`make help` lists the targets. `make test` needs Docker, since the tests run against a real
Postgres in a container. `make check` runs everything CI runs.

## License

MIT
