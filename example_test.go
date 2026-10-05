package quartermaster_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/martinffx/quartermaster"
	"github.com/martinffx/quartermaster/internal/testdb"
)

// Wire the Transactor once, next to your sqlc-generated Queries.
func ExampleNew() {
	// pool is a *pgxpool.Pool; testdb.New is sqlc's generated constructor.
	txr := quartermaster.New(pool, testdb.New(pool))
	_ = txr
}

// Config sets defaults for every transaction the Transactor starts.
func ExampleNewWithConfig() {
	ctx := context.Background()

	// pool is a *pgxpool.Pool; testdb.New is sqlc's generated constructor.
	txr := quartermaster.NewWithConfig(pool, testdb.New(pool), quartermaster.Config{
		TxOptions:       pgx.TxOptions{IsoLevel: pgx.RepeatableRead},
		RollbackTimeout: 2 * time.Second,
	})

	level, err := txr.RunTx(ctx, func(q *testdb.Queries) (string, error) {
		return q.IsolationLevel(ctx)
	})
	fmt.Println(level, err)
	// Output: repeatable read <nil>
}

// Give each repository method its own short transaction. The Queries passed to
// the closure are bound to the transaction; nothing is read from ctx.
func ExampleTransactor_RunTx() {
	ctx := context.Background()

	// pool is a *pgxpool.Pool; testdb.New is sqlc's generated constructor.
	txr := quartermaster.New(pool, testdb.New(pool))

	item, err := txr.RunTx(ctx, func(q *testdb.Queries) (testdb.Item, error) {
		return q.InsertItem(ctx, "example")
	})
	if err != nil {
		fmt.Println("rolled back:", err)
		return
	}
	fmt.Println("committed:", item.Name)
	// Output: committed: example
}

// An error from the closure rolls the transaction back and comes back
// unwrapped, so errors.Is and errors.As work on it.
func ExampleTransactor_RunTx_rollback() {
	ctx := context.Background()
	txr := quartermaster.New(pool, testdb.New(pool))

	errInsufficient := errors.New("insufficient funds")
	_, err := txr.RunTx(ctx, func(q *testdb.Queries) (struct{}, error) {
		if _, err := q.InsertItem(ctx, "rolled-back"); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, errInsufficient
	})
	fmt.Println("same error:", errors.Is(err, errInsufficient))

	n, _ := testdb.New(pool).CountItemsByName(ctx, "rolled-back")
	fmt.Println("rows committed:", n)
	// Output:
	// same error: true
	// rows committed: 0
}

// RunTxOpts overrides the default options for one call. Under Serializable,
// PostgreSQL can fail a transaction with SQLSTATE 40001 (a serialization
// failure); retrying the whole closure is up to the caller.
func ExampleTransactor_RunTxOpts() {
	ctx := context.Background()
	txr := quartermaster.New(pool, testdb.New(pool))

	level, err := txr.RunTxOpts(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable},
		func(q *testdb.Queries) (string, error) {
			return q.IsolationLevel(ctx)
		})
	fmt.Println(level, err)
	// Output: serializable <nil>
}
