package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

const maxRequestBodyBytes = 64 << 10

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, errorBody{Error: code})
}

func writeErrorMessage(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: code, Message: message})
}

func (a *api) writeInternalError(w http.ResponseWriter, r *http.Request, err error) {
	a.logger.ErrorContext(r.Context(), "request failed",
		slog.String("request_id", RequestID(r.Context())),
		slog.String("route", r.Pattern),
		slog.String("error", err.Error()),
	)
	writeError(w, http.StatusInternalServerError, "internal_error")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(dst)
	if err == nil && !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		err = errors.New("body must contain a single JSON object")
	}
	if err != nil {
		writeErrorMessage(w, http.StatusBadRequest, "invalid_request", "request body must be a single JSON object with the documented fields")
		return false
	}
	return true
}
