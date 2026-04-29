package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/gdey/outcrop/server"
)

// ipcClient is a thin http.Client over the local-IPC Unix socket. RFD 0014
// §2 — the IPC transport carries the privileged route surface the network
// listener does not expose. The auth boundary on this transport is
// filesystem permissions on the socket, not a bearer token, so requests
// don't carry an Authorization header.
type ipcClient struct {
	socket string
	http   *http.Client
}

// newIPCClient constructs an IPC client pinned to the local outcrop's
// socket path. Returns an error if the socket path can't be resolved
// (which is unrelated to whether anything is listening there).
func newIPCClient() (*ipcClient, error) {
	path, err := server.IPCSocketPath()
	if err != nil {
		return nil, err
	}
	return &ipcClient{
		socket: path,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", path)
				},
			},
			Timeout: 5 * time.Second,
		},
	}, nil
}

// errIPCDown is returned by ipcClient methods when nothing is listening on
// the socket. Distinct from arbitrary HTTP errors so callers can render a
// nicer "server is not running" message.
var errIPCDown = errors.New("outcrop server is not running (IPC socket unreachable)")

// getJSON issues a GET against the IPC socket at the given path and decodes
// the response body into out. Connect failures are normalised to errIPCDown.
func (c *ipcClient) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://outcrop"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// Distinguish "nothing listening" from other errors so callers can
		// decide whether to surface a friendlier "server is down" message.
		var nerr *net.OpError
		if errors.As(err, &nerr) {
			return errIPCDown
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
