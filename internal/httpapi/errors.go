package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// errorEnvelope is the single, consistent shape for every error we return.
// A stable error contract matters as much as the success contract: clients and
// the audit/observability tooling can rely on `error.code` being machine-stable
// while `error.message` stays human-readable.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeJSON marshals v and writes it with the given status. Marshaling errors
// are logged and downgraded to a 500 so a handler can't accidentally write a
// half response.
func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.Error("failed to marshal response", "error", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeError emits the standard error envelope.
func writeError(w http.ResponseWriter, log *slog.Logger, status int, code, msg string) {
	writeJSON(w, log, status, errorEnvelope{Error: errorBody{Code: code, Message: msg}})
}
