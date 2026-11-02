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

type transferBody struct {
	ID            uuid.UUID `json:"id"`
	Type          string    `json:"type"`
	FromAccountID uuid.UUID `json:"from_account_id"`
	ToAccountID   uuid.UUID `json:"to_account_id"`
	Amount        int64     `json:"amount"`
	CreatedAt     time.Time `json:"created_at"`
}

func transfer(t *testing.T, h http.Handler, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	var headers map[string]string
	if key != "" {
		headers = map[string]string{"Idempotency-Key": key}
	}
	return doWithHeaders(t, h, http.MethodPost, "/v1/transfers", body, headers)
}

func transferJSON(from, to uuid.UUID, amount int64) string {
	return fmt.Sprintf(`{"from_account_id":%q,"to_account_id":%q,"amount":%d}`, from, to, amount)
}

func TestTransfer(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)
	from, to := createAccount(t, h, 1000), createAccount(t, h, 200)

	rec := transfer(t, h, uuid.NewString(), transferJSON(from.ID, to.ID, 300))

	assertStatus(t, rec, http.StatusCreated)
	got := decode[transferBody](t, rec)
	if got.ID == uuid.Nil || got.CreatedAt.IsZero() {
		t.Errorf("missing id or created_at: %+v", got)
	}
	if got.Type != "transfer" || got.FromAccountID != from.ID || got.ToAccountID != to.ID || got.Amount != 300 {
		t.Errorf("got %+v, want transfer of 300 from %s to %s", got, from.ID, to.ID)
	}
	if bal := getBalance(t, h, from.ID); bal != 700 {
		t.Errorf("source balance = %d, want 700", bal)
	}
	if bal := getBalance(t, h, to.ID); bal != 500 {
		t.Errorf("destination balance = %d, want 500", bal)
	}
	db.AssertLedgerConsistent(t, from.ID)
	db.AssertLedgerConsistent(t, to.ID)

	var entries int
	var sum int64
	if err := db.Pool.QueryRow(t.Context(),
		`SELECT COUNT(*), SUM(amount) FROM ledger_entries WHERE transaction_id = $1`, got.ID,
	).Scan(&entries, &sum); err != nil {
		t.Fatal(err)
	}
	if entries != 2 || sum != 0 {
		t.Errorf("transfer has %d ledger entries summing to %d, want 2 summing to 0", entries, sum)
	}
}

func TestTransferEntireBalance(t *testing.T) {
	t.Parallel()
	h, _ := newServer(t)
	from, to := createAccount(t, h, 500), createAccount(t, h, 0)

	assertStatus(t, transfer(t, h, uuid.NewString(), transferJSON(from.ID, to.ID, 500)), http.StatusCreated)

	if bal := getBalance(t, h, from.ID); bal != 0 {
		t.Errorf("source balance = %d, want 0", bal)
	}
}

func TestTransferInsufficientFunds(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)
	from, to := createAccount(t, h, 100), createAccount(t, h, 0)
	key := uuid.NewString()

	rec := transfer(t, h, key, transferJSON(from.ID, to.ID, 101))

	assertErrorCode(t, rec, http.StatusUnprocessableEntity, "insufficient_funds")
	if a, b := getBalance(t, h, from.ID), getBalance(t, h, to.ID); a != 100 || b != 0 {
		t.Errorf("balances = %d, %d after rejected transfer, want 100, 0", a, b)
	}

	// Nothing was recorded, so the same key succeeds once funds are available.
	assertStatus(t, deposit(t, h, from.ID.String(), uuid.NewString(), `{"amount":1}`), http.StatusCreated)
	assertStatus(t, transfer(t, h, key, transferJSON(from.ID, to.ID, 101)), http.StatusCreated)
	db.AssertLedgerConsistent(t, from.ID)
	db.AssertLedgerConsistent(t, to.ID)
}

func TestTransferRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	h, _ := newServer(t)
	a, b := createAccount(t, h, 1000), createAccount(t, h, 1000)
	unknown := uuid.New()

	tests := []struct {
		name        string
		key         string
		body        string
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{"same account", uuid.NewString(), transferJSON(a.ID, a.ID, 100), http.StatusUnprocessableEntity, "same_account", ""},
		{"zero amount", uuid.NewString(), transferJSON(a.ID, b.ID, 0), http.StatusUnprocessableEntity, "invalid_amount", ""},
		{"negative amount", uuid.NewString(), transferJSON(a.ID, b.ID, -5), http.StatusUnprocessableEntity, "invalid_amount", ""},
		{"unknown source", uuid.NewString(), transferJSON(unknown, b.ID, 100), http.StatusUnprocessableEntity, "account_not_found", "source"},
		{"unknown destination", uuid.NewString(), transferJSON(a.ID, unknown, 100), http.StatusUnprocessableEntity, "account_not_found", "destination"},
		{"missing source", uuid.NewString(), fmt.Sprintf(`{"to_account_id":%q,"amount":100}`, b.ID), http.StatusBadRequest, "invalid_request", "from_account_id"},
		{"missing destination", uuid.NewString(), fmt.Sprintf(`{"from_account_id":%q,"amount":100}`, a.ID), http.StatusBadRequest, "invalid_request", "to_account_id"},
		{"malformed source id", uuid.NewString(), fmt.Sprintf(`{"from_account_id":"nope","to_account_id":%q,"amount":100}`, b.ID), http.StatusBadRequest, "invalid_request", "from_account_id must be a UUID"},
		{"malformed destination id", uuid.NewString(), fmt.Sprintf(`{"from_account_id":%q,"to_account_id":"nope","amount":100}`, a.ID), http.StatusBadRequest, "invalid_request", "to_account_id must be a UUID"},
		{"missing idempotency key", "", transferJSON(a.ID, b.ID, 100), http.StatusBadRequest, "invalid_idempotency_key", ""},
		{"missing key is reported before same account", "", transferJSON(a.ID, a.ID, 100), http.StatusBadRequest, "invalid_idempotency_key", ""},
		{"account id of wrong JSON type", uuid.NewString(), fmt.Sprintf(`{"from_account_id":123,"to_account_id":%q,"amount":100}`, b.ID), http.StatusBadRequest, "invalid_request", `\"from_account_id\" must be a string`},
		{"duplicate amount", uuid.NewString(), fmt.Sprintf(`{"from_account_id":%q,"to_account_id":%q,"amount":1,"amount":900}`, a.ID, b.ID), http.StatusBadRequest, "invalid_request", "duplicate field"},
		{"malformed JSON", uuid.NewString(), `{"amount":`, http.StatusBadRequest, "invalid_request", ""},
		{"unknown field", uuid.NewString(), `{"amount":1,"currency":"EUR"}`, http.StatusBadRequest, "invalid_request", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := transfer(t, h, tt.key, tt.body)
			body := rec.Body.String() // read before assertErrorCode consumes it
			assertErrorCode(t, rec, tt.wantStatus, tt.wantCode)
			if tt.wantMessage != "" && !strings.Contains(body, tt.wantMessage) {
				t.Errorf("error message %s does not mention %q", body, tt.wantMessage)
			}
		})
	}

	if x, y := getBalance(t, h, a.ID), getBalance(t, h, b.ID); x != 1000 || y != 1000 {
		t.Errorf("balances = %d, %d after rejected transfers, want 1000, 1000", x, y)
	}
}

