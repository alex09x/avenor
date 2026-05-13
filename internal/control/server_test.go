package control

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sdougbrown/avenor/internal/events"
)

func TestSocketLifecycleActiveListenerFails(t *testing.T) {
	state := NewState("run_1", "", 0)
	s1 := NewServer(state)
	path := testSocketPath(t)
	if err := s1.Start(path); err != nil {
		t.Fatalf("start first: %v", err)
	}
	defer s1.Stop()

	s2 := NewServer(state)
	if err := s2.Start(path); err == nil {
		t.Fatal("expected active listener failure")
	}
}

func TestOwnerRejectionForMutatingMethods(t *testing.T) {
	state := NewState("run_1", "", 0)
	s := NewServer(state)
	path := testSocketPath(t)
	if err := s.Start(path); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()

	c1 := mustDial(t, path)
	defer c1.Close()
	c2 := mustDial(t, path)
	defer c2.Close()

	_ = writeReq(t, c1, Request{JSONRPC: "2.0", ID: 1, Method: "cancel"})
	r1 := readResp(t, c1)
	if r1.Error != nil {
		t.Fatalf("owner cancel failed: %+v", r1.Error)
	}
	_ = writeReq(t, c2, Request{JSONRPC: "2.0", ID: 2, Method: "cancel"})
	r2 := readResp(t, c2)
	if r2.Error == nil || r2.Error.Code != -32010 {
		t.Fatalf("expected permission denied, got %+v", r2)
	}
}

func TestSubscriberBackpressureLaggedEvent(t *testing.T) {
	state := NewState("run_1", "", 0)
	s := NewServer(state)
	client, srv := net.Pipe()
	defer client.Close()
	defer srv.Close()
	cs := &connState{server: s, conn: srv}
	sub := &subscriber{conn: cs, ch: make(chan events.Event, 1)}
	go sub.loop()

	for i := 0; i < 5; i++ {
		sub.enqueue(events.Event{Event: "e", Fields: map[string]any{"i": i}})
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	scanner := bufio.NewScanner(client)
	foundLag := false
	for scanner.Scan() {
		var n Notification
		if err := json.Unmarshal(scanner.Bytes(), &n); err != nil {
			continue
		}
		if n.Method == "event" {
			m, _ := n.Params.(map[string]any)
			if ev, _ := m["event"].(string); ev == "subscriber.lagged" {
				foundLag = true
				break
			}
		}
	}
	if !foundLag {
		t.Fatal("expected subscriber.lagged notification")
	}
}

func TestSubscribeAndStatus(t *testing.T) {
	state := NewState("run_1", "label", 2)
	s := NewServer(state)
	path := testSocketPath(t)
	if err := s.Start(path); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()

	c := mustDial(t, path)
	defer c.Close()
	_ = writeReq(t, c, Request{JSONRPC: "2.0", ID: 1, Method: "subscribe"})
	_ = readResp(t, c)
	_ = writeReq(t, c, Request{JSONRPC: "2.0", ID: 2, Method: "status"})
	r := readResp(t, c)
	if r.Error != nil {
		t.Fatalf("status error: %+v", r.Error)
	}
}

func mustDial(t *testing.T, path string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := net.Dial("unix", path)
		if err == nil {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeReq(t *testing.T, c net.Conn, req Request) error {
	t.Helper()
	b, _ := json.Marshal(req)
	b = append(b, '\n')
	_, err := c.Write(b)
	return err
}

func readResp(t *testing.T, c net.Conn) Response {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	scanner := bufio.NewScanner(c)
	if !scanner.Scan() {
		t.Fatal("no response")
	}
	var r Response
	if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return r
}

func TestHTTPDebugStatusAndCancel(t *testing.T) {
	state := NewState("run_1", "", 0)
	s := NewServer(state)
	h := NewHTTPDebugServer("127.0.0.1:0", s)
	if err := h.Start(); err != nil {
		t.Fatalf("http start: %v", err)
	}
	defer h.Stop(context.Background())
}

func testSocketPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.TempDir(), "avc-"+time.Now().Format("150405.000000")+".sock")
}
