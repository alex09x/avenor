package factory

import (
	"testing"

	"github.com/sdougbrown/avenor/internal/runtime"
)

func TestNewProviderAcp(t *testing.T) {
	p, err := NewProvider(runtime.StartOptions{}, "opencode-acp")
	if err != nil {
		t.Fatalf("NewProvider(acp) error = %v", err)
	}
	if p == nil {
		t.Fatal("NewProvider(acp) provider is nil")
	}
}

func TestNewProviderHTTP(t *testing.T) {
	p, err := NewProvider(runtime.StartOptions{}, "opencode-http")
	if p != nil {
		t.Fatal("NewProvider(http) expected nil provider")
	}
	if err == nil {
		t.Fatal("NewProvider(http) expected error")
	}
	if err.Error() != "opencode-http backend not yet implemented" {
		t.Fatalf("NewProvider(http) error = %q, want %q", err.Error(), "opencode-http backend not yet implemented")
	}
}

func TestNewProviderUnknown(t *testing.T) {
	p, err := NewProvider(runtime.StartOptions{}, "unknown")
	if p != nil {
		t.Fatal("NewProvider(unknown) expected nil provider")
	}
	if err == nil {
		t.Fatal("NewProvider(unknown) expected error")
	}
}
