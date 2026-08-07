package codexappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alex09x/avenor/agyclient/events"
)

// fakeClient creates a client backed by in-memory pipes.
// Returns the client, a writer to simulate server→client messages (stdout),
// and a reader to inspect client→server writes (stdin).
func fakeClient() (*client, *io.PipeWriter, *io.PipeReader) {
	clientInR, clientInW := io.Pipe()   // stdin: client writes, we read
	clientOutR, clientOutW := io.Pipe() // stdout: we write, client reads
	errR, errW := io.Pipe()             // stderr: subprocess writes, client drains
	_ = errW.Close()                    // no real stderr; close writer so drain exits

	c := newClient(nil, clientInW, clientOutR, errR)
	return c, clientOutW, clientInR
}

func writeLine(w io.Writer, v any) {
	b, _ := json.Marshal(v)
	b = append(b, '\n')
	_, _ = w.Write(b)
}

func readLine(r io.Reader) (rpcMessage, error) {
	var msg rpcMessage
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return msg, err
		}
		return msg, io.EOF
	}
	if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
		return msg, err
	}
	return msg, nil
}

func TestClientRequestResponse(t *testing.T) {
	c, wOut, rIn := fakeClient()
	defer c.Close()

	done := make(chan json.RawMessage, 1)
	go func() {
		msg, err := readLine(rIn)
		if err != nil {
			t.Logf("read request: %v", err)
			return
		}
		// Respond with the same ID.
		writeLine(wOut, map[string]any{
			"id":     msg.ID,
			"result": map[string]any{"thread": map[string]any{"id": "th_abc"}},
		})
		done <- msg.Result
	}()

	result, err := c.request(context.Background(), "thread/start", threadStartParams{CWD: "/tmp"})
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	var parsed struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if parsed.Thread.ID != "th_abc" {
		t.Errorf("thread ID = %q, want th_abc", parsed.Thread.ID)
	}
}

func TestClientRequestError(t *testing.T) {
	c, wOut, rIn := fakeClient()
	defer c.Close()

	go func() {
		msg, err := readLine(rIn)
		if err != nil {
			t.Logf("read request: %v", err)
			return
		}
		writeLine(wOut, map[string]any{
			"id": msg.ID,
			"error": map[string]any{
				"code":    -32000,
				"message": "something went wrong",
			},
		})
	}()

	_, err := c.request(context.Background(), "thread/start", nil)
	if err == nil {
		t.Fatal("expected error from rpc error response")
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("error = %v, want something went wrong", err)
	}
}

