package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// CmdInstallService implements `outcrop install-service [install|uninstall|status]`.
// Default subcommand is `install`. Per-OS plumbing lives in build-tag-gated
// install_service_{darwin,linux,windows,other}.go.
//
// The user-facing contract:
//
//   - `install` writes a launch unit (launchd plist on macOS, systemd user
//     unit on Linux, Task Scheduler entry on Windows) that runs the *current*
//     outcrop binary at user login. Idempotent — re-running replaces the
//     existing unit.
//   - `uninstall` removes the unit and, where applicable, stops the running
//     service.
//   - `status` reports whether the unit is registered and whether the service
//     is currently running.
//
// All three operate at the **per-user** level (no sudo) — the launchd domain
// is `gui/<uid>`, systemd is `--user`, the Windows Task is created without
// elevation when feasible.
func CmdInstallService(args []string) error {
	sub := "install"
	var rest []string
	if len(args) > 0 {
		switch args[0] {
		case "install", "uninstall", "status":
			sub = args[0]
			rest = args[1:]
		default:
			// First arg is a flag like --help; treat as install with flags.
			rest = args
		}
	}

	switch sub {
	case "install":
		return installServiceInstall(rest)
	case "uninstall":
		return installServiceUninstall(rest)
	case "status":
		return installServiceStatus(rest)
	}
	return fmt.Errorf("unknown install-service subcommand %q", sub)
}

func installServiceInstall(args []string) error {
	fs := flag.NewFlagSet("install-service install", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	binPath, err := resolveBinaryPath()
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stdout, "Installing outcrop run-on-login service.")
	fmt.Fprintf(os.Stdout, "  binary: %s\n", binPath)

	if err := platformInstall(binPath); err != nil {
		return err
	}

	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "Installed and started.")
	fmt.Fprintln(os.Stdout, "  status:    outcrop install-service status")
	fmt.Fprintln(os.Stdout, "  uninstall: outcrop install-service uninstall")
	return nil
}

func installServiceUninstall(args []string) error {
	fs := flag.NewFlagSet("install-service uninstall", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Uninstalling outcrop run-on-login service.")
	if err := platformUninstall(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Done.")
	return nil
}

func installServiceStatus(args []string) error {
	fs := flag.NewFlagSet("install-service status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	installed, running, info, err := platformStatus()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Installed:  %v\n", installed)
	fmt.Fprintf(os.Stdout, "Running:    %v\n", running)
	if info != "" {
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "Details:")
		fmt.Fprintln(os.Stdout, info)
	}
	return nil
}

// resolveBinaryPath returns an absolute, symlink-resolved path to the running
// outcrop binary. The launch unit references this path verbatim, so resolving
// symlinks now means the unit doesn't break later if the symlink moves.
func resolveBinaryPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return filepath.Clean(p), nil
}

// xmlEscape escapes the small set of XML metacharacters that might appear in
// a filesystem path. Used by the macOS plist writer.
func xmlEscape(s string) string {
	r := s
	r = replaceAll(r, "&", "&amp;")
	r = replaceAll(r, "<", "&lt;")
	r = replaceAll(r, ">", "&gt;")
	r = replaceAll(r, `"`, "&quot;")
	r = replaceAll(r, "'", "&apos;")
	return r
}

// replaceAll is fmt.Sprintf-free strings.ReplaceAll to keep this file's import
// list small and consistent across platforms.
func replaceAll(s, old, new string) string {
	out := ""
	for {
		i := indexOf(s, old)
		if i < 0 {
			return out + s
		}
		out += s[:i] + new
		s = s[i+len(old):]
	}
}

func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
