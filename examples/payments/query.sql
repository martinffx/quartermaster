-- name: ClaimIdempotencyKey :execrows
INSERT INTO idempotency_keys (scope, key, resource_id)
VALUES ($1, $2, $3)
ON CONFLICT (scope, key) DO NOTHING;

-- name: GetIdempotencyKey :one
SELECT resource_id FROM idempotency_keys WHERE scope = $1 AND key = $2;

-- name: InsertPayment :exec
INSERT INTO payments (id, account_id, amount, status) VALUES ($1, $2, $3, $4);

-- name: GetPayment :one
SELECT * FROM payments WHERE id = $1;

-- name: UpdatePaymentResult :exec
UPDATE payments SET status = $2, reference = $3 WHERE id = $1;

-- name: InsertOutbox :exec
INSERT INTO outbox (topic, payload) VALUES ($1, $2);

-- name: CountOutbox :one
SELECT count(*) FROM outbox;

-- name: CountIdempotencyKeys :one
SELECT count(*) FROM idempotency_keys WHERE scope = $1 AND key = $2;
