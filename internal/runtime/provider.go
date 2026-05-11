package runtime

import (
	"context"

	"github.com/sdougbrown/avenor/internal/events"
)

// Provider is the interface that all ACP runtime backends must implement.
type Provider interface {
	Start(ctx context.Context, opts StartOptions) (Session, error)
	Resume(ctx context.Context, sessionID string) (Session, error)
	Prompt(ctx context.Context, sessionID string, prompt string) error
	Cancel(ctx context.Context, sessionID string) error
	Events(ctx context.Context, sessionID string) (<-chan events.Event, error)
	AnswerPermission(ctx context.Context, sessionID string, requestID string, response PermissionResponse) error
	Capabilities(ctx context.Context) (Capabilities, error)
}

// StartOptions holds options for starting a new session.
type StartOptions struct{}

// Session represents an active ACP session.
type Session struct{}

// PermissionResponse is the response to a permission request.
type PermissionResponse struct{}

// Capabilities describes what a runtime backend supports.
type Capabilities struct{}
