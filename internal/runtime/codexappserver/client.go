package codexappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/sdougbrown/avenor/internal/events"
)

const stderrCap = 400 // max lines of stderr retained in rolling buffer

type pendingApproval struct {
	method string
	params json.RawMessage
}

type client struct {
	proc   *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu        sync.Mutex
	pending   map[string]chan json.RawMessage // requestID → response result
	turns     map[string]chan events.Event    // turnID → completion event
	subs      map[string][]chan events.Event  // threadID → Events subscribers
	approvals map[string]pendingApproval      // requestID → pending approval

	stderr *rollingBuffer

	eventsCh chan events.Event // output channel for all events (notifications + approvals)
	done     chan struct{}     // closed when reader goroutine exits
}

func newClient(proc *exec.Cmd, stdin io.WriteCloser, stdout io.ReadCloser, stderr io.ReadCloser) *client {
	c := &client{
		proc:      proc,
		stdin:     stdin,
		stdout:    stdout,
		pending:   map[string]chan json.RawMessage{},
		turns:     map[string]chan events.Event{},
		subs:      map[string][]chan events.Event{},
		approvals: map[string]pendingApproval{},
		stderr:    newRollingBuffer(stderrCap),
		eventsCh:  make(chan events.Event, 256),
		done:      make(chan struct{}),
	}
	go c.drainStderr(stderr)
	go c.readLoop()
	return c
}

// StartClient spawns `codex app-server` and performs the initialize handshake.
func StartClient(ctx context.Context) (*client, error) {
	proc := exec.CommandContext(ctx, "codex", "app-server")
	stdin, err := proc.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex stdin pipe: %w", err)
	}
	stdout, err := proc.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex stdout pipe: %w", err)
	}
	stderr, err := proc.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("codex stderr pipe: %w", err)
	}
	if err := proc.Start(); err != nil {
		return nil, fmt.Errorf("start codex app-server: %w", err)
	}

	c := newClient(proc, stdin, stdout, stderr)

	// Initialize handshake
	initParams := map[string]any{
		"clientInfo": map[string]any{
			"name":    "avenor",
			"version": "0.0.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}
	_, err = c.request("initialize", initParams)
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("codex initialize: %w", err)
	}
	if err := c.notify("initialized", nil); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("codex initialized: %w", err)
	}
	return c, nil
}

func (c *client) Close() error {
	// Close stdin and stdout to unblock the readLoop.
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.stdout != nil {
		_ = c.stdout.Close()
	}

	// Wait for the process if there is one.
	if c.proc != nil {
		done := make(chan error, 1)
		go func() {
			done <- c.proc.Wait()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = c.proc.Process.Kill()
			<-done
		}
	}

	// Wait for reader goroutine.
	<-c.done

	// Close subscriber channels.
	c.mu.Lock()
	for _, chans := range c.subs {
		for _, ch := range chans {
			close(ch)
		}
	}
	c.subs = nil
	c.mu.Unlock()

	close(c.eventsCh)
	return nil
}

func (c *client) Events() <-chan events.Event { return c.eventsCh }

func (c *client) Stderr() string { return c.stderr.String() }

// request sends a JSON-RPC request and blocks until the response arrives.
func (c *client) request(method string, params any) (json.RawMessage, error) {
	id := newRequestID()
	ch := make(chan json.RawMessage, 1)

	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	pb, err := json.Marshal(params)
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	msg := rpcMessage{
		ID:     json.RawMessage(`"` + id + `"`),
		Method: method,
		Params: pb,
	}
	data, _ := json.Marshal(msg)
	data = append(data, '\n')

	c.mu.Lock()
	if _, err := c.stdin.Write(data); err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("write request: %w", err)
	}
	c.mu.Unlock()

	result, ok := <-ch
	if !ok {
		return nil, errors.New("client closed while waiting for response")
	}
	return result, nil
}

func (c *client) notify(method string, params any) error {
	pb, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal notify params: %w", err)
	}

	msg := rpcMessage{
		Method: method,
		Params: pb,
	}
	data, _ := json.Marshal(msg)
	data = append(data, '\n')

	c.mu.Lock()
	_, err = c.stdin.Write(data)
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("write notify: %w", err)
	}
	return nil
}

func (c *client) answerApproval(requestID string, allow bool) error {
	c.mu.Lock()
	_, ok := c.approvals[requestID]
	if ok {
		delete(c.approvals, requestID)
	}
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("approval request %q not found", requestID)
	}

	decision := "decline"
	if allow {
		decision = "accept"
	}

	msg := rpcMessage{
		ID: json.RawMessage(`"` + requestID + `"`),
		Result: mustRaw(map[string]any{
			"decision": decision,
		}),
	}
	data, _ := json.Marshal(msg)
	data = append(data, '\n')

	c.mu.Lock()
	_, err := c.stdin.Write(data)
	c.mu.Unlock()
	return err
}

