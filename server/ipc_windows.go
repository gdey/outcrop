//go:build windows

package server

import (
	"fmt"
	"os"
	"path/filepath"
)

// socketPath on Windows: %LocalAppData%\outcrop\outcrop.sock.
//
// Go 1.20+ supports AF_UNIX on Windows 10 1803+, so a Unix-style socket
// path on the local filesystem is portable. %LocalAppData% (what
// `os.UserCacheDir()` resolves to on Windows) is the per-user, non-roaming
// location — appropriate for transient socket state that shouldn't follow
// the user across machines.
func socketPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	return filepath.Join(dir, "outcrop", "outcrop.sock"), nil
}
