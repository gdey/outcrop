package server

import (
	"net/http"

	"github.com/gdey/outcrop/store"
)

// tokenResponse is the JSON shape returned by GET /config/token.
type tokenResponse struct {
	Token string `json:"token"`
}

// handleGetToken returns the bearer token. IPC-only (RFD 0014 §2): the
// token's secrecy from the network listener is the whole point of the
// dual-transport design — putting this on `:7878` would defeat it.
//
// Reads from the DB rather than s.token so a future POST /config/token/rotate
// (also IPC-only) is reflected immediately without any in-memory swap.
func (s *Server) handleGetToken(w http.ResponseWriter, r *http.Request) {
	tok, err := s.store.Meta(r.Context(), store.MetaToken)
	if err != nil {
		s.log.Error("read token", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "read token failed")
		return
	}
	if tok == "" {
		writeError(w, http.StatusInternalServerError, "internal", "no token configured")
		return
	}
	writeJSON(w, tokenResponse{Token: tok})
}
