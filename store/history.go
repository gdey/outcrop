package store

import (
	"context"
	"fmt"
	"time"
)

// RecordClip bumps the (domain, vaultKey) row in history, inserting if
// missing. Called on every successful POST /clip.
func (s *Store) RecordClip(ctx context.Context, domain, vaultKey string, when time.Time) error {
	if when.IsZero() {
		when = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO history (domain, vault_key, count, last_used)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(domain, vault_key) DO UPDATE SET
			count = count + 1,
			last_used = excluded.last_used
	`, domain, vaultKey, when.Unix())
	if err != nil {
		return fmt.Errorf("record clip: %w", err)
	}
	return nil
}

// VaultKeysForDomain returns vault keys with history for the domain, ordered
// most-recently-used first. An empty result is normal and not an error.
func (s *Store) VaultKeysForDomain(ctx context.Context, domain string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT vault_key FROM history
		WHERE domain = ?
		ORDER BY last_used DESC
	`, domain)
	if err != nil {
		return nil, fmt.Errorf("query history: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate history: %w", err)
	}
	return keys, nil
}
