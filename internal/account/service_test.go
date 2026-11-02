package account_test

import (
	"context"
	"errors"
	"strings"
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

func (r unreachableRepo) Deposit(context.Context, account.DepositInput) (account.Transaction, error) {
	r.t.Fatal("repository must not be called")
	return account.Transaction{}, nil
}

func (r unreachableRepo) Transfer(context.Context, account.TransferInput) (account.Transaction, error) {
	r.t.Fatal("repository must not be called")
	return account.Transaction{}, nil
}

// recordingRepo captures the input the service passes to storage.
type recordingRepo struct {
	unreachableRepo
	got         account.DepositInput
	gotTransfer account.TransferInput
}

func (r *recordingRepo) Deposit(_ context.Context, in account.DepositInput) (account.Transaction, error) {
	r.got = in
	return account.Transaction{}, nil
}

func (r *recordingRepo) Transfer(_ context.Context, in account.TransferInput) (account.Transaction, error) {
	r.gotTransfer = in
	return account.Transaction{}, nil
}

func TestTransferRejectsInvalidInput(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	tests := []struct {
		name     string
		from, to uuid.UUID
		amount   int64
		key      string
		wantErr  error
	}{
		{"same account", a, a, 100, "key", account.ErrSameAccount},
		{"zero amount", a, b, 0, "key", account.ErrInvalidAmount},
		{"negative amount", a, b, -1, "key", account.ErrInvalidAmount},
		{"empty key", a, b, 100, "", account.ErrInvalidIdempotencyKey},
		{"key longer than 255 characters", a, b, 100, strings.Repeat("k", 256), account.ErrInvalidIdempotencyKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := account.NewService(unreachableRepo{t})

			_, err := svc.Transfer(context.Background(), tt.from, tt.to, tt.amount, tt.key)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestTransferRequestHash(t *testing.T) {
	hash := func(from, to uuid.UUID, amount int64) string {
		repo := &recordingRepo{unreachableRepo: unreachableRepo{t}}
		if _, err := account.NewService(repo).Transfer(context.Background(), from, to, amount, "key"); err != nil {
			t.Fatal(err)
		}
		return repo.gotTransfer.RequestHash
	}
	a, b := uuid.New(), uuid.New()

	first, second := hash(a, b, 100), hash(a, b, 100)
	if first != second {
		t.Error("identical requests must have the same hash")
	}
	if hash(a, b, 100) == hash(b, a, 100) {
		t.Error("opposite directions must have different hashes")
	}
	if hash(a, b, 100) == hash(a, b, 200) {
		t.Error("different amounts must have different hashes")
	}
}

func TestCreateAccountRejectsNegativeInitialBalance(t *testing.T) {
	svc := account.NewService(unreachableRepo{t})

	_, err := svc.CreateAccount(context.Background(), -1)

	if !errors.Is(err, account.ErrInvalidAmount) {
		t.Fatalf("got error %v, want %v", err, account.ErrInvalidAmount)
	}
}

func TestDepositRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		amount  int64
		key     string
		wantErr error
	}{
		{"zero amount", 0, "key", account.ErrInvalidAmount},
		{"negative amount", -1, "key", account.ErrInvalidAmount},
		{"empty key", 100, "", account.ErrInvalidIdempotencyKey},
		{"key longer than 255 characters", 100, strings.Repeat("k", 256), account.ErrInvalidIdempotencyKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := account.NewService(unreachableRepo{t})

			_, err := svc.Deposit(context.Background(), uuid.New(), tt.amount, tt.key)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestDepositPassesValidInputToRepository(t *testing.T) {
	repo := &recordingRepo{unreachableRepo: unreachableRepo{t}}
	svc := account.NewService(repo)
	id := uuid.New()
	key := strings.Repeat("k", 255)

	if _, err := svc.Deposit(context.Background(), id, 100, key); err != nil {
		t.Fatal(err)
	}

	if repo.got.AccountID != id || repo.got.Amount != 100 || repo.got.IdempotencyKey != key {
		t.Errorf("repository got %+v", repo.got)
	}
	if repo.got.RequestHash == "" {
		t.Error("request hash is empty")
	}
}

func TestDepositRequestHash(t *testing.T) {
	hash := func(id uuid.UUID, amount int64) string {
		repo := &recordingRepo{unreachableRepo: unreachableRepo{t}}
		if _, err := account.NewService(repo).Deposit(context.Background(), id, amount, "key"); err != nil {
			t.Fatal(err)
		}
		return repo.got.RequestHash
	}
	id, other := uuid.New(), uuid.New()

	first, second := hash(id, 100), hash(id, 100)
	if first != second {
		t.Error("identical requests must have the same hash")
	}
	if hash(id, 100) == hash(id, 200) {
		t.Error("different amounts must have different hashes")
	}
	if hash(id, 100) == hash(other, 100) {
		t.Error("different accounts must have different hashes")
	}
}
