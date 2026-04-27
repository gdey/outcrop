package server

import (
	"net/http"

	"github.com/gdey/outcrop/agent"
	"github.com/gdey/outcrop/store"
)

// vaultDTO is the JSON shape for a vault entry on /vaults.
type vaultDTO struct {
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
	IsDefault   bool   `json:"isDefault"`
}

func (s *Server) handleListVaults(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	vaults, err := s.store.ListVaults(ctx)
	if err != nil {
		s.log.Error("list vaults", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "list vaults failed")
		return
	}

	defaultKey, err := s.store.Meta(ctx, store.MetaDefaultVaultKey)
	if err != nil {
		s.log.Error("read default vault", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "read default vault failed")
		return
	}

	ranked := s.scorer.Score(ctx, agent.Input{
		URL:   r.URL.Query().Get("url"),
		Title: r.URL.Query().Get("title"),
	}, vaults)

	out := make([]vaultDTO, 0, len(ranked))
	for _, v := range ranked {
		out = append(out, vaultDTO{
			Key:         v.Key,
			DisplayName: v.DisplayName,
			IsDefault:   v.Key == defaultKey,
		})
	}
	writeJSON(w, out)
}
