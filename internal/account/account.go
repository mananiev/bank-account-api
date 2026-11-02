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
// depends on Type: AccountID for opening and deposit; FromAccountID and
// ToAccountID for transfer.
type Transaction struct {
	ID            uuid.UUID
	Type          TransactionType
	AccountID     uuid.UUID
	FromAccountID uuid.UUID
	ToAccountID   uuid.UUID
	Amount        int64
	CreatedAt     time.Time
}

// TransferInput is a validated transfer, ready to be stored.
type TransferInput struct {
	FromAccountID  uuid.UUID
	ToAccountID    uuid.UUID
	Amount         int64
	IdempotencyKey string
	RequestHash    string
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
	// ErrSameAccount is returned for a transfer from an account to itself.
	ErrSameAccount = errors.New("source and destination accounts must differ")
	// ErrInsufficientFunds is returned when a transfer would make the source balance negative.
	ErrInsufficientFunds = errors.New("insufficient funds")
	// ErrTransferAccountNotFound is returned when an account referenced in a
	// transfer body does not exist. Unlike ErrNotFound, the account is not the
	// resource addressed by the request.
	ErrTransferAccountNotFound = errors.New("account not found")
)

// Repository is the persistence the account service needs.
type Repository interface {
	CreateAccount(ctx context.Context, initialBalance int64) (Account, error)
	GetAccount(ctx context.Context, id uuid.UUID) (Account, error)
	// Deposit applies the deposit once per idempotency key. Repeating a key
	// with the same RequestHash returns the original transaction; a different
	// RequestHash returns ErrIdempotencyKeyReused.
	Deposit(ctx context.Context, in DepositInput) (Transaction, error)
	// Transfer moves money atomically, at most once per idempotency key, with
	// the same replay semantics as Deposit. It returns ErrTransferAccountNotFound
	// or ErrInsufficientFunds when the transfer cannot be applied.
	Transfer(ctx context.Context, in TransferInput) (Transaction, error)
	// ListTransactions returns the account's transactions, newest first, or
	// ErrNotFound if the account does not exist.
	ListTransactions(ctx context.Context, accountID uuid.UUID) ([]Transaction, error)
}
