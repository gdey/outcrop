package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// statusResponse mirrors server.serverStatus. We define it here rather than
// importing the unexported server type — the wire shape is the contract.
type statusResponse struct {
	RunningSince string `json:"runningSince"`
	UptimeMS     int64  `json:"uptimeMs"`
	ListenAddr   string `json:"listenAddr"`
	IPCSocket    string `json:"ipcSocket,omitempty"`
}

// CmdStatus implements `outcrop status` — a liveness probe that connects to
// the server's IPC socket. RFD 0014 §2a: socket-present-and-accepting is a
// more reliable signal than probing 127.0.0.1:7878, because the port is
// configurable and the IPC socket path is derivable from the same XDG /
// macOS-Library logic as the DB path (no config-read required).
//
// Exit codes:
//
//	0 — server is up and responding
//	1 — server is not running, or IPC is unreachable
//	2 — internal error (couldn't resolve socket path, malformed response)
func CmdStatus(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("status: unexpected arguments: %v", args)
	}

	c, err := newIPCClient()
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var s statusResponse
	if err := c.getJSON(ctx, "/server/status", &s); err != nil {
		if errors.Is(err, errIPCDown) {
			fmt.Fprintln(os.Stdout, "outcrop server: not running")
			os.Exit(1)
		}
		return fmt.Errorf("status: %w", err)
	}

	uptime := time.Duration(s.UptimeMS) * time.Millisecond
	fmt.Printf("outcrop server: running\n")
	fmt.Printf("  since:  %s\n", s.RunningSince)
	fmt.Printf("  uptime: %s\n", uptime.Round(time.Second))
	fmt.Printf("  http:   %s\n", s.ListenAddr)
	if s.IPCSocket != "" {
		fmt.Printf("  ipc:    %s\n", s.IPCSocket)
	}
	return nil
}
