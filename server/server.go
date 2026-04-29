// Package server wires the HTTP routes, middleware, and handlers for the
// outcrop local server.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gdey/outcrop/agent"
	"github.com/gdey/outcrop/store"
)

// Server holds the configuration and dependencies for the running HTTP
// server. Instantiate with New and run with Serve.
type Server struct {
	store        *store.Store
	log          *slog.Logger
	token        string
	addr         string
	runningSince time.Time

	// buildScorer is the CLI-supplied factory called on each /vaults to
	// produce (scorer, fingerprint). The server caches the most recent
	// (fingerprint, scorer) pair and reuses the cached scorer when the
	// returned fingerprint matches. RFD 0012.
	buildScorer func(ctx context.Context) (agent.Scorer, string)

	cacheMu      sync.Mutex
	cachedFp     string
	cachedScorer agent.Scorer
}

// closer is satisfied by Scorers that hold expensive resources (e.g.
// KronkSuggester's loaded GGUF model) and need a graceful release when
// they're swapped out by a config change.
type closer interface {
	Close(ctx context.Context) error
}

// New constructs a Server. The token must be non-empty; the address must
// resolve to a loopback IP. buildScorer is the factory the server calls on
// each /vaults to derive the current Scorer (RFD 0012). If nil, a default
// history-based factory is used.
func New(
	st *store.Store,
	log *slog.Logger,
	token, addr string,
	buildScorer func(ctx context.Context) (agent.Scorer, string),
) (*Server, error) {
	if token == "" {
		return nil, fmt.Errorf("token is empty")
	}
	if err := validateLoopback(addr); err != nil {
		return nil, err
	}
	if buildScorer == nil {
		buildScorer = func(_ context.Context) (agent.Scorer, string) {
			return agent.HistoryScorer{History: st, Log: log}, "default-history"
		}
	}
	return &Server{
		store:        st,
		log:          log,
		token:        token,
		addr:         addr,
		runningSince: time.Now(),
		buildScorer:  buildScorer,
	}, nil
}

// currentScorer returns the cached Scorer when the buildScorer-supplied
// fingerprint matches the last seen one, or builds + caches a new one when
// it changed. Old Scorers that satisfy `closer` are released after a grace
// period so an in-flight /vaults using the previous Scorer doesn't get the
// rug pulled. RFD 0012.
func (s *Server) currentScorer(ctx context.Context) agent.Scorer {
	newScorer, fp := s.buildScorer(ctx)

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	if s.cachedScorer != nil && fp == s.cachedFp {
		return s.cachedScorer
	}

	if s.cachedScorer != nil {
		s.log.Info("agent config changed, rebuilding scorer",
			"old_fingerprint", s.cachedFp,
			"new_fingerprint", fp)
		s.scheduleClose(s.cachedScorer)
	} else {
		s.log.Info("agent scorer initialised", "fingerprint", fp)
	}
	s.cachedScorer = newScorer
	s.cachedFp = fp
	return newScorer
}

// scheduleClose runs Close on a Scorer in a goroutine after a short grace
// period so any in-flight handler still using it can complete. The agent's
// per-call timeout is on the order of 1 s; 5 s gives ample margin.
func (s *Server) scheduleClose(sc agent.Scorer) {
	c, ok := sc.(closer)
	if !ok {
		return
	}
	go func() {
		time.Sleep(scorerCloseGrace)
		if err := c.Close(context.Background()); err != nil {
			s.log.Warn("close superseded scorer", "err", err)
		}
	}()
}

// scorerCloseGrace is the delay between a config-driven scorer swap and
// closing the previous scorer. Generous on purpose — the cost of waiting
// is briefly holding the model in memory; the cost of closing too soon is
// an in-flight /vaults call hitting a closed model. Overridable for tests.
var scorerCloseGrace = 5 * time.Second

// Serve runs the HTTP server (network) and the IPC server (Unix socket)
// concurrently until ctx is cancelled or either listener fails.
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

	httpSrv := &http.Server{
		Addr:              s.addr,
		Handler:           wrapped,
		ReadHeaderTimeout: 10 * time.Second,
		// No global write timeout — POST /clip may carry a sizable image.
	}

	// IPC transport (RFD 0014 §2). Carries a strict superset of the network
	// routes plus the privileged surface that's not bound to any TCP port.
	// Failure to bring up IPC is non-fatal — the server still serves the
	// extension over HTTP — but is logged loudly so the tray's failure to
	// connect later is debuggable.
	ipcListener, ipcPath, ipcErr := listenIPC()
	if ipcErr != nil {
		s.log.Warn("IPC listener failed to start; tray and `outcrop status` won't work",
			"err", ipcErr)
	}
	var ipcSrv *http.Server
	if ipcListener != nil {
		ipcSrv = &http.Server{
			Handler:           s.ipcMux(),
			ReadHeaderTimeout: 10 * time.Second,
		}
	}

	listenErrCh := make(chan error, 2)
	go func() {
		s.log.Info("listening", "addr", s.addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			listenErrCh <- fmt.Errorf("http listener: %w", err)
			return
		}
		listenErrCh <- nil
	}()
	if ipcSrv != nil {
		go func() {
			s.log.Info("listening (IPC)", "socket", ipcPath)
			if err := ipcSrv.Serve(ipcListener); err != nil && err != http.ErrServerClosed {
				listenErrCh <- fmt.Errorf("ipc listener: %w", err)
				return
			}
			listenErrCh <- nil
		}()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var firstErr error
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			firstErr = fmt.Errorf("http shutdown: %w", err)
		}
		if ipcSrv != nil {
			if err := ipcSrv.Shutdown(shutdownCtx); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("ipc shutdown: %w", err)
			}
		}
		// net.Listen("unix") doesn't auto-remove the socket file. The next
		// startup detects it as stale and cleans up, but tidy-on-clean-exit
		// avoids that one log line and matches what the install-service
		// uninstall flow expects.
		if ipcPath != "" {
			_ = os.Remove(ipcPath)
		}
		return firstErr
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
