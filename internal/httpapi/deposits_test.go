package httpapi_test

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type depositBody struct {
	ID        uuid.UUID `json:"id"`
	Type      string    `json:"type"`
	AccountID uuid.UUID `json:"account_id"`
	Amount    int64     `json:"amount"`
	CreatedAt time.Time `json:"created_at"`
}

func createAccount(t *testing.T, h http.Handler, initialBalance int64) accountBody {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/v1/accounts", fmt.Sprintf(`{"initial_balance":%d}`, initialBalance))
	assertStatus(t, rec, http.StatusCreated)
	return decode[accountBody](t, rec)
}

func getBalance(t *testing.T, h http.Handler, id uuid.UUID) int64 {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/v1/accounts/"+id.String(), "")
	assertStatus(t, rec, http.StatusOK)
	return decode[accountBody](t, rec).Balance
}

func deposit(t *testing.T, h http.Handler, accountID, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	var headers map[string]string
	if key != "" {
		headers = map[string]string{"Idempotency-Key": key}
	}
	return doWithHeaders(t, h, http.MethodPost, "/v1/accounts/"+accountID+"/deposits", body, headers)
}

func TestDeposit(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)
	acc := createAccount(t, h, 1000)

	rec := deposit(t, h, acc.ID.String(), uuid.NewString(), `{"amount":2500}`)

	assertStatus(t, rec, http.StatusCreated)
	got := decode[depositBody](t, rec)
	if got.ID == uuid.Nil || got.CreatedAt.IsZero() {
		t.Errorf("missing id or created_at: %+v", got)
	}
	if got.Type != "deposit" || got.AccountID != acc.ID || got.Amount != 2500 {
		t.Errorf("got %+v, want deposit of 2500 into %s", got, acc.ID)
	}
	if bal := getBalance(t, h, acc.ID); bal != 3500 {
		t.Errorf("balance = %d, want 3500", bal)
	}
	db.AssertLedgerConsistent(t, acc.ID)
}

func TestDepositReplayReturnsOriginalResult(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)
	acc := createAccount(t, h, 0)
	key := uuid.NewString()

	first := deposit(t, h, acc.ID.String(), key, `{"amount":500}`)
	assertStatus(t, first, http.StatusCreated)
	firstBody := first.Body.String()

	// Same request, different formatting: still a replay.
	second := deposit(t, h, acc.ID.String(), key, `{ "amount" : 500 }`)

	assertStatus(t, second, http.StatusCreated)
	if second.Body.String() != firstBody {
		t.Errorf("replay body = %s, want %s", second.Body.String(), firstBody)
	}
	if bal := getBalance(t, h, acc.ID); bal != 500 {
		t.Errorf("balance = %d, want 500 (deposited once)", bal)
	}
	db.AssertLedgerConsistent(t, acc.ID)
}

func TestDepositKeyReusedForDifferentRequest(t *testing.T) {
	t.Parallel()
	h, _ := newServer(t)

	t.Run("different amount", func(t *testing.T) {
		t.Parallel()
		acc := createAccount(t, h, 0)
		key := uuid.NewString()
		assertStatus(t, deposit(t, h, acc.ID.String(), key, `{"amount":500}`), http.StatusCreated)

		rec := deposit(t, h, acc.ID.String(), key, `{"amount":600}`)

		assertErrorCode(t, rec, http.StatusConflict, "idempotency_key_reused")
		if bal := getBalance(t, h, acc.ID); bal != 500 {
			t.Errorf("balance = %d, want 500", bal)
		}
	})
	t.Run("different account", func(t *testing.T) {
		t.Parallel()
		a, b := createAccount(t, h, 0), createAccount(t, h, 0)
		key := uuid.NewString()
		assertStatus(t, deposit(t, h, a.ID.String(), key, `{"amount":500}`), http.StatusCreated)

		rec := deposit(t, h, b.ID.String(), key, `{"amount":500}`)

		assertErrorCode(t, rec, http.StatusConflict, "idempotency_key_reused")
		if bal := getBalance(t, h, b.ID); bal != 0 {
			t.Errorf("balance of second account = %d, want 0", bal)
		}
	})
}

