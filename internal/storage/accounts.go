package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mananiev/bank-account-api/internal/account"
)

var _ account.Repository = (*Postgres)(nil)

// CreateAccount stores a new account. A non-zero initial balance is recorded
// as an OPENING transaction with one ledger entry, in the same DB transaction.
func (p *Postgres) CreateAccount(ctx context.Context, initialBalance int64) (account.Account, error) {
	var acc account.Account
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`INSERT INTO accounts (balance) VALUES ($1) RETURNING id, balance, created_at`,
			initialBalance,
		).Scan(&acc.ID, &acc.Balance, &acc.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert account: %w", err)
		}

		if initialBalance == 0 {
			return nil
		}

		var txID uuid.UUID
		err = tx.QueryRow(ctx,
			`INSERT INTO transactions (type, amount) VALUES ('OPENING', $1) RETURNING id`,
			initialBalance,
		).Scan(&txID)
		if err != nil {
			return fmt.Errorf("insert opening transaction: %w", err)
		}

		_, err = tx.Exec(ctx,
			`INSERT INTO ledger_entries (transaction_id, account_id, amount) VALUES ($1, $2, $3)`,
			txID, acc.ID, initialBalance,
		)
		if err != nil {
			return fmt.Errorf("insert opening ledger entry: %w", err)
		}
		return nil
	})
	if err != nil {
		return account.Account{}, err
	}
	return acc, nil
}

// GetAccount loads an account by ID, returning account.ErrNotFound if absent.
func (p *Postgres) GetAccount(ctx context.Context, id uuid.UUID) (account.Account, error) {
	var acc account.Account
	err := p.pool.QueryRow(ctx,
		`SELECT id, balance, created_at FROM accounts WHERE id = $1`, id,
	).Scan(&acc.ID, &acc.Balance, &acc.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return account.Account{}, account.ErrNotFound
	}
	if err != nil {
		return account.Account{}, fmt.Errorf("select account: %w", err)
	}
	return acc, nil
}
