package stable

import (
	"context"
	"testing"
	"time"

	"github.com/sdougbrown/avenor/internal/events"
	"github.com/sdougbrown/avenor/internal/looprunner"
	"github.com/sdougbrown/avenor/internal/runtime"
)

func TestNewSupervisor(t *testing.T) {
	sup := NewSupervisor(Config{
		ControlSocket:   "/tmp/test-stable.sock",
		MaxRuntimes:     4,
		ShutdownTimeout: 0,
	})
	if sup == nil {
		t.Fatal("NewSupervisor returned nil")
	}
	if sup.config.MaxRuntimes != 4 {
		t.Errorf("MaxRuntimes = %d, want 4", sup.config.MaxRuntimes)
	}
}

func TestStableHandlerNoRuntimes(t *testing.T) {
	sup := NewSupervisor(Config{
		ControlSocket: "/tmp/test-stable-none.sock",
		MaxRuntimes:   4,
	})

	// List with no runtimes
	list := sup.List()
	runtimes, ok := list.([]map[string]any)
	if !ok {
		t.Fatalf("List() returned %T, want []map[string]any", list)
	}
	if len(runtimes) != 0 {
		t.Errorf("List() = %d runtimes, want 0", len(runtimes))
	}

	// Status for nonexistent runtime
	_, err := sup.RuntimeStatus("rt_nonexistent")
	if err == nil {
		t.Fatal("RuntimeStatus for nonexistent runtime should error")
	}

	// Cancel for nonexistent runtime
	err = sup.RuntimeCancel("rt_nonexistent")
	if err == nil {
		t.Fatal("RuntimeCancel for nonexistent runtime should error")
	}

	// Prompt for nonexistent runtime
	err = sup.RuntimePrompt("rt_nonexistent", "hello")
	if err == nil {
		t.Fatal("RuntimePrompt for nonexistent runtime should error")
	}

	// AnswerPermission for nonexistent runtime
	err = sup.RuntimeAnswerPermission("rt_nonexistent", "req_1", "allow")
	if err == nil {
		t.Fatal("RuntimeAnswerPermission for nonexistent runtime should error")
	}
}

func TestShutdownModeValidation(t *testing.T) {
	sup := NewSupervisor(Config{
		ControlSocket: "/tmp/test-shutdown.sock",
		MaxRuntimes:   4,
	})

	if err := sup.Shutdown("graceful"); err != nil {
		t.Errorf("Shutdown graceful: %v", err)
	}
	if err := sup.Shutdown("kill"); err != nil {
		t.Errorf("Shutdown kill: %v", err)
	}
	if err := sup.Shutdown("invalid"); err == nil {
		t.Fatal("Shutdown with invalid mode should error")
	}
}

func TestSpawnParamsValidation(t *testing.T) {
	sup := NewSupervisor(Config{
		ControlSocket: "/tmp/test-spawn-validate.sock",
		MaxRuntimes:   2,
	})

	// Missing prompt and prompt_file
	_, err := sup.spawn(SpawnParams{Dir: "/tmp"})
	if err == nil {
		t.Fatal("spawn with no prompt should error")
	}

	// Missing dir
	_, err = sup.spawn(SpawnParams{Prompt: "hello"})
	// Dir defaults to ".", so this shouldn't error on validation alone
	// It might fail on starting the acp session though
}

func TestSpawnLoopFileFailureCleansReservedRuntime(t *testing.T) {
	sup := NewSupervisor(Config{
		ControlSocket: "/tmp/test-loop-spawn-cleanup.sock",
		MaxRuntimes:   1,
	})

	_, err := sup.spawn(SpawnParams{LoopFile: "/path/does/not/exist.json"})
	if err == nil {
		t.Fatal("spawn with missing loop file should error")
	}
	if got := sup.activeRuntimeCount(); got != 0 {
		t.Fatalf("activeRuntimeCount() = %d, want 0", got)
	}
	if len(sup.runtimes) != 0 {
		t.Fatalf("runtimes = %d, want 0 after failed loop spawn", len(sup.runtimes))
	}
}

