//go:build windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const windowsTaskName = "Outcrop"

func platformInstall(binPath string) error {
	// /TR is parsed by Task Scheduler as a single string; quote the binary
	// path so paths with spaces (e.g. C:\Program Files\…) work.
	trArg := `"` + binPath + `" serve`

	cmd := exec.Command("schtasks",
		"/Create",
		"/TN", windowsTaskName,
		"/TR", trArg,
		"/SC", "ONLOGON",
		"/RL", "HIGHEST",
		"/F", // force overwrite if the task already exists; install is idempotent
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks /Create: %w\noutput: %s",
			err, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(os.Stdout, "  task:   %s\n", windowsTaskName)
	fmt.Fprintln(os.Stdout, "  trigger: ONLOGON")
	return nil
}

func platformUninstall() error {
	cmd := exec.Command("schtasks", "/Delete", "/TN", windowsTaskName, "/F")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// "ERROR: The system cannot find the file specified." is a not-installed
		// state; treat it as success on uninstall.
		if strings.Contains(strings.ToLower(string(out)), "cannot find") {
			return nil
		}
		return fmt.Errorf("schtasks /Delete: %w\noutput: %s",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

func platformStatus() (installed bool, running bool, info string, err error) {
	cmd := exec.Command("schtasks", "/Query", "/TN", windowsTaskName, "/FO", "LIST", "/V")
	out, qErr := cmd.CombinedOutput()
	if qErr != nil {
		// Not installed.
		return false, false, "", nil
	}
	installed = true
	text := string(out)

	// Task Scheduler's "Status" field reports "Ready", "Running", or
	// "Disabled" for ONLOGON tasks. We treat "Running" as the live state.
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "Status:") {
			val := strings.TrimSpace(strings.TrimPrefix(l, "Status:"))
			if strings.EqualFold(val, "Running") {
				running = true
			}
			break
		}
	}
	info = strings.TrimSpace(text)
	return
}
