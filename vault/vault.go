// Package vault knows how to write files into an Obsidian vault directory.
// It does not talk to SQLite; callers pass in a resolved Vault struct and the
// package handles path validation and atomic file IO.
package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Vault carries everything a writer needs about a single vault: where it
// lives on disk and where clippings and attachments go inside it.
type Vault struct {
	Path           string // absolute path to the vault root
	ClippingPath   string // vault-relative subdirectory for note files
	AttachmentPath string // vault-relative subdirectory for image files
}

// ValidateRelPath ensures p is a relative path that stays strictly inside
// its parent (i.e., a real subdirectory, never the parent itself). It
// rejects absolute paths, .. segments anywhere in the original or cleaned
// form, ".", and Windows drive letters. Empty is also invalid.
func ValidateRelPath(p string) error {
	if p == "" {
		return errors.New("path is empty")
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("path must be relative: %q", p)
	}
	// Reject Windows-style drive prefixes like "C:" even on non-Windows hosts;
	// tampered DBs shouldn't be able to escape via path encoding tricks.
	if len(p) >= 2 && p[1] == ':' {
		return fmt.Errorf("path must not contain a drive letter: %q", p)
	}
	// Look at the raw segments first — filepath.Clean("a/..") collapses to ".",
	// which would hide the .. from the cleaned form.
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if seg == ".." {
			return fmt.Errorf("path must not contain ..: %q", p)
		}
	}
	cleaned := filepath.ToSlash(filepath.Clean(p))
	if cleaned == "." || cleaned == "" {
		return fmt.Errorf("path must be a subdirectory, not the vault root: %q", p)
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return fmt.Errorf("path escapes the vault: %q", p)
	}
	return nil
}

// Resolve joins the vault root with a vault-relative subdirectory and
// re-validates. It is the only function that should produce absolute paths
// for vault writes.
func (v Vault) Resolve(rel string) (string, error) {
	if err := ValidateRelPath(rel); err != nil {
		return "", err
	}
	return filepath.Join(v.Path, rel), nil
}

// EnsureDirs creates the clipping and attachment directories under the vault
// root if they do not exist.
func (v Vault) EnsureDirs() error {
	if err := ValidateRelPath(v.ClippingPath); err != nil {
		return fmt.Errorf("clipping_path: %w", err)
	}
	if err := ValidateRelPath(v.AttachmentPath); err != nil {
		return fmt.Errorf("attachment_path: %w", err)
	}
	for _, rel := range []string{v.ClippingPath, v.AttachmentPath} {
		abs := filepath.Join(v.Path, rel)
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", abs, err)
		}
	}
	return nil
}

// WriteExclusive writes data to abs using O_EXCL. If the file already exists
// the call returns os.ErrExist; the caller is expected to retry with a new
// filename.
func WriteExclusive(abs string, data []byte) error {
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(abs)
		return fmt.Errorf("write %s: %w", abs, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(abs)
		return fmt.Errorf("sync %s: %w", abs, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(abs)
		return fmt.Errorf("close %s: %w", abs, err)
	}
	return nil
}

// WriteAtomic writes data to abs by writing to a sibling tempfile, fsyncing,
// and renaming. Overwrites any existing file at abs.
func WriteAtomic(abs string, data []byte) error {
	dir := filepath.Dir(abs)
	tmp, err := os.CreateTemp(dir, ".outcrop-tmp-*")
	if err != nil {
		return fmt.Errorf("create tmp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, abs); err != nil {
		cleanup()
		return fmt.Errorf("rename tmp -> %s: %w", abs, err)
	}
	return nil
}