func TestActiveRuntimeCountIgnoresCompletedHistory(t *testing.T) {
	sup := NewSupervisor(Config{
		ControlSocket: "/tmp/test-active-count.sock",
		MaxRuntimes:   1,
	})
	sup.runtimes["rt_done"] = &childRuntime{
		id:        "rt_done",
		done:      make(chan struct{}),
		promptCh:  make(chan struct{}, 1),
		completed: true,
	}

	if got := sup.activeRuntimeCount(); got != 0 {
		t.Fatalf("activeRuntimeCount() = %d, want 0 for completed history", got)
	}
}

func TestRuntimePromptRejectsCompletedRuntime(t *testing.T) {
	sup := NewSupervisor(Config{
		ControlSocket: "/tmp/test-prompt-completed.sock",
		MaxRuntimes:   1,
	})
	sup.runtimes["rt_done"] = &childRuntime{
		id:        "rt_done",
		done:      make(chan struct{}),
		promptCh:  make(chan struct{}, 1),
		completed: true,
	}

	if err := sup.RuntimePrompt("rt_done", "hello"); err == nil {
		t.Fatal("RuntimePrompt on completed runtime should error")
	}
}

func TestRuntimeInterruptAndPromptCancelsTurnNotRuntime(t *testing.T) {
	sup := NewSupervisor(Config{
		ControlSocket: "/tmp/test-interrupt-turn.sock",
		MaxRuntimes:   1,
	})
	runtimeCancelled := false
	turnInterrupted := false
	sup.runtimes["rt_1"] = &childRuntime{
		id:          "rt_1",
		done:        make(chan struct{}),
		promptCh:    make(chan struct{}, 1),
		cancelFn:    func() { runtimeCancelled = true },
		interruptFn: func() { turnInterrupted = true },
	}

	if err := sup.RuntimeInterruptAndPrompt("rt_1", "replacement", false); err != nil {
		t.Fatalf("RuntimeInterruptAndPrompt: %v", err)
	}
	if runtimeCancelled {
		t.Fatal("interrupt_and_prompt called runtime cancelFn; it should only cancel the active turn")
	}
	if !turnInterrupted {
		t.Fatal("interrupt_and_prompt did not call interruptFn")
	}
	rt := sup.runtimes["rt_1"]
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.promptQueue) != 1 || rt.promptQueue[0] != "replacement" {
		t.Fatalf("promptQueue = %#v, want replacement queued first", rt.promptQueue)
	}
}

func TestRunChildAttemptUsesInitialSpawnSession(t *testing.T) {
	sup := NewSupervisor(Config{
		ControlSocket: "/tmp/test-initial-session.sock",
		MaxRuntimes:   1,
	})
	provider := &stableFakeProvider{
		events: make(chan events.Event, 1),
	}
	child := &childRuntime{
		id:          "rt_1",
		label:       "test",
		provider:    provider,
		session:     runtime.Session{SessionID: "ses_initial"},
		eventWriter: stableTestSink{},
		done:        make(chan struct{}),
		promptCh:    make(chan struct{}, 1),
	}

	result := sup.runChildAttempt(context.Background(), child, "", "hello", nil)
	if result.exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", result.exitCode)
	}
	if result.sessionID != "ses_initial" {
		t.Fatalf("sessionID = %q, want initial session", result.sessionID)
	}
	if provider.startCalls != 0 {
		t.Fatalf("Start called %d times, want 0 for initial spawn session", provider.startCalls)
	}
}

func TestShutdownTimeoutDoesNotHangWithMultipleStuckRuntimes(t *testing.T) {
	sup := NewSupervisor(Config{
		ControlSocket:   "/tmp/test-shutdown-timeout.sock",
		MaxRuntimes:     2,
		ShutdownTimeout: 10 * time.Millisecond,
	})
	sup.runtimes["rt_1"] = &childRuntime{
		id:       "rt_1",
		done:     make(chan struct{}),
		cancelFn: func() {},
	}
	sup.runtimes["rt_2"] = &childRuntime{
		id:       "rt_2",
		done:     make(chan struct{}),
		cancelFn: func() {},
	}

	done := make(chan struct{})
	go func() {
		sup.shutdown("graceful")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("shutdown did not return after timeout with multiple stuck runtimes")
	}
}

