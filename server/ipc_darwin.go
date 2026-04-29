//go:build darwin

package server

import (
	"fmt"
	"os"
	"path/filepath"
)

// socketPath on macOS: ~/Library/Caches/outcrop/outcrop.sock.
//
// `os.UserCacheDir()` returns ~/Library/Caches by Apple convention. macOS
// has no equivalent of $XDG_RUNTIME_DIR; ~/Library/Caches is the closest
// match for "user-private transient state" — it's per-user, not backed up,
// and not synced via iCloud.
func socketPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	return filepath.Join(dir, "outcrop", "outcrop.sock"), nil
}
