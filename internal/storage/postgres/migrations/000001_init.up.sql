CREATE SCHEMA IF NOT EXISTS gophermart;

CREATE TABLE gophermart.users (
    id            BIGSERIAL PRIMARY KEY,
    login         TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    balance       BIGINT      NOT NULL DEFAULT 0 CHECK (balance >= 0),
    withdrawn     BIGINT      NOT NULL DEFAULT 0 CHECK (withdrawn >= 0),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE gophermart.orders (
    number      TEXT PRIMARY KEY,
    user_id     BIGINT      NOT NULL REFERENCES gophermart.users (id),
    status      TEXT        NOT NULL DEFAULT 'NEW',
    accrual     BIGINT,
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    checked_at  TIMESTAMPTZ
);

CREATE INDEX orders_user_uploaded_idx ON gophermart.orders (user_id, uploaded_at DESC);
CREATE INDEX orders_pending_idx ON gophermart.orders (checked_at NULLS FIRST)
    WHERE status IN ('NEW', 'PROCESSING');

CREATE TABLE gophermart.withdrawals (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT      NOT NULL REFERENCES gophermart.users (id),
    order_number TEXT        NOT NULL UNIQUE,
    sum          BIGINT      NOT NULL CHECK (sum > 0),
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX withdrawals_user_processed_idx ON gophermart.withdrawals (user_id, processed_at DESC);
