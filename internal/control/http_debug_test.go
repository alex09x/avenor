package control

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sdougbrown/avenor/internal/events"
)

// --- helpers -----------------------------------------------------------------

// startDebugServer creates an HTTPDebugServer bound to a random loopback port
// and returns it along with the address and auth token.  The server is stopped
// automatically via t.Cleanup.
func startDebugServer(t *testing.T, ctrl *ControlServer, adapter StableAdapter) (*HTTPDebugServer, string, string) {
	t.Helper()
	h, err := NewHTTPDebugServer("127.0.0.1:0", ctrl)
	if err != nil {
		t.Fatalf("NewHTTPDebugServer: %v", err)
	}
	if adapter != nil {
		h.SetStableAdapter(adapter)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	go func() { _ = h.server.Serve(ln) }()
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	return h, addr, h.token
}

func authedGet(t *testing.T, client *http.Client, url, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, http.NoBody)
	req.Header.Set("X-Avenor-Token", token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func authedPost(t *testing.T, client *http.Client, url, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, http.NoBody)
	req.Header.Set("X-Avenor-Token", token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

// --- mock StableAdapter ------------------------------------------------------

type mockAdapter struct {
	runtimes    map[string]any
	cancelCalls []string
	cancelErr   error
}

func newMockAdapter(runtimes map[string]any) *mockAdapter {
	return &mockAdapter{runtimes: runtimes}
}

func (m *mockAdapter) HTTPRuntimeStatus(runtimeID string) (any, bool) {
	snap, ok := m.runtimes[runtimeID]
	return snap, ok
}

func (m *mockAdapter) HTTPCancelRuntime(runtimeID string) error {
	if m.cancelErr != nil {
		return m.cancelErr
	}
	if _, ok := m.runtimes[runtimeID]; !ok {
		return fmt.Errorf("runtime %q not found", runtimeID)
	}
	m.cancelCalls = append(m.cancelCalls, runtimeID)
	return nil
}

// --- resolveDebugAddr tests --------------------------------------------------

func TestResolveDebugAddr(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		wantErr bool
		wantOut string // empty means "any non-empty"
	}{
		// Bare :port rewrites to 127.0.0.1.
		{
			name:    "bare port",
			addr:    ":8080",
			wantOut: "127.0.0.1:8080",
		},
		// Numeric loopback fast paths.
		{
			name:    "127.0.0.1 explicit",
			addr:    "127.0.0.1:8080",
			wantOut: "127.0.0.1:8080",
		},
		{
			name:    "::1 explicit",
			addr:    "[::1]:8080",
			wantOut: "[::1]:8080",
		},
		// Non-loopback numeric IP must be rejected.
		{
			name:    "non-loopback IP",
			addr:    "192.168.1.1:8080",
			wantErr: true,
		},
		{
			name:    "0.0.0.0 bind-all",
			addr:    "0.0.0.0:8080",
			wantErr: true,
		},
		// Malformed address.
		{
			name:    "missing port",
			addr:    "127.0.0.1",
			wantErr: true,
		},
		{
			name:    "garbage",
			addr:    "not:an:address:8080",
			wantErr: true,
		},
		// Hostname "localhost" — resolved by the system; must be loopback.
		// We rely on the test environment having a sane /etc/hosts.
		{
			name: "localhost hostname",
			addr: "localhost:8080",
			// Not asserting exact output because localhost could map to ::1 on
			// some systems; we just want no error.
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveDebugAddr(tc.addr)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveDebugAddr(%q) = %q, nil; want error", tc.addr, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveDebugAddr(%q): %v", tc.addr, err)
			}
			if tc.wantOut != "" && got != tc.wantOut {
				t.Fatalf("resolveDebugAddr(%q) = %q, want %q", tc.addr, got, tc.wantOut)
			}
			if got == "" {
				t.Fatalf("resolveDebugAddr(%q) returned empty string", tc.addr)
			}
		})
	}
}

// --- /status/<runtime_id> tests ----------------------------------------------

func TestHTTPStatusRuntimeStableMode(t *testing.T) {
	state := NewState("run_stable", "", 0)
	ctrl := NewServer(state)
	adapter := newMockAdapter(map[string]any{
		"rt_1": map[string]any{
			"runtime_id": "rt_1",
			"status":     "running",
			"session_id": "ses_abc",
		},
	})
	_, addr, token := startDebugServer(t, ctrl, adapter)
	client := &http.Client{Timeout: 2 * time.Second}

	// Existing runtime returns 200 + snapshot.
	resp := authedGet(t, client, "http://"+addr+"/status/rt_1", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /status/rt_1: %d, want 200", resp.StatusCode)
	}
	var snap map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v, _ := snap["runtime_id"].(string); v != "rt_1" {
		t.Errorf("runtime_id = %q, want rt_1", v)
	}
	if v, _ := snap["status"].(string); v != "running" {
		t.Errorf("status = %q, want running", v)
	}
}

func TestHTTPStatusRuntimeNotFound(t *testing.T) {
	state := NewState("run_stable", "", 0)
	ctrl := NewServer(state)
	adapter := newMockAdapter(map[string]any{})
	_, addr, token := startDebugServer(t, ctrl, adapter)
	client := &http.Client{Timeout: 2 * time.Second}

	resp := authedGet(t, client, "http://"+addr+"/status/rt_unknown", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /status/rt_unknown: %d, want 404", resp.StatusCode)
	}
}

func TestHTTPStatusRuntimeCLIMode(t *testing.T) {
	// No stable adapter — CLI mode. /status/<id> must return 404.
	state := NewState("run_cli", "", 0)
	ctrl := NewServer(state)
	_, addr, token := startDebugServer(t, ctrl, nil)
	client := &http.Client{Timeout: 2 * time.Second}

	resp := authedGet(t, client, "http://"+addr+"/status/rt_1", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /status/rt_1 (CLI mode): %d, want 404", resp.StatusCode)
	}
}

