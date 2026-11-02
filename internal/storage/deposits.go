package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mananiev/bank-account-api/internal/account"
)

// pgNumericValueOutOfRange is raised when a BIGINT balance would overflow.
const pgNumericValueOutOfRange = "22003"

// Deposit applies a deposit at most once per idempotency key, in one DB transaction:
//
//  1. Claim the key by inserting the transaction row. If another request holds
//     the key, the insert waits for it to commit and then inserts nothing.
//  2. If the key was already used: replay the stored transaction when the request
//     hash matches, otherwise return ErrIdempotencyKeyReused.
//  3. Credit the account and append the ledger entry.
//
// Any failure rolls everything back, including the key, so a failed request can
// be retried with the same key.
func (p *Postgres) Deposit(ctx context.Context, in account.DepositInput) (account.Transaction, error) {
	var result account.Transaction
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		t := account.Transaction{Type: account.TypeDeposit, AccountID: in.AccountID, Amount: in.Amount}
		err := tx.QueryRow(ctx, `
			INSERT INTO transactions (type, amount, idempotency_key, request_hash)
			VALUES ('DEPOSIT', $1, $2, $3)
			ON CONFLICT (idempotency_key) DO NOTHING
			RETURNING id, created_at`,
			in.Amount, in.IdempotencyKey, in.RequestHash,
		).Scan(&t.ID, &t.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			result, err = replay(ctx, tx, in.IdempotencyKey, in.RequestHash)
			return err
		}
		if err != nil {
			return fmt.Errorf("insert deposit transaction: %w", err)
		}

		tag, err := tx.Exec(ctx, `UPDATE accounts SET balance = balance + $1 WHERE id = $2`, in.Amount, in.AccountID)
		if err != nil {
			if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgNumericValueOutOfRange {
				return fmt.Errorf("%w: balance would exceed the maximum", account.ErrInvalidAmount)
			}
			return fmt.Errorf("credit account: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return account.ErrNotFound
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO ledger_entries (transaction_id, account_id, amount) VALUES ($1, $2, $3)`,
			t.ID, in.AccountID, in.Amount,
		); err != nil {
			return fmt.Errorf("insert deposit ledger entry: %w", err)
		}

		result = t
		return nil
	})
	if err != nil {
		return account.Transaction{}, err
	}
	return result, nil
}

// replay returns the transaction stored under key if it was created by the
// same request, or ErrIdempotencyKeyReused otherwise.
func replay(ctx context.Context, tx pgx.Tx, key, requestHash string) (account.Transaction, error) {
	var t account.Transaction
	var dbType, storedHash string
	err := tx.QueryRow(ctx, `
		SELECT t.id, t.type, t.amount, t.request_hash, t.created_at, e.account_id
		FROM transactions t
		JOIN ledger_entries e ON e.transaction_id = t.id
		WHERE t.idempotency_key = $1`, key,
	).Scan(&t.ID, &dbType, &t.Amount, &storedHash, &t.CreatedAt, &t.AccountID)
	if err != nil {
		return account.Transaction{}, fmt.Errorf("load transaction for idempotency key: %w", err)
	}
	if storedHash != requestHash {
		return account.Transaction{}, account.ErrIdempotencyKeyReused
	}
	if dbType != "DEPOSIT" {
		return account.Transaction{}, fmt.Errorf("replay of %s transactions is not supported", dbType)
	}
	t.Type = account.TypeDeposit
	return t, nil
}
