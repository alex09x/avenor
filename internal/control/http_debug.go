package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
)

type HTTPDebugServer struct {
	control *ControlServer
	server  *http.Server
}

func NewHTTPDebugServer(addr string, control *ControlServer) *HTTPDebugServer {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	h := &HTTPDebugServer{control: control}
	mux := http.NewServeMux()
	mux.HandleFunc("/status", h.handleStatus)
	mux.HandleFunc("/events", h.handleEvents)
	mux.HandleFunc("/cancel", h.handleCancel)
	mux.HandleFunc("/answer-permission", h.handleAnswerPermission)
	h.server = &http.Server{Addr: addr, Handler: mux}
	return h
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
	if !isLocalhost(r.Host) {
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
	if !isLocalhost(r.Host) {
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
	if !isLocalhost(r.Host) {
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
	if !isLocalhost(r.Host) {
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

func isLocalhost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}
