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
		return fmt.Errorf("usage: outcrop vault <add|list|rename|describe|show|remove|default> ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return cmdVaultAdd(rest)
	case "list":
		return cmdVaultList(rest)
	case "rename":
		return cmdVaultRename(rest)
	case "describe":
		return cmdVaultDescribe(rest)
	case "show":
		return cmdVaultShow(rest)
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
	description := fs.String("description", "", "free-form description; strongly recommended if you plan to use the LLM router — vaults without a description effectively can't win against described vaults in routing decisions (RFD 0005)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: outcrop vault add [--default] [--description \"…\"] <displayName> <path>")
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
		Description: *description,
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
	if *description != "" {
		fmt.Fprintf(os.Stdout, "  desc: %s\n", *description)
	}
	if *makeDefault {
		fmt.Fprintf(os.Stdout, "  (default)\n")
	}
	if *description == "" {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Tip: this vault has no description. The LLM router (when enabled) picks")
		fmt.Fprintln(os.Stdout, "the right vault more reliably when each vault has one — vaults without a")
		fmt.Fprintln(os.Stdout, "description effectively can't win against described vaults. Add one with:")
		fmt.Fprintf(os.Stdout, "  outcrop vault describe %s \"…\"\n", key)
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

	// Show description column only if at least one vault has one — keeps the
	// table narrow for users who don't bother with descriptions.
	anyDescription := false
	for _, v := range vaults {
		if v.Description != "" {
			anyDescription = true
			break
		}
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	if anyDescription {
		fmt.Fprintln(tw, "KEY\tNAME\tDEFAULT\tDESCRIPTION\tPATH")
	} else {
		fmt.Fprintln(tw, "KEY\tNAME\tDEFAULT\tPATH")
	}
	for _, v := range vaults {
		flag := ""
		if v.Key == defaultKey {
			flag = "*"
		}
		if anyDescription {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", v.Key, v.DisplayName, flag, truncate(v.Description, 40), v.Path)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", v.Key, v.DisplayName, flag, v.Path)
		}
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

func cmdVaultDescribe(args []string) error {
	fs := flag.NewFlagSet("vault describe", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: outcrop vault describe <key> \"<description>\" (use \"\" to clear)")
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.DescribeVault(context.Background(), fs.Arg(0), fs.Arg(1)); err != nil {
		if errors.Is(err, store.ErrVaultNotFound) {
			return fmt.Errorf("no vault with key %q", fs.Arg(0))
		}
		return err
	}
	if fs.Arg(1) == "" {
		fmt.Fprintln(os.Stdout, "Description cleared.")
	} else {
		fmt.Fprintln(os.Stdout, "Description set.")
	}
	return nil
}

func cmdVaultShow(args []string) error {
	fs := flag.NewFlagSet("vault show", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: outcrop vault show <key>")
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	v, err := st.GetVault(ctx, fs.Arg(0))
	if err != nil {
		if errors.Is(err, store.ErrVaultNotFound) {
			return fmt.Errorf("no vault with key %q", fs.Arg(0))
		}
		return err
	}
	defaultKey, err := st.Meta(ctx, store.MetaDefaultVaultKey)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "Key:         %s\n", v.Key)
	fmt.Fprintf(os.Stdout, "Name:        %s\n", v.DisplayName)
	if v.Description == "" {
		fmt.Fprintf(os.Stdout, "Description: (unset)\n")
	} else {
		fmt.Fprintf(os.Stdout, "Description: %s\n", v.Description)
	}
	fmt.Fprintf(os.Stdout, "Path:        %s\n", v.Path)
	fmt.Fprintf(os.Stdout, "Clippings:   %s\n", v.ClippingPath)
	fmt.Fprintf(os.Stdout, "Attachments: %s\n", v.AttachmentPath)
	fmt.Fprintf(os.Stdout, "Created:     %s\n", v.CreatedAt.Format(time.RFC3339))
	if v.Key == defaultKey {
		fmt.Fprintf(os.Stdout, "Default:     yes\n")
	}
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}
