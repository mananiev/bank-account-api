package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mananiev/bank-account-api/internal/account"
)

type createAccountRequest struct {
	InitialBalance int64 `json:"initial_balance"`
}

type accountResponse struct {
	ID        uuid.UUID `json:"id"`
	Balance   int64     `json:"balance"`
	CreatedAt time.Time `json:"created_at"`
}

func toAccountResponse(a account.Account) accountResponse {
	return accountResponse{ID: a.ID, Balance: a.Balance, CreatedAt: a.CreatedAt}
}

func (h *handlers) createAccount(w http.ResponseWriter, r *http.Request) {
	var req createAccountRequest
	if err := decodeJSON(w, r, &req, true); err != nil {
		h.writeError(w, r, err)
		return
	}

	acc, err := h.accounts.CreateAccount(r.Context(), req.InitialBalance)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/v1/accounts/"+acc.ID.String())
	writeJSON(w, http.StatusCreated, toAccountResponse(acc))
}

func (h *handlers) getAccount(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.writeError(w, r, badRequest("invalid_account_id", "account id must be a UUID"))
		return
	}

	acc, err := h.accounts.GetAccount(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAccountResponse(acc))
}
