package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sdougbrown/avenor/internal/events"
	"github.com/sdougbrown/avenor/internal/permission"
	"github.com/sdougbrown/avenor/internal/runtime"
)

func TestSelectPermissionOption(t *testing.T) {
	opts := func(pairs ...string) []any {
		out := make([]any, 0, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			out = append(out, map[string]any{"optionId": pairs[i], "kind": pairs[i+1]})
		}
		return out
	}

	tests := []struct {
		name    string
		options []any
		approve bool
		want    string
		wantErr bool
	}{
		{
			name:    "approve picks allow-kind",
			options: opts("deny", "reject", "allow", "allow"),
			approve: true,
			want:    "allow",
		},
		{
			name:    "reject picks reject-kind",
			options: opts("allow", "allow", "deny", "reject"),
			approve: false,
			want:    "deny",
		},
		{
			name:    "unknown kind returns error",
			options: opts("x1", "other"),
			approve: true,
			wantErr: true,
		},
		{
			name:    "empty options returns error",
			options: nil,
			approve: true,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectPermissionOption(tt.options, tt.approve)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("selectPermissionOption() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("selectPermissionOption() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("selectPermissionOption(approve=%v) = %q, want %q", tt.approve, got, tt.want)
			}
		})
	}
}

func TestEffectivePermissionHandlerAutoApproveSkipsDerivedSentinelHandler(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "run.done")

	if got := effectivePermissionHandler(sentinel, "", true); got != "" {
		t.Fatalf("effectivePermissionHandler(autoApprove=true) = %q, want empty", got)
	}
	if got := effectivePermissionHandler(sentinel, "", false); got != "file:"+derivePermBase(sentinel) {
		t.Fatalf("effectivePermissionHandler(autoApprove=false) = %q, want derived file handler", got)
	}
	if got := effectivePermissionHandler(sentinel, "file:/tmp/custom.perm", true); got != "file:/tmp/custom.perm" {
		t.Fatalf("effectivePermissionHandler(explicit, autoApprove=true) = %q, want explicit handler", got)
	}
}

type cliFakeProvider struct {
	answerSessionID string
	answerRequestID string
	answerResponse  runtime.PermissionResponse
}

func (f *cliFakeProvider) Start(ctx context.Context, opts runtime.StartOptions) (runtime.Session, error) {
	return runtime.Session{}, nil
}

func (f *cliFakeProvider) Resume(ctx context.Context, sessionID string) (runtime.Session, error) {
	return runtime.Session{}, nil
}

func (f *cliFakeProvider) Prompt(ctx context.Context, sessionID string, prompt string) error {
	return nil
}

func (f *cliFakeProvider) Cancel(ctx context.Context, sessionID string) error {
	return nil
}

func (f *cliFakeProvider) Events(ctx context.Context, sessionID string) (<-chan events.Event, error) {
	return nil, nil
}

func (f *cliFakeProvider) AnswerPermission(ctx context.Context, sessionID string, requestID string, response runtime.PermissionResponse) error {
	f.answerSessionID = sessionID
	f.answerRequestID = requestID
	f.answerResponse = response
	return nil
}

func (f *cliFakeProvider) Capabilities(ctx context.Context) (runtime.Capabilities, error) {
	return runtime.Capabilities{}, nil
}

