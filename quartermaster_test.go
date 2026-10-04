package quartermaster_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/martinffx/quartermaster"
	"github.com/martinffx/quartermaster/internal/pgtest"
	"github.com/martinffx/quartermaster/internal/testdb"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := pgtest.Start(context.Background(), testdb.Schema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres: %v\n", err)
		os.Exit(1)
	}
	pool = p
	code := m.Run()
	stop()
	os.Exit(code)
}

func newTxr(opts ...quartermaster.Option) *quartermaster.Transactor[*testdb.Queries] {
	return quartermaster.New(pool, testdb.New(pool), opts...)
}

// count returns how many committed rows have name, read outside any transaction.
func count(t *testing.T, name string) int64 {
	t.Helper()
	n, err := testdb.New(pool).CountItemsByName(t.Context(), name)
	if err != nil {
		t.Fatalf("count %q: %v", name, err)
	}
	return n
}

func TestRunTxCommits(t *testing.T) {
	ctx := t.Context()
	name := pgtest.Unique(t, "commit")
	item, err := newTxr().RunTx(ctx, func(q *testdb.Queries) (testdb.Item, error) {
		return q.InsertItem(ctx, name)
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != name || item.ID == 0 {
		t.Fatalf("returned item = %+v", item)
	}
	if got := count(t, name); got != 1 {
		t.Fatalf("committed rows = %d, want 1", got)
	}
}

func TestRunTxRollsBackOnError(t *testing.T) {
	ctx := t.Context()
	name := pgtest.Unique(t, "error")
	boom := errors.New("boom")
	_, err := newTxr().RunTx(ctx, func(q *testdb.Queries) (struct{}, error) {
		if _, err := q.InsertItem(ctx, name); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, boom
	})
	if err != boom { //nolint:errorlint // asserting the error is returned unwrapped, not just matchable
		t.Fatalf("err = %v, want the original error unwrapped", err)
	}
	if got := count(t, name); got != 0 {
		t.Fatalf("rows after rollback = %d, want 0", got)
	}
}

func TestRunTxRollsBackOnPanic(t *testing.T) {
	ctx := t.Context()
	name := pgtest.Unique(t, "panic")
	func() {
		defer func() {
			if r := recover(); r != "kaboom" {
				t.Fatalf("recovered %v, want the panic to propagate", r)
			}
		}()
		_, _ = newTxr().RunTx(ctx, func(q *testdb.Queries) (struct{}, error) {
			if _, err := q.InsertItem(ctx, name); err != nil {
				return struct{}{}, err
			}
			panic("kaboom")
		})
	}()
	if got := count(t, name); got != 0 {
		t.Fatalf("rows after panic = %d, want 0", got)
	}
}

func TestRunTxRollsBackWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	name := pgtest.Unique(t, "cancel")

	_, err := newTxr().RunTx(ctx, func(q *testdb.Queries) (struct{}, error) {
		if _, err := q.InsertItem(ctx, name); err != nil {
			return struct{}{}, err
		}
		cancel()
		return struct{}{}, ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := count(t, name); got != 0 {
		t.Fatalf("rows after cancel = %d, want 0", got)
	}
	if n := pool.Stat().AcquiredConns(); n != 0 {
		t.Fatalf("acquired conns = %d, want 0: the rollback leaked a connection", n)
	}
}

func TestRunTxWrapsBeginError(t *testing.T) {
	// pgxpool.New connects lazily, so a pool built from the live DSN and closed
	// straight away makes BeginTx fail without a second container.
	closed, err := pgxpool.New(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()

	ran := false
	_, err = quartermaster.New(closed, testdb.New(closed)).RunTx(t.Context(), func(*testdb.Queries) (struct{}, error) {
		ran = true
		return struct{}{}, nil
	})
	if err == nil || ran {
		t.Fatalf("err = %v, ran = %v; want a begin error and fn never called", err, ran)
	}
	if want := "quartermaster: begin: "; !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %q, want prefix %q", err, want)
	}
	if errors.Unwrap(err) == nil {
		t.Fatalf("err = %q does not wrap the cause with %%w", err)
	}
}

func TestRunTxCommitRollbackSurfaces(t *testing.T) {
	ctx := t.Context()
	name := pgtest.Unique(t, "commit-rollback")

	// The insert fails in a read-only transaction. fn swallows that error and
	// returns nil, so COMMIT is answered with ROLLBACK and pgx reports it.
	_, err := newTxr().RunTxOpts(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(q *testdb.Queries) (struct{}, error) {
		_, _ = q.InsertItem(ctx, name)
		return struct{}{}, nil
	})
	if !errors.Is(err, pgx.ErrTxCommitRollback) {
		t.Fatalf("err = %v, want pgx.ErrTxCommitRollback", err)
	}
	if want := "quartermaster: commit: "; !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %q, want prefix %q", err, want)
	}
	if got := count(t, name); got != 0 {
		t.Fatalf("committed rows = %d, want 0", got)
	}
}

func TestRunTxSerializationConflictSurfaces(t *testing.T) {
	ctx := t.Context()
	name := pgtest.Unique(t, "serial")
	opts := pgx.TxOptions{IsoLevel: pgx.Serializable}
	txr := newTxr()

	// Two serializable transactions that each read what the other writes. Both
	// read the count first, then both insert, so one must fail with 40001.
	var ready, done sync.WaitGroup
	ready.Add(2)
	done.Add(2)
	errs := make([]error, 2)
	for i := range errs {
		go func() {
			defer done.Done()
			// Release the barrier on every path, so a goroutine that fails before
			// reaching it can't leave the other one waiting forever.
			var once sync.Once
			release := func() { once.Do(ready.Done) }
			defer release()

			_, errs[i] = txr.RunTxOpts(ctx, opts, func(q *testdb.Queries) (struct{}, error) {
				if _, err := q.CountItemsByName(ctx, name); err != nil {
					return struct{}{}, err
				}
				release()
				ready.Wait()
				_, err := q.InsertItem(ctx, name)
				return struct{}{}, err
			})
		}()
	}
	done.Wait()

	var failed int
	for _, err := range errs {
		if err == nil {
			continue
		}
		failed++
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "40001" {
			t.Fatalf("err = %v, want a serialization failure (40001)", err)
		}
	}
	if failed != 1 {
		t.Fatalf("%d transactions failed, want exactly 1", failed)
	}
	if got := count(t, name); got != 1 {
		t.Fatalf("committed rows = %d, want 1", got)
	}
}

func TestTxOptionsApply(t *testing.T) {
	ctx := t.Context()
	level := func(q *testdb.Queries) (string, error) { return q.IsolationLevel(ctx) }

	got, err := newTxr().RunTx(ctx, level)
	if err != nil || got != "read committed" {
		t.Fatalf("default level = %q, %v; want read committed", got, err)
	}

	got, err = newTxr(quartermaster.WithTxOptions(pgx.TxOptions{IsoLevel: pgx.RepeatableRead})).RunTx(ctx, level)
	if err != nil || got != "repeatable read" {
		t.Fatalf("WithTxOptions level = %q, %v; want repeatable read", got, err)
	}

	got, err = newTxr().RunTxOpts(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, level)
	if err != nil || got != "serializable" {
		t.Fatalf("RunTxOpts level = %q, %v; want serializable", got, err)
	}
}

func TestNestedRunTxIsIndependent(t *testing.T) {
	ctx := t.Context()
	txr := newTxr()
	outerName, innerName := pgtest.Unique(t, "outer"), pgtest.Unique(t, "inner")
	boom := errors.New("outer fails")

	_, err := txr.RunTx(ctx, func(outer *testdb.Queries) (struct{}, error) {
		if _, err := outer.InsertItem(ctx, outerName); err != nil {
			return struct{}{}, err
		}
		// A nested RunTx must not join the outer transaction.
		if _, err := txr.RunTx(ctx, func(inner *testdb.Queries) (testdb.Item, error) {
			return inner.InsertItem(ctx, innerName)
		}); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, boom
	})
	if err != boom { //nolint:errorlint // asserting the error is returned unwrapped, not just matchable
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if got := count(t, innerName); got != 1 {
		t.Fatalf("inner rows = %d, want 1: nested tx should commit on its own", got)
	}
	if got := count(t, outerName); got != 0 {
		t.Fatalf("outer rows = %d, want 0", got)
	}
}
