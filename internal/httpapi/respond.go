package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mananiev/bank-account-api/internal/account"
)

const maxBodyBytes = 1 << 20 // 1 MB

// requestError is a client error detected by the HTTP layer (malformed input).
type requestError struct {
	code    string
	message string
}

func (e *requestError) Error() string { return e.message }

func badRequest(code, message string) error {
	return &requestError{code: code, message: message}
}

// decodeJSON strictly decodes the request body into dst: unknown fields,
// trailing data and bodies over 1 MB are rejected. With allowEmpty, an empty
// body leaves dst at its zero value.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) && allowEmpty {
			return nil
		}
		return badRequest("invalid_request", invalidJSONMessage(err))
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return badRequest("invalid_request", "request body must contain a single JSON object")
	}
	return nil
}

func invalidJSONMessage(err error) string {
	var maxBytesErr *http.MaxBytesError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.Is(err, io.EOF):
		return "request body is required"
	case errors.As(err, &maxBytesErr):
		return fmt.Sprintf("request body must not exceed %d bytes", maxBytesErr.Limit)
	case errors.As(err, &typeErr) && typeErr.Field == "":
		return "request body must be a JSON object"
	case errors.As(err, &typeErr):
		return fmt.Sprintf("field %q must be of type %s", typeErr.Field, typeErr.Type)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		// encoding/json has no typed error for unknown fields.
		return strings.TrimPrefix(err.Error(), "json: ")
	default:
		return "invalid JSON: " + err.Error()
	}
}

type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError maps an error to its HTTP status and machine-readable code.
// Unexpected errors are logged and returned as a generic 500.
func (h *handlers) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var reqErr *requestError
	var status int
	var code string

	switch {
	case errors.As(err, &reqErr):
		status, code = http.StatusBadRequest, reqErr.code
	case errors.Is(err, account.ErrInvalidAmount):
		status, code = http.StatusUnprocessableEntity, "invalid_amount"
	case errors.Is(err, account.ErrNotFound):
		status, code = http.StatusNotFound, "account_not_found"
	case errors.Is(err, account.ErrInvalidIdempotencyKey):
		status, code = http.StatusBadRequest, "invalid_idempotency_key"
	case errors.Is(err, account.ErrIdempotencyKeyReused):
		status, code = http.StatusConflict, "idempotency_key_reused"
	case errors.Is(err, account.ErrTransferAccountNotFound):
		// 422, not 404: the account is referenced in the body, not the URL.
		status, code = http.StatusUnprocessableEntity, "account_not_found"
	case errors.Is(err, account.ErrSameAccount):
		status, code = http.StatusUnprocessableEntity, "same_account"
	case errors.Is(err, account.ErrInsufficientFunds):
		status, code = http.StatusUnprocessableEntity, "insufficient_funds"
	default:
		h.logger.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{errorDetail{
			Code: "internal_error", Message: "internal server error",
		}})
		return
	}

	writeJSON(w, status, errorResponse{errorDetail{Code: code, Message: err.Error()}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
