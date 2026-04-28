//go:build linux

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const linuxUnitName = "outcrop.service"

func linuxUnitPath() (string, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(cfgDir, "systemd", "user", linuxUnitName), nil
}

func platformInstall(binPath string) error {
	unitPath, err := linuxUnitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("create systemd user dir: %w", err)
	}

	unit := buildSystemdUnit(binPath)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	fmt.Fprintf(os.Stdout, "  unit:   %s\n", unitPath)

	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user daemon-reload: %w\noutput: %s",
			err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", linuxUnitName).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user enable --now %s: %w\noutput: %s\n\n"+
			"If you're on a system without a user systemd instance (e.g. a "+
			"raw container), the unit was written but not started — invoke "+
			"`outcrop serve` manually.",
			linuxUnitName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func platformUninstall() error {
	unitPath, err := linuxUnitPath()
	if err != nil {
		return err
	}

	// Best-effort: disable --now stops + disables; ok if it fails (e.g. unit
	// already missing).
	_ = exec.Command("systemctl", "--user", "disable", "--now", linuxUnitName).Run()

	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit: %w", err)
	}

	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

func platformStatus() (installed bool, running bool, info string, err error) {
	unitPath, perr := linuxUnitPath()
	if perr != nil {
		err = perr
		return
	}
	if _, statErr := os.Stat(unitPath); statErr == nil {
		installed = true
	}

	activeOut, _ := exec.Command("systemctl", "--user", "is-active", linuxUnitName).Output()
	if strings.TrimSpace(string(activeOut)) == "active" {
		running = true
	}
	enabledOut, _ := exec.Command("systemctl", "--user", "is-enabled", linuxUnitName).Output()

	if installed {
		info = fmt.Sprintf("systemctl --user is-active:  %s\nsystemctl --user is-enabled: %s",
			strings.TrimSpace(string(activeOut)),
			strings.TrimSpace(string(enabledOut)))
	}
	return
}

// buildSystemdUnit renders the systemd user unit for outcrop. Restart=on-
// failure recovers from crashes; After=network.target ensures the loopback
// listener can bind. WantedBy=default.target is the user-level equivalent of
// the system multi-user.target — runs on login.
func buildSystemdUnit(binPath string) string {
	return `[Unit]
Description=Outcrop local web-clip server
After=network.target

[Service]
Type=simple
ExecStart=` + binPath + ` serve
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`
}
