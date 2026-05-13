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
