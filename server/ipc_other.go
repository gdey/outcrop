//go:build !darwin && !linux && !windows

package server

import (
	"fmt"
	"os"
	"path/filepath"
)

// socketPath on platforms outside the primary three (the *BSDs, illumos,
// Plan 9, etc.). Falls back to os.UserCacheDir() — the path may not match
// local conventions on every system but the location is at least
// user-private. If somebody actually runs outcrop on one of these and
// finds the location wrong, we'll add a dedicated file then.
func socketPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	return filepath.Join(dir, "outcrop", "outcrop.sock"), nil
}
