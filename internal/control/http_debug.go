package control

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
)

type HTTPDebugServer struct {
	control *ControlServer
	server  *http.Server
	token   string
}

func NewHTTPDebugServer(addr string, control *ControlServer) (*HTTPDebugServer, error) {
	resolved, err := resolveDebugAddr(addr)
	if err != nil {
		return nil, err
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		panic("avenor: failed to generate debug token: " + err.Error())
	}
	token := hex.EncodeToString(tokenBytes)
	h := &HTTPDebugServer{control: control, token: token}
	mux := http.NewServeMux()
	mux.HandleFunc("/status", h.handleStatus)
	mux.HandleFunc("/events", h.handleEvents)
	mux.HandleFunc("/cancel", h.handleCancel)
	mux.HandleFunc("/answer-permission", h.handleAnswerPermission)
	h.server = &http.Server{Addr: resolved, Handler: mux}
	// Only print the token when stderr is an interactive terminal.  When stderr
	// is redirected to a file, pipe, or captured by a service manager (journald,
	// systemd, etc.) the token would persist in logs for the lifetime of the
	// process and could be read by anything with access to those logs.  Skipping
	// the print in non-TTY environments is intentional; operators that need the
	// token in non-interactive setups should inject it via another channel.
	if stat, serr := os.Stderr.Stat(); serr == nil && (stat.Mode()&os.ModeCharDevice) != 0 {
		fmt.Fprintf(os.Stderr, "avenor: http debug token: %s\n", token)
	}
	return h, nil
}

// resolveDebugAddr normalises addr and rejects anything that would bind on a
// non-loopback interface.  Accepted forms: ":8080", "localhost:8080",
// "127.0.0.1:8080", "[::1]:8080".  Bare ":port" is rewritten to
// "127.0.0.1:port".
func resolveDebugAddr(addr string) (string, error) {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("avenor: --http-debug: invalid address %q: %w", addr, err)
	}
	if port == "" {
		return "", fmt.Errorf("avenor: --http-debug: address %q must include a port", addr)
	}
	switch host {
	case "localhost", "127.0.0.1", "::1":
		// allowed
	default:
		return "", fmt.Errorf("avenor: --http-debug: address %q must bind to a loopback interface (localhost, 127.0.0.1, or ::1)", addr)
	}
	return net.JoinHostPort(host, port), nil
}

// checkAuthHeader verifies the X-Avenor-Token request header only.
// Use this for all endpoints except /events (which must support EventSource).
func (h *HTTPDebugServer) checkAuthHeader(r *http.Request) bool {
	if h.token == "" {
		return false
	}
	hdr := r.Header.Get("X-Avenor-Token")
	return hdr != "" && subtle.ConstantTimeCompare([]byte(hdr), []byte(h.token)) == 1
}

// checkAuthEventSource verifies the X-Avenor-Token header or the ?token=
// query parameter.  The query-param fallback exists solely for EventSource
// clients (browser APIs that cannot set custom headers).  Do not use this on
// POST endpoints — query params leak into access logs, Referer headers, and
// shell history.
func (h *HTTPDebugServer) checkAuthEventSource(r *http.Request) bool {
	if h.checkAuthHeader(r) {
		return true
	}
	if h.token == "" {
		return false
	}
	q := r.URL.Query().Get("token")
	return q != "" && subtle.ConstantTimeCompare([]byte(q), []byte(h.token)) == 1
}

func (h *HTTPDebugServer) Start() error {
	ln, err := net.Listen("tcp", h.server.Addr)
	if err != nil {
		return err
	}
	go func() { _ = h.server.Serve(ln) }()
	return nil
}

func (h *HTTPDebugServer) Stop(ctx context.Context) error {
	return h.server.Shutdown(ctx)
}

func (h *HTTPDebugServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.checkAuthHeader(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !isLocalhostRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	writeJSON(w, h.control.state.Snapshot())
}

func (h *HTTPDebugServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.checkAuthEventSource(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !isLocalhostRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch := h.control.SubscribeEvents(r.Context())
	for ev := range ch {
		data, _ := json.Marshal(eventToMap(ev))
		_, _ = fmt.Fprintf(w, "event: event\ndata: %s\n\n", data)
		flusher.Flush()
	}
}

func (h *HTTPDebugServer) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.checkAuthHeader(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !isLocalhostRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.control.cancelFn != nil {
		h.control.cancelFn()
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (h *HTTPDebugServer) handleAnswerPermission(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.checkAuthHeader(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !isLocalhostRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var p PermissionAnswer
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	h.control.pendingMu.Lock()
	pendingReq := h.control.pendingRequest
	pending := h.control.pendingAnswer
	h.control.pendingMu.Unlock()
	if pending == nil || pendingReq != p.RequestID {
		http.Error(w, "no pending permission", http.StatusConflict)
		return
	}
	select {
	case pending <- p:
	default:
	}
	writeJSON(w, map[string]any{"accepted": true})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// isLocalhostRequest returns true only when the TCP connection itself
// originated from a loopback address.  The Host header is intentionally
// ignored because it is caller-controlled and provides no security guarantee.
func isLocalhostRequest(r *http.Request) bool {
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}
	return remoteHost == "127.0.0.1" || remoteHost == "::1"
}
