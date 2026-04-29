package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// IPCSocketPath returns the path to outcrop's local-IPC socket. The actual
// per-OS resolution is in ipc_<goos>.go (build-tag-gated, mirroring the
// install_service_<goos>.go pattern in cli/).
//
// RFD 0014 §2: the IPC transport carries the privileged route surface that
// the network HTTP listener does not expose. Filesystem permissions on this
// socket (mode 0600) are the security boundary, not bearer tokens.
func IPCSocketPath() (string, error) {
	return socketPath()
}

// listenIPC opens a Unix-socket listener at IPCSocketPath() with the
// containing directory and the socket file restricted to the calling user
// (0700 / 0600 respectively). A stale socket file from a previous (crashed)
// run is cleaned up first; if a *live* outcrop is already listening there,
// the call refuses rather than clobber.
func listenIPC() (net.Listener, string, error) {
	path, err := IPCSocketPath()
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, "", fmt.Errorf("create IPC dir: %w", err)
	}

	if _, statErr := os.Stat(path); statErr == nil {
		// Something exists. Probe to see if it's a live listener; only clean up
		// if nothing's home.
		if c, derr := net.DialTimeout("unix", path, 100*time.Millisecond); derr == nil {
			c.Close()
			return nil, "", fmt.Errorf("IPC socket %s is in use by another outcrop process", path)
		}
		if rmErr := os.Remove(path); rmErr != nil {
			return nil, "", fmt.Errorf("remove stale IPC socket: %w", rmErr)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, "", fmt.Errorf("stat IPC socket: %w", statErr)
	}

	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, "", fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, "", fmt.Errorf("chmod IPC socket: %w", err)
	}
	return l, path, nil
}

// ipcMux returns the http.Handler for the IPC transport. It is a strict
// superset of the network HTTP mux: every route the public HTTP listener
// exposes is also reachable here so the tray (and other local clients) use
// one transport for everything, plus the privileged routes that don't exist
// on the network at all.
//
// The IPC handler does NOT apply the bearer-token auth middleware: the
// security boundary on this transport is filesystem permissions on the
// socket (mode 0600), per RFD 0014 §2. logging and panic-recovery
// middleware still apply for symmetry with the HTTP path.
func (s *Server) ipcMux() http.Handler {
	mux := http.NewServeMux()

	// Public/extension routes — also reachable here so the tray uses one
	// transport for everything.
	mux.Handle("GET /healthz", http.HandlerFunc(s.handleHealth))
	mux.Handle("GET /vaults", http.HandlerFunc(s.handleListVaults))
	mux.Handle("POST /clip", http.HandlerFunc(s.handleClip))

	// Privileged routes — IPC only, never on the network HTTP listener.
	mux.Handle("GET /server/status", http.HandlerFunc(s.handleServerStatus))
	mux.Handle("POST /vaults", http.HandlerFunc(s.handleCreateVault))
	mux.Handle("PUT /vaults/{key}", http.HandlerFunc(s.handleUpdateVault))
	mux.Handle("DELETE /vaults/{key}", http.HandlerFunc(s.handleDeleteVault))
	mux.Handle("PUT /vaults/{key}/default", http.HandlerFunc(s.handleSetDefaultVault))
	mux.Handle("GET /config/token", http.HandlerFunc(s.handleGetToken))

	return chain(
		recoverMiddleware(s.log),
		logMiddleware(s.log),
	)(mux)
}
