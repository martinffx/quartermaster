// Package payments is the worked example from the post: an idempotent payment
// that writes an outbox event, and a service that never holds a transaction
// across the network call to the payment rail.
package payments

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

import _ "embed"

// Schema is the DDL for the example tables.
//
//go:embed schema.sql
var Schema string
