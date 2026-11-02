package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mananiev/bank-account-api/internal/account"
)

const idempotencyKeyHeader = "Idempotency-Key"

type depositRequest struct {
	Amount int64 `json:"amount"`
}

// transactionResponse has type-specific account fields; unused ones are omitted.
type transactionResponse struct {
	ID            uuid.UUID `json:"id"`
	Type          string    `json:"type"`
	AccountID     uuid.UUID `json:"account_id,omitzero"`
	FromAccountID uuid.UUID `json:"from_account_id,omitzero"`
	ToAccountID   uuid.UUID `json:"to_account_id,omitzero"`
	Amount        int64     `json:"amount"`
	CreatedAt     time.Time `json:"created_at"`
}

func toTransactionResponse(t account.Transaction) transactionResponse {
	return transactionResponse{
		ID:            t.ID,
		Type:          string(t.Type),
		AccountID:     t.AccountID,
		FromAccountID: t.FromAccountID,
		ToAccountID:   t.ToAccountID,
		Amount:        t.Amount,
		CreatedAt:     t.CreatedAt,
	}
}

func (h *handlers) createDeposit(w http.ResponseWriter, r *http.Request) {
	accountID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.writeError(w, r, badRequest("invalid_account_id", "account id must be a UUID"))
		return
	}

	var req depositRequest
	if err := decodeJSON(w, r, &req, false); err != nil {
		h.writeError(w, r, err)
		return
	}

	t, err := h.accounts.Deposit(r.Context(), accountID, req.Amount, r.Header.Get(idempotencyKeyHeader))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toTransactionResponse(t))
}
