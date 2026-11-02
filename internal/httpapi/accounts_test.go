package httpapi_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mananiev/bank-account-api/internal/account"
	"github.com/mananiev/bank-account-api/internal/httpapi"
	"github.com/mananiev/bank-account-api/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

type accountBody struct {
	ID        uuid.UUID `json:"id"`
	Balance   int64     `json:"balance"`
	CreatedAt time.Time `json:"created_at"`
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func newServer(t *testing.T) (http.Handler, *testdb.DB) {
	t.Helper()
	db := testdb.New(t)
	logger := slog.New(slog.DiscardHandler)
	return httpapi.NewRouter(logger, db.Store, account.NewService(db.Store)), db
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequestWithContext(t.Context(), method, path, nil)
	} else {
		req = httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var v T
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return v
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, want, rec.Body.String())
	}
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	assertStatus(t, rec, wantStatus)
	body := decode[errorBody](t, rec)
	if body.Error.Code != wantCode {
		t.Errorf("error code = %q, want %q", body.Error.Code, wantCode)
	}
	if body.Error.Message == "" {
		t.Error("error message is empty")
	}
}

func TestCreateAccount(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)

	tests := []struct {
		name        string
		body        string
		wantBalance int64
	}{
		{name: "no body defaults to zero", body: "", wantBalance: 0},
		{name: "empty object defaults to zero", body: `{}`, wantBalance: 0},
		{name: "explicit zero", body: `{"initial_balance":0}`, wantBalance: 0},
		{name: "positive initial balance", body: `{"initial_balance":5000}`, wantBalance: 5000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := do(t, h, http.MethodPost, "/v1/accounts", tt.body)

			assertStatus(t, rec, http.StatusCreated)
			got := decode[accountBody](t, rec)
			if got.ID == uuid.Nil {
				t.Error("id is empty")
			}
			if got.Balance != tt.wantBalance {
				t.Errorf("balance = %d, want %d", got.Balance, tt.wantBalance)
			}
			if got.CreatedAt.IsZero() {
				t.Error("created_at is empty")
			}
			if loc, want := rec.Header().Get("Location"), "/v1/accounts/"+got.ID.String(); loc != want {
				t.Errorf("Location = %q, want %q", loc, want)
			}
			db.AssertLedgerConsistent(t, got.ID)
		})
	}
}

func TestCreateAccountRecordsOpeningBalanceInLedger(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)

	rec := do(t, h, http.MethodPost, "/v1/accounts", `{"initial_balance":5000}`)
	assertStatus(t, rec, http.StatusCreated)
	acc := decode[accountBody](t, rec)

	var entries int
	var amount int64
	err := db.Pool.QueryRow(t.Context(), `
		SELECT COUNT(*), COALESCE(SUM(e.amount), 0)
		FROM ledger_entries e JOIN transactions tx ON tx.id = e.transaction_id
		WHERE e.account_id = $1 AND tx.type = 'OPENING'`, acc.ID).Scan(&entries, &amount)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 1 || amount != 5000 {
		t.Errorf("OPENING entries = %d with sum %d, want 1 entry of 5000", entries, amount)
	}
}

func TestCreateAccountWithZeroBalanceWritesNoLedgerEntries(t *testing.T) {
	t.Parallel()
	h, db := newServer(t)

	rec := do(t, h, http.MethodPost, "/v1/accounts", `{"initial_balance":0}`)
	assertStatus(t, rec, http.StatusCreated)
	acc := decode[accountBody](t, rec)

	var entries int
	if err := db.Pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM ledger_entries WHERE account_id = $1`, acc.ID).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 0 {
		t.Errorf("ledger entries = %d, want 0", entries)
	}
}

func TestCreateAccountRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	h, _ := newServer(t)

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"negative initial balance", `{"initial_balance":-1}`, http.StatusUnprocessableEntity, "invalid_amount"},
		{"malformed JSON", `{"initial_balance":`, http.StatusBadRequest, "invalid_request"},
		{"unknown field", `{"inital_balance":5000}`, http.StatusBadRequest, "invalid_request"},
		{"fractional amount", `{"initial_balance":10.5}`, http.StatusBadRequest, "invalid_request"},
		{"amount as string", `{"initial_balance":"5000"}`, http.StatusBadRequest, "invalid_request"},
		{"amount overflows int64", `{"initial_balance":9223372036854775808}`, http.StatusBadRequest, "invalid_request"},
		{"trailing data", `{"initial_balance":1}{"initial_balance":2}`, http.StatusBadRequest, "invalid_request"},
		{"not an object", `[]`, http.StatusBadRequest, "invalid_request"},
		{"body too large", `{"initial_balance":1` + strings.Repeat(" ", 1<<20) + `}`, http.StatusBadRequest, "invalid_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := do(t, h, http.MethodPost, "/v1/accounts", tt.body)
			assertErrorCode(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestGetAccount(t *testing.T) {
	t.Parallel()
	h, _ := newServer(t)

	created := decode[accountBody](t, do(t, h, http.MethodPost, "/v1/accounts", `{"initial_balance":1234}`))

	rec := do(t, h, http.MethodGet, "/v1/accounts/"+created.ID.String(), "")

	assertStatus(t, rec, http.StatusOK)
	got := decode[accountBody](t, rec)
	if got.ID != created.ID || got.Balance != 1234 || !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("got %+v, want %+v", got, created)
	}
}

func TestGetAccountErrors(t *testing.T) {
	t.Parallel()
	h, _ := newServer(t)

	t.Run("unknown id", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/v1/accounts/"+uuid.NewString(), "")
		assertErrorCode(t, rec, http.StatusNotFound, "account_not_found")
	})
	t.Run("malformed id", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/v1/accounts/not-a-uuid", "")
		assertErrorCode(t, rec, http.StatusBadRequest, "invalid_account_id")
	})
}
