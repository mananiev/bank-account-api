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

// TransactionType is the kind of money movement.
type TransactionType string

// Transaction types.
const (
	TypeOpening  TransactionType = "opening"
	TypeDeposit  TransactionType = "deposit"
	TypeTransfer TransactionType = "transfer"
)

// Transaction is a recorded money movement. Which account fields are set
// depends on Type: AccountID for opening and deposit.
type Transaction struct {
	ID        uuid.UUID
	Type      TransactionType
	AccountID uuid.UUID
	Amount    int64
	CreatedAt time.Time
}

// DepositInput is a validated deposit, ready to be stored.
type DepositInput struct {
	AccountID      uuid.UUID
	Amount         int64
	IdempotencyKey string
	// RequestHash identifies the request behind IdempotencyKey, so that
	// reusing the key for a different request can be detected.
	RequestHash string
}

var (
	// ErrNotFound is returned when an account does not exist.
	ErrNotFound = errors.New("account not found")
	// ErrInvalidAmount is returned when an amount violates a business rule.
	ErrInvalidAmount = errors.New("invalid amount")
	// ErrInvalidIdempotencyKey is returned when the idempotency key is missing or malformed.
	ErrInvalidIdempotencyKey = errors.New("invalid idempotency key")
	// ErrIdempotencyKeyReused is returned when an idempotency key was already
	// used for a different request.
	ErrIdempotencyKeyReused = errors.New("idempotency key already used for a different request")
)

// Repository is the persistence the account service needs.
type Repository interface {
	CreateAccount(ctx context.Context, initialBalance int64) (Account, error)
	GetAccount(ctx context.Context, id uuid.UUID) (Account, error)
	// Deposit applies the deposit once per idempotency key. Repeating a key
	// with the same RequestHash returns the original transaction; a different
	// RequestHash returns ErrIdempotencyKeyReused.
	Deposit(ctx context.Context, in DepositInput) (Transaction, error)
}
