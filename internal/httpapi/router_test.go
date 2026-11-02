package httpapi_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/mananiev/bank-account-api/internal/account"
	"github.com/mananiev/bank-account-api/internal/httpapi"
)

type okPinger struct{}

func (okPinger) Ping(context.Context) error { return nil }

// newRouterWithoutDB is enough for routing behaviour that never reaches storage.
func newRouterWithoutDB() http.Handler {
	return httpapi.NewRouter(slog.New(slog.DiscardHandler), okPinger{}, account.NewService(nil))
}

type failingPinger struct{}

func (failingPinger) Ping(context.Context) error { return errors.New("connection refused") }

func TestHealthzReportsUnavailableDatabase(t *testing.T) {
	h := httpapi.NewRouter(slog.New(slog.DiscardHandler), failingPinger{}, account.NewService(nil))

	rec := do(t, h, http.MethodGet, "/healthz", "")

	assertStatus(t, rec, http.StatusServiceUnavailable)
	if got := decode[map[string]string](t, rec)["status"]; got != "unavailable" {
		t.Errorf("status = %q, want unavailable", got)
	}
}

func TestUnknownRouteReturnsJSONError(t *testing.T) {
	h := newRouterWithoutDB()

	rec := do(t, h, http.MethodGet, "/v1/nope", "")

	assertErrorCode(t, rec, http.StatusNotFound, "route_not_found")
}

func TestWrongMethodReturnsJSONError(t *testing.T) {
	h := newRouterWithoutDB()

	rec := do(t, h, http.MethodDelete, "/v1/accounts/0b6f7a6e-8f2e-4b8a-9b1a-2f1d3c4e5a6b", "")

	assertErrorCode(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, http.MethodGet) {
		t.Errorf("Allow = %q, want it to list GET", allow)
	}
}

func TestRegisteredRoutesStillWork(t *testing.T) {
	h := newRouterWithoutDB()

	assertStatus(t, do(t, h, http.MethodGet, "/healthz", ""), http.StatusOK)
	// Path parameters must still reach the handler through the wrapper.
	assertErrorCode(t, do(t, h, http.MethodGet, "/v1/accounts/not-a-uuid", ""), http.StatusBadRequest, "invalid_account_id")
}
