package cli

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/gdey/outcrop/server"
	"github.com/gdey/outcrop/store"
)

// CmdServe implements `outcrop serve`.
func CmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addrOverride := fs.String("addr", "", "override listen address from config")
	logFormat := fs.String("log-format", "text", "log format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}

	log := newLogger(*logFormat)

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
	if token == "" {
		return fmt.Errorf("no token configured; run `outcrop init` first")
	}

	addr := *addrOverride
	if addr == "" {
		addr, err = st.Meta(ctx, store.MetaListenAddr)
		if err != nil {
			return err
		}
		if addr == "" {
			addr = defaultListenAddr
		}
	}

	srv, err := server.New(st, log, token, addr)
	if err != nil {
		return err
	}

	// Cancel on SIGINT/SIGTERM for a clean shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return srv.Serve(ctx)
}

func newLogger(format string) *slog.Logger {
	var h slog.Handler
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	switch format {
	case "json":
		h = slog.NewJSONHandler(os.Stderr, opts)
	default:
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(h)
}
