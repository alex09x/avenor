package opencodehttp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/sdougbrown/avenor/internal/events"
	"github.com/sdougbrown/avenor/internal/runtime"
)

const backendID = "opencode-http"

// Provider implements runtime.Provider for opencode serve's HTTP API.
type Provider struct {
	opts runtime.StartOptions

	mu           sync.Mutex
	client       *Client
	events       chan events.Event
	streamCancel context.CancelFunc
	sessions     map[string]struct {
		sessionID string
		dir       string
	}
}

// New creates a Provider with empty options.
func New() (runtime.Provider, error) {
	return NewWithOptions(runtime.StartOptions{})
}

// NewWithOptions creates a Provider with the given options.
func NewWithOptions(opts runtime.StartOptions) (runtime.Provider, error) {
	return &Provider{
		opts:     opts,
		sessions: make(map[string]struct {
			sessionID string
			dir       string
		}),
	}, nil
}

var _ runtime.Provider = (*Provider)(nil)

func (p *Provider) Start(ctx context.Context, opts runtime.StartOptions) (runtime.Session, error) {
	merged := mergeStartOptions(p.opts, opts)
	if merged.ServerURL == "" {
		return runtime.Session{}, errors.New("server URL is required for opencode-http backend")
	}
	c, err := p.ensureClient(merged)
	if err != nil {
		return runtime.Session{}, err
	}
	// Persist ServerURL so Resume works after Start-only URL provision.
	p.mu.Lock()
	p.opts.ServerURL = merged.ServerURL
	p.mu.Unlock()

	sessionID, err := c.CreateSession(ctx)
	if err != nil {
		return runtime.Session{}, fmt.Errorf("create session: %w", err)
	}
	p.mu.Lock()
	p.sessions[sessionID] = struct {
		sessionID string
		dir       string
	}{sessionID: sessionID, dir: merged.Dir}
	p.mu.Unlock()

	return runtime.Session{
		SessionID: sessionID,
		Backend:   backendID,
		Dir:       merged.Dir,
	}, nil
}

func (p *Provider) Resume(ctx context.Context, sessionID string) (runtime.Session, error) {
	if sessionID == "" {
		return runtime.Session{}, errors.New("session id is required")
	}
	if p.opts.ServerURL == "" {
		return runtime.Session{}, errors.New("server URL is required for opencode-http backend")
	}
	c, err := p.ensureClient(p.opts)
	if err != nil {
		return runtime.Session{}, err
	}
	info, err := c.GetSession(ctx, sessionID)
	if err != nil {
		return runtime.Session{}, fmt.Errorf("resume session %q: %w", sessionID, err)
	}
	dir, _ := info["directory"].(string)

	p.mu.Lock()
	p.sessions[sessionID] = struct {
		sessionID string
		dir       string
	}{sessionID: sessionID, dir: dir}
	p.mu.Unlock()

	return runtime.Session{
		SessionID: sessionID,
		Backend:   backendID,
		Dir:       dir,
	}, nil
}

func (p *Provider) Prompt(ctx context.Context, sessionID string, prompt string) error {
	c, err := p.clientLocked()
	if err != nil {
		return err
	}
	payload := map[string]any{
		"parts": []map[string]any{
			{"type": "text", "text": prompt},
		},
	}
	if p.opts.Agent != "" {
		payload["agent"] = p.opts.Agent
	}
	if p.opts.Model != "" {
		payload["model"] = mapModel(p.opts.Model)
	}
	_, err = c.SendMessage(ctx, sessionID, payload)
	return err
}

func (p *Provider) Cancel(ctx context.Context, sessionID string) error {
	c, err := p.clientLocked()
	if err != nil {
		return err
	}
	return c.Abort(ctx, sessionID)
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
	return errors.New("permissions not yet implemented for opencode-http backend")
}

func (p *Provider) Capabilities(ctx context.Context) (runtime.Capabilities, error) {
	return runtime.Capabilities{
		Backend:             backendID,
		Permissions:         false,
		Resume:              true,
		ExternalServerURL:   true,
		SubprocessDiscovery: false,
		ModelSelection:      true,
	}, nil
}

// Close shuts down the event stream and releases resources.
func (p *Provider) Close() error {
	p.mu.Lock()
	if p.streamCancel != nil {
		p.streamCancel()
		p.streamCancel = nil
	}
	p.mu.Unlock()
	return nil
}

func (p *Provider) ensureClient(opts runtime.StartOptions) (*Client, error) {
	p.mu.Lock()
	if p.client != nil {
		c := p.client
		p.mu.Unlock()
		return c, nil
	}
	p.mu.Unlock()

	co := ClientOptions{BaseURL: opts.ServerURL}
	if u, err := url.Parse(opts.ServerURL); err == nil && u.User != nil {
		co.Username = u.User.Username()
		co.Password, _ = u.User.Password()
	}

	client := NewClient(co)
	if err := client.Health(context.Background()); err != nil {
		return nil, fmt.Errorf("connect to opencode server at %s: %w", opts.ServerURL, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil {
		return p.client, nil
	}
	p.client = client
	p.events = make(chan events.Event, 256)

	// Start the SSE event stream in the background.
	go p.streamEvents(client)
	return client, nil
}

func (p *Provider) streamEvents(c *Client) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.mu.Lock()
	p.streamCancel = cancel
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.streamCancel = nil
		// Close the event channel so Events() readers unblock.
		if p.events != nil {
			close(p.events)
			p.events = nil
		}
		p.mu.Unlock()
	}()

	body, err := c.StreamEvents(ctx)
	if err != nil {
		return
	}
	defer body.Close()

	rawEvents := make(chan events.Event, 64)
	go readSSEEvents(ctx, body, rawEvents)

	for {
		select {
		case evt, ok := <-rawEvents:
			if !ok {
				return
			}
			select {
			case p.events <- evt:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (p *Provider) clientLocked() (*Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client == nil {
		return nil, errors.New("provider has not been started")
	}
	return p.client, nil
}

// mapModel converts a StartOptions.Model string (e.g. "deepseek/deepseek-v4-pro")
// to the object format expected by opencode serve.
func mapModel(model string) map[string]string {
	result := map[string]string{}
	if model == "" {
		return result
	}
	if idx := strings.IndexByte(model, '/'); idx >= 0 {
		result["providerID"] = model[:idx]
		result["modelID"] = model[idx+1:]
	} else {
		result["modelID"] = model
	}
	return result
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
