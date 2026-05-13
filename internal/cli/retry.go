package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/sdougbrown/avenor/internal/events"
	"github.com/sdougbrown/avenor/internal/permission"
	"github.com/sdougbrown/avenor/internal/runtime"
	"github.com/sdougbrown/avenor/internal/runtime/opencodeacp"
)

// attemptResult holds the outcome of a single session attempt.
type attemptResult struct {
	exitCode  int
	sessionID string
}

// runSingleAttempt creates a fresh provider, starts or resumes a session,
// sends the prompt, and runs the event loop. The provider is closed before
// returning regardless of outcome.
func runSingleAttempt(
	ctx context.Context,
	startOptions runtime.StartOptions,
	resumeID string,
	writer *eventWriter,
	fileHandler *permission.FileHandler,
	prompt string,
	runID string,
	runLabel string,
	autoApprove bool,
	timer <-chan time.Time,
	stderr io.Writer,
) attemptResult {
	provider := opencodeacp.NewWithOptions(startOptions)
	defer func() {
		if closer, ok := provider.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}()

	session, err := startSession(ctx, provider, startOptions, resumeID)
	if err != nil {
		fmt.Fprintf(stderr, "avenor: start session: %v\n", err)
		return attemptResult{exitCode: 1}
	}

	eventCtx, cancelEvents := context.WithCancel(ctx)
	defer cancelEvents()

	eventCh, err := provider.Events(eventCtx, session.SessionID)
	if err != nil {
		fmt.Fprintf(stderr, "avenor: subscribe events: %v\n", err)
		return attemptResult{exitCode: 1, sessionID: session.SessionID}
	}

	promptDone := make(chan error, 1)
	go func() {
		promptDone <- provider.Prompt(ctx, session.SessionID, prompt)
	}()

	exitCode := waitForSession(ctx, provider, writer, fileHandler, eventCh, promptDone, session.SessionID, runID, runLabel, autoApprove, timer, stderr)
	return attemptResult{exitCode: exitCode, sessionID: session.SessionID}
}

// backoffDelay returns the retry sleep duration for the nth attempt (1-indexed).
// Starts at 2s, doubles each time, capped at 30s.
func backoffDelay(attempt int) time.Duration {
	seconds := 2 << uint(attempt-1)
	if seconds > 30 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}

// writeRetryEvent emits an avenor.retry event to the writer.
func writeRetryEvent(writer *eventWriter, sessionID, runID string, attempt, maxRetries int, runLabel ...string) error {
	fields := map[string]any{
		"attempt":     attempt,
		"max_retries": maxRetries,
		"ts":          time.Now().UnixMilli(),
	}
	if runID != "" {
		fields["run_id"] = runID
	}
	if len(runLabel) > 0 && runLabel[0] != "" {
		fields["run_label"] = runLabel[0]
	}
	return writer.Write(events.Event{
		Event:     "avenor.retry",
		SessionID: sessionID,
		Fields:    fields,
	})
}
