package cli

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/alex09x/avenor/agyclient/events"
)

func TestWaitForSession_ProgressTimeout(t *testing.T) {
	eventCh := make(chan events.Event)
	promptDone := make(chan error)

	provider := &cliFakeProvider{}
	writer, err := NewEventWriter("")
	if err != nil {
		t.Fatalf("NewEventWriter: %v", err)
	}
	defer writer.Close()

	cfg := SessionWaitConfig{
		EventCh:         eventCh,
		PromptDone:      promptDone,
		SessionID:       "ses_1",
		RunID:           "run_1",
		ProgressTimeout: 200 * time.Millisecond,
	}
	deps := SessionWaitDeps{
		Writer: writer,
		Stderr: io.Discard,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result := WaitForSession(ctx, provider, cfg, deps)

	if result.ExitCode != 124 {
		t.Errorf("ExitCode = %d, want 124", result.ExitCode)
	}
	if result.StopReason != "progress_timeout" {
		t.Errorf("StopReason = %q, want %q", result.StopReason, "progress_timeout")
	}
}

func TestWaitForSession_ProgressTimeoutResets(t *testing.T) {
	eventCh := make(chan events.Event)

	provider := &cliFakeProvider{}
	writer, err := NewEventWriter("")
	if err != nil {
		t.Fatalf("NewEventWriter: %v", err)
	}
	defer writer.Close()

	promptDone := make(chan error, 1)

	cfg := SessionWaitConfig{
		EventCh:         eventCh,
		PromptDone:      promptDone,
		SessionID:       "ses_1",
		RunID:           "run_1",
		ProgressTimeout: 100 * time.Millisecond,
	}
	deps := SessionWaitDeps{
		Writer: writer,
		Stderr: io.Discard,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var result sessionResult
	done := make(chan struct{})
	go func() {
		result = WaitForSession(ctx, provider, cfg, deps)
		close(done)
	}()

	// Send an event after 60ms — well before the 100ms timeout would fire.
	time.Sleep(60 * time.Millisecond)
	eventCh <- events.Event{
		Event:     "agent.status",
		SessionID: "ses_1",
		Fields:    map[string]any{"phase": "working"},
	}

	// Wait another 60ms (total ~120ms). If the timer had NOT reset at the
	// first event, it would have fired at 100ms. Instead it reset to fire at
	// 160ms. We confirm it survived by sending session.end at 120ms.
	time.Sleep(60 * time.Millisecond)
	eventCh <- events.Event{
		Event:     "session.end",
		SessionID: "ses_1",
		Fields:    map[string]any{"stop_reason": "end_turn"},
	}
	promptDone <- nil
	close(eventCh)

	<-done

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.StopReason == "progress_timeout" {
		t.Errorf("StopReason = %q, unexpected progress_timeout (timer did not reset)", result.StopReason)
	}
}

func TestWaitForSession_ProgressTimeoutDisabled(t *testing.T) {
	eventCh := make(chan events.Event)
	promptDone := make(chan error, 1)
	promptDone <- errors.New("prompt failed")

	provider := &cliFakeProvider{}
	writer, err := NewEventWriter("")
	if err != nil {
		t.Fatalf("NewEventWriter: %v", err)
	}
	defer writer.Close()

	cfg := SessionWaitConfig{
		EventCh:         eventCh,
		PromptDone:      promptDone,
		SessionID:       "ses_1",
		RunID:           "run_1",
		ProgressTimeout: 0,
	}
	deps := SessionWaitDeps{
		Writer: writer,
		Stderr: io.Discard,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result := WaitForSession(ctx, provider, cfg, deps)

	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
	}
	if result.ExitCode == 124 {
		t.Error("ExitCode is 124, progress timeout should be disabled")
	}
}
