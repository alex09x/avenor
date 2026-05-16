package opencodeacp

import (
	"context"
	"strings"
	"testing"

	"github.com/sdougbrown/avenor/internal/events"
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
		wantErr string
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
			wantErr: `permission options missing kind "allow"`,
		},
		{
			name:    "empty options returns error",
			options: nil,
			approve: true,
			wantErr: `permission options missing kind "allow"`,
		},
		{
			name:    "reject missing returns error",
			options: opts("allow", "allow"),
			approve: false,
			wantErr: `permission options missing kind "reject"`,
		},
		{
			name:    "optionId empty string is an error",
			options: opts("", "allow"),
			approve: true,
			wantErr: `missing optionId`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectPermissionOption(tt.options, tt.approve)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("selectPermissionOption() error = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("selectPermissionOption() error = %v, want error containing %q", err, tt.wantErr)
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

func TestAnswerPermissionMissingOptions(t *testing.T) {
	p := &Provider{
		client:         &Client{},
		pendingOptions: map[string][]any{},
	}

	err := p.AnswerPermission(context.Background(), "ses", "req_missing", runtime.PermissionResponse{Allow: true})
	if err == nil {
		t.Fatal("expected error for missing options")
	}
	if !strings.Contains(err.Error(), `resolve permission request "req_missing"`) {
		t.Fatalf("expected resolve error, got: %v", err)
	}
	if !strings.Contains(err.Error(), `permission options missing kind "allow"`) {
		t.Fatalf("expected missing allow option error, got: %v", err)
	}
}

func TestCachePermissionOptions(t *testing.T) {
	p := &Provider{}
	ev := events.Event{
		Event: "permission.request",
		Fields: map[string]any{
			"request_id": "req_cache",
			"options": []any{
				map[string]any{"optionId": "allow", "kind": "allow"},
			},
		},
	}
	p.cachePermissionOptions(ev)

	p.mu.Lock()
	opts, ok := p.pendingOptions["req_cache"]
	p.mu.Unlock()
	if !ok {
		t.Fatal("options not cached")
	}
	if len(opts) != 1 {
		t.Fatalf("got %d options, want 1", len(opts))
	}
}

func TestCachePermissionOptionsNonRequestEvent(t *testing.T) {
	p := &Provider{
		pendingOptions: map[string][]any{
			"req_existing": {
				map[string]any{"optionId": "allow", "kind": "allow"},
			},
		},
	}
	ev := events.Event{
		Event:  "message",
		Fields: map[string]any{"request_id": "req_nope", "options": []any{map[string]any{"optionId": "deny", "kind": "reject"}}},
	}
	p.cachePermissionOptions(ev)

	p.mu.Lock()
	_, cachedNope := p.pendingOptions["req_nope"]
	_, keptExisting := p.pendingOptions["req_existing"]
	gotLen := len(p.pendingOptions)
	p.mu.Unlock()
	if cachedNope {
		t.Fatal("non-request event should not cache options")
	}
	if !keptExisting || gotLen != 1 {
		t.Fatalf("non-request event changed cache: len=%d keptExisting=%v", gotLen, keptExisting)
	}
}

func TestCachePermissionOptionsSessionEndClearsCache(t *testing.T) {
	p := &Provider{
		pendingOptions: map[string][]any{
			"req_pending": {
				map[string]any{"optionId": "allow", "kind": "allow"},
			},
		},
	}
	p.cachePermissionOptions(events.Event{
		Event:  "session.end",
		Fields: map[string]any{"stop_reason": "end_turn"},
	})

	p.mu.Lock()
	gotLen := len(p.pendingOptions)
	p.mu.Unlock()
	if gotLen != 0 {
		t.Fatalf("session.end should clear pending options, got %d entries", gotLen)
	}
}

func TestCachePermissionOptionsNoOptions(t *testing.T) {
	p := &Provider{}
	ev := events.Event{
		Event: "permission.request",
		Fields: map[string]any{
			"request_id": "req_noopts",
		},
	}
	p.cachePermissionOptions(ev)

	p.mu.Lock()
	_, ok := p.pendingOptions["req_noopts"]
	p.mu.Unlock()
	if ok {
		t.Fatal("event without options should not cache entry")
	}
}

func TestCloseClearsPermissionOptions(t *testing.T) {
	p := &Provider{
		pendingOptions: map[string][]any{
			"req_pending": {
				map[string]any{"optionId": "allow", "kind": "allow"},
			},
		},
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	p.mu.Lock()
	gotLen := len(p.pendingOptions)
	p.mu.Unlock()
	if gotLen != 0 {
		t.Fatalf("Close should clear pending options, got %d entries", gotLen)
	}
}
