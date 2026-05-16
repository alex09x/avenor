package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/sdougbrown/avenor/internal/events"
	"github.com/sdougbrown/avenor/internal/runtime"
)

const backendID = "codex-app-server"

// Provider implements runtime.Provider for Codex's app-server JSON-RPC protocol.
type Provider struct {
	opts runtime.StartOptions

	mu      sync.Mutex
	client  *client
	threads map[string]string // sessionID (=threadID) → threadID
	turns   map[string]string // sessionID → current turnID
}

func NewWithOptions(opts runtime.StartOptions) *Provider {
	return &Provider{
		opts:    opts,
		threads: map[string]string{},
		turns:   map[string]string{},
	}
}

var _ runtime.Provider = (*Provider)(nil)

func (p *Provider) Start(ctx context.Context, opts runtime.StartOptions) (runtime.Session, error) {
	c, err := p.ensureClient(ctx)
	if err != nil {
		return runtime.Session{}, err
	}

	cwd := opts.Dir
	if cwd == "" {
		cwd = p.opts.Dir
	}
	model := opts.Model
	if model == "" {
		model = p.opts.Model
	}

	params := threadStartParams{CWD: cwd, Model: model}
	result, err := c.request(ctx, "thread/start", params)
	if err != nil {
		return runtime.Session{}, fmt.Errorf("thread/start: %w", err)
	}

	var tsr threadStartResult
	if err := json.Unmarshal(result, &tsr); err != nil {
		return runtime.Session{}, fmt.Errorf("thread/start result: %w", err)
	}
	threadID := tsr.Thread.ID

	p.mu.Lock()
	p.threads[threadID] = threadID
	p.mu.Unlock()

	return runtime.Session{
		SessionID: threadID,
		Backend:   backendID,
		Dir:       cwd,
	}, nil
}

func (p *Provider) Resume(ctx context.Context, sessionID string) (runtime.Session, error) {
	if sessionID == "" {
		return runtime.Session{}, errors.New("session id is required")
	}

	p.mu.Lock()
	if _, ok := p.threads[sessionID]; ok {
		p.mu.Unlock()
		return runtime.Session{SessionID: sessionID, Backend: backendID}, nil
	}
	p.mu.Unlock()

	c, err := p.ensureClient(ctx)
	if err != nil {
		return runtime.Session{}, err
	}

	params := threadResumeParams{ThreadID: sessionID}
	result, err := c.request(ctx, "thread/resume", params)
	if err != nil {
		return runtime.Session{}, fmt.Errorf("thread/resume: %w", err)
	}

	var tsr threadStartResult
	if err := json.Unmarshal(result, &tsr); err != nil {
		return runtime.Session{}, fmt.Errorf("thread/resume result: %w", err)
	}
	threadID := tsr.Thread.ID

	p.mu.Lock()
	p.threads[threadID] = threadID
	p.mu.Unlock()

	return runtime.Session{
		SessionID: threadID,
		Backend:   backendID,
	}, nil
}

func (p *Provider) Prompt(ctx context.Context, sessionID string, prompt string) error {
	if _, err := p.sessionThread(sessionID); err != nil {
		return err
	}

	c, err := p.getClient()
	if err != nil {
		return err
	}

	params := turnStartParams{
		ThreadID: sessionID,
		Input: []inputPart{
			{Type: "text", Text: prompt},
		},
	}
	result, err := c.request(ctx, "turn/start", params)
	if err != nil {
		return fmt.Errorf("turn/start: %w", err)
	}

	var tsr turnStartResult
	if err := json.Unmarshal(result, &tsr); err != nil {
		return fmt.Errorf("turn/start result: %w", err)
	}
	turnID := tsr.Turn.ID

	turnCh := c.registerTurn(turnID)
	p.mu.Lock()
	p.turns[sessionID] = turnID
	p.mu.Unlock()

	defer func() {
		c.unregisterTurn(turnID)
		p.mu.Lock()
		delete(p.turns, sessionID)
		p.mu.Unlock()
	}()

	select {
	case ev, ok := <-turnCh:
		if !ok {
			return errors.New("client closed while waiting for turn completion")
		}
		reason, _ := ev.Fields["stop_reason"].(string)
		switch reason {
		case "end_turn":
			return nil
		case "cancelled":
			return fmt.Errorf("turn cancelled")
		case "error":
			msg, _ := ev.Fields["error_message"].(string)
			if msg == "" {
				msg = "unknown error"
			}
			return fmt.Errorf("turn failed: %s", msg)
		default:
			return fmt.Errorf("turn ended: %s", reason)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Provider) Cancel(ctx context.Context, sessionID string) error {
	p.mu.Lock()
	turnID := p.turns[sessionID]
	p.mu.Unlock()
	if turnID == "" {
		return fmt.Errorf("no active turn for session %q", sessionID)
	}

	c, err := p.getClient()
	if err != nil {
		return err
	}

	params := turnInterruptParams{
		ThreadID: sessionID,
		TurnID:   turnID,
	}
	if err := c.notify("turn/interrupt", params); err != nil {
		return fmt.Errorf("turn/interrupt: %w", err)
	}
	return nil
}

func (p *Provider) Events(ctx context.Context, sessionID string) (<-chan events.Event, error) {
	c, err := p.getClient()
	if err != nil {
		return nil, err
	}

	out := make(chan events.Event, 128)
	c.subscribe(sessionID, out)
	go func() {
		<-ctx.Done()
		c.unsubscribe(sessionID, out)
		close(out)
	}()
	return out, nil
}

func (p *Provider) AnswerPermission(ctx context.Context, sessionID string, requestID string, response runtime.PermissionResponse) error {
	c, err := p.getClient()
	if err != nil {
		return err
	}
	if requestID == "" {
		return errors.New("permission request id is required")
	}
	return c.answerApprovalThreaded(requestID, sessionID, response.Allow)
}

func (p *Provider) Capabilities(ctx context.Context) (runtime.Capabilities, error) {
	return runtime.Capabilities{
		Backend:             backendID,
		Permissions:         true,
		Resume:              true,
		ExternalServerURL:   false,
		SubprocessDiscovery: false,
		ModelSelection:      true,
	}, nil
}

func (p *Provider) Close() error {
	p.mu.Lock()
	c := p.client
	p.client = nil
	p.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.Close()
}

func (p *Provider) ensureClient(ctx context.Context) (*client, error) {
	p.mu.Lock()
	if p.client != nil {
		c := p.client
		p.mu.Unlock()
		return c, nil
	}

	c, err := StartClient(ctx)
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	p.client = c
	p.mu.Unlock()
	return c, nil
}

func (p *Provider) sessionThread(sessionID string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	threadID, ok := p.threads[sessionID]
	if !ok {
		return "", fmt.Errorf("session %q not found", sessionID)
	}
	return threadID, nil
}

// getClient returns the current client, or an error if the provider has not been started or was closed.
func (p *Provider) getClient() (*client, error) {
	p.mu.Lock()
	c := p.client
	p.mu.Unlock()
	if c == nil {
		return nil, errors.New("provider has not been started")
	}
	return c, nil
}