func TestWaitForSessionAutoApproveAnswersAllowKindAndOrdersEvents(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.ndjson")
	writer, err := newEventWriter(eventsPath)
	if err != nil {
		t.Fatalf("newEventWriter: %v", err)
	}

	eventCh := make(chan events.Event, 2)
	eventCh <- events.Event{
		Event:     "permission.request",
		SessionID: "ses_1",
		Fields: map[string]any{
			"request_id": "req_1",
			"question":   "Allow write?",
			"options": []any{
				map[string]any{"optionId": "deny", "kind": "reject"},
				map[string]any{"optionId": "allow", "kind": "allow"},
			},
		},
	}
	eventCh <- events.Event{
		Event:     "session.end",
		SessionID: "ses_1",
		Fields:    map[string]any{"stop_reason": "end_turn"},
	}
	close(eventCh)
	promptDone := make(chan error, 1)
	promptDone <- nil

	provider := &cliFakeProvider{}
	var stderr strings.Builder
	code := waitForSession(context.Background(), provider, writer, nil, eventCh, promptDone, "ses_1", "run_1", "review", true, nil, &stderr)
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatalf("close writer: %v", closeErr)
	}
	if code != 0 {
		t.Fatalf("waitForSession() = %d, want 0; stderr=%s", code, stderr.String())
	}
	if provider.answerSessionID != "ses_1" || provider.answerRequestID != "req_1" {
		t.Fatalf("answered session=%q request=%q", provider.answerSessionID, provider.answerRequestID)
	}
	if provider.answerResponse.OptionID != "allow" {
		t.Fatalf("answer option = %q, want allow", provider.answerResponse.OptionID)
	}

	got := readEventLogForTest(t, eventsPath)
	requestIndex := -1
	responseAfterRequest := false
	for i, ev := range got {
		if ev.Event == "permission.request" {
			requestIndex = i
		}
		// With the goroutine-based auto-answer, AnswerPermission runs concurrently
		// with event draining, so session.end may arrive before the permissionDone
		// case fires. The reliable audit signal is permission.response, not
		// agent.status working (which is suppressed when the session is already done).
		if requestIndex >= 0 && i > requestIndex && ev.Event == "permission.response" {
			responseAfterRequest = true
		}
	}
	if requestIndex < 0 {
		t.Fatalf("permission.request not written: %+v", got)
	}
	if !responseAfterRequest {
		t.Fatalf("permission.response not emitted after permission.request: %+v", got)
	}
	if got[0].Fields["run_label"] != "review" {
		t.Fatalf("run_label = %v, want review", got[0].Fields["run_label"])
	}
}

func readEventLogForTest(t *testing.T, path string) []events.Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	out := make([]events.Event, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var ev events.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode event %q: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}

func TestRunCleansSentinelFilesBetweenRetries(t *testing.T) {
	oldRunAttempt := runAttempt
	oldRetryAfter := retryAfter
	t.Cleanup(func() {
		runAttempt = oldRunAttempt
		retryAfter = oldRetryAfter
	})

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	eventsPath := filepath.Join(dir, "events.ndjson")
	sentinelPath := filepath.Join(dir, "run.done")
	permBase := filepath.Join(dir, "run.perm")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	attempts := 0
	runAttempt = func(
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
		attempts++
		switch attempts {
		case 1:
			if err := os.WriteFile(permBase+".req", []byte("stale request"), 0o600); err != nil {
				t.Fatalf("write stale req: %v", err)
			}
			if err := os.WriteFile(permBase+".req.response", []byte("stale response"), 0o600); err != nil {
				t.Fatalf("write stale response: %v", err)
			}
			return attemptResult{exitCode: 1, sessionID: "ses_1"}
		case 2:
			for _, path := range []string{permBase + ".req", permBase + ".req.response"} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("%s was not cleaned before retry", path)
				}
			}
			return attemptResult{exitCode: 0, sessionID: "ses_2"}
		default:
			t.Fatalf("unexpected attempt %d", attempts)
			return attemptResult{exitCode: 1}
		}
	}
	retryAfter = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}

	var stderr strings.Builder
	exitCode := run([]string{
		"--prompt-file", promptPath,
		"--on-event", eventsPath,
		"--sentinel-file", sentinelPath,
		"--max-retries", "1",
	}, func(string) string { return "" }, &stderr)
	if exitCode != 0 {
		t.Fatalf("run() = %d, want 0; stderr=%s", exitCode, stderr.String())
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestWriteRetryEventReturnsWriterError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	writer, err := newEventWriter(path)
	if err != nil {
		t.Fatalf("newEventWriter: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if err := writeRetryEvent(writer, "ses_1", "run_1", 2, 3); err == nil {
		t.Fatal("writeRetryEvent() error = nil, want error")
	}
}

func TestNewEventWriterNullPath(t *testing.T) {
	w, err := newEventWriter("")
	if err != nil {
		t.Fatalf("newEventWriter(\"\") error = %v", err)
	}
	defer w.Close()
	// Write should succeed and silently discard.
	if err := w.Write(makeEvent("agent.status", nil)); err != nil {
		t.Fatalf("Write() to null writer error = %v", err)
	}
}

func TestDiscoverServerSelection(t *testing.T) {
	env := func(key string) string {
		if key == "AVENOR_OPENCODE_URL" {
			return "http://env.example"
		}
		return ""
	}

	tests := []struct {
		name   string
		flag   string
		env    func(string) string
		source string
		mode   string
		url    string
	}{
		{
			name:   "flag wins",
			flag:   "http://flag.example",
			env:    env,
			source: "flag",
			mode:   "external",
			url:    "http://flag.example",
		},
		{
			name:   "env wins over fallback",
			env:    env,
			source: "env",
			mode:   "external",
			url:    "http://env.example",
		},
		{
			name:   "subprocess fallback",
			env:    func(string) string { return "" },
			source: "subprocess",
			mode:   "subprocess",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DiscoverServer(tt.flag, tt.env)
			if got.Source != tt.source || got.Mode != tt.mode || got.URL != tt.url {
				t.Fatalf("DiscoverServer() = %+v, want source=%s mode=%s url=%s", got, tt.source, tt.mode, tt.url)
			}
		})
	}
}

