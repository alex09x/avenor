package opencodeacp

import (
	"context"
	"errors"

	"github.com/sdougbrown/avenor/internal/events"
	"github.com/sdougbrown/avenor/internal/runtime"
)

// Provider is a no-op implementation of runtime.Provider for opencode ACP.
type Provider struct{}

// New returns a new no-op opencode ACP provider.
func New() runtime.Provider {
	return &Provider{}
}

// Verify Provider implements runtime.Provider at compile time.
var _ runtime.Provider = (*Provider)(nil)

// Start returns "not implemented".
func (p *Provider) Start(ctx context.Context, opts runtime.StartOptions) (runtime.Session, error) {
	return runtime.Session{}, errors.New("not implemented")
}

// Resume returns "not implemented".
func (p *Provider) Resume(ctx context.Context, sessionID string) (runtime.Session, error) {
	return runtime.Session{}, errors.New("not implemented")
}

// Prompt returns "not implemented".
func (p *Provider) Prompt(ctx context.Context, sessionID string, prompt string) error {
	return errors.New("not implemented")
}

// Cancel returns "not implemented".
func (p *Provider) Cancel(ctx context.Context, sessionID string) error {
	return errors.New("not implemented")
}

// Events returns "not implemented".
func (p *Provider) Events(ctx context.Context, sessionID string) (<-chan events.Event, error) {
	return nil, errors.New("not implemented")
}

// AnswerPermission returns "not implemented".
func (p *Provider) AnswerPermission(ctx context.Context, sessionID string, requestID string, response runtime.PermissionResponse) error {
	return errors.New("not implemented")
}

// Capabilities returns "not implemented".
func (p *Provider) Capabilities(ctx context.Context) (runtime.Capabilities, error) {
	return runtime.Capabilities{}, errors.New("not implemented")
}
