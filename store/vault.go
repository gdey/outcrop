package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrVaultNotFound is returned when a query targets a non-existent vault key.
var ErrVaultNotFound = errors.New("vault not found")

// Vault is one row of the vaults table.
type Vault struct {
	Key            string
	DisplayName    string
	Description    string
	Path           string
	ClippingPath   string
	AttachmentPath string
	CreatedAt      time.Time
}

// CreateVault inserts a new vault row. The caller supplies the key (typically
// a freshly-generated ULID) so the same value can be returned to the user.
func (s *Store) CreateVault(ctx context.Context, v Vault) error {
	if v.ClippingPath == "" {
		v.ClippingPath = "Clippings"
	}
	if v.AttachmentPath == "" {
		v.AttachmentPath = "Clippings/attachments"
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO vaults (key, display_name, description, path, clipping_path, attachment_path, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, v.Key, v.DisplayName, v.Description, v.Path, v.ClippingPath, v.AttachmentPath, v.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert vault: %w", err)
	}
	return nil
}

// GetVault returns the vault with the given key.
func (s *Store) GetVault(ctx context.Context, key string) (Vault, error) {
	var v Vault
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT key, display_name, description, path, clipping_path, attachment_path, created_at
		FROM vaults WHERE key = ?
	`, key).Scan(&v.Key, &v.DisplayName, &v.Description, &v.Path, &v.ClippingPath, &v.AttachmentPath, &createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Vault{}, ErrVaultNotFound
	case err != nil:
		return Vault{}, fmt.Errorf("read vault: %w", err)
	}
	v.CreatedAt = time.Unix(createdAt, 0).UTC()
	return v, nil
}

// ListVaults returns all vaults ordered alphabetically by display name.
func (s *Store) ListVaults(ctx context.Context) ([]Vault, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT key, display_name, description, path, clipping_path, attachment_path, created_at
		FROM vaults ORDER BY display_name
	`)
	if err != nil {
		return nil, fmt.Errorf("list vaults: %w", err)
	}
	defer rows.Close()

	var out []Vault
	for rows.Next() {
		var v Vault
		var createdAt int64
		if err := rows.Scan(&v.Key, &v.DisplayName, &v.Description, &v.Path, &v.ClippingPath, &v.AttachmentPath, &createdAt); err != nil {
			return nil, fmt.Errorf("scan vault: %w", err)
		}
		v.CreatedAt = time.Unix(createdAt, 0).UTC()
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vaults: %w", err)
	}
	return out, nil
}

// RenameVault changes a vault's display name.
func (s *Store) RenameVault(ctx context.Context, key, displayName string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE vaults SET display_name = ? WHERE key = ?`, displayName, key)
	if err != nil {
		return fmt.Errorf("rename vault: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrVaultNotFound
	}
	return nil
}

// DescribeVault sets the free-form description column on a vault. Empty string
// is a valid value and clears the description.
func (s *Store) DescribeVault(ctx context.Context, key, description string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE vaults SET description = ? WHERE key = ?`, description, key)
	if err != nil {
		return fmt.Errorf("describe vault: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrVaultNotFound
	}
	return nil
}

// DeleteVault removes a vault. The FK cascade on history.vault_key prunes
// associated history rows automatically.
func (s *Store) DeleteVault(ctx context.Context, key string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM vaults WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("delete vault: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrVaultNotFound
	}
	return nil
}
