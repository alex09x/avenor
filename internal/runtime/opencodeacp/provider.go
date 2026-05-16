package opencodeacp

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/sdougbrown/avenor/internal/events"
	"github.com/sdougbrown/avenor/internal/runtime"
)

const backendID = "opencode-acp"

// Provider implements runtime.Provider for opencode's ACP stdio transport.
type Provider struct {
	opts runtime.StartOptions

	mu          sync.Mutex
	client      *Client
	events      chan events.Event
	sessions    map[string]*Session
	initialized bool
}

func New() runtime.Provider {
	return NewWithOptions(runtime.StartOptions{})
}

func NewWithOptions(opts runtime.StartOptions) runtime.Provider {
	return &Provider{
		opts:     opts,
		sessions: map[string]*Session{},
	}
}

var _ runtime.Provider = (*Provider)(nil)

func (p *Provider) Start(ctx context.Context, opts runtime.StartOptions) (runtime.Session, error) {
	merged := mergeStartOptions(p.opts, opts)
	if err := p.ensureClient(ctx, merged); err != nil {
		return runtime.Session{}, err
	}

	session, err := p.client.NewSession(ctx)
	if err != nil {
		return runtime.Session{}, err
	}
	if err := p.configureSession(ctx, session, merged); err != nil {
		return runtime.Session{}, err
	}

	p.mu.Lock()
	p.sessions[session.SessionID] = session
	p.mu.Unlock()

	return runtime.Session{
		SessionID: session.SessionID,
		Backend:   backendID,
		Dir:       merged.Dir,
	}, nil
}

func (p *Provider) Resume(ctx context.Context, sessionID string) (runtime.Session, error) {
	if sessionID == "" {
		return runtime.Session{}, errors.New("session id is required")
	}
	if err := p.ensureClient(ctx, p.opts); err != nil {
		return runtime.Session{}, err
	}

	session, err := p.client.ResumeSession(ctx, sessionID)
	if err != nil {
		return runtime.Session{}, err
	}
	if err := p.configureSession(ctx, session, p.opts); err != nil {
		return runtime.Session{}, err
	}

	p.mu.Lock()
	p.sessions[session.SessionID] = session
	p.mu.Unlock()

	return runtime.Session{
		SessionID: session.SessionID,
		Backend:   backendID,
		Dir:       p.opts.Dir,
	}, nil
}

func (p *Provider) Prompt(ctx context.Context, sessionID string, prompt string) error {
	session, err := p.session(sessionID)
	if err != nil {
		return err
	}
	event, err := session.Prompt(ctx, prompt)
	if err != nil {
		return err
	}
	p.publish(event)
	return nil
}

func (p *Provider) Cancel(ctx context.Context, sessionID string) error {
	session, err := p.session(sessionID)
	if err != nil {
		return err
	}
	return session.Cancel()
}

func (p *Provider) Events(ctx context.Context, sessionID string) (<-chan events.Event, error) {
	p.mu.Lock()
	source := p.events
	p.mu.Unlock()
	if source == nil {
		return nil, errors.New("provider has not been started")
	}

	out := make(chan events.Event, 128)
	go func() {
		defer close(out)
		for {
			select {
			case event, ok := <-source:
				if !ok {
					return
				}
				if sessionID == "" || event.SessionID == "" || event.SessionID == sessionID {
					out <- event
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (p *Provider) AnswerPermission(ctx context.Context, sessionID string, requestID string, response runtime.PermissionResponse) error {
	p.mu.Lock()
	client := p.client
	p.mu.Unlock()
	if client == nil {
		return errors.New("provider has not been started")
	}
	if requestID == "" {
		return errors.New("permission request id is required")
	}
	return client.answerPermission(requestID, permissionResponseResult(response))
}

func (p *Provider) Capabilities(ctx context.Context) (runtime.Capabilities, error) {
	return runtime.Capabilities{
		Backend:             backendID,
		Permissions:         true,
		Resume:              true,
		ExternalServerURL:   false,
		SubprocessDiscovery: true,
		ModelSelection:      true,
	}, nil
}

func (p *Provider) Close() error {
	p.mu.Lock()
	client := p.client
	p.client = nil
	p.mu.Unlock()
	if client == nil {
		return nil
	}
	return client.Close()
}

func (p *Provider) ensureClient(ctx context.Context, opts runtime.StartOptions) error {
	p.mu.Lock()
	if p.client != nil {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	if opts.ServerURL != "" {
		return fmt.Errorf("%s external server URL transport is not implemented by the stdio client", backendID)
	}

	client, err := NewClientWithOptions(ctx, ClientOptions{
		Dir:                  opts.Dir,
		AutoAnswerPermission: false,
	})
	if err != nil {
		return err
	}
	if _, err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil {
		_ = client.Close()
		return nil
	}
	p.client = client
	p.events = make(chan events.Event, 256)
	p.initialized = true
	go p.pumpClientEvents(client, p.events)
	return nil
}

func (p *Provider) pumpClientEvents(client *Client, out chan<- events.Event) {
	for event := range client.Events() {
		out <- event
	}
}

func (p *Provider) publish(event events.Event) {
	p.mu.Lock()
	out := p.events
	p.mu.Unlock()
	if out == nil {
		return
	}
	out <- event
}

func (p *Provider) configureSession(ctx context.Context, session *Session, opts runtime.StartOptions) error {
	if opts.Model != "" {
		if err := session.Client.SetSessionModel(ctx, session.SessionID, opts.Model); err != nil {
			return fmt.Errorf("set session model %q: %w", opts.Model, err)
		}
	}
	if opts.Agent != "" {
		if err := session.Client.SetSessionMode(ctx, session.SessionID, opts.Agent); err != nil {
			return fmt.Errorf("set session agent %q: %w", opts.Agent, err)
		}
	}
	return nil
}

func (p *Provider) session(sessionID string) (*Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	session := p.sessions[sessionID]
	if session == nil {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	return session, nil
}

func permissionResponseResult(response runtime.PermissionResponse) map[string]any {
	optionID := "reject"
	if response.Allow {
		optionID = "allow"
	}
	result := map[string]any{
		"outcome":  "selected",
		"optionId": optionID,
	}
	if response.Message != "" {
		result["message"] = response.Message
	}
	return map[string]any{"outcome": result}
}

func mergeStartOptions(base, override runtime.StartOptions) runtime.StartOptions {
	if override.Agent != "" {
		base.Agent = override.Agent
	}
	if override.Label != "" {
		base.Label = override.Label
	}
	if override.Dir != "" {
		base.Dir = override.Dir
	}
	if override.ServerURL != "" {
		base.ServerURL = override.ServerURL
	}
	if override.Model != "" {
		base.Model = override.Model
	}
	return base
}