func TestOverrideStopReason(t *testing.T) {
	tests := []struct {
		name      string
		server    string
		cancelled bool
		timedOut  bool
		want      string
	}{
		{name: "normal", server: "end_turn", want: "end_turn"},
		{name: "cancelled", server: "end_turn", cancelled: true, want: "cancelled"},
		{name: "timeout", server: "end_turn", timedOut: true, want: "timeout"},
		{name: "timeout wins", server: "end_turn", cancelled: true, timedOut: true, want: "timeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OverrideStopReason(tt.server, tt.cancelled, tt.timedOut); got != tt.want {
				t.Fatalf("OverrideStopReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

// errorFakeProvider is a variant of cliFakeProvider whose AnswerPermission
// returns a fixed error. Used to test the error path in the auto-answer goroutine.
type errorFakeProvider struct {
	cliFakeProvider
	answerErr error
}

func (f *errorFakeProvider) AnswerPermission(ctx context.Context, sessionID, requestID string, response runtime.PermissionResponse) error {
	return f.answerErr
}

// blockingFakeProvider is a variant of cliFakeProvider whose AnswerPermission
// blocks until unblockAnswer is closed. Used to test that the auto-answer
// goroutine does not wedge the event loop.
type blockingFakeProvider struct {
	cliFakeProvider
	unblockAnswer chan struct{}
	answered      chan struct{} // closed when AnswerPermission is called
	returned      chan struct{} // closed when AnswerPermission is about to return
}

func newBlockingFakeProvider() *blockingFakeProvider {
	return &blockingFakeProvider{
		unblockAnswer: make(chan struct{}),
		answered:      make(chan struct{}),
		returned:      make(chan struct{}),
	}
}

func (f *blockingFakeProvider) AnswerPermission(ctx context.Context, sessionID, requestID string, response runtime.PermissionResponse) error {
	f.cliFakeProvider.answerSessionID = sessionID
	f.cliFakeProvider.answerRequestID = requestID
	f.cliFakeProvider.answerResponse = response
	close(f.answered) // signal that we have been called
	<-f.unblockAnswer // block until the test unblocks us
	close(f.returned) // signal that we are about to return
	return nil
}

// TestAutoAnswerGoroutineDoesNotBlockEventLoop verifies that a slow
// AnswerPermission call does not prevent subsequent events from draining.
// It injects a blocking provider, sends a permission.request followed by
// session.end, and asserts that session.end is processed before the
// AnswerPermission goroutine returns.
func TestAutoAnswerGoroutineDoesNotBlockEventLoop(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.ndjson")
	writer, err := newEventWriter(eventsPath)
	if err != nil {
		t.Fatalf("newEventWriter: %v", err)
	}

	eventCh := make(chan events.Event, 3)
	eventCh <- events.Event{
		Event:     "permission.request",
		SessionID: "ses_1",
		Fields: map[string]any{
			"request_id": "req_1",
			"options": []any{
				map[string]any{"optionId": "allow", "kind": "allow"},
			},
		},
	}

	provider := newBlockingFakeProvider()
	promptDone := make(chan error, 1)

	var wg sync.WaitGroup
	wg.Add(1)
	var exitCode int
	go func() {
		defer wg.Done()
		exitCode = waitForSession(context.Background(), provider, writer, nil, eventCh, promptDone, "ses_1", "run_1", "", true, nil, io.Discard)
	}()

	// Wait until AnswerPermission has been called (goroutine is now blocked inside it).
	select {
	case <-provider.answered:
	case <-time.After(5 * time.Second):
		t.Fatal("AnswerPermission was not called within 5 seconds")
	}

	// Now push session.end — this must drain even while AnswerPermission is blocked.
	eventCh <- events.Event{
		Event:     "session.end",
		SessionID: "ses_1",
		Fields:    map[string]any{"stop_reason": "end_turn"},
	}
	promptDone <- nil
	close(eventCh)

	// Confirm the loop is not wedged by verifying it reads the session.end.
	// The goroutine cannot unblock (close(provider.unblockAnswer) below) until
	// AFTER sessionEndSeen fires, so there is no race: the goroutine is held
	// blocked in AnswerPermission for the entire duration of the poll.
	sessionEndSeen := make(chan struct{})
	go func() {
		// Poll the event log briefly. Each iteration is 50ms; 50 iterations = 2.5s.
		for i := 0; i < 50; i++ {
			time.Sleep(50 * time.Millisecond)
			data, _ := os.ReadFile(eventsPath)
			if strings.Contains(string(data), "session.end") {
				close(sessionEndSeen)
				return
			}
		}
	}()

	select {
	case <-sessionEndSeen:
		// Good — session.end was written while AnswerPermission was still blocked.
	case <-time.After(5 * time.Second):
		t.Fatal("session.end was not written within 5 seconds; event loop appears blocked")
	}

	// Unblock the goroutine and wait for the loop to finish.
	close(provider.unblockAnswer)
	wg.Wait()
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("waitForSession() = %d, want 0", exitCode)
	}
}

// TestAutoAnswerMissingRequestIDEmitsErrorNotWorking verifies that a
// permission.request without a request_id does NOT transition the agent to
// "working" and DOES emit an avenor.error event.
func TestAutoAnswerMissingRequestIDEmitsErrorNotWorking(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.ndjson")
	writer, err := newEventWriter(eventsPath)
	if err != nil {
		t.Fatalf("newEventWriter: %v", err)
	}

	eventCh := make(chan events.Event, 3)
	// permission.request without request_id
	eventCh <- events.Event{
		Event:     "permission.request",
		SessionID: "ses_1",
		Fields: map[string]any{
			// intentionally no "request_id"
			"options": []any{
				map[string]any{"optionId": "allow", "kind": "allow"},
			},
		},
	}
	// session.end so the loop can terminate
	eventCh <- events.Event{
		Event:     "session.end",
		SessionID: "ses_1",
		Fields:    map[string]any{"stop_reason": "end_turn"},
	}
	close(eventCh)

	promptDone := make(chan error, 1)
	promptDone <- nil

	provider := &cliFakeProvider{}
	var stderr strings.Builder
	code := waitForSession(context.Background(), provider, writer, nil, eventCh, promptDone, "ses_1", "run_1", "", true, nil, &stderr)
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if code != 0 {
		t.Fatalf("waitForSession() = %d, want 0", code)
	}
	// AnswerPermission must NOT have been called.
	if provider.answerRequestID != "" {
		t.Fatalf("AnswerPermission was called with requestID=%q, want no call", provider.answerRequestID)
	}

	got := readEventLogForTest(t, eventsPath)
	var hasError, hasWorking bool
	for _, ev := range got {
		if ev.Event == "avenor.error" {
			msg, _ := ev.Fields["message"].(string)
			if strings.Contains(msg, "missing request_id") {
				hasError = true
			}
		}
		if ev.Event == "agent.status" && ev.Fields["phase"] == "working" {
			hasWorking = true
		}
	}
	if !hasError {
		t.Fatalf("expected avenor.error with 'missing request_id'; events: %+v", got)
	}
	if hasWorking {
		t.Fatalf("agent.status working emitted despite missing request_id; events: %+v", got)
	}
}

// TestAutoAnswerEmitsPermissionResponse verifies that a successful auto-answer
// emits a permission.response event with the correct fields.
func TestAutoAnswerEmitsPermissionResponse(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.ndjson")
	writer, err := newEventWriter(eventsPath)
	if err != nil {
		t.Fatalf("newEventWriter: %v", err)
	}

	eventCh := make(chan events.Event, 3)
	eventCh <- events.Event{
		Event:     "permission.request",
		SessionID: "ses_1",
		Fields: map[string]any{
			"request_id": "req_42",
			"options": []any{
				map[string]any{"optionId": "deny_it", "kind": "reject"},
				map[string]any{"optionId": "allow_it", "kind": "allow"},
			},
		},
	}
	eventCh <- events.Event{
		Event:     "session.end",
		SessionID: "ses_1",
		Fields:    map[string]any{"stop_reason": "end_turn"},
	}
	close(eventCh)

	promptDone := make(chan error, 1)
	promptDone <- nil

	provider := &cliFakeProvider{}
	var stderr strings.Builder
	code := waitForSession(context.Background(), provider, writer, nil, eventCh, promptDone, "ses_1", "run_1", "mylabel", true, nil, &stderr)
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if code != 0 {
		t.Fatalf("waitForSession() = %d, want 0; stderr=%s", code, stderr.String())
	}

	got := readEventLogForTest(t, eventsPath)
	var resp *events.Event
	for i := range got {
		if got[i].Event == "permission.response" {
			resp = &got[i]
			break
		}
	}
	if resp == nil {
		t.Fatalf("permission.response not found in events: %+v", got)
	}
	if resp.Fields["request_id"] != "req_42" {
		t.Errorf("request_id = %v, want req_42", resp.Fields["request_id"])
	}
	if resp.Fields["option_id"] != "allow_it" {
		t.Errorf("option_id = %v, want allow_it", resp.Fields["option_id"])
	}
	if resp.Fields["kind"] != "allow" {
		t.Errorf("kind = %v, want allow", resp.Fields["kind"])
	}
	if resp.Fields["source"] != "avenor" {
		t.Errorf("source = %v, want avenor", resp.Fields["source"])
	}
	if resp.Fields["run_id"] != "run_1" {
		t.Errorf("run_id = %v, want run_1", resp.Fields["run_id"])
	}
	if resp.Fields["run_label"] != "mylabel" {
		t.Errorf("run_label = %v, want mylabel", resp.Fields["run_label"])
	}
	if ts, ok := resp.Fields["ts"].(float64); !ok || ts <= 0 {
		t.Errorf("ts missing or non-positive: %v", resp.Fields["ts"])
	}
}

// TestAutoAnswerAnswerPermissionErrorEmitsError verifies that when
// AnswerPermission returns an error, the loop emits an avenor.error event
// and exits with code 1. A session.end is included so the loop waits for the
// goroutine to complete rather than exiting early on the closed eventCh.
func TestAutoAnswerAnswerPermissionErrorEmitsError(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.ndjson")
	writer, err := newEventWriter(eventsPath)
	if err != nil {
		t.Fatalf("newEventWriter: %v", err)
	}

	eventCh := make(chan events.Event, 3)
	eventCh <- events.Event{
		Event:     "permission.request",
		SessionID: "ses_1",
		Fields: map[string]any{
			"request_id": "req_err",
			"options": []any{
				map[string]any{"optionId": "allow", "kind": "allow"},
			},
		},
	}
	// Include session.end so finalStopReason is set and the loop waits
	// for permissionDone before deciding to exit.
	eventCh <- events.Event{
		Event:     "session.end",
		SessionID: "ses_1",
		Fields:    map[string]any{"stop_reason": "end_turn"},
	}
	close(eventCh)

	promptDone := make(chan error, 1)
	promptDone <- nil

	provider := &errorFakeProvider{answerErr: fmt.Errorf("backend unavailable")}
	var stderr strings.Builder
	code := waitForSession(context.Background(), provider, writer, nil, eventCh, promptDone, "ses_1", "run_1", "", true, nil, &stderr)
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatalf("close writer: %v", closeErr)
	}
	if code != 1 {
		t.Fatalf("waitForSession() = %d, want 1 (error exit)", code)
	}

	got := readEventLogForTest(t, eventsPath)
	var hasError bool
	for _, ev := range got {
		if ev.Event == "avenor.error" {
			msg, _ := ev.Fields["message"].(string)
			if strings.Contains(msg, "backend unavailable") {
				hasError = true
			}
		}
	}
	if !hasError {
		t.Fatalf("expected avenor.error with 'backend unavailable'; events: %+v", got)
	}
}

// TestAutoAnswerWorkingStatusEmittedOnPermissionResume verifies that when
// AnswerPermission completes before session.end, the event loop emits
// agent.status working to signal the agent has resumed.
// The test uses file-polling to confirm the working event is written before
// sending session.end, ensuring the permissionDone case fires first.
func TestAutoAnswerWorkingStatusEmittedOnPermissionResume(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.ndjson")
	writer, err := newEventWriter(eventsPath)
	if err != nil {
		t.Fatalf("newEventWriter: %v", err)
	}

	provider := newBlockingFakeProvider()
	eventCh := make(chan events.Event, 4)
	eventCh <- events.Event{
		Event:     "permission.request",
		SessionID: "ses_1",
		Fields: map[string]any{
			"request_id": "req_resume",
			"options": []any{
				map[string]any{"optionId": "allow", "kind": "allow"},
			},
		},
	}

	promptDone := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	var exitCode int
	go func() {
		defer wg.Done()
		exitCode = waitForSession(context.Background(), provider, writer, nil, eventCh, promptDone, "ses_1", "run_1", "", true, nil, io.Discard)
	}()

	// Wait until AnswerPermission is called, then unblock it so the
	// permissionDone case can fire and emit agent.status working.
	select {
	case <-provider.answered:
	case <-time.After(5 * time.Second):
		t.Fatal("AnswerPermission not called within 5 seconds")
	}
	close(provider.unblockAnswer)

	// Poll for agent.status working in the event log. This confirms the
	// permissionDone case has fired and written the status before we send
	// session.end (which would advance the tracker to phaseDone and suppress
	// the working emission).
	workingSeen := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			time.Sleep(25 * time.Millisecond)
			data, _ := os.ReadFile(eventsPath)
			if strings.Contains(string(data), `"working"`) {
				close(workingSeen)
				return
			}
		}
	}()
	select {
	case <-workingSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("agent.status working not written within 5 seconds")
	}

	eventCh <- events.Event{
		Event:     "session.end",
		SessionID: "ses_1",
		Fields:    map[string]any{"stop_reason": "end_turn"},
	}
	promptDone <- nil
	close(eventCh)

	wg.Wait()
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatalf("close writer: %v", closeErr)
	}
	if exitCode != 0 {
		t.Fatalf("waitForSession() = %d, want 0", exitCode)
	}

	got := readEventLogForTest(t, eventsPath)
	// Find permission.request index, then verify agent.status working follows it.
	requestIdx := -1
	var workingAfterRequest bool
	for i, ev := range got {
		if ev.Event == "permission.request" {
			requestIdx = i
		}
		if requestIdx >= 0 && ev.Event == "agent.status" && ev.Fields["phase"] == "working" {
			workingAfterRequest = true
		}
	}
	if requestIdx < 0 {
		t.Fatalf("permission.request not written: %+v", got)
	}
	if !workingAfterRequest {
		t.Fatalf("agent.status working not emitted after permission resolved; events: %+v", got)
	}
}