func TestClientNotificationRouting(t *testing.T) {
	c, wOut, _ := fakeClient()
	defer c.Close()

	sub := make(chan events.Event, 4)
	c.subscribe("th_x", sub)

	go func() {
		writeLine(wOut, map[string]any{
			"method": "turn/completed",
			"params": map[string]any{
				"threadId": "th_x",
				"turn": map[string]any{
					"id":     "turn_1",
					"status": "completed",
				},
			},
		})
	}()

	select {
	case ev := <-sub:
		if ev.Event != "session.end" {
			t.Errorf("event = %q, want session.end", ev.Event)
		}
		if sid, _ := ev.Fields["stop_reason"].(string); sid != "end_turn" {
			t.Errorf("stop_reason = %q, want end_turn", sid)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for notification event")
	}
}

func TestClientTurnWaiter(t *testing.T) {
	c, wOut, _ := fakeClient()
	defer c.Close()

	go func() {
		writeLine(wOut, map[string]any{
			"method": "turn/completed",
			"params": map[string]any{
				"threadId": "th_x",
				"turn": map[string]any{
					"id":     "turn_w",
					"status": "completed",
				},
			},
		})
	}()

	turnCh := c.registerTurn("turn_w")

	select {
	case ev := <-turnCh:
		if ev.Event != "session.end" {
			t.Errorf("event = %q, want session.end", ev.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for turn completion")
	}
}

func TestClientTurnCompletionBufferedBeforeRegister(t *testing.T) {
	c, _, _ := fakeClient()
	defer c.Close()

	params := mustRaw(map[string]any{
		"threadId": "th_x",
		"turn": map[string]any{
			"id":     "turn_buf",
			"status": "completed",
		},
	})
	c.routeNotification("turn/completed", params)

	c.mu.Lock()
	_, buffered := c.turnDone["turn_buf"]
	c.mu.Unlock()
	if !buffered {
		t.Fatal("turn completion was not buffered before registerTurn")
	}

	turnCh := c.registerTurn("turn_buf")

	select {
	case ev := <-turnCh:
		if ev.Event != "session.end" {
			t.Errorf("event = %q, want session.end", ev.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for buffered turn completion")
	}
}

func TestClientTurnCompletedClearsPendingApprovals(t *testing.T) {
	c, _, _ := fakeClient()
	defer c.Close()

	c.mu.Lock()
	c.approvals["req_turn"] = pendingApproval{threadID: "th_x", turnID: "turn_done"}
	c.mu.Unlock()

	c.routeNotification("turn/completed", mustRaw(map[string]any{
		"threadId": "th_x",
		"turn": map[string]any{
			"id":     "turn_done",
			"status": "completed",
		},
	}))

	c.mu.Lock()
	_, ok := c.approvals["req_turn"]
	c.mu.Unlock()
	if ok {
		t.Fatal("approval should be cleared after its turn completes")
	}
}

func TestClientApprovalRoutingPreservesRawNumericID(t *testing.T) {
	c, wOut, rIn := fakeClient()
	defer c.Close()

	go func() {
		writeLine(wOut, map[string]any{
			"id":     42,
			"method": "item/commandExecution/requestApproval",
			"params": map[string]any{
				"threadId": "th_perm",
				"turnId":   "turn_perm",
				"command":  "rm -rf /",
			},
		})
	}()

	select {
	case <-c.eventsCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for approval event")
	}

	done := make(chan rpcMessage, 1)
	go func() {
		msg, err := readLine(rIn)
		if err != nil {
			t.Logf("read error: %v", err)
			return
		}
		done <- msg
	}()

	if err := c.answerApprovalThreaded("42", "th_perm", true); err != nil {
		t.Fatalf("answerApprovalThreaded: %v", err)
	}

	select {
	case msg := <-done:
		if string(msg.ID) != "42" {
			t.Fatalf("response ID = %s, want numeric 42", msg.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for answer write")
	}
}

func TestClientApprovalRouting(t *testing.T) {
	c, wOut, _ := fakeClient()
	defer c.Close()

	sub := make(chan events.Event, 4)
	c.subscribe("th_perm", sub)

	go func() {
		writeLine(wOut, map[string]any{
			"id":     "42",
			"method": "item/commandExecution/requestApproval",
			"params": map[string]any{
				"threadId": "th_perm",
				"command":  "rm -rf /",
				"summary":  "dangerous command",
			},
		})
	}()

	select {
	case ev := <-c.eventsCh:
		if ev.Event != "permission.request" {
			t.Errorf("event = %q, want permission.request", ev.Event)
		}
		rid, _ := ev.Fields["request_id"].(string)
		if rid != "42" {
			t.Errorf("request_id = %q, want 42", rid)
		}
		kind, _ := ev.Fields["kind"].(string)
		if kind != "command" {
			t.Errorf("kind = %q, want command", kind)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for approval event")
	}

	select {
	case ev := <-sub:
		if ev.Event != "permission.request" {
			t.Errorf("event = %q, want permission.request", ev.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscriber approval event")
	}

	c.mu.Lock()
	_, ok := c.approvals["42"]
	c.mu.Unlock()
	if !ok {
		t.Fatal("approval not stored")
	}
}

func TestClientAnswerApproval(t *testing.T) {
	c, _, rIn := fakeClient()
	defer c.Close()

	c.mu.Lock()
	c.approvals["req_accept"] = pendingApproval{threadID: "th_x"}
	c.mu.Unlock()

	done := make(chan rpcMessage, 1)
	go func() {
		msg, err := readLine(rIn)
		if err != nil {
			t.Logf("read error: %v", err)
			return
		}
		done <- msg
	}()

	if err := c.answerApproval("req_accept", true); err != nil {
		t.Fatalf("answerApproval: %v", err)
	}

	select {
	case msg := <-done:
		var resp approvalDecision
		if err := json.Unmarshal(msg.Result, &resp); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if resp.Decision != "accept" {
			t.Errorf("decision = %q, want accept", resp.Decision)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for answer write")
	}
}

func TestClientAnswerApprovalNotFound(t *testing.T) {
	c, _, _ := fakeClient()
	defer c.Close()

	err := c.answerApproval("unknown", true)
	if err == nil {
		t.Fatal("expected error for unknown approval")
	}
}

func TestClientAnswerApprovalWrongThreadPreservesRequest(t *testing.T) {
	c, _, _ := fakeClient()
	defer c.Close()

	c.mu.Lock()
	c.approvals["req_keep"] = pendingApproval{threadID: "th_right"}
	c.mu.Unlock()

	err := c.answerApprovalThreaded("req_keep", "th_wrong", true)
	if err == nil {
		t.Fatal("expected error for wrong thread")
	}

	c.mu.Lock()
	_, ok := c.approvals["req_keep"]
	c.mu.Unlock()
	if !ok {
		t.Fatal("approval should remain pending after wrong-thread answer")
	}
}

func TestClientUnknownApprovalRequestWritesRPCError(t *testing.T) {
	c, _, rIn := fakeClient()
	defer c.Close()

	done := make(chan rpcMessage, 1)
	go func() {
		msg, err := readLine(rIn)
		if err != nil {
			t.Logf("read error: %v", err)
			return
		}
		done <- msg
	}()

	c.routeApprovalRequest(json.RawMessage(`"req_unknown_method"`), "item/unknown/requestApproval", mustRaw(map[string]any{"threadId": "th_x"}))

	select {
	case msg := <-done:
		if string(msg.ID) != `"req_unknown_method"` {
			t.Fatalf("response ID = %s, want req_unknown_method", msg.ID)
		}
		if msg.Error == nil || msg.Error.Code != -32601 {
			t.Fatalf("error = %+v, want method-not-found", msg.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for unknown method error response")
	}
}

func TestClientMalformedKnownApprovalRequestWritesInvalidParams(t *testing.T) {
	c, _, rIn := fakeClient()
	defer c.Close()

	done := make(chan rpcMessage, 1)
	go func() {
		msg, err := readLine(rIn)
		if err != nil {
			t.Logf("read error: %v", err)
			return
		}
		done <- msg
	}()

	c.routeApprovalRequest(json.RawMessage(`"req_bad_params"`), "item/commandExecution/requestApproval", json.RawMessage(`{bad`))

	select {
	case msg := <-done:
		if string(msg.ID) != `"req_bad_params"` {
			t.Fatalf("response ID = %s, want req_bad_params", msg.ID)
		}
		if msg.Error == nil || msg.Error.Code != -32602 {
			t.Fatalf("error = %+v, want invalid params", msg.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for invalid params response")
	}
}

func TestClientFanoutRecordsDroppedEvents(t *testing.T) {
	c, _, _ := fakeClient()
	defer c.Close()

	for i := 0; i < cap(c.eventsCh); i++ {
		c.eventsCh <- events.Event{Event: "existing"}
	}

	sub := make(chan events.Event)
	c.subscribe("th_drop", sub)
	c.fanout(&events.Event{Event: "agent.message", SessionID: "th_drop"})

	stderr := c.Stderr()
	if !strings.Contains(stderr, "global event buffer full") {
		t.Fatalf("stderr = %q, want global drop note", stderr)
	}
	if !strings.Contains(stderr, "subscriber buffer full") {
		t.Fatalf("stderr = %q, want subscriber drop note", stderr)
	}
}

func TestClientMalformedJSON(t *testing.T) {
	c, wOut, _ := fakeClient()
	defer c.Close()

	go func() {
		_, _ = wOut.Write([]byte("not json\n"))
		writeLine(wOut, map[string]any{
			"method": "turn/started",
			"params": map[string]any{"threadId": "th_ok", "turnId": "t_ok"},
		})
	}()

	select {
	case ev := <-c.eventsCh:
		if ev.Event != "avenor.turn.start" {
			t.Errorf("expected turn.start after malformed line, got %q", ev.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event after malformed line")
	}
	if !strings.Contains(c.Stderr(), "dropped malformed JSON-RPC line") {
		t.Fatalf("stderr = %q, want malformed JSON diagnostic", c.Stderr())
	}
}

func TestClientClose(t *testing.T) {
	proc := exec.Command("cat")
	stdin, _ := proc.StdinPipe()
	stdout, _ := proc.StdoutPipe()
	stderr, _ := proc.StderrPipe()
	_ = proc.Start()

	c := newClient(proc, stdin, stdout, stderr)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if proc.ProcessState == nil {
		t.Fatal("process should have exited")
	}
}

func TestClientNotify(t *testing.T) {
	c, _, rIn := fakeClient()
	defer c.Close()

	done := make(chan rpcMessage, 1)
	go func() {
		msg, err := readLine(rIn)
		if err != nil {
			t.Logf("read error: %v", err)
			return
		}
		done <- msg
	}()

	if err := c.notify("initialized", nil); err != nil {
		t.Fatalf("notify: %v", err)
	}

	select {
	case msg := <-done:
		if msg.Method != "initialized" {
			t.Errorf("method = %q, want initialized", msg.Method)
		}
		if len(msg.ID) > 0 && string(msg.ID) != "null" && string(msg.ID) != `"null"` {
			t.Error("notify should have no id")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for notify write")
	}
}

func TestRollingBuffer(t *testing.T) {
	b := newRollingBuffer(3)
	b.Append("line 1")
	b.Append("line 2")
	b.Append("line 3")
	b.Append("line 4")
	b.Append("line 5")

	out := b.String()
	if out != "line 3\nline 4\nline 5\n" {
		t.Errorf("rolling buffer = %q", out)
	}
}

func TestClientSubscribeUnsubscribe(t *testing.T) {
	c, _, _ := fakeClient()
	defer c.Close()

	ch := make(chan events.Event, 1)
	c.subscribe("th_sub", ch)
	c.mu.Lock()
	if len(c.subs["th_sub"]) != 1 {
		t.Fatal("sub not registered")
	}
	c.mu.Unlock()

	c.unsubscribe("th_sub", ch)
	c.mu.Lock()
	if len(c.subs["th_sub"]) != 0 {
		t.Fatal("sub not removed")
	}
	c.mu.Unlock()
}
