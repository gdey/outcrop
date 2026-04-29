package server

import (
	"net/http"
	"time"
)

// serverStatus is the JSON body returned by GET /server/status. RFD 0014 §2
// specifies this as the IPC-only liveness/info endpoint.
type serverStatus struct {
	RunningSince string `json:"runningSince"`           // RFC3339
	UptimeMS     int64  `json:"uptimeMs"`               // milliseconds
	ListenAddr   string `json:"listenAddr"`             // network HTTP listen addr
	IPCSocket    string `json:"ipcSocket,omitempty"`    // path to the IPC socket
}

func (s *Server) handleServerStatus(w http.ResponseWriter, r *http.Request) {
	sockPath, _ := IPCSocketPath() // best-effort; status response still useful without it

	resp := serverStatus{
		RunningSince: s.runningSince.UTC().Format(time.RFC3339),
		UptimeMS:     time.Since(s.runningSince).Milliseconds(),
		ListenAddr:   s.addr,
		IPCSocket:    sockPath,
	}
	writeJSON(w, resp)
}
