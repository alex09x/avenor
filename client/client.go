package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RespError      `json:"error,omitempty"`
}

type RespError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Event struct {
	Event     string         `json:"event"`
	SessionID string         `json:"session_id,omitempty"`
	RuntimeID string         `json:"runtime_id,omitempty"`
	Raw       map[string]any `json:"-"`
}

func (e *Event) UnmarshalJSON(data []byte) error {
	e.Raw = map[string]any{}
	if err := json.Unmarshal(data, &e.Raw); err != nil {
		return err
	}
	if v, ok := e.Raw["event"].(string); ok {
		e.Event = v
	}
	if v, ok := e.Raw["session_id"].(string); ok {
		e.SessionID = v
	}
	if v, ok := e.Raw["runtime_id"].(string); ok {
		e.RuntimeID = v
	}
	return nil
}

type Client struct {
	conn   net.Conn
	mu     sync.Mutex
	readMu sync.Mutex
	scan   *bufio.Scanner
	nextID int
}

func Dial(socketPath string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial control socket: %w", err)
	}
	return &Client{conn: conn, scan: bufio.NewScanner(conn)}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) Call(method string, params any, result any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	req := Request{JSONRPC: "2.0", ID: id, Method: method}
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			c.mu.Unlock()
			return fmt.Errorf("marshal params: %w", err)
		}
		req.Params = data
	}

	data, err := json.Marshal(req)
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')
	if _, err := c.conn.Write(data); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("write request: %w", err)
	}
	c.mu.Unlock()

	c.readMu.Lock()
	defer c.readMu.Unlock()

	var resp Response
	if !c.scan.Scan() {
		err := c.scan.Err()
		if err == nil {
			err = fmt.Errorf("connection closed")
		}
		return fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(c.scan.Bytes(), &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("rpc error [%d]: %s", resp.Error.Code, resp.Error.Message)
	}
	if result != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("unmarshal result: %w", err)
		}
	}
	return nil
}

func (c *Client) subscribe() error {
	var result struct {
		Subscribed bool `json:"subscribed"`
	}
	return c.Call("subscribe", nil, &result)
}

func (c *Client) Events() <-chan Event {
	ch := make(chan Event, 256)
	go func() {
		defer close(ch)
		for c.scan.Scan() {
			var n Notification
			if err := json.Unmarshal(c.scan.Bytes(), &n); err != nil {
				continue
			}
			if n.Method != "event" {
				continue
			}
			var ev Event
			if err := json.Unmarshal(n.Params, &ev); err != nil {
				continue
			}
			ch <- ev
		}
	}()
	return ch
}

// Status returns the snapshot for the one-shot run or a runtime if runtimeID is set.
func (c *Client) Status(runtimeID string) (map[string]any, error) {
	var params any
	if runtimeID != "" {
		params = map[string]string{"runtime_id": runtimeID}
	}
	var result map[string]any
	err := c.Call("status", params, &result)
	return result, err
}

// Cancel cancels the one-shot run or a specific runtime if runtimeID is set.
func (c *Client) Cancel(runtimeID string) error {
	var params any
	if runtimeID != "" {
		params = map[string]string{"runtime_id": runtimeID}
	}
	return c.Call("cancel", params, nil)
}

// Prompt sends a follow-up prompt to the one-shot session or a runtime.
func (c *Client) Prompt(runtimeID, text string) error {
	params := map[string]string{"text": text}
	if runtimeID != "" {
		params["runtime_id"] = runtimeID
	}
	return c.Call("prompt", params, nil)
}

// AnswerPermission answers a pending permission request.
func (c *Client) AnswerPermission(runtimeID, requestID, optionID string) error {
	params := map[string]string{
		"request_id": requestID,
		"option_id":  optionID,
	}
	if runtimeID != "" {
		params["runtime_id"] = runtimeID
	}
	return c.Call("answer_permission", params, nil)
}

// List returns all active runtimes from the stable supervisor.
func (c *Client) List() ([]map[string]any, error) {
	var result []map[string]any
	err := c.Call("list", nil, &result)
	return result, err
}

// Spawn creates a new runtime in the stable supervisor.
func (c *Client) Spawn(params map[string]any) (map[string]any, error) {
	var result map[string]any
	err := c.Call("spawn", params, &result)
	return result, err
}

// Shutdown shuts down the stable supervisor.
func (c *Client) Shutdown(mode string) error {
	return c.Call("shutdown", map[string]string{"mode": mode}, nil)
}
