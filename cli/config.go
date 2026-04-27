package cli

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/gdey/outcrop/store"
)

// CmdConfig implements `outcrop config <subcommand>`.
func CmdConfig(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: outcrop config <show|path>")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "show":
		return cmdConfigShow(rest)
	case "path":
		return cmdConfigPath(rest)
	default:
		return fmt.Errorf("unknown config subcommand %q", sub)
	}
}

func cmdConfigShow(args []string) error {
	fs := flag.NewFlagSet("config show", flag.ContinueOnError)
	showToken := fs.Bool("show-token", false, "print the token in clear")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	token, err := st.Meta(ctx, store.MetaToken)
	if err != nil {
		return err
	}
	addr, err := st.Meta(ctx, store.MetaListenAddr)
	if err != nil {
		return err
	}
	def, err := st.Meta(ctx, store.MetaDefaultVaultKey)
	if err != nil {
		return err
	}

	dbPath, _ := DBPath()
	fmt.Fprintf(os.Stdout, "DB:            %s\n", dbPath)
	fmt.Fprintf(os.Stdout, "Listen addr:   %s\n", addr)
	fmt.Fprintf(os.Stdout, "Default vault: %s\n", def)
	if *showToken {
		fmt.Fprintf(os.Stdout, "Token:         %s\n", token)
	} else if token != "" {
		fmt.Fprintf(os.Stdout, "Token:         (set; pass --show-token to reveal)\n")
	} else {
		fmt.Fprintf(os.Stdout, "Token:         (unset)\n")
	}
	return nil
}

func cmdConfigPath(args []string) error {
	fs := flag.NewFlagSet("config path", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := DBPath()
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, p)
	return nil
}