// subscribe registers a channel to receive events for a given threadID.
func (c *client) subscribe(threadID string, ch chan events.Event) {
	c.mu.Lock()
	c.subs[threadID] = append(c.subs[threadID], ch)
	c.mu.Unlock()
}

// unsubscribe removes a subscriber channel.
func (c *client) unsubscribe(threadID string, ch chan events.Event) {
	c.mu.Lock()
	chans := c.subs[threadID]
	for i, sub := range chans {
		if sub == ch {
			c.subs[threadID] = append(chans[:i], chans[i+1:]...)
			break
		}
	}
	c.mu.Unlock()
}

// registerTurn registers a buffered channel to receive the completion event for a turn.
func (c *client) registerTurn(turnID string) chan events.Event {
	ch := make(chan events.Event, 1)
	c.mu.Lock()
	c.turns[turnID] = ch
	c.mu.Unlock()
	return ch
}

// unregisterTurn removes a turn waiter.
func (c *client) unregisterTurn(turnID string) {
	c.mu.Lock()
	delete(c.turns, turnID)
	c.mu.Unlock()
}

// readLoop is the single goroutine that reads lines from stdout.
func (c *client) readLoop() {
	defer close(c.done)
	scanner := bufio.NewScanner(c.stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		c.dispatchLine(line)
	}
}

// dispatchLine classifies and routes a single JSON-RPC line from stdout.
func (c *client) dispatchLine(line []byte) {
	var msg rpcMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return // malformed line, drop
	}

	hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
	hasMethod := msg.Method != ""

	if hasID && !hasMethod {
		// RPC response: route to pending request.
		c.routeResponse(msg.ID, msg.Result, msg.Error)
		return
	}

	if hasMethod && !hasID {
		// Notification: translate and route to subscribers.
		c.routeNotification(msg.Method, msg.Params)
		return
	}

	if hasID && hasMethod {
		// Server-initiated request: store approval, translate, route to subscribers.
		c.routeApprovalRequest(msg.ID, msg.Method, msg.Params)
		return
	}
}

func (c *client) routeResponse(id json.RawMessage, result json.RawMessage, rpcErr *rpcError) {
	idStr := string(id)
	// Strip quotes from string-encoded IDs.
	if len(idStr) >= 2 && idStr[0] == '"' && idStr[len(idStr)-1] == '"' {
		idStr = idStr[1 : len(idStr)-1]
	}

	c.mu.Lock()
	ch, ok := c.pending[idStr]
	delete(c.pending, idStr)
	c.mu.Unlock()

	if !ok {
		return
	}
	if rpcErr != nil {
		ch <- mustRaw(map[string]any{"error": rpcErr})
	} else {
		ch <- result
	}
}

func (c *client) routeNotification(method string, params json.RawMessage) {
	ev := translateNotification(method, params)
	if ev == nil {
		return
	}

	// Handle turn/completed: signal turn waiters.
	if method == "turn/completed" {
		var n turnCompletedNotification
		if err := json.Unmarshal(params, &n); err == nil {
			c.mu.Lock()
			ch, ok := c.turns[n.Turn.ID]
			c.mu.Unlock()
			if ok {
				ch <- *ev
			}
		}
	}

	c.fanout(ev)
}

func (c *client) routeApprovalRequest(id json.RawMessage, method string, params json.RawMessage) {
	idStr := string(id)
	if len(idStr) >= 2 && idStr[0] == '"' && idStr[len(idStr)-1] == '"' {
		idStr = idStr[1 : len(idStr)-1]
	}

	ev, _ := translateApprovalRequest(method, params)
	if ev == nil {
		return
	}

	// Attach request_id so AnswerPermission can find it.
	ev.Fields["request_id"] = idStr

	c.mu.Lock()
	c.approvals[idStr] = pendingApproval{method: method, params: params}
	c.mu.Unlock()

	c.fanout(ev)
}

func (c *client) fanout(ev *events.Event) {
	if ev == nil {
		return
	}
	c.eventsCh <- *ev

	c.mu.Lock()
	chans := c.subs[ev.SessionID]
	c.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- *ev:
		default:
		}
	}
}

func (c *client) drainStderr(r io.ReadCloser) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		c.stderr.Append(scanner.Text())
	}
	_ = r.Close()
}

// rollingBuffer keeps the last N lines of text.
type rollingBuffer struct {
	mu    sync.Mutex
	lines []string
	cap   int
}

func newRollingBuffer(cap int) *rollingBuffer {
	return &rollingBuffer{cap: cap}
}

func (b *rollingBuffer) Append(line string) {
	b.mu.Lock()
	b.lines = append(b.lines, line)
	if len(b.lines) > b.cap {
		b.lines = b.lines[len(b.lines)-b.cap:]
	}
	b.mu.Unlock()
}

func (b *rollingBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := ""
	for _, line := range b.lines {
		out += line + "\n"
	}
	return out
}

var requestIDCounter int64

func newRequestID() string {
	// Simple counter-based IDs — enough for a single client.
	requestIDCounter++
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), requestIDCounter)
}

func mustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return b
}
