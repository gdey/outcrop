package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"crypto/rand"

	"github.com/oklog/ulid/v2"

	"github.com/gdey/outcrop/store"
)

// CmdVault implements `outcrop vault <subcommand>`.
func CmdVault(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: outcrop vault <add|list|rename|remove|default> ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return cmdVaultAdd(rest)
	case "list":
		return cmdVaultList(rest)
	case "rename":
		return cmdVaultRename(rest)
	case "remove":
		return cmdVaultRemove(rest)
	case "default":
		return cmdVaultDefault(rest)
	default:
		return fmt.Errorf("unknown vault subcommand %q", sub)
	}
}

func cmdVaultAdd(args []string) error {
	fs := flag.NewFlagSet("vault add", flag.ContinueOnError)
	makeDefault := fs.Bool("default", false, "set this vault as the default")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: outcrop vault add [--default] <displayName> <path>")
	}
	displayName := fs.Arg(0)
	rawPath := fs.Arg(1)

	abs, err := resolveExistingDir(rawPath)
	if err != nil {
		return err
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	key, err := newULID()
	if err != nil {
		return err
	}

	v := store.Vault{
		Key:         key,
		DisplayName: displayName,
		Path:        abs,
	}
	if err := st.CreateVault(ctx, v); err != nil {
		return err
	}

	if *makeDefault {
		if err := st.SetMeta(ctx, store.MetaDefaultVaultKey, key); err != nil {
			return err
		}
	} else {
		// If no default is set yet, this becomes the default.
		current, err := st.Meta(ctx, store.MetaDefaultVaultKey)
		if err != nil {
			return err
		}
		if current == "" {
			if err := st.SetMeta(ctx, store.MetaDefaultVaultKey, key); err != nil {
				return err
			}
			*makeDefault = true
		}
	}

	fmt.Fprintf(os.Stdout, "Added vault\n  key:  %s\n  name: %s\n  path: %s\n", key, displayName, abs)
	if *makeDefault {
		fmt.Fprintf(os.Stdout, "  (default)\n")
	}
	return nil
}

func cmdVaultList(args []string) error {
	fs := flag.NewFlagSet("vault list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	vaults, err := st.ListVaults(ctx)
	if err != nil {
		return err
	}
	defaultKey, err := st.Meta(ctx, store.MetaDefaultVaultKey)
	if err != nil {
		return err
	}

	if len(vaults) == 0 {
		fmt.Fprintln(os.Stdout, "(no vaults configured)")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tNAME\tDEFAULT\tPATH")
	for _, v := range vaults {
		flag := ""
		if v.Key == defaultKey {
			flag = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", v.Key, v.DisplayName, flag, v.Path)
	}
	return tw.Flush()
}

func cmdVaultRename(args []string) error {
	fs := flag.NewFlagSet("vault rename", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: outcrop vault rename <key> <newName>")
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.RenameVault(context.Background(), fs.Arg(0), fs.Arg(1)); err != nil {
		if errors.Is(err, store.ErrVaultNotFound) {
			return fmt.Errorf("no vault with key %q", fs.Arg(0))
		}
		return err
	}
	fmt.Fprintf(os.Stdout, "Renamed.\n")
	return nil
}

func cmdVaultRemove(args []string) error {
	fs := flag.NewFlagSet("vault remove", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: outcrop vault remove <key>")
	}
	key := fs.Arg(0)

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()

	// If this was the default, clear it.
	current, err := st.Meta(ctx, store.MetaDefaultVaultKey)
	if err != nil {
		return err
	}

	if err := st.DeleteVault(ctx, key); err != nil {
		if errors.Is(err, store.ErrVaultNotFound) {
			return fmt.Errorf("no vault with key %q", key)
		}
		return err
	}

	if current == key {
		if err := st.DeleteMeta(ctx, store.MetaDefaultVaultKey); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "Removed (was default; default cleared).")
	} else {
		fmt.Fprintln(os.Stdout, "Removed.")
	}
	return nil
}

func cmdVaultDefault(args []string) error {
	fs := flag.NewFlagSet("vault default", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: outcrop vault default <key>")
	}
	key := fs.Arg(0)

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	if _, err := st.GetVault(ctx, key); err != nil {
		if errors.Is(err, store.ErrVaultNotFound) {
			return fmt.Errorf("no vault with key %q", key)
		}
		return err
	}
	if err := st.SetMeta(ctx, store.MetaDefaultVaultKey, key); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Default set to %s.\n", key)
	return nil
}

func resolveExistingDir(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("path %q does not exist or cannot be read: %w", p, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", p)
	}
	return resolved, nil
}

func newULID() (string, error) {
	now := time.Now().UTC()
	id, err := ulid.New(ulid.Timestamp(now), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate ulid: %w", err)
	}
	return id.String(), nil
}

// openStore is a small helper used by the vault subcommands.
func openStore() (*store.Store, error) {
	dbPath, err := DBPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no config at %s; run `outcrop init` first", dbPath)
	}
	return store.Open(dbPath)
}
