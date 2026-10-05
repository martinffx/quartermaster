package payments

import (
	"context"
	"encoding/json"

	"github.com/martinffx/quartermaster"
	"github.com/martinffx/quartermaster/examples/payments/db"
)

// Status is the lifecycle state of a Payment.
type Status string

// The states a Payment moves through. It starts pending and ends succeeded or
// failed.
const (
	StatusPending   Status = "pending"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// Payment is the domain entity; it owns the translation from the sqlc record.
type Payment struct {
	ID        string
	AccountID string
	Amount    int64
	Status    Status
	Reference string
}

// NewPayment returns a pending Payment.
func NewPayment(id, accountID string, amount int64) Payment {
	return Payment{ID: id, AccountID: accountID, Amount: amount, Status: StatusPending}
}

func paymentFromRecord(r db.Payment) Payment {
	return Payment{ID: r.ID, AccountID: r.AccountID, Amount: r.Amount, Status: Status(r.Status), Reference: r.Reference}
}

const idempotencyScope = "payment"

// PaymentRepo owns its transactions. Each method opens one, does only database
// work, and returns; the transaction never leaves the method.
type PaymentRepo struct {
	tx *quartermaster.Transactor[*db.Queries]
}

// NewPaymentRepo returns a PaymentRepo that runs its transactions on tx.
func NewPaymentRepo(tx *quartermaster.Transactor[*db.Queries]) *PaymentRepo {
	return &PaymentRepo{tx: tx}
}

// CreatePending claims the idempotency key, inserts the payment and writes the
// outbox event in one transaction. A retry with the same key returns the
// payment the first call created. A failed call leaves nothing behind, not even
// the key, so the client can retry with the same key.
func (r *PaymentRepo) CreatePending(ctx context.Context, p Payment, idemKey string) (Payment, error) {
	return r.tx.RunTx(ctx, func(q *db.Queries) (Payment, error) {
		claimed, err := q.ClaimIdempotencyKey(ctx, db.ClaimIdempotencyKeyParams{
			Scope: idempotencyScope, Key: idemKey, ResourceID: p.ID,
		})
		if err != nil {
			return Payment{}, err
		}
		if claimed == 0 { // someone else got there first; at READ COMMITTED, the default, their commit is visible now
			id, err := q.GetIdempotencyKey(ctx, db.GetIdempotencyKeyParams{Scope: idempotencyScope, Key: idemKey})
			if err != nil {
				return Payment{}, err
			}
			rec, err := q.GetPayment(ctx, id)
			if err != nil {
				return Payment{}, err
			}
			return paymentFromRecord(rec), nil
		}

		if err := q.InsertPayment(ctx, db.InsertPaymentParams{
			ID: p.ID, AccountID: p.AccountID, Amount: p.Amount, Status: string(p.Status),
		}); err != nil {
			return Payment{}, err
		}
		payload, err := json.Marshal(map[string]any{"payment_id": p.ID, "amount": p.Amount})
		if err != nil {
			return Payment{}, err
		}
		// Taking q makes it plain the outbox write is inside this transaction.
		if err := q.InsertOutbox(ctx, db.InsertOutboxParams{Topic: "payment.created", Payload: payload}); err != nil {
			return Payment{}, err
		}
		return p, nil
	})
}

// RecordResult stores what the rail said. submitErr is the error from the rail,
// if any, and decides between succeeded and failed.
func (r *PaymentRepo) RecordResult(ctx context.Context, id, reference string, submitErr error) (Payment, error) {
	status := StatusSucceeded
	if submitErr != nil {
		status = StatusFailed
	}
	return r.tx.RunTx(ctx, func(q *db.Queries) (Payment, error) {
		if err := q.UpdatePaymentResult(ctx, db.UpdatePaymentResultParams{
			ID: id, Status: string(status), Reference: reference,
		}); err != nil {
			return Payment{}, err
		}
		rec, err := q.GetPayment(ctx, id)
		if err != nil {
			return Payment{}, err
		}
		return paymentFromRecord(rec), nil
	})
}

// Rail is the external payment provider. Calls to it are slow and fallible.
type Rail interface {
	Submit(ctx context.Context, paymentID, idemKey string) (reference string, err error)
}

// Request is a caller's intent to pay. IdempotencyKey identifies retries of the
// same request, so a retry returns the first payment instead of paying twice.
type Request struct {
	ID             string
	AccountID      string
	Amount         int64
	IdempotencyKey string
}

// Service pays by orchestrating the repository and the rail. It owns the order
// of operations; the repository owns the transactions.
type Service struct {
	payments *PaymentRepo
	rail     Rail
}

// NewService returns a Service that stores payments in payments and submits
// them to rail.
func NewService(payments *PaymentRepo, rail Rail) *Service {
	return &Service{payments: payments, rail: rail}
}

// Pay stores the payment as pending, calls the rail, then stores the result:
// two short transactions and no connection held during the network call. If the
// process dies after Submit the payment stays pending; this example has no
// reconciler, so something else would have to find and finish it.
func (s *Service) Pay(ctx context.Context, req Request) (Payment, error) {
	p, err := s.payments.CreatePending(ctx, NewPayment(req.ID, req.AccountID, req.Amount), req.IdempotencyKey)
	if err != nil {
		return Payment{}, err
	}
	if p.Status != StatusPending {
		return p, nil
	}
	ref, submitErr := s.rail.Submit(ctx, p.ID, req.IdempotencyKey)
	return s.payments.RecordResult(ctx, p.ID, ref, submitErr)
}
