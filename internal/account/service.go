package account

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/google/uuid"
)

// MaxIdempotencyKeyLength is the maximum length of an idempotency key, in characters.
const MaxIdempotencyKeyLength = 255

// Service enforces the account business rules.
type Service struct {
	repo Repository
}

// NewService returns a Service backed by repo.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// CreateAccount opens an account. The initial balance must not be negative.
func (s *Service) CreateAccount(ctx context.Context, initialBalance int64) (Account, error) {
	if initialBalance < 0 {
		return Account{}, fmt.Errorf("%w: initial balance must not be negative", ErrInvalidAmount)
	}
	return s.repo.CreateAccount(ctx, initialBalance)
}

// GetAccount returns the account with the given ID, or ErrNotFound.
func (s *Service) GetAccount(ctx context.Context, id uuid.UUID) (Account, error) {
	return s.repo.GetAccount(ctx, id)
}

// Deposit adds amount to the account, at most once per idempotency key.
func (s *Service) Deposit(ctx context.Context, accountID uuid.UUID, amount int64, idempotencyKey string) (Transaction, error) {
	if amount <= 0 {
		return Transaction{}, fmt.Errorf("%w: amount must be positive", ErrInvalidAmount)
	}
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return Transaction{}, err
	}

	return s.repo.Deposit(ctx, DepositInput{
		AccountID:      accountID,
		Amount:         amount,
		IdempotencyKey: idempotencyKey,
		RequestHash:    requestHash(TypeDeposit, accountID.String(), strconv.FormatInt(amount, 10)),
	})
}

func validateIdempotencyKey(key string) error {
	if key == "" || utf8.RuneCountInString(key) > MaxIdempotencyKeyLength {
		return fmt.Errorf("%w: Idempotency-Key header is required and must be 1-%d characters",
			ErrInvalidIdempotencyKey, MaxIdempotencyKeyLength)
	}
	return nil
}

// requestHash fingerprints a request from its parsed values (not the raw
// body), so formatting differences in a retried request do not matter.
// Fields never contain the separator: they are a type name, UUIDs and integers.
func requestHash(op TransactionType, fields ...string) string {
	h := sha256.New()
	h.Write([]byte(op))
	for _, f := range fields {
		h.Write([]byte{'|'})
		h.Write([]byte(f))
	}
	return hex.EncodeToString(h.Sum(nil))
}
