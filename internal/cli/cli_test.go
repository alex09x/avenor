package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	workingAfterRequest := false
	for i, ev := range got {
		if ev.Event == "permission.request" {
			requestIndex = i
		}
		if requestIndex >= 0 && i > requestIndex && ev.Event == "agent.status" && ev.Fields["phase"] == "working" {
			workingAfterRequest = true
		}
	}
	if requestIndex < 0 {
		t.Fatalf("permission.request not written: %+v", got)
	}
	if !workingAfterRequest {
		t.Fatalf("working status was not emitted after permission.request: %+v", got)
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
