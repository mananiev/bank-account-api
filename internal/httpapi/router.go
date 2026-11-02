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
	return jsonRoutingErrors(mux)
}

// jsonRoutingErrors makes the mux's own 404 and 405 responses use the API's
// JSON error format instead of plain text. Matched routes pass straight through.
func jsonRoutingErrors(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallback, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r) // sets path values for the matched route
			return
		}

		// No route matched: let the mux decide what it would answer (404, or
		// 405 with an Allow header), then answer the same in JSON.
		probe := &headerRecorder{header: http.Header{}, status: http.StatusOK}
		fallback.ServeHTTP(probe, r)

		switch probe.status {
		case http.StatusMethodNotAllowed:
			w.Header().Set("Allow", probe.header.Get("Allow"))
			writeJSON(w, http.StatusMethodNotAllowed, errorResponse{errorDetail{
				Code: "method_not_allowed", Message: r.Method + " is not allowed for " + r.URL.Path,
			}})
		case http.StatusNotFound:
			writeJSON(w, http.StatusNotFound, errorResponse{errorDetail{
				Code: "route_not_found", Message: "no route for " + r.Method + " " + r.URL.Path,
			}})
		default:
			// Anything else (e.g. a redirect to a cleaned path) is passed through unchanged.
			mux.ServeHTTP(w, r)
		}
	})
}

// headerRecorder captures a handler's headers and status and discards the body.
type headerRecorder struct {
	header http.Header
	status int
}

func (h *headerRecorder) Header() http.Header         { return h.header }
func (h *headerRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (h *headerRecorder) WriteHeader(status int)      { h.status = status }

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
