package client

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func startTestServer(t *testing.T) (string, func()) {
	t.Helper()
	path := filepath.Join(os.TempDir(), "avc-client-"+time.Now().Format("150405.000000")+".sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var mu sync.Mutex
	var seq int

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				n, _ := c.Read(buf)
				var req Request
				_ = json.Unmarshal(buf[:n], &req)

				var resp Response
				switch req.Method {
				case "status":
					resp = Response{JSONRPC: "2.0", ID: req.ID}
					snap := map[string]any{"session_id": "ses_test", "phase": "working"}
					resp.Result, _ = json.Marshal(snap)
				case "subscribe":
					resp = Response{JSONRPC: "2.0", ID: req.ID}
					resp.Result, _ = json.Marshal(map[string]any{"subscribed": true})
				case "list":
					resp = Response{JSONRPC: "2.0", ID: req.ID}
					list := []map[string]any{{"runtime_id": "rt_1", "status": "running"}}
					resp.Result, _ = json.Marshal(list)
				case "cancel":
					resp = Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"ok":true}`)}
				case "prompt":
					resp = Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"accepted":true}`)}
				case "answer_permission":
					resp = Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"accepted":true}`)}
				case "shutdown":
					resp = Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"shutting_down":true}`)}
				default:
					resp = Response{JSONRPC: "2.0", ID: req.ID, Error: &RespError{Code: -32601, Message: "method not found"}}
				}
				mu.Lock()
				seq++
				mu.Unlock()
				data, _ := json.Marshal(resp)
				data = append(data, '\n')
				c.Write(data)

				// After subscribe, send a test event notification.
				if req.Method == "subscribe" {
					ev := map[string]any{"event": "agent.status", "phase": "thinking"}
					notif := Notification{JSONRPC: "2.0", Method: "event"}
					notif.Params, _ = json.Marshal(ev)
					data, _ = json.Marshal(notif)
					data = append(data, '\n')
					c.Write(data)
				}
			}(conn)
		}
	}()

	return path, func() { ln.Close(); os.Remove(path) }
}

func TestClientStatus(t *testing.T) {
	path, cleanup := startTestServer(t)
	defer cleanup()

	c, err := Dial(path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	snap, err := c.Status("")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if snap["session_id"] != "ses_test" {
		t.Errorf("session_id = %v, want ses_test", snap["session_id"])
	}
}

func TestClientCancel(t *testing.T) {
	path, cleanup := startTestServer(t)
	defer cleanup()

	c, err := Dial(path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if err := c.Cancel(""); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

func TestClientPrompt(t *testing.T) {
	path, cleanup := startTestServer(t)
	defer cleanup()

	c, err := Dial(path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if err := c.Prompt("", "do something"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
}

func TestClientAnswerPermission(t *testing.T) {
	path, cleanup := startTestServer(t)
	defer cleanup()

	c, err := Dial(path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if err := c.AnswerPermission("", "req_1", "allow"); err != nil {
		t.Fatalf("answer_permission: %v", err)
	}
}

func TestClientList(t *testing.T) {
	path, cleanup := startTestServer(t)
	defer cleanup()

	c, err := Dial(path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	runtimes, err := c.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(runtimes) != 1 || runtimes[0]["runtime_id"] != "rt_1" {
		t.Errorf("runtimes = %v, want [rt_1]", runtimes)
	}
}

func TestClientShutdown(t *testing.T) {
	path, cleanup := startTestServer(t)
	defer cleanup()

	c, err := Dial(path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if err := c.Shutdown("graceful"); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestClientSubscribe(t *testing.T) {
	path, cleanup := startTestServer(t)
	defer cleanup()

	c, err := Dial(path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	// Must call subscribe before Events.
	if err := c.Call("subscribe", nil, nil); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ch := c.Events()
	select {
	case ev := <-ch:
		if ev.Event != "agent.status" {
			t.Errorf("event = %q, want agent.status", ev.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestClientStatusWithRuntimeID(t *testing.T) {
	path, cleanup := startTestServer(t)
	defer cleanup()

	c, err := Dial(path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	_, err = c.Status("rt_1")
	if err != nil {
		t.Fatalf("status with runtime_id: %v", err)
	}
}
