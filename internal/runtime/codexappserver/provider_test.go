package codexappserver

import (
	"context"
	"testing"
	"time"

	"github.com/sdougbrown/avenor/internal/runtime"
)

func TestCapabilities(t *testing.T) {
	p := NewWithOptions(runtime.StartOptions{})
	caps, err := p.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if caps.Backend != "codex-app-server" {
		t.Errorf("Backend = %q, want codex-app-server", caps.Backend)
	}
	if !caps.Permissions {
		t.Error("Permissions should be true")
	}
	if !caps.Resume {
		t.Error("Resume should be true")
	}
	if caps.ExternalServerURL {
		t.Error("ExternalServerURL should be false")
	}
	if caps.SubprocessDiscovery {
		t.Error("SubprocessDiscovery should be false")
	}
	if !caps.ModelSelection {
		t.Error("ModelSelection should be true")
	}
}

func TestResumeNoop(t *testing.T) {
	p := NewWithOptions(runtime.StartOptions{})
	p.threads["th_existing"] = "th_existing"

	sess, err := p.Resume(context.Background(), "th_existing")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if sess.SessionID != "th_existing" {
		t.Errorf("SessionID = %q, want th_existing", sess.SessionID)
	}
	if sess.Backend != "codex-app-server" {
		t.Errorf("Backend = %q", sess.Backend)
	}
}

func TestResumeEmptyID(t *testing.T) {
	p := NewWithOptions(runtime.StartOptions{})
	_, err := p.Resume(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty session id")
	}
}

func TestAnswerPermissionNotStarted(t *testing.T) {
	p := NewWithOptions(runtime.StartOptions{})
	err := p.AnswerPermission(context.Background(), "ses", "req", runtime.PermissionResponse{Allow: true})
	if err == nil {
		t.Fatal("expected error for not-started provider")
	}
}

func TestAnswerPermissionEmptyRequestID(t *testing.T) {
	p := NewWithOptions(runtime.StartOptions{})
	p.client = nil // not started
	err := p.AnswerPermission(context.Background(), "ses", "", runtime.PermissionResponse{Allow: true})
	if err == nil {
		t.Fatal("expected error for empty request id")
	}
}

func TestEventsNotStarted(t *testing.T) {
	p := NewWithOptions(runtime.StartOptions{})
	_, err := p.Events(context.Background(), "ses")
	if err == nil {
		t.Fatal("expected error for not-started provider")
	}
}

func TestCancelNoActiveTurn(t *testing.T) {
	p := NewWithOptions(runtime.StartOptions{})
	err := p.Cancel(context.Background(), "ses_unknown")
	if err == nil {
		t.Fatal("expected error for no active turn")
	}
}

func TestCloseNotStarted(t *testing.T) {
	p := NewWithOptions(runtime.StartOptions{})
	if err := p.Close(); err != nil {
		t.Fatalf("Close on nil client: %v", err)
	}
}

// TestProviderPipeRoundtrip tests the full Prompt flow via pipe simulation.
// This exercises: request routing, response routing, turn waiter notification.
func TestProviderPipeRoundtrip(t *testing.T) {
	c, wOut, rIn := fakeClient()
	p := NewWithOptions(runtime.StartOptions{})
	p.client = c
	p.threads["th_pipe"] = "th_pipe"

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.Prompt(context.Background(), "th_pipe", "hello")
	}()

	// Server side: read request, respond with turn ID, then send turn/completed
	// immediately to exercise the buffered completion path.
	msg, err := readLine(rIn)
	if err != nil {
		t.Fatalf("read turn/start: %v", err)
	}

	writeLine(wOut, map[string]any{
		"id": msg.ID,
		"result": map[string]any{
			"turn": map[string]any{"id": "turn_pipe"},
		},
	})
	writeLine(wOut, map[string]any{
		"method": "turn/completed",
		"params": map[string]any{
			"threadId": "th_pipe",
			"turn": map[string]any{
				"id":     "turn_pipe",
				"status": "completed",
			},
		},
	})

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Prompt: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Prompt")
	}
}

func TestProviderImplementsInterface(t *testing.T) {
	var _ runtime.Provider = (*Provider)(nil)
}