func TestDepositToUnknownAccountStoresNothing(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)
	key := uuid.NewString()

	rec := deposit(t, h, uuid.NewString(), key, `{"amount":500}`)
	assertErrorCode(t, rec, http.StatusNotFound, "account_not_found")

	// The failed request recorded nothing, so the key is still free.
	acc := createAccount(t, h, 0)
	assertStatus(t, deposit(t, h, acc.ID.String(), key, `{"amount":500}`), http.StatusCreated)
	db.AssertLedgerConsistent(t, acc.ID)
}

func TestDepositRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	h, _ := newServer(t)
	acc := createAccount(t, h, 0)
	accID := acc.ID.String()

	tests := []struct {
		name       string
		accountID  string
		key        string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"zero amount", accID, uuid.NewString(), `{"amount":0}`, http.StatusUnprocessableEntity, "invalid_amount"},
		{"negative amount", accID, uuid.NewString(), `{"amount":-100}`, http.StatusUnprocessableEntity, "invalid_amount"},
		{"missing amount", accID, uuid.NewString(), `{}`, http.StatusUnprocessableEntity, "invalid_amount"},
		{"missing idempotency key", accID, "", `{"amount":100}`, http.StatusBadRequest, "invalid_idempotency_key"},
		{"idempotency key too long", accID, strings.Repeat("k", 256), `{"amount":100}`, http.StatusBadRequest, "invalid_idempotency_key"},
		{"idempotency key not valid UTF-8", accID, "key-\xff\xfe", `{"amount":100}`, http.StatusBadRequest, "invalid_idempotency_key"},
		{"missing key is reported before invalid amount", accID, "", `{"amount":0}`, http.StatusBadRequest, "invalid_idempotency_key"},
		{"duplicate field", accID, uuid.NewString(), `{"amount":1,"amount":7}`, http.StatusBadRequest, "invalid_request"},
		{"duplicate field differing in case", accID, uuid.NewString(), `{"amount":1,"AMOUNT":7}`, http.StatusBadRequest, "invalid_request"},
		{"malformed JSON", accID, uuid.NewString(), `{"amount":`, http.StatusBadRequest, "invalid_request"},
		{"empty body", accID, uuid.NewString(), ``, http.StatusBadRequest, "invalid_request"},
		{"unknown field", accID, uuid.NewString(), `{"amount":100,"currency":"EUR"}`, http.StatusBadRequest, "invalid_request"},
		{"malformed account id", "not-a-uuid", uuid.NewString(), `{"amount":100}`, http.StatusBadRequest, "invalid_account_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := deposit(t, h, tt.accountID, tt.key, tt.body)
			assertErrorCode(t, rec, tt.wantStatus, tt.wantCode)
		})
	}

	if bal := getBalance(t, h, acc.ID); bal != 0 {
		t.Errorf("balance = %d after rejected deposits, want 0", bal)
	}
}

func TestDepositAcceptsMaxLengthIdempotencyKey(t *testing.T) {
	t.Parallel()
	h, _ := newServer(t)
	acc := createAccount(t, h, 0)

	rec := deposit(t, h, acc.ID.String(), uuid.NewString()+strings.Repeat("k", 255-36), `{"amount":1}`)

	assertStatus(t, rec, http.StatusCreated)
}

func TestDepositRejectsBalanceOverflow(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)
	acc := createAccount(t, h, math.MaxInt64)

	rec := deposit(t, h, acc.ID.String(), uuid.NewString(), `{"amount":1}`)

	assertErrorCode(t, rec, http.StatusUnprocessableEntity, "invalid_amount")
	db.AssertLedgerConsistent(t, acc.ID)
}

func TestConcurrentDepositsWithSameKeyDepositOnce(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)
	acc := createAccount(t, h, 0)
	key := uuid.NewString()

	const n = 20
	// Requests run in goroutines; assertions (which may call t.Fatal) run after.
	recs := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			recs[i] = deposit(t, h, acc.ID.String(), key, `{"amount":100}`)
		})
	}
	wg.Wait()

	var firstID uuid.UUID
	for i, rec := range recs {
		assertStatus(t, rec, http.StatusCreated)
		id := decode[depositBody](t, rec).ID
		if i == 0 {
			firstID = id
		} else if id != firstID {
			t.Errorf("request %d: transaction id %s, want %s", i, id, firstID)
		}
	}
	if bal := getBalance(t, h, acc.ID); bal != 100 {
		t.Errorf("balance = %d, want 100 (deposited once)", bal)
	}
	db.AssertLedgerConsistent(t, acc.ID)
}
