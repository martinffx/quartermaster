package payments_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/martinffx/quartermaster"
	"github.com/martinffx/quartermaster/examples/payments"
	"github.com/martinffx/quartermaster/examples/payments/db"
	"github.com/martinffx/quartermaster/internal/pgtest"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := pgtest.Start(context.Background(), payments.Schema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres: %v\n", err)
		os.Exit(1)
	}
	pool = p
	code := m.Run()
	stop()
	os.Exit(code)
}

func newRepo() *payments.PaymentRepo {
	return payments.NewPaymentRepo(quartermaster.New(pool, db.New(pool)))
}

type fakeRail struct {
	acquiredDuringSubmit int32
	calls                int
	err                  error
}

func (f *fakeRail) Submit(_ context.Context, paymentID, _ string) (string, error) {
	f.calls++
	f.acquiredDuringSubmit = pool.Stat().AcquiredConns()
	return "ref-" + paymentID, f.err
}

func countOutbox(t *testing.T, q *db.Queries) int64 {
	t.Helper()
	n, err := q.CountOutbox(t.Context())
	if err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return n
}

func TestRetryWithSameKeyReturnsTheFirstPayment(t *testing.T) {
	ctx := t.Context()
	repo := newRepo()
	key := pgtest.Unique(t, "key")
	id1, id2 := pgtest.Unique(t, "p1"), pgtest.Unique(t, "p2")

	first, err := repo.CreatePending(ctx, payments.NewPayment(id1, "acct", 100), key)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.CreatePending(ctx, payments.NewPayment(id2, "acct", 100), key)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != id1 || second.ID != id1 {
		t.Fatalf("ids = %q, %q; want the retry to return %q", first.ID, second.ID, id1)
	}
	if _, err := db.New(pool).GetPayment(ctx, id2); err == nil {
		t.Fatal("the retry's payment should never have been inserted")
	}
}

func TestFailedTransactionLeavesNoKeyBehind(t *testing.T) {
	ctx := t.Context()
	repo := newRepo()
	q := db.New(pool)
	key := pgtest.Unique(t, "key")
	outboxBefore := countOutbox(t, q)

	// amount 0 violates the CHECK constraint after the key has been claimed.
	if _, err := repo.CreatePending(ctx, payments.NewPayment(pgtest.Unique(t, "bad"), "acct", 0), key); err == nil {
		t.Fatal("want a constraint error")
	}
	n, err := q.CountIdempotencyKeys(ctx, db.CountIdempotencyKeysParams{Scope: "payment", Key: key})
	if err != nil || n != 0 {
		t.Fatalf("keys after failure = %d, %v; want 0", n, err)
	}
	if outboxAfter := countOutbox(t, q); outboxAfter != outboxBefore {
		t.Fatalf("outbox rows changed: %d -> %d", outboxBefore, outboxAfter)
	}

	// The client can retry with the same key.
	okID := pgtest.Unique(t, "ok")
	p, err := repo.CreatePending(ctx, payments.NewPayment(okID, "acct", 50), key)
	if err != nil || p.ID != okID {
		t.Fatalf("retry = %+v, %v", p, err)
	}
}

func TestNoConnectionHeldDuringSubmit(t *testing.T) {
	ctx := t.Context()
	rail := &fakeRail{}
	svc := payments.NewService(newRepo(), rail)
	id, key := pgtest.Unique(t, "p"), pgtest.Unique(t, "key")

	p, err := svc.Pay(ctx, payments.Request{ID: id, AccountID: "acct", Amount: 100, IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if rail.acquiredDuringSubmit != 0 {
		t.Fatalf("%d connections held during Submit, want 0", rail.acquiredDuringSubmit)
	}
	if p.Status != payments.StatusSucceeded || p.Reference != "ref-"+id {
		t.Fatalf("payment = %+v", p)
	}

	// A replay of a finished payment must not call the rail again.
	if _, err := svc.Pay(ctx, payments.Request{ID: pgtest.Unique(t, "p2"), AccountID: "acct", Amount: 100, IdempotencyKey: key}); err != nil {
		t.Fatal(err)
	}
	if rail.calls != 1 {
		t.Fatalf("rail called %d times, want 1", rail.calls)
	}
}

func TestRailFailureIsRecorded(t *testing.T) {
	ctx := t.Context()
	rail := &fakeRail{err: errors.New("provider down")}
	svc := payments.NewService(newRepo(), rail)

	p, err := svc.Pay(ctx, payments.Request{ID: pgtest.Unique(t, "p"), AccountID: "acct", Amount: 100, IdempotencyKey: pgtest.Unique(t, "key")})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != payments.StatusFailed {
		t.Fatalf("status = %q, want failed", p.Status)
	}
}
