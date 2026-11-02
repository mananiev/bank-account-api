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
//  1. Claim the key by inserting the transaction row.
//  2. If the key was already used: replay the stored transaction when the request
//     hash matches, otherwise return ErrIdempotencyKeyReused.
//  3. Credit the account and append the ledger entry.
//
// Any failure rolls everything back, including the key, so a failed request can
// be retried with the same key.
func (p *Postgres) Deposit(ctx context.Context, in account.DepositInput) (account.Transaction, error) {
	var result account.Transaction
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		id, createdAt, claimed, err := claimIdempotencyKey(ctx, tx, "DEPOSIT", in.Amount, in.IdempotencyKey, in.RequestHash)
		if err != nil {
			return err
		}
		if !claimed {
			result, err = replay(ctx, tx, in.IdempotencyKey, in.RequestHash)
			return err
		}

		tag, err := tx.Exec(ctx, `UPDATE accounts SET balance = balance + $1 WHERE id = $2`, in.Amount, in.AccountID)
		if err != nil {
			return creditError(err)
		}
		if tag.RowsAffected() == 0 {
			return account.ErrNotFound
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO ledger_entries (transaction_id, account_id, amount) VALUES ($1, $2, $3)`,
			id, in.AccountID, in.Amount,
		); err != nil {
			return fmt.Errorf("insert deposit ledger entry: %w", err)
		}

		result = account.Transaction{
			ID: id, Type: account.TypeDeposit, AccountID: in.AccountID, Amount: in.Amount, CreatedAt: createdAt,
		}
		return nil
	})
	if err != nil {
		return account.Transaction{}, err
	}
	return result, nil
}

// creditError maps a failed balance increase: a BIGINT overflow is a business
// rule violation (the balance would exceed the maximum), not a server error.
func creditError(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgNumericValueOutOfRange {
		return fmt.Errorf("%w: balance would exceed the maximum", account.ErrInvalidAmount)
	}
	return fmt.Errorf("credit account: %w", err)
}