// --- /events?runtime_id= filter tests ----------------------------------------

func TestHTTPEventsRuntimeIDFilter(t *testing.T) {
	state := NewState("run_stable", "", 0)
	ctrl := NewServer(state)
	_, addr, token := startDebugServer(t, ctrl, nil)

	// Connect to SSE stream with runtime_id filter.
	// Use no client-level timeout — SSE is a long-lived streaming response.
	// Cancellation is handled via the request context.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	req, _ := http.NewRequest(http.MethodGet,
		"http://"+addr+"/events?runtime_id=rt_1&token="+token, http.NoBody)
	req = req.WithContext(ctx)
	sseClient := &http.Client{} // no Timeout — context controls cancellation
	resp, err := sseClient.Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /events: %d, want 200", resp.StatusCode)
	}

	// Publish one event for rt_1 and one for rt_2.
	go func() {
		time.Sleep(20 * time.Millisecond) // let the SSE loop start
		ctrl.PublishEvent(events.Event{
			Event: "agent.status",
			Fields: map[string]any{
				"runtime_id": "rt_2",
				"phase":      "running",
			},
		})
		ctrl.PublishEvent(events.Event{
			Event: "agent.status",
			Fields: map[string]any{
				"runtime_id": "rt_1",
				"phase":      "idle",
			},
		})
	}()

	// Read SSE data lines in a goroutine; collect runtime_id values seen.
	scanner := bufio.NewScanner(resp.Body)
	lines := make(chan string, 32)
	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	var receivedRuntimeIDs []string
loop:
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				break loop
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
				continue
			}
			if rtID, _ := payload["runtime_id"].(string); rtID != "" {
				receivedRuntimeIDs = append(receivedRuntimeIDs, rtID)
				if rtID == "rt_1" {
					// Received the expected event; cancel the stream.
					cancel()
					break loop
				}
			}
		case <-ctx.Done():
			break loop
		}
	}

	for _, id := range receivedRuntimeIDs {
		if id == "rt_2" {
			t.Errorf("received event for rt_2 but filter was rt_1; ids received: %v", receivedRuntimeIDs)
			break
		}
	}
	if len(receivedRuntimeIDs) == 0 {
		t.Error("no runtime_id events received for rt_1")
	}
}

// --- /cancel stable-mode tests -----------------------------------------------

func TestHTTPCancelStableModeRequiresRuntimeID(t *testing.T) {
	state := NewState("run_stable", "", 0)
	ctrl := NewServer(state)
	adapter := newMockAdapter(map[string]any{
		"rt_1": map[string]any{"runtime_id": "rt_1"},
	})
	_, addr, token := startDebugServer(t, ctrl, adapter)
	client := &http.Client{Timeout: 2 * time.Second}

	// POST /cancel without runtime_id in stable mode must return 400.
	resp := authedPost(t, client, "http://"+addr+"/cancel", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /cancel (stable, no id): %d, want 400", resp.StatusCode)
	}
}

func TestHTTPCancelRuntimeStableMode(t *testing.T) {
	state := NewState("run_stable", "", 0)
	ctrl := NewServer(state)
	adapter := newMockAdapter(map[string]any{
		"rt_1": map[string]any{"runtime_id": "rt_1"},
	})
	_, addr, token := startDebugServer(t, ctrl, adapter)
	client := &http.Client{Timeout: 2 * time.Second}

	// POST /cancel/rt_1 — must return 200 and call adapter.
	resp := authedPost(t, client, "http://"+addr+"/cancel/rt_1", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cancel/rt_1: %d, want 200", resp.StatusCode)
	}
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v, _ := result["ok"].(bool); !v {
		t.Errorf("ok = %v, want true", result["ok"])
	}
	if len(adapter.cancelCalls) != 1 || adapter.cancelCalls[0] != "rt_1" {
		t.Errorf("cancelCalls = %v, want [rt_1]", adapter.cancelCalls)
	}
}

func TestHTTPCancelRuntimeNotFound(t *testing.T) {
	state := NewState("run_stable", "", 0)
	ctrl := NewServer(state)
	adapter := newMockAdapter(map[string]any{})
	_, addr, token := startDebugServer(t, ctrl, adapter)
	client := &http.Client{Timeout: 2 * time.Second}

	resp := authedPost(t, client, "http://"+addr+"/cancel/rt_unknown", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /cancel/rt_unknown: %d, want 404", resp.StatusCode)
	}
}

func TestHTTPCancelRuntimeCLIMode(t *testing.T) {
	// No stable adapter — CLI mode. /cancel/<id> must return 404.
	state := NewState("run_cli", "", 0)
	ctrl := NewServer(state)
	_, addr, token := startDebugServer(t, ctrl, nil)
	client := &http.Client{Timeout: 2 * time.Second}

	resp := authedPost(t, client, "http://"+addr+"/cancel/rt_1", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /cancel/rt_1 (CLI mode): %d, want 404", resp.StatusCode)
	}
}

func TestHTTPCancelCLIModeFiresGlobalCancel(t *testing.T) {
	// CLI mode: POST /cancel (no id) should fire cancelFn.
	state := NewState("run_cli", "", 0)
	ctrl := NewServer(state)
	cancelled := false
	ctrl.SetCancelFunc(func() { cancelled = true })
	_, addr, token := startDebugServer(t, ctrl, nil)
	client := &http.Client{Timeout: 2 * time.Second}

	resp := authedPost(t, client, "http://"+addr+"/cancel", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cancel (CLI mode): %d, want 200", resp.StatusCode)
	}
	if !cancelled {
		t.Error("cancelFn was not called")
	}
}
