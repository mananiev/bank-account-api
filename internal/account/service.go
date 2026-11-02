package account

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

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
		return Account{}, fmt.Errorf("initial balance must not be negative: %w", ErrInvalidAmount)
	}
	return s.repo.CreateAccount(ctx, initialBalance)
}

// GetAccount returns the account with the given ID, or ErrNotFound.
func (s *Service) GetAccount(ctx context.Context, id uuid.UUID) (Account, error) {
	return s.repo.GetAccount(ctx, id)
}
