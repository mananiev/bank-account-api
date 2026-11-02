package storage

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mananiev/bank-account-api/internal/account"
)

// ListTransactions returns the account's transactions, newest first, or
// account.ErrNotFound if the account does not exist.
//
// Each of the account's ledger entries is joined with its transaction and,
// for transfers, with the other side's entry to find the counterparty. The
// (account_id, created_at DESC, id DESC) index serves the filter and order.
func (p *Postgres) ListTransactions(ctx context.Context, accountID uuid.UUID) ([]account.Transaction, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT t.id, t.type, t.amount, t.created_at, e.account_id, e.amount, o.account_id, o.amount
		FROM ledger_entries e
		JOIN transactions t ON t.id = e.transaction_id
		LEFT JOIN ledger_entries o ON o.transaction_id = e.transaction_id AND o.id <> e.id
		WHERE e.account_id = $1
		ORDER BY e.created_at DESC, e.id DESC`, accountID)
	if err != nil {
		return nil, fmt.Errorf("query transactions: %w", err)
	}

	txs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (account.Transaction, error) {
		var t account.Transaction
		var dbType string
		var own ledgerEntry
		var otherAccount *uuid.UUID
		var otherAmount *int64
		if err := row.Scan(&t.ID, &dbType, &t.Amount, &t.CreatedAt,
			&own.accountID, &own.amount, &otherAccount, &otherAmount); err != nil {
			return account.Transaction{}, err
		}
		entries := []ledgerEntry{own}
		if otherAccount != nil && otherAmount != nil {
			entries = append(entries, ledgerEntry{accountID: *otherAccount, amount: *otherAmount})
		}
		return withAccounts(t, dbType, entries)
	})
	if err != nil {
		return nil, fmt.Errorf("read transactions: %w", err)
	}

	if len(txs) == 0 {
		// No entries: distinguish an empty account from a missing one.
		if _, err := p.GetAccount(ctx, accountID); err != nil {
			return nil, err
		}
	}
	return txs, nil
}
