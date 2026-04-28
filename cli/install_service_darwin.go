//go:build darwin

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const darwinServiceLabel = "me.gdey.outcrop"

func darwinPaths() (plistPath, logDir string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolve home dir: %w", err)
	}
	plistPath = filepath.Join(home, "Library", "LaunchAgents", darwinServiceLabel+".plist")
	logDir = filepath.Join(home, "Library", "Logs", "outcrop")
	return
}

func platformInstall(binPath string) error {
	plistPath, logDir, err := darwinPaths()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	plist := buildLaunchdPlist(binPath, logDir)
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	fmt.Fprintf(os.Stdout, "  plist:  %s\n", plistPath)
	fmt.Fprintf(os.Stdout, "  logs:   %s/\n", logDir)

	target := fmt.Sprintf("gui/%d", os.Getuid())
	serviceTarget := target + "/" + darwinServiceLabel

	// launchctl bootstrap is idempotent against fresh state; if the service
	// is already loaded, bootstrap returns 17 (EEXIST). Bootout first to
	// keep re-runs clean.
	_ = exec.Command("launchctl", "bootout", serviceTarget).Run()

	out, err := exec.Command("launchctl", "bootstrap", target, plistPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap %s: %w\noutput: %s",
			target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func platformUninstall() error {
	plistPath, _, err := darwinPaths()
	if err != nil {
		return err
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), darwinServiceLabel)

	// Best-effort: bootout returns non-zero if the service isn't loaded,
	// which is fine on a re-run after a manual unload.
	_ = exec.Command("launchctl", "bootout", target).Run()

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}

func platformStatus() (installed bool, running bool, info string, err error) {
	plistPath, _, perr := darwinPaths()
	if perr != nil {
		err = perr
		return
	}
	if _, statErr := os.Stat(plistPath); statErr == nil {
		installed = true
	}

	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), darwinServiceLabel)
	out, _ := exec.Command("launchctl", "print", target).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if strings.Contains(text, "state = running") {
		running = true
	}
	if installed && text != "" {
		info = "launchctl print " + target + ":\n" + indentEachLine(text, "  ")
	}
	return
}

func indentEachLine(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// buildLaunchdPlist renders the launchd plist for outcrop. The path values
// are XML-escaped; everything else is constant text.
//
// RunAtLoad + KeepAlive together give us "run on login, restart on crash"
// without manual intervention.
func buildLaunchdPlist(binPath, logDir string) string {
	stdout := filepath.Join(logDir, "outcrop.out.log")
	stderr := filepath.Join(logDir, "outcrop.err.log")
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + darwinServiceLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + xmlEscape(binPath) + `</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>` + xmlEscape(stdout) + `</string>
  <key>StandardErrorPath</key>
  <string>` + xmlEscape(stderr) + `</string>
</dict>
</plist>
`
}
