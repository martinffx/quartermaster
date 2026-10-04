// Package pgtest starts a throwaway PostgreSQL container for tests.
package pgtest

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var seq atomic.Int64

// Unique returns s made unique to this call: it embeds the test name and a
// process-wide counter. Tests share one database for the whole package run, so
// fixed names and keys would collide under -count=N or when tests are
// reordered.
func Unique(t testing.TB, s string) string {
	t.Helper()
	return fmt.Sprintf("%s/%s/%d", t.Name(), s, seq.Add(1))
}

// Start runs a postgres container, applies schema, and returns a pool and a
// stop function. It is meant for TestMain, so one container serves the package.
func Start(ctx context.Context, schema string) (*pgxpool.Pool, func(), error) {
	ctr, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	// Run can return a started container together with an error, such as a
	// wait-strategy timeout. TerminateContainer accepts nil.
	stop := func() { _ = testcontainers.TerminateContainer(ctr) }
	if err != nil {
		stop()
		return nil, nil, err
	}

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		stop()
		return nil, nil, err
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		stop()
		return nil, nil, err
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		stop()
		return nil, nil, err
	}
	return pool, func() { pool.Close(); stop() }, nil
}