// TestAutoAnswerSecondRequestWhilePendingEmitsErrorAndExits verifies that
// receiving a second permission.request while one is already in-flight causes
// the loop to emit an avenor.error and exit with code 1.
func TestAutoAnswerSecondRequestWhilePendingEmitsErrorAndExits(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.ndjson")
	writer, err := newEventWriter(eventsPath)
	if err != nil {
		t.Fatalf("newEventWriter: %v", err)
	}

	provider := newBlockingFakeProvider()
	eventCh := make(chan events.Event, 4)
	eventCh <- events.Event{
		Event:     "permission.request",
		SessionID: "ses_1",
		Fields: map[string]any{
			"request_id": "req_first",
			"options": []any{
				map[string]any{"optionId": "allow", "kind": "allow"},
			},
		},
	}

	promptDone := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	var exitCode int
	go func() {
		defer wg.Done()
		exitCode = waitForSession(context.Background(), provider, writer, nil, eventCh, promptDone, "ses_1", "run_1", "", true, nil, io.Discard)
	}()

	// Wait until the first AnswerPermission call is in-flight (blocking).
	select {
	case <-provider.answered:
	case <-time.After(5 * time.Second):
		t.Fatal("AnswerPermission not called within 5 seconds")
	}

	// Send a second permission.request while the first is still pending.
	eventCh <- events.Event{
		Event:     "permission.request",
		SessionID: "ses_1",
		Fields: map[string]any{
			"request_id": "req_second",
			"options": []any{
				map[string]any{"optionId": "allow", "kind": "allow"},
			},
		},
	}
	// Unblock so the loop can proceed to process the second request.
	close(provider.unblockAnswer)

	wg.Wait()
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatalf("close writer: %v", closeErr)
	}
	if exitCode != 1 {
		t.Fatalf("waitForSession() = %d, want 1 (duplicate permission guard)", exitCode)
	}

	got := readEventLogForTest(t, eventsPath)
	var hasError bool
	for _, ev := range got {
		if ev.Event == "avenor.error" {
			msg, _ := ev.Fields["message"].(string)
			if strings.Contains(msg, "already pending") {
				hasError = true
			}
		}
	}
	if !hasError {
		t.Fatalf("expected avenor.error about 'already pending'; events: %+v", got)
	}
}
