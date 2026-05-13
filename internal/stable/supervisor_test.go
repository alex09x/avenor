package stable

import (
	"testing"
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
