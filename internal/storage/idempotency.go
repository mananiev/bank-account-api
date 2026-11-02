package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mananiev/bank-account-api/internal/account"
)

// claimIdempotencyKey inserts the transaction row that owns key. If another
// request already holds the key, Postgres waits for that transaction to finish;
// if it committed, nothing is inserted and claimed is false. If it rolled back,
// the key is free again and this insert claims it.
func claimIdempotencyKey(ctx context.Context, tx pgx.Tx, dbType string, amount int64, key, requestHash string) (id uuid.UUID, createdAt time.Time, claimed bool, err error) {
	err = tx.QueryRow(ctx, `
		INSERT INTO transactions (type, amount, idempotency_key, request_hash)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id, created_at`,
		dbType, amount, key, requestHash,
	).Scan(&id, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, time.Time{}, false, nil
	}
	if err != nil {
		return uuid.Nil, time.Time{}, false, fmt.Errorf("insert %s transaction: %w", dbType, err)
	}
	return id, createdAt, true, nil
}

// replay returns the transaction stored under key if it was created by the
// same request, or account.ErrIdempotencyKeyReused otherwise.
func replay(ctx context.Context, tx pgx.Tx, key, requestHash string) (account.Transaction, error) {
	var t account.Transaction
	var dbType, storedHash string
	err := tx.QueryRow(ctx, `
		SELECT id, type, amount, request_hash, created_at
		FROM transactions
		WHERE idempotency_key = $1`, key,
	).Scan(&t.ID, &dbType, &t.Amount, &storedHash, &t.CreatedAt)
	if err != nil {
		return account.Transaction{}, fmt.Errorf("load transaction for idempotency key: %w", err)
	}
	if storedHash != requestHash {
		return account.Transaction{}, account.ErrIdempotencyKeyReused
	}

	rows, err := tx.Query(ctx, `SELECT account_id, amount FROM ledger_entries WHERE transaction_id = $1`, t.ID)
	if err != nil {
		return account.Transaction{}, fmt.Errorf("load ledger entries: %w", err)
	}
	type entry struct {
		accountID uuid.UUID
		amount    int64
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entry, error) {
		var e entry
		err := row.Scan(&e.accountID, &e.amount)
		return e, err
	})
	if err != nil {
		return account.Transaction{}, fmt.Errorf("load ledger entries: %w", err)
	}

	switch {
	case dbType == "DEPOSIT" && len(entries) == 1:
		t.Type = account.TypeDeposit
		t.AccountID = entries[0].accountID
	case dbType == "TRANSFER" && len(entries) == 2:
		// The debit (negative entry) is the source, the credit the destination.
		t.Type = account.TypeTransfer
		for _, e := range entries {
			if e.amount < 0 {
				t.FromAccountID = e.accountID
			} else {
				t.ToAccountID = e.accountID
			}
		}
	default:
		return account.Transaction{}, fmt.Errorf("unexpected %s transaction %s with %d ledger entries", dbType, t.ID, len(entries))
	}
	return t, nil
}
