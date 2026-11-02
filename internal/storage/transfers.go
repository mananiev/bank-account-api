package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mananiev/bank-account-api/internal/account"
)

// Transfer moves money between two accounts atomically, at most once per
// idempotency key, in one DB transaction:
//
//  1. Claim the key (or replay / reject a reused key), as for deposits.
//  2. Lock both account rows with SELECT … FOR UPDATE, always the lower ID
//     first. Every transfer takes the locks in the same global order, so two
//     opposite transfers (A→B and B→A) cannot deadlock.
//  3. With both rows locked, check the source balance, update both balances
//     and append two ledger entries that sum to zero.
//
// The CHECK (balance >= 0) constraint remains as a last line of defence.
func (p *Postgres) Transfer(ctx context.Context, in account.TransferInput) (account.Transaction, error) {
	var result account.Transaction
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		id, createdAt, claimed, err := claimIdempotencyKey(ctx, tx, "TRANSFER", in.Amount, in.IdempotencyKey, in.RequestHash)
		if err != nil {
			return err
		}
		if !claimed {
			result, err = replay(ctx, tx, in.IdempotencyKey, in.RequestHash)
			return err
		}

		sourceBalance, err := lockAccounts(ctx, tx, in.FromAccountID, in.ToAccountID)
		if err != nil {
			return err
		}
		if sourceBalance < in.Amount {
			return account.ErrInsufficientFunds
		}

		if _, err := tx.Exec(ctx,
			`UPDATE accounts SET balance = balance - $1 WHERE id = $2`, in.Amount, in.FromAccountID,
		); err != nil {
			return fmt.Errorf("debit source account: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE accounts SET balance = balance + $1 WHERE id = $2`, in.Amount, in.ToAccountID,
		); err != nil {
			return creditError(err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (transaction_id, account_id, amount)
			VALUES ($1, $2, $3), ($1, $4, $5)`,
			id, in.FromAccountID, -in.Amount, in.ToAccountID, in.Amount,
		); err != nil {
			return fmt.Errorf("insert transfer ledger entries: %w", err)
		}

		result = account.Transaction{
			ID:            id,
			Type:          account.TypeTransfer,
			FromAccountID: in.FromAccountID,
			ToAccountID:   in.ToAccountID,
			Amount:        in.Amount,
			CreatedAt:     createdAt,
		}
		return nil
	})
	if err != nil {
		return account.Transaction{}, err
	}
	return result, nil
}

// lockAccounts locks both accounts in ascending ID order and returns the
// source account's balance.
func lockAccounts(ctx context.Context, tx pgx.Tx, from, to uuid.UUID) (sourceBalance int64, err error) {
	first, second := from, to
	if bytes.Compare(first[:], second[:]) > 0 {
		first, second = second, first
	}

	for _, id := range []uuid.UUID{first, second} {
		var balance int64
		err := tx.QueryRow(ctx, `SELECT balance FROM accounts WHERE id = $1 FOR UPDATE`, id).Scan(&balance)
		if errors.Is(err, pgx.ErrNoRows) {
			side := "destination"
			if id == from {
				side = "source"
			}
			return 0, fmt.Errorf("%w: %s account %s", account.ErrTransferAccountNotFound, side, id)
		}
		if err != nil {
			return 0, fmt.Errorf("lock account: %w", err)
		}
		if id == from {
			sourceBalance = balance
		}
	}
	return sourceBalance, nil
}
