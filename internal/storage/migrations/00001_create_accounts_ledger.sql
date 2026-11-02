-- +goose Up
CREATE TABLE accounts (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    balance    BIGINT      NOT NULL DEFAULT 0 CHECK (balance >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE transactions (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    type            TEXT        NOT NULL CHECK (type IN ('OPENING', 'DEPOSIT', 'TRANSFER')),
    amount          BIGINT      NOT NULL CHECK (amount > 0),
    idempotency_key TEXT        UNIQUE,
    request_hash    TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- OPENING is created together with the account and has no key;
    -- every client-initiated movement must carry a key and its request hash.
    CHECK ((type = 'OPENING') = (idempotency_key IS NULL)),
    CHECK ((idempotency_key IS NULL) = (request_hash IS NULL))
);

CREATE TABLE ledger_entries (
    id             BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transaction_id UUID        NOT NULL REFERENCES transactions (id),
    account_id     UUID        NOT NULL REFERENCES accounts (id),
    amount         BIGINT      NOT NULL CHECK (amount <> 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ledger_entries_account_created_idx ON ledger_entries (account_id, created_at DESC, id DESC);
CREATE INDEX ledger_entries_transaction_idx ON ledger_entries (transaction_id);

-- The ledger is append-only: reject any attempt to change or remove entries.
-- +goose StatementBegin
CREATE FUNCTION ledger_entries_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only: % is not allowed', TG_OP;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER ledger_entries_no_update_delete
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION ledger_entries_append_only();

CREATE TRIGGER ledger_entries_no_truncate
    BEFORE TRUNCATE ON ledger_entries
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_entries_append_only();

-- +goose Down
DROP TABLE ledger_entries;
DROP FUNCTION ledger_entries_append_only();
DROP TABLE transactions;
DROP TABLE accounts;
