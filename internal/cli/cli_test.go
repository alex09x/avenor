package cli

import (
	"testing"
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
	}{
		{
			name:    "approve picks approve-kind",
			options: opts("r1", "reject", "a1", "approve"),
			approve: true,
			want:    "a1",
		},
		{
			name:    "reject picks reject-kind",
			options: opts("r1", "reject", "a1", "approve"),
			approve: false,
			want:    "r1",
		},
		{
			name:    "fallback to first when no matching kind",
			options: opts("x1", "other"),
			approve: true,
			want:    "x1",
		},
		{
			name:    "empty options returns empty string",
			options: nil,
			approve: true,
			want:    "",
		},
		{
			name:    "approve prefix match (approve_session)",
			options: opts("a2", "approve_session"),
			approve: true,
			want:    "a2",
		},
		{
			name:    "reject prefix match (reject_all)",
			options: opts("r2", "reject_all"),
			approve: false,
			want:    "r2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectPermissionOption(tt.options, tt.approve); got != tt.want {
				t.Errorf("selectPermissionOption(approve=%v) = %q, want %q", tt.approve, got, tt.want)
			}
		})
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
