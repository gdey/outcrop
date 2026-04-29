//go:build linux

package server

import (
	"fmt"
	"os"
	"path/filepath"
)

// socketPath on Linux: $XDG_RUNTIME_DIR/outcrop.sock when the env var is
// set (the canonical location for transient per-user sockets — typically
// /run/user/<uid> on systemd systems, on tmpfs, mode 0700, owned by the
// user); falls back to ~/.cache/outcrop/outcrop.sock for setups without
// an XDG runtime dir (raw containers, minimal init systems).
func socketPath() (string, error) {
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "outcrop.sock"), nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	return filepath.Join(dir, "outcrop", "outcrop.sock"), nil
}
