// Package httpapi exposes the account service over HTTP.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/mananiev/bank-account-api/internal/account"
)

// Pinger reports whether a dependency (the database) is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// NewRouter builds the HTTP handler with all routes registered.
func NewRouter(logger *slog.Logger, db Pinger, accounts *account.Service) http.Handler {
	h := &handlers{logger: logger, accounts: accounts}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz(logger, db))
	mux.HandleFunc("POST /v1/accounts", h.createAccount)
	mux.HandleFunc("GET /v1/accounts/{id}", h.getAccount)
	mux.HandleFunc("POST /v1/accounts/{id}/deposits", h.createDeposit)
	mux.HandleFunc("GET /v1/accounts/{id}/transactions", h.listTransactions)
	mux.HandleFunc("POST /v1/transfers", h.createTransfer)
	return mux
}

type handlers struct {
	logger   *slog.Logger
	accounts *account.Service
}

func healthz(logger *slog.Logger, db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			logger.WarnContext(ctx, "health check failed", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
