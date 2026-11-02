package account_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mananiev/bank-account-api/internal/account"
)

// unreachableRepo fails the test if the service touches storage.
type unreachableRepo struct{ t *testing.T }

func (r unreachableRepo) CreateAccount(context.Context, int64) (account.Account, error) {
	r.t.Fatal("repository must not be called")
	return account.Account{}, nil
}

func (r unreachableRepo) GetAccount(context.Context, uuid.UUID) (account.Account, error) {
	r.t.Fatal("repository must not be called")
	return account.Account{}, nil
}

func TestCreateAccountRejectsNegativeInitialBalance(t *testing.T) {
	svc := account.NewService(unreachableRepo{t})

	_, err := svc.CreateAccount(context.Background(), -1)

	if !errors.Is(err, account.ErrInvalidAmount) {
		t.Fatalf("got error %v, want %v", err, account.ErrInvalidAmount)
	}
}
