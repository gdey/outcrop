package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gdey/outcrop/agent"
	"github.com/gdey/outcrop/clip"
	"github.com/gdey/outcrop/store"
	"github.com/gdey/outcrop/vault"
)

// clipRequest is the JSON shape POSTed by the extension to /clip.
type clipRequest struct {
	Vault        string `json:"vault"`
	URL          string `json:"url"`
	Title        string `json:"title"`
	SelectedText string `json:"selectedText"`
	Notes        string `json:"notes"`
	ImageBase64  string `json:"imageBase64"`
}

// clipResponse is the JSON shape returned on success.
type clipResponse struct {
	NotePath  string `json:"notePath"`
	ImagePath string `json:"imagePath"`
}

func (s *Server) handleClip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Cap the request body at 32 MiB. A Retina full-screen PNG base64-encoded
	// runs ~10-20 MiB; 32 MiB gives headroom without inviting abuse.
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)

	var req clipRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	if req.Vault == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "vault key is required")
		return
	}

	v, err := s.store.GetVault(ctx, req.Vault)
	if err != nil {
		if errors.Is(err, store.ErrVaultNotFound) {
			writeError(w, http.StatusBadRequest, "vault_not_found", "no vault with that key")
			return
		}
		s.log.Error("read vault", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "read vault failed")
		return
	}

	res, err := clip.Write(vault.Vault{
		Path:           v.Path,
		ClippingPath:   v.ClippingPath,
		AttachmentPath: v.AttachmentPath,
	}, clip.Input{
		URL:          req.URL,
		Title:        req.Title,
		SelectedText: req.SelectedText,
		Notes:        req.Notes,
		ImageBase64:  req.ImageBase64,
		When:         time.Now().UTC(),
	})
	if err != nil {
		if errors.Is(err, clip.ErrInvalidImage) {
			writeError(w, http.StatusBadRequest, "image_decode_failed", err.Error())
			return
		}
		s.log.Error("write clip", "err", err)
		writeError(w, http.StatusInternalServerError, "disk_write_failed", "could not write clip")
		return
	}

	if domain := agent.RegistrableDomain(req.URL); domain != "" {
		if err := s.store.RecordClip(ctx, domain, v.Key, time.Now().UTC()); err != nil {
			// History is best-effort; the clip is on disk.
			s.log.Warn("record history", "err", err, "domain", domain)
		}
	}

	writeJSON(w, clipResponse{
		NotePath:  res.NotePath,
		ImagePath: res.ImagePath,
	})
}
