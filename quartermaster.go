// Package quartermaster runs short PostgreSQL transactions for sqlc's pgx/v5
// output and returns a typed result from the closure. It requires Go 1.27,
// whose generic methods let [Transactor.RunTx] declare its own result type: it
// takes a func(Q) (R, error) and returns (R, error).
//
// The transaction is an argument, never read from or stored in
// context.Context. The quartermaster issues the kit, knows exactly who has it,
// and makes sure it comes back: [Transactor.RunTx] begins a transaction, binds
// it to your generated Queries with WithTx, calls fn exactly once, and then
// commits or rolls back.
//
// # Usage
//
// Wire a [Transactor] once, next to your sqlc-generated Queries:
//
//	txr := quartermaster.New(pool, db.New(pool)) // pool is a *pgxpool.Pool
//
//	payment, err := txr.RunTx(ctx, func(q *db.Queries) (Payment, error) {
//		// q is bound to the transaction; nothing is read from ctx.
//		...
//	})
//
// # Errors
//
// The error returned by fn is returned unwrapped, so [errors.Is] and
// [errors.As] work on it. Errors from beginning and committing are wrapped with
// the prefixes "quartermaster: begin: " and "quartermaster: commit: ".
//
// A commit error does not always mean the transaction was rolled back. If the
// context is canceled while COMMIT is on the wire, the server may or may not
// have committed, and the caller cannot tell which. Check the state before
// retrying a write that is not idempotent.
//
// # Rollback
//
// If fn returns an error or panics, the transaction is rolled back; a panic
// propagates. The rollback runs even if ctx was canceled, so a canceled request
// still gets a clean ROLLBACK and the connection can be reused. It is bounded
// so that an unresponsive server cannot block [Transactor.RunTx] forever. The
// bound is, in order: the duration set in [Config.RollbackTimeout]; the time
// remaining until ctx's deadline, if it has one and has not passed; otherwise
// five seconds.
//
// # Nested transactions
//
// There is no context lookup and no joining: calling RunTx inside fn starts a
// separate transaction that commits or rolls back independently. That has two
// costs. The nested transaction holds a second pooled connection, so enough
// concurrent nesting can exhaust the pool, with every outer transaction waiting
// for a connection that only its own return can free. And if the nested
// transaction touches rows the outer one has locked, it waits on the outer
// transaction while the outer one waits in Go; PostgreSQL's deadlock detector
// cannot see that, so only ctx or lock_timeout ends it. Pass a context with a
// deadline, and prefer writing one method that owns an invariant over nesting.
package quartermaster

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultRollbackTimeout bounds a rollback when neither
// [Config.RollbackTimeout] nor a live ctx deadline applies.
const defaultRollbackTimeout = 5 * time.Second

// Queries is satisfied by sqlc-generated *Queries (sql_package: "pgx/v5"),
// which has a WithTx method returning a Queries bound to the transaction.
type Queries[Q any] interface {
	WithTx(pgx.Tx) Q
}

// Config configures a Transactor. The zero value is valid.
type Config struct {
	// TxOptions are the isolation level, access mode and deferrable mode for
	// every transaction the Transactor starts. For a different level, build
	// another Transactor with [NewWithConfig].
	//
	// Under [pgx.Serializable] and [pgx.RepeatableRead], PostgreSQL can fail a
	// transaction with SQLSTATE 40001; retrying the whole closure is up to the
	// caller.
	TxOptions pgx.TxOptions

	// RollbackTimeout bounds how long a rollback may take. Zero or less uses the
	// default: the time remaining until the context's deadline if it has one
	// that has not passed, otherwise five seconds. See the package
	// documentation.
	RollbackTimeout time.Duration
}

// Transactor runs functions inside a transaction. Q is the sqlc-generated
// *Queries type. A Transactor is safe for concurrent use by multiple
// goroutines.
type Transactor[Q Queries[Q]] struct {
	pool *pgxpool.Pool
	q    Q
	cfg  Config
}

// New returns a Transactor that begins transactions on pool and binds each one
// to q with q.WithTx, using the default [Config]:
//
//	txr := quartermaster.New(pool, db.New(pool))
func New[Q Queries[Q]](pool *pgxpool.Pool, q Q) *Transactor[Q] {
	return NewWithConfig(pool, q, Config{})
}

// NewWithConfig is like [New] but uses cfg:
//
//	txr := quartermaster.NewWithConfig(pool, db.New(pool), quartermaster.Config{
//		TxOptions:       pgx.TxOptions{IsoLevel: pgx.RepeatableRead},
//		RollbackTimeout: 2 * time.Second,
//	})
func NewWithConfig[Q Queries[Q]](pool *pgxpool.Pool, q Q, cfg Config) *Transactor[Q] {
	return &Transactor[Q]{pool: pool, q: q, cfg: cfg}
}

// RunTx begins a transaction, calls fn with Queries bound to it, and commits if
// fn returns a nil error. If fn returns an error or panics, the transaction is
// rolled back; the error from fn is returned unwrapped, and a panic propagates.
// See the package documentation for how errors from begin and commit are
// reported.
//
// Keep fn short and don't make network calls inside it. The transaction holds a
// pooled connection and any row locks until fn returns.
//
// RunTx never reads a transaction from ctx. Calling RunTx from inside fn starts
// a separate transaction on a second connection that commits or rolls back
// independently; see "Nested transactions" in the package documentation.
func (t *Transactor[Q]) RunTx[R any](ctx context.Context, fn func(Q) (R, error)) (R, error) {
	var zero R

	tx, err := t.pool.BeginTx(ctx, t.cfg.TxOptions)
	if err != nil {
		return zero, fmt.Errorf("quartermaster: begin: %w", err)
	}
	// Roll back even if ctx was canceled: a clean ROLLBACK lets the connection go
	// back to the pool, where a canceled ctx would make pgx close it instead.
	// WithoutCancel also drops ctx's deadline, so bound the rollback ourselves; a
	// wedged server must not be able to block RunTx forever. After a successful
	// commit Rollback returns pgx.ErrTxClosed, which is expected, and on the
	// error path there is already an error to return, so this one is ignored.
	defer func() {
		rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout(ctx, t.cfg.RollbackTimeout))
		defer cancel()
		_ = tx.Rollback(rbCtx)
	}()

	res, err := fn(t.q.WithTx(tx))
	if err != nil {
		return zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, fmt.Errorf("quartermaster: commit: %w", err)
	}
	return res, nil
}

// rollbackTimeout returns how long a rollback may take: configured if it is
// positive, else the time left until ctx's deadline if that is still in the
// future, else defaultRollbackTimeout. It is evaluated when the rollback runs,
// because the usual reason to roll back is that ctx has already expired.
func rollbackTimeout(ctx context.Context, configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 {
			return remaining
		}
	}
	return defaultRollbackTimeout
}