func TestRunLoopChildCleansUpOnLooprunnerError(t *testing.T) {
	sup := NewSupervisor(Config{
		ControlSocket: "/tmp/test-loop-cleanup.sock",
		MaxRuntimes:   1,
	})
	sink := &closeRecordingSink{}
	cancelled := false
	child := &childRuntime{
		id:          "rt_loop",
		done:        make(chan struct{}),
		promptCh:    make(chan struct{}, 1),
		eventWriter: sink,
		cancelFn:    func() { cancelled = true },
	}
	sup.runtimes[child.id] = child

	cfg := &looprunner.LoopConfig{
		MaxIterations: 1,
		Pre:           []looprunner.Phase{{Name: "broken", Prompt: "{{"}},
	}
	sup.runLoopChild(context.Background(), child, cfg, 0, 0, "", "", "")

	select {
	case <-child.done:
	default:
		t.Fatal("loop child done channel was not closed")
	}
	if !sink.closed {
		t.Fatal("loop child event writer was not closed")
	}
	if len(sink.events) == 0 {
		t.Fatal("loop child did not write lifecycle events")
	}
	for _, ev := range sink.events {
		if ev.Fields["runtime_id"] != "rt_loop" {
			t.Fatalf("event %s runtime_id = %v, want rt_loop", ev.Event, ev.Fields["runtime_id"])
		}
	}
	if !cancelled {
		t.Fatal("loop child cancel function was not called")
	}
	child.mu.Lock()
	completed := child.completed
	exitCode := child.exitCode
	child.mu.Unlock()
	if !completed {
		t.Fatal("loop child was not marked completed")
	}
	if exitCode != 1 {
		t.Fatalf("loop child exitCode = %d, want 1", exitCode)
	}
	if _, ok := sup.runtimes[child.id]; ok {
		t.Fatal("loop child was not removed from supervisor runtimes")
	}
}

func TestRuntimeAnswerPermissionRejectsRuntimeWithoutActiveSession(t *testing.T) {
	sup := NewSupervisor(Config{
		ControlSocket: "/tmp/test-loop-answer-no-session.sock",
		MaxRuntimes:   1,
	})
	sup.runtimes["rt_loop"] = &childRuntime{
		id:       "rt_loop",
		done:     make(chan struct{}),
		promptCh: make(chan struct{}, 1),
	}

	if err := sup.RuntimeAnswerPermission("rt_loop", "req_1", "allow"); err == nil {
		t.Fatal("RuntimeAnswerPermission should reject runtime without active session")
	}
}

type stableTestSink struct{}

func (stableTestSink) Write(events.Event) error { return nil }
func (stableTestSink) Close() error             { return nil }

type closeRecordingSink struct {
	closed bool
	events []events.Event
}

func (s *closeRecordingSink) Write(ev events.Event) error {
	s.events = append(s.events, ev)
	return nil
}
func (s *closeRecordingSink) Close() error {
	s.closed = true
	return nil
}

type stableFakeProvider struct {
	events     chan events.Event
	startCalls int
}

func (p *stableFakeProvider) Start(context.Context, runtime.StartOptions) (runtime.Session, error) {
	p.startCalls++
	return runtime.Session{SessionID: "ses_started"}, nil
}

func (p *stableFakeProvider) Resume(context.Context, string) (runtime.Session, error) {
	return runtime.Session{SessionID: "ses_resumed"}, nil
}

func (p *stableFakeProvider) Prompt(context.Context, string, string) error {
	p.events <- events.Event{
		Event:     "session.end",
		SessionID: "ses_initial",
		Fields:    map[string]any{"stop_reason": "end_turn"},
	}
	close(p.events)
	return nil
}

func (p *stableFakeProvider) Cancel(context.Context, string) error { return nil }

func (p *stableFakeProvider) Events(context.Context, string) (<-chan events.Event, error) {
	return p.events, nil
}

func (p *stableFakeProvider) AnswerPermission(context.Context, string, string, runtime.PermissionResponse) error {
	return nil
}

func (p *stableFakeProvider) Capabilities(context.Context) (runtime.Capabilities, error) {
	return runtime.Capabilities{}, nil
}
