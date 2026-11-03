package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// JSON writes payload as a JSON response with the given status code.
func JSON(w http.ResponseWriter, code int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		slog.Error("failed to marshal response", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

type errorBody struct {
	Error string `json:"error"`
}

// Error writes {"error": msg} with the given status code.
func Error(w http.ResponseWriter, code int, msg string) {
	JSON(w, code, errorBody{Error: msg})
}

// ServerError logs err with the request's id and user, then answers 500
// without leaking the cause to the client.
func ServerError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "request failed",
		slog.String("route", RoutePattern(r)),
		slog.String("error", err.Error()),
	)
	Error(w, http.StatusInternalServerError, "internal server error")
}
