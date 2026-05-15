package opencodehttp

import (
	"context"
	"testing"

	"github.com/sdougbrown/avenor/internal/runtime"
)

func TestNewWithOptions(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{
		Agent: "jockey",
		Model: "deepseek/deepseek-v4-pro",
	})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	if p == nil {
		t.Fatal("NewWithOptions returned nil provider")
	}
}

func TestStartFailsWithoutServerURL(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	_, err = p.Start(context.Background(), runtime.StartOptions{})
	if err == nil {
		t.Fatal("Start without ServerURL should fail")
	}
}

func TestCapabilities(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	caps, err := p.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities error = %v", err)
	}
	if caps.Backend != "opencode-http" {
		t.Errorf("Backend = %q, want %q", caps.Backend, "opencode-http")
	}
	if caps.Permissions {
		t.Error("Permissions should be false initially")
	}
	if !caps.Resume {
		t.Error("Resume should be true")
	}
	if !caps.ExternalServerURL {
		t.Error("ExternalServerURL should be true")
	}
	if caps.SubprocessDiscovery {
		t.Error("SubprocessDiscovery should be false")
	}
	if !caps.ModelSelection {
		t.Error("ModelSelection should be true")
	}
}

func TestResumeFailsWithoutServerURL(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	_, err = p.Resume(context.Background(), "ses_test")
	if err == nil {
		t.Fatal("Resume without ServerURL should fail")
	}
}

func TestEventsFailsBeforeStart(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	_, err = p.Events(context.Background(), "ses_test")
	if err == nil {
		t.Fatal("Events before Start should fail")
	}
}

func TestAnswerPermissionNotImplemented(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	err = p.AnswerPermission(context.Background(), "ses_test", "req_1", runtime.PermissionResponse{})
	if err == nil {
		t.Fatal("AnswerPermission should return not-implemented error")
	}
}

func TestOptionMerge(t *testing.T) {
	base := runtime.StartOptions{
		Agent: "default-agent",
		Model: "default-model",
		Dir:   "/default",
	}
	override := runtime.StartOptions{
		Agent:     "jockey",
		ServerURL: "http://localhost:4096",
	}
	merged := mergeStartOptions(base, override)
	if merged.Agent != "jockey" {
		t.Errorf("Agent = %q, want %q", merged.Agent, "jockey")
	}
	if merged.Model != "default-model" {
		t.Errorf("Model = %q, want %q (override is empty, should keep base)", merged.Model, "default-model")
	}
	if merged.ServerURL != "http://localhost:4096" {
		t.Errorf("ServerURL = %q, want %q", merged.ServerURL, "http://localhost:4096")
	}
	if merged.Dir != "/default" {
		t.Errorf("Dir = %q, want %q", merged.Dir, "/default")
	}
}

func TestMapModel(t *testing.T) {
	tests := []struct {
		input    string
		provider string
		model    string
	}{
		{"deepseek/deepseek-v4-pro", "deepseek", "deepseek-v4-pro"},
		{"anthropic/claude-sonnet-4-20250514", "anthropic", "claude-sonnet-4-20250514"},
		{"just-a-model", "", "just-a-model"},
		{"", "", ""},
	}
	for _, tt := range tests {
		result := mapModel(tt.input)
		if result["providerID"] != tt.provider {
			t.Errorf("mapModel(%q).providerID = %q, want %q", tt.input, result["providerID"], tt.provider)
		}
		if result["modelID"] != tt.model {
			t.Errorf("mapModel(%q).modelID = %q, want %q", tt.input, result["modelID"], tt.model)
		}
	}
}

func TestClose(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	// Close should not panic even before Start.
	if closer, ok := p.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			t.Errorf("Close error = %v", err)
		}
	}
}

func TestPromptFailsBeforeStart(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	err = p.Prompt(context.Background(), "ses_test", "hello")
	if err == nil {
		t.Fatal("Prompt before Start should fail")
	}
}

func TestCancelFailsBeforeStart(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	err = p.Cancel(context.Background(), "ses_test")
	if err == nil {
		t.Fatal("Cancel before Start should fail")
	}
}

func TestResumeWithEmptyID(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{
		ServerURL: "http://localhost:4096",
	})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	_, err = p.Resume(context.Background(), "")
	if err == nil {
		t.Fatal("Resume with empty sessionID should fail")
	}
}