func TestTransferReplayReturnsOriginalResult(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)
	from, to := createAccount(t, h, 1000), createAccount(t, h, 0)
	key := uuid.NewString()

	first := transfer(t, h, key, transferJSON(from.ID, to.ID, 400))
	assertStatus(t, first, http.StatusCreated)
	firstBody := first.Body.String()

	second := transfer(t, h, key, transferJSON(from.ID, to.ID, 400))

	assertStatus(t, second, http.StatusCreated)
	if second.Body.String() != firstBody {
		t.Errorf("replay body = %s, want %s", second.Body.String(), firstBody)
	}
	if bal := getBalance(t, h, from.ID); bal != 600 {
		t.Errorf("source balance = %d, want 600 (transferred once)", bal)
	}
	db.AssertLedgerConsistent(t, from.ID)
	db.AssertLedgerConsistent(t, to.ID)
}

func TestTransferKeyReusedForDifferentRequest(t *testing.T) {
	t.Parallel()
	h, _ := newServer(t)

	t.Run("different amount", func(t *testing.T) {
		t.Parallel()
		from, to := createAccount(t, h, 1000), createAccount(t, h, 0)
		key := uuid.NewString()
		assertStatus(t, transfer(t, h, key, transferJSON(from.ID, to.ID, 100)), http.StatusCreated)

		rec := transfer(t, h, key, transferJSON(from.ID, to.ID, 200))

		assertErrorCode(t, rec, http.StatusConflict, "idempotency_key_reused")
	})
	t.Run("key from a deposit", func(t *testing.T) {
		t.Parallel()
		from, to := createAccount(t, h, 1000), createAccount(t, h, 0)
		key := uuid.NewString()
		assertStatus(t, deposit(t, h, from.ID.String(), key, `{"amount":100}`), http.StatusCreated)

		rec := transfer(t, h, key, transferJSON(from.ID, to.ID, 100))

		assertErrorCode(t, rec, http.StatusConflict, "idempotency_key_reused")
		if bal := getBalance(t, h, to.ID); bal != 0 {
			t.Errorf("destination balance = %d, want 0", bal)
		}
	})
}

// runConcurrently sends all request bodies at once and returns the responses.
// Assertions happen afterwards: t.Fatal must not be called from goroutines.
func runConcurrently(t *testing.T, h http.Handler, keys, bodies []string) []*httptest.ResponseRecorder {
	t.Helper()
	recs := make([]*httptest.ResponseRecorder, len(bodies))
	var wg sync.WaitGroup
	for i := range bodies {
		wg.Go(func() {
			recs[i] = transfer(t, h, keys[i], bodies[i])
		})
	}
	wg.Wait()
	return recs
}

func TestConcurrentTransfersConserveMoney(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)
	accounts := []uuid.UUID{
		createAccount(t, h, 1000).ID,
		createAccount(t, h, 1000).ID,
		createAccount(t, h, 1000).ID,
	}
	// Every ordered pair, including both directions between the same two
	// accounts: without consistent lock ordering these would deadlock.
	pairs := [][2]int{{0, 1}, {1, 0}, {1, 2}, {2, 1}, {0, 2}, {2, 0}}

	const n = 90
	keys, bodies := make([]string, n), make([]string, n)
	for i := range n {
		p := pairs[i%len(pairs)]
		keys[i] = uuid.NewString()
		bodies[i] = transferJSON(accounts[p[0]], accounts[p[1]], int64(100+(i%7)*50))
	}

	recs := runConcurrently(t, h, keys, bodies)

	for _, rec := range recs {
		if rec.Code == http.StatusCreated {
			continue
		}
		// The only acceptable failure; a deadlock or constraint error would be a 500.
		assertErrorCode(t, rec, http.StatusUnprocessableEntity, "insufficient_funds")
	}
	var total int64
	for _, id := range accounts {
		bal := getBalance(t, h, id)
		if bal < 0 {
			t.Errorf("account %s has negative balance %d", id, bal)
		}
		total += bal
		db.AssertLedgerConsistent(t, id)
	}
	if total != 3000 {
		t.Errorf("total balance = %d, want 3000 (money created or lost)", total)
	}
}

