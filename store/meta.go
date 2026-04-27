package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Meta keys.
const (
	MetaToken           = "token"
	MetaListenAddr      = "listen_addr"
	MetaDefaultVaultKey = "default_vault_key"

	// Agent (RFD 0005).
	MetaAgentEnabled   = "agent_enabled"    // "true" / "false"
	MetaAgentBackend   = "agent_backend"    // "kronk" or "http"
	MetaAgentModelPath = "agent_model_path" // absolute path to GGUF (kronk only)
	MetaAgentEndpoint  = "agent_endpoint"   // OpenAI-compatible base URL (http only)
	MetaAgentModel     = "agent_model"      // model name on the endpoint (http only)
	MetaAgentAPIKey    = "agent_api_key"    // optional bearer (http only)
	MetaAgentTimeoutMs = "agent_timeout_ms" // integer milliseconds
)

// Meta returns the value of a singleton config key, or "" with no error if the
// key is unset.
func (s *Store) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("read meta %q: %w", key, err)
	}
	return v, nil
}

// SetMeta upserts a singleton config key.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	if err != nil {
		return fmt.Errorf("write meta %q: %w", key, err)
	}
	return nil
}

// DeleteMeta removes a meta key. Missing keys are not an error.
func (s *Store) DeleteMeta(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM meta WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("delete meta %q: %w", key, err)
	}
	return nil
}
