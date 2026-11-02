package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
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
// duplicate fields, trailing data and bodies over 1 MB are rejected. With
// allowEmpty, an empty body leaves dst at its zero value.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return badRequest("invalid_request", invalidJSONMessage(err))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		if allowEmpty {
			return nil
		}
		return badRequest("invalid_request", "request body is required")
	}
	if field, ok := duplicateField(body); ok {
		return badRequest("invalid_request", fmt.Sprintf("duplicate field %q", field))
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return badRequest("invalid_request", invalidJSONMessage(err))
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return badRequest("invalid_request", "request body must contain a single JSON object")
	}
	return nil
}

// duplicateField reports a field that appears more than once in a top-level
// JSON object. encoding/json silently keeps the last occurrence, so
// {"amount":1,"amount":7} would otherwise move 7. Field names are compared
// case-insensitively because encoding/json also matches them that way.
// Malformed JSON is left for the real decode to report.
func duplicateField(body []byte) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", false
	}
	seen := make(map[string]bool)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		name, ok := tok.(string)
		if !ok {
			return "", false
		}
		if seen[strings.ToLower(name)] {
			return name, true
		}
		seen[strings.ToLower(name)] = true

		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return "", false
		}
	}
	return "", false
}

// jsonTypeName describes a Go type the way a client sees it in JSON.
func jsonTypeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "an integer"
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	default:
		return "of a different type"
	}
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
		return fmt.Sprintf("field %q must be %s", typeErr.Field, jsonTypeName(typeErr.Type))
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