func TestConcurrentTransfersCannotOverspend(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)
	from, to := createAccount(t, h, 100), createAccount(t, h, 0)

	const n = 10
	keys, bodies := make([]string, n), make([]string, n)
	for i := range n {
		keys[i] = uuid.NewString()
		bodies[i] = transferJSON(from.ID, to.ID, 60)
	}

	recs := runConcurrently(t, h, keys, bodies)

	succeeded := 0
	for _, rec := range recs {
		if rec.Code == http.StatusCreated {
			succeeded++
			continue
		}
		assertErrorCode(t, rec, http.StatusUnprocessableEntity, "insufficient_funds")
	}
	if succeeded != 1 {
		t.Errorf("%d transfers succeeded, want exactly 1", succeeded)
	}
	if a, b := getBalance(t, h, from.ID), getBalance(t, h, to.ID); a != 40 || b != 60 {
		t.Errorf("balances = %d, %d, want 40, 60", a, b)
	}
	db.AssertLedgerConsistent(t, from.ID)
	db.AssertLedgerConsistent(t, to.ID)
}

func TestConcurrentTransfersWithSameKeyTransferOnce(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)
	from, to := createAccount(t, h, 1000), createAccount(t, h, 0)
	key := uuid.NewString()

	const n = 20
	keys, bodies := make([]string, n), make([]string, n)
	for i := range n {
		keys[i] = key
		bodies[i] = transferJSON(from.ID, to.ID, 100)
	}

	recs := runConcurrently(t, h, keys, bodies)

	var firstID uuid.UUID
	for i, rec := range recs {
		assertStatus(t, rec, http.StatusCreated)
		id := decode[transferBody](t, rec).ID
		if i == 0 {
			firstID = id
		} else if id != firstID {
			t.Errorf("request %d: transaction id %s, want %s", i, id, firstID)
		}
	}
	if bal := getBalance(t, h, from.ID); bal != 900 {
		t.Errorf("source balance = %d, want 900 (transferred once)", bal)
	}
	db.AssertLedgerConsistent(t, from.ID)
	db.AssertLedgerConsistent(t, to.ID)
}

func TestTransferRejectsDestinationOverflow(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)
	from, to := createAccount(t, h, 10), createAccount(t, h, math.MaxInt64)

	rec := transfer(t, h, uuid.NewString(), transferJSON(from.ID, to.ID, 1))

	assertErrorCode(t, rec, http.StatusUnprocessableEntity, "invalid_amount")
	if bal := getBalance(t, h, from.ID); bal != 10 {
		t.Errorf("source balance = %d, want 10 (rolled back)", bal)
	}
	db.AssertLedgerConsistent(t, from.ID)
	db.AssertLedgerConsistent(t, to.ID)
}

// The hardest idempotency path: concurrent requests share a key and the
// request holding it fails. Each waiter must then claim the key itself (the
// failed claim was rolled back) and get its own business error, not a 500 or
// a replay of a result that was never stored.
func TestConcurrentFailingTransfersWithSameKeyStoreNothing(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)
	from, to := createAccount(t, h, 0), createAccount(t, h, 0)
	key := uuid.NewString()

	const n = 20
	keys, bodies := make([]string, n), make([]string, n)
	for i := range n {
		keys[i] = key
		bodies[i] = transferJSON(from.ID, to.ID, 100)
	}

	for _, rec := range runConcurrently(t, h, keys, bodies) {
		assertErrorCode(t, rec, http.StatusUnprocessableEntity, "insufficient_funds")
	}

	var stored int
	if err := db.Pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM transactions WHERE idempotency_key = $1`, key).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Errorf("%d transactions stored under the key, want 0", stored)
	}

	// The key is still free once the transfer can succeed.
	assertStatus(t, deposit(t, h, from.ID.String(), uuid.NewString(), `{"amount":100}`), http.StatusCreated)
	assertStatus(t, transfer(t, h, key, transferJSON(from.ID, to.ID, 100)), http.StatusCreated)
}
