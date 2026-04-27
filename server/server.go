// Package server wires the HTTP routes, middleware, and handlers for the
// outcrop local server.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gdey/outcrop/agent"
	"github.com/gdey/outcrop/store"
)

// Server holds the configuration and dependencies for the running HTTP
// server. Instantiate with New and run with Serve.
type Server struct {
	store  *store.Store
	log    *slog.Logger
	token  string
	addr   string
	scorer agent.Scorer
}

// New constructs a Server. The token must be non-empty; the address must
// resolve to a loopback IP. The default Scorer is history-based per RFD 0003;
// LLM-augmented scorers (RFD 0005) wrap it.
func New(st *store.Store, log *slog.Logger, token, addr string) (*Server, error) {
	if token == "" {
		return nil, fmt.Errorf("token is empty")
	}
	if err := validateLoopback(addr); err != nil {
		return nil, err
	}
	return &Server{
		store:  st,
		log:    log,
		token:  token,
		addr:   addr,
		scorer: agent.HistoryScorer{History: st, Log: log},
	}, nil
}

// Serve runs the HTTP server until ctx is cancelled or the listener fails.
func (s *Server) Serve(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", http.HandlerFunc(s.handleHealth))
	mux.Handle("GET /vaults", http.HandlerFunc(s.handleListVaults))
	mux.Handle("POST /clip", http.HandlerFunc(s.handleClip))

	// All /* routes get the OPTIONS preflight via corsMiddleware. The middleware
	// chain runs outer-to-inner: recover -> log -> cors -> auth -> handler.
	wrapped := chain(
		recoverMiddleware(s.log),
		logMiddleware(s.log),
		corsMiddleware(),
		authMiddleware(s.token),
	)(mux)

	// Catch-all OPTIONS route so preflight to any path is handled even before
	// auth runs. corsMiddleware short-circuits OPTIONS, so the inner mux
	// doesn't need to know about them.
	root := http.NewServeMux()
	root.Handle("OPTIONS /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// corsMiddleware in the chain already responded; if we get here the
		// origin wasn't a moz-extension and we should reject.
		w.WriteHeader(http.StatusForbidden)
	}))
	root.Handle("/", wrapped)

	srv := &http.Server{
		Addr:              s.addr,
		Handler:           wrapped,
		ReadHeaderTimeout: 10 * time.Second,
		// No global write timeout — POST /clip may carry a sizable image.
	}

	listenErrCh := make(chan error, 1)
	go func() {
		s.log.Info("listening", "addr", s.addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			listenErrCh <- err
			return
		}
		listenErrCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	case err := <-listenErrCh:
		return err
	}
}

// validateLoopback returns an error unless host:port resolves to a loopback
// address. RFD 0001 requires that we never bind to non-loopback interfaces.
func validateLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("parse listen address %q: %w", addr, err)
	}
	if host == "" {
		return fmt.Errorf("listen address %q has no host", addr)
	}
	// Treat an IP literal as the source of truth; resolve hostnames cautiously.
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsLoopback() {
			return fmt.Errorf("listen address %q is not loopback", addr)
		}
		return nil
	}
	// Permit "localhost" but no other names — DNS-resolution behaviour can
	// drift, and the RFD is explicit about loopback only.
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	return fmt.Errorf("listen address host %q is not a loopback IP or 'localhost'", host)
}
