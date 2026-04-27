// Command outcrop is the local server and CLI for the Outcrop web-clipper.
//
// See docs/rfd/0001-architecture-overview.md and 0003-v1-server.md for the
// design.
package main

import (
	"fmt"
	"os"

	"github.com/gdey/outcrop/cli"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "outcrop:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "init":
		return cli.CmdInit(rest)
	case "serve":
		return cli.CmdServe(rest)
	case "vault":
		return cli.CmdVault(rest)
	case "config":
		return cli.CmdConfig(rest)
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q (try `outcrop help`)", cmd)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stdout, `Outcrop — local server for the Firefox web-clipper.

Usage:
  outcrop init [--force]
  outcrop serve [--addr 127.0.0.1:7878] [--log-format text|json]
  outcrop vault add [--default] <displayName> <path>
  outcrop vault list
  outcrop vault rename <key> <newName>
  outcrop vault remove <key>
  outcrop vault default <key>
  outcrop config show [--show-token]
  outcrop config path
  outcrop help`)
}
