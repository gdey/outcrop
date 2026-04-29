package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/gdey/outcrop/store"
)

// vaultDetailDTO is the IPC-side vault representation. Richer than the
// extension-facing vaultDTO (which intentionally omits paths and
// descriptions) — the tray and CLI see filesystem paths and free-form
// descriptions because they're trusted local clients (RFD 0014 §2).
type vaultDetailDTO struct {
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Path        string `json:"path"`
	IsDefault   bool   `json:"isDefault"`
}

func toVaultDetailDTO(v store.Vault, defaultKey string) vaultDetailDTO {
	return vaultDetailDTO{
		Key:         v.Key,
		DisplayName: v.DisplayName,
		Description: v.Description,
		Path:        v.Path,
		IsDefault:   v.Key == defaultKey,
	}
}

// createVaultRequest is the body of POST /vaults.
type createVaultRequest struct {
	DisplayName string `json:"displayName"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	MakeDefault bool   `json:"makeDefault,omitempty"`
}

// updateVaultRequest is the body of PUT /vaults/{key}. All fields are
// optional; only those present are applied. Pointer types let us
// distinguish "empty string" (clear the description) from "absent"
// (leave it unchanged).
type updateVaultRequest struct {
	DisplayName *string `json:"displayName,omitempty"`
	Description *string `json:"description,omitempty"`
	Path        *string `json:"path,omitempty"`
}

func (s *Server) handleCreateVault(w http.ResponseWriter, r *http.Request) {
	var req createVaultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "displayName is required")
		return
	}
	abs, err := resolveExistingDir(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	ctx := r.Context()
	key, err := newServerULID()
	if err != nil {
		s.log.Error("generate ulid", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "generate vault key failed")
		return
	}
	v := store.Vault{
		Key:         key,
		DisplayName: displayName,
		Description: req.Description,
		Path:        abs,
	}
	if err := s.store.CreateVault(ctx, v); err != nil {
		s.log.Error("create vault", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "create vault failed")
		return
	}

	// First vault on the server, or caller asked for default → make it so.
	defaultKey, err := s.store.Meta(ctx, store.MetaDefaultVaultKey)
	if err != nil {
		s.log.Error("read default vault", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "read default vault failed")
		return
	}
	makeDefault := req.MakeDefault || defaultKey == ""
	if makeDefault {
		if err := s.store.SetMeta(ctx, store.MetaDefaultVaultKey, key); err != nil {
			s.log.Error("set default vault", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "set default vault failed")
			return
		}
		defaultKey = key
	}

	stored, err := s.store.GetVault(ctx, key)
	if err != nil {
		s.log.Error("re-read vault", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "re-read vault failed")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(toVaultDetailDTO(stored, defaultKey))
}

func (s *Server) handleUpdateVault(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing vault key")
		return
	}

	var req updateVaultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.DisplayName == nil && req.Description == nil && req.Path == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "no fields to update")
		return
	}

	ctx := r.Context()

	// Confirm the vault exists up front so all our error paths are 404 vs 500
	// in the predictable place.
	if _, err := s.store.GetVault(ctx, key); err != nil {
		if errors.Is(err, store.ErrVaultNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no vault with that key")
			return
		}
		s.log.Error("get vault", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "get vault failed")
		return
	}

	if req.DisplayName != nil {
		dn := strings.TrimSpace(*req.DisplayName)
		if dn == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "displayName cannot be empty")
			return
		}
		if err := s.store.RenameVault(ctx, key, dn); err != nil {
			s.log.Error("rename vault", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "rename vault failed")
			return
		}
	}
	if req.Description != nil {
		if err := s.store.DescribeVault(ctx, key, *req.Description); err != nil {
			s.log.Error("describe vault", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "describe vault failed")
			return
		}
	}
	if req.Path != nil {
		// Path mutation isn't supported by the existing store API yet — flag
		// it as not implemented rather than silently dropping it. When we
		// add `store.SetVaultPath`, this clause flips on.
		writeError(w, http.StatusNotImplemented, "not_implemented",
			"path updates are not yet supported; remove the vault and re-add it")
		return
	}

	defaultKey, err := s.store.Meta(ctx, store.MetaDefaultVaultKey)
	if err != nil {
		s.log.Error("read default vault", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "read default vault failed")
		return
	}
	stored, err := s.store.GetVault(ctx, key)
	if err != nil {
		s.log.Error("re-read vault", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "re-read vault failed")
		return
	}
	writeJSON(w, toVaultDetailDTO(stored, defaultKey))
}

func (s *Server) handleDeleteVault(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing vault key")
		return
	}
	ctx := r.Context()

	current, err := s.store.Meta(ctx, store.MetaDefaultVaultKey)
	if err != nil {
		s.log.Error("read default vault", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "read default vault failed")
		return
	}
	if err := s.store.DeleteVault(ctx, key); err != nil {
		if errors.Is(err, store.ErrVaultNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no vault with that key")
			return
		}
		s.log.Error("delete vault", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "delete vault failed")
		return
	}
	if current == key {
		if err := s.store.DeleteMeta(ctx, store.MetaDefaultVaultKey); err != nil {
			s.log.Error("clear default vault", "err", err)
			// vault is already gone; surface a soft warning but still 204.
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetDefaultVault(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing vault key")
		return
	}
	ctx := r.Context()
	if _, err := s.store.GetVault(ctx, key); err != nil {
		if errors.Is(err, store.ErrVaultNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no vault with that key")
			return
		}
		s.log.Error("get vault", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "get vault failed")
		return
	}
	if err := s.store.SetMeta(ctx, store.MetaDefaultVaultKey, key); err != nil {
		s.log.Error("set default vault", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "set default vault failed")
		return
	}
	stored, _ := s.store.GetVault(ctx, key) // already checked above
	writeJSON(w, toVaultDetailDTO(stored, key))
}

// resolveExistingDir validates that p resolves to an existing directory and
// returns its absolute, symlink-resolved path. Mirrors the CLI's helper of
// the same name; kept duplicated rather than cross-imported because the
// shape is so small and cli/server share no other code.
func resolveExistingDir(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("path is empty")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("path %q does not exist or cannot be read", p)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", p)
	}
	return resolved, nil
}

// newServerULID generates a fresh ULID for vault keys. Mirrors cli.newULID.
func newServerULID() (string, error) {
	id, err := ulid.New(ulid.Timestamp(time.Now().UTC()), rand.Reader)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
