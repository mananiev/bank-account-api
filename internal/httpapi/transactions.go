package httpapi

import (
	"net/http"

	"github.com/google/uuid"
)

// transactionListResponse wraps the list in an object so pagination fields
// (e.g. a cursor) can be added later without breaking clients.
type transactionListResponse struct {
	Transactions []transactionResponse `json:"transactions"`
}

func (h *handlers) listTransactions(w http.ResponseWriter, r *http.Request) {
	accountID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.writeError(w, r, badRequest("invalid_account_id", "account id must be a UUID"))
		return
	}

	txs, err := h.accounts.ListTransactions(r.Context(), accountID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	resp := transactionListResponse{Transactions: make([]transactionResponse, 0, len(txs))}
	for _, t := range txs {
		resp.Transactions = append(resp.Transactions, toTransactionResponse(t))
	}
	writeJSON(w, http.StatusOK, resp)
}
