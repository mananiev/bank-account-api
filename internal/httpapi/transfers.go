package httpapi

import (
	"net/http"

	"github.com/google/uuid"
)

type transferRequest struct {
	FromAccountID uuid.UUID `json:"from_account_id"`
	ToAccountID   uuid.UUID `json:"to_account_id"`
	Amount        int64     `json:"amount"`
}

func (h *handlers) createTransfer(w http.ResponseWriter, r *http.Request) {
	var req transferRequest
	if err := decodeJSON(w, r, &req, false); err != nil {
		h.writeError(w, r, err)
		return
	}
	if req.FromAccountID == uuid.Nil {
		h.writeError(w, r, badRequest("invalid_request", "from_account_id is required"))
		return
	}
	if req.ToAccountID == uuid.Nil {
		h.writeError(w, r, badRequest("invalid_request", "to_account_id is required"))
		return
	}

	t, err := h.accounts.Transfer(r.Context(), req.FromAccountID, req.ToAccountID, req.Amount, r.Header.Get(idempotencyKeyHeader))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toTransactionResponse(t))
}
