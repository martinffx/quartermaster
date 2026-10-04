CREATE TABLE payments (
    id         text PRIMARY KEY,
    account_id text   NOT NULL,
    amount     bigint NOT NULL CHECK (amount > 0),
    status     text   NOT NULL,
    reference  text   NOT NULL DEFAULT ''
);

CREATE TABLE idempotency_keys (
    scope       text NOT NULL,
    key         text NOT NULL,
    resource_id text NOT NULL,
    PRIMARY KEY (scope, key)
);

CREATE TABLE outbox (
    id         bigserial PRIMARY KEY,
    topic      text  NOT NULL,
    payload    jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
