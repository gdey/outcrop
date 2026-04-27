package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/gdey/outcrop/store"
)

const defaultListenAddr = "127.0.0.1:7878"

// CmdInit implements `outcrop init`.
func CmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	force := fs.Bool("force", false, "overwrite existing config (token will change)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dbPath, err := DBPath()
	if err != nil {
		return err
	}

	if !*force {
		if _, err := os.Stat(dbPath); err == nil {
			return fmt.Errorf("config already exists at %s; pass --force to overwrite", dbPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", dbPath, err)
		}
	} else {
		// Remove existing DB and any WAL/SHM siblings.
		for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove %s: %w", p, err)
			}
		}
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	token, err := generateToken()
	if err != nil {
		return err
	}
	if err := st.SetMeta(ctx, store.MetaToken, token); err != nil {
		return err
	}
	if err := st.SetMeta(ctx, store.MetaListenAddr, defaultListenAddr); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "Initialized %s\n", dbPath)
	fmt.Fprintf(os.Stdout, "\nToken (copy into the extension's settings):\n  %s\n", token)
	fmt.Fprintf(os.Stdout, "\nListen address: %s\n", defaultListenAddr)
	fmt.Fprintf(os.Stdout, "\nNext: register a vault with\n  outcrop vault add <displayName> <path>\n")
	return nil
}

func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
