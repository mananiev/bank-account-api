package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

type transactionItem struct {
	ID            uuid.UUID `json:"id"`
	Type          string    `json:"type"`
	AccountID     uuid.UUID `json:"account_id"`
	FromAccountID uuid.UUID `json:"from_account_id"`
	ToAccountID   uuid.UUID `json:"to_account_id"`
	Amount        int64     `json:"amount"`
	CreatedAt     time.Time `json:"created_at"`
}

type transactionList struct {
	Transactions []transactionItem `json:"transactions"`
}

func listTransactions(t *testing.T, h http.Handler, id string) transactionList {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/v1/accounts/"+id+"/transactions", "")
	assertStatus(t, rec, http.StatusOK)
	return decode[transactionList](t, rec)
}

func TestListTransactions(t *testing.T) {
	t.Parallel()
	h, _ := newServer(t)
	a, b := createAccount(t, h, 1000), createAccount(t, h, 500)

	dep := decode[depositBody](t, deposit(t, h, a.ID.String(), uuid.NewString(), `{"amount":2500}`))
	out := decode[transferBody](t, transfer(t, h, uuid.NewString(), transferJSON(a.ID, b.ID, 400)))
	in := decode[transferBody](t, transfer(t, h, uuid.NewString(), transferJSON(b.ID, a.ID, 100)))

	got := listTransactions(t, h, a.ID.String()).Transactions

	want := []transactionItem{
		{ID: in.ID, Type: "transfer", FromAccountID: b.ID, ToAccountID: a.ID, Amount: 100},
		{ID: out.ID, Type: "transfer", FromAccountID: a.ID, ToAccountID: b.ID, Amount: 400},
		{ID: dep.ID, Type: "deposit", AccountID: a.ID, Amount: 2500},
		{Type: "opening", AccountID: a.ID, Amount: 1000},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d transactions, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if w.ID != uuid.Nil && g.ID != w.ID {
			t.Errorf("transaction %d: id %s, want %s", i, g.ID, w.ID)
		}
		if g.ID == uuid.Nil || g.CreatedAt.IsZero() {
			t.Errorf("transaction %d: missing id or created_at: %+v", i, g)
		}
		if g.Type != w.Type || g.AccountID != w.AccountID || g.FromAccountID != w.FromAccountID ||
			g.ToAccountID != w.ToAccountID || g.Amount != w.Amount {
			t.Errorf("transaction %d = %+v, want %+v", i, g, w)
		}
		if i > 0 && g.CreatedAt.After(got[i-1].CreatedAt) {
			t.Errorf("transaction %d is newer than transaction %d: not newest first", i, i-1)
		}
	}
}

// The listed movements must add up to the balance: the list is an audit trail.
func TestListTransactionsSumsToBalance(t *testing.T) {
	t.Parallel()
	h, _ := newServer(t)
	a, b := createAccount(t, h, 700), createAccount(t, h, 300)
	assertStatus(t, deposit(t, h, a.ID.String(), uuid.NewString(), `{"amount":50}`), http.StatusCreated)
	assertStatus(t, transfer(t, h, uuid.NewString(), transferJSON(a.ID, b.ID, 250)), http.StatusCreated)
	assertStatus(t, transfer(t, h, uuid.NewString(), transferJSON(b.ID, a.ID, 75)), http.StatusCreated)

	for _, acc := range []uuid.UUID{a.ID, b.ID} {
		var sum int64
		for _, tx := range listTransactions(t, h, acc.String()).Transactions {
			if tx.Type == "transfer" && tx.FromAccountID == acc {
				sum -= tx.Amount
			} else {
				sum += tx.Amount
			}
		}
		if bal := getBalance(t, h, acc); sum != bal {
			t.Errorf("account %s: transactions sum to %d, balance is %d", acc, sum, bal)
		}
	}
}

func TestListTransactionsEmptyAccount(t *testing.T) {
	t.Parallel()
	h, _ := newServer(t)
	acc := createAccount(t, h, 0)

	rec := do(t, h, http.MethodGet, "/v1/accounts/"+acc.ID.String()+"/transactions", "")

	assertStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); body != "{\"transactions\":[]}\n" {
		t.Errorf("body = %q, want an empty list (not null)", body)
	}
}

func TestListTransactionsErrors(t *testing.T) {
	t.Parallel()
	h, _ := newServer(t)

	t.Run("unknown account", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/v1/accounts/"+uuid.NewString()+"/transactions", "")
		assertErrorCode(t, rec, http.StatusNotFound, "account_not_found")
	})
	t.Run("malformed id", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/v1/accounts/not-a-uuid/transactions", "")
		assertErrorCode(t, rec, http.StatusBadRequest, "invalid_account_id")
	})
}
