// Package cli implements the outcrop command-line subcommands.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// DBPath returns the absolute path to outcrop.db, honouring the OUTCROP_DB
// environment variable as an override (useful for tests and dev). Otherwise
// it sits under os.UserConfigDir / outcrop / outcrop.db.
func DBPath() (string, error) {
	if v := os.Getenv("OUTCROP_DB"); v != "" {
		return v, nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(cfg, "outcrop", "outcrop.db"), nil
}

// ModelsDir returns the directory where downloaded GGUF model files live —
// a sibling of the DB directory, per RFD 0005 §"Storage". Honours the
// OUTCROP_MODELS_DIR override for tests.
func ModelsDir() (string, error) {
	if v := os.Getenv("OUTCROP_MODELS_DIR"); v != "" {
		return v, nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(cfg, "outcrop", "models"), nil
}
