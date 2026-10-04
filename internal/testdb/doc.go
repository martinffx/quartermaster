// Package testdb is sqlc output used to test quartermaster against the code
// sqlc really generates. Schema is the DDL the tests apply.
package testdb

import _ "embed"

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

// Schema is applied to the test database before the tests run.
//
//go:embed schema.sql
var Schema string
