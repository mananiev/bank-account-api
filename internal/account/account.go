package account

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Account is a bank account with its current balance in minor units (cents).
type Account struct {
	ID        uuid.UUID
	Balance   int64
	CreatedAt time.Time
}

var (
	// ErrNotFound is returned when an account does not exist.
	ErrNotFound = errors.New("account not found")
	// ErrInvalidAmount is returned when an amount violates a business rule.
	ErrInvalidAmount = errors.New("invalid amount")
)

// Repository is the persistence the account service needs.
type Repository interface {
	CreateAccount(ctx context.Context, initialBalance int64) (Account, error)
	GetAccount(ctx context.Context, id uuid.UUID) (Account, error)
}
