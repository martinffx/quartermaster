-- name: InsertItem :one
INSERT INTO items (name) VALUES ($1) RETURNING id, name;

-- name: CountItemsByName :one
SELECT count(*) FROM items WHERE name = $1;

-- name: IsolationLevel :one
SELECT current_setting('transaction_isolation')::text;
