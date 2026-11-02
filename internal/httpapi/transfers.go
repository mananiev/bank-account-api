package httpapi

import (
	"net/http"

	"github.com/google/uuid"
)

// Account IDs are decoded as strings and parsed here, so errors can name the
// field ("from_account_id must be a UUID") instead of surfacing a generic
// decoder message.
type transferRequest struct {
	FromAccountID string `json:"from_account_id"`
	ToAccountID   string `json:"to_account_id"`
	Amount        int64  `json:"amount"`
}

func (h *handlers) createTransfer(w http.ResponseWriter, r *http.Request) {
	var req transferRequest
	if err := decodeJSON(w, r, &req, false); err != nil {
		h.writeError(w, r, err)
		return
	}
	from, err := parseAccountID("from_account_id", req.FromAccountID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	to, err := parseAccountID("to_account_id", req.ToAccountID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	t, err := h.accounts.Transfer(r.Context(), from, to, req.Amount, r.Header.Get(idempotencyKeyHeader))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toTransactionResponse(t))
}

func parseAccountID(field, value string) (uuid.UUID, error) {
	if value == "" {
		return uuid.Nil, badRequest("invalid_request", field+" is required")
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, badRequest("invalid_request", field+" must be a UUID")
	}
	return id, nil
}
