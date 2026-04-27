package server

import (
	"encoding/json"
	"net/http"
)

// errorResponse is the JSON shape returned for all error responses.
type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// writeError marshals an error response and writes the given status code.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: code, Message: message})
}

// writeJSON marshals v as a 200 JSON response.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}
