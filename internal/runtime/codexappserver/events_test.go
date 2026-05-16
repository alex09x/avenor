package codexappserver

import (
	"encoding/json"
	"testing"
)

func TestTranslateTurnCompleted(t *testing.T) {
	tests := []struct {
		name      string
		params    string
		wantEvent string
		wantStop  string
		wantError string
	}{
		{
			name:      "completed → end_turn",
			params:    `{"threadId":"th1","turn":{"id":"t1","status":"completed"}}`,
			wantEvent: "session.end",
			wantStop:  "end_turn",
		},
		{
			name:      "interrupted → cancelled",
			params:    `{"threadId":"th1","turn":{"id":"t1","status":"interrupted"}}`,
			wantEvent: "session.end",
			wantStop:  "cancelled",
		},
		{
			name:      "failed → error",
			params:    `{"threadId":"th1","turn":{"id":"t1","status":"failed","error":{"message":"boom"}}}`,
			wantEvent: "session.end",
			wantStop:  "error",
			wantError: "boom",
		},
		{
			name:      "unknown status dropped",
			params:    `{"threadId":"th1","turn":{"id":"t1","status":"running"}}`,
			wantEvent: "",
		},
		{
			name:      "malformed json dropped",
			params:    `{bad`,
			wantEvent: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := translateTurnCompleted(json.RawMessage(tt.params))
			if tt.wantEvent == "" {
				if ev != nil {
					t.Fatalf("expected nil, got %+v", ev)
				}
				return
			}
			if ev == nil {
				t.Fatal("expected event, got nil")
			}
			if ev.Event != tt.wantEvent {
				t.Errorf("event = %q, want %q", ev.Event, tt.wantEvent)
			}
			if got, _ := ev.Fields["stop_reason"].(string); got != tt.wantStop {
				t.Errorf("stop_reason = %q, want %q", got, tt.wantStop)
			}
			if tt.wantError != "" {
				if got, _ := ev.Fields["error_message"].(string); got != tt.wantError {
					t.Errorf("error_message = %q, want %q", got, tt.wantError)
				}
			}
		})
	}
}

func TestTranslateNotificationDrop(t *testing.T) {
	if ev := translateNotification("unknown/method", nil); ev != nil {
		t.Fatalf("unknown method should be dropped, got %+v", ev)
	}
}

func TestTranslateApprovalCommand(t *testing.T) {
	params := json.RawMessage(`{"threadId":"th1","command":"rm -rf /","summary":"run command"}`)
	ev, ar := translateApprovalRequest("item/commandExecution/requestApproval", params)
	if ev == nil {
		t.Fatal("expected event")
	}
	if ev.Event != "permission.request" {
		t.Errorf("event = %q, want permission.request", ev.Event)
	}
	if ev.SessionID != "th1" {
		t.Errorf("sessionID = %q, want th1", ev.SessionID)
	}
	kind, _ := ev.Fields["kind"].(string)
	if kind != "command" {
		t.Errorf("kind = %q, want command", kind)
	}
	desc, _ := ev.Fields["description"].(string)
	if desc != "rm -rf /" {
		t.Errorf("description = %q, want 'rm -rf /'", desc)
	}
	if ar.Command != "rm -rf /" {
		t.Errorf("ar.Command = %q", ar.Command)
	}
}

func TestTranslateApprovalFile(t *testing.T) {
	params := json.RawMessage(`{"threadId":"th2","path":"/etc/hosts","summary":"modify hosts"}`)
	ev, ar := translateApprovalRequest("item/fileChange/requestApproval", params)
	if ev == nil {
		t.Fatal("expected event")
	}
	kind, _ := ev.Fields["kind"].(string)
	if kind != "file" {
		t.Errorf("kind = %q, want file", kind)
	}
	desc, _ := ev.Fields["description"].(string)
	if desc != "/etc/hosts" {
		t.Errorf("description = %q, want /etc/hosts", desc)
	}
	if ar.Path != "/etc/hosts" {
		t.Errorf("ar.Path = %q", ar.Path)
	}
}

func TestTranslateApprovalPermissions(t *testing.T) {
	params := json.RawMessage(`{"threadId":"th3","summary":"need full access","message":"allow all"}`)
	ev, ar := translateApprovalRequest("item/permissions/requestApproval", params)
	if ev == nil {
		t.Fatal("expected event")
	}
	kind, _ := ev.Fields["kind"].(string)
	if kind != "permissions" {
		t.Errorf("kind = %q, want permissions", kind)
	}
	desc, _ := ev.Fields["description"].(string)
	if desc != "need full access" {
		t.Errorf("description = %q, want 'need full access'", desc)
	}
	if ar.Summary != "need full access" {
		t.Errorf("ar.Summary = %q", ar.Summary)
	}
}

func TestTranslateApprovalMissingDescription(t *testing.T) {
	params := json.RawMessage(`{"threadId":"th4"}`)
	ev, _ := translateApprovalRequest("item/commandExecution/requestApproval", params)
	if ev == nil {
		t.Fatal("expected event")
	}
	desc, _ := ev.Fields["description"].(string)
	if desc != "" {
		t.Errorf("description = %q, want empty", desc)
	}
}

func TestTranslateApprovalNilParams(t *testing.T) {
	ev, ar := translateApprovalRequest("item/commandExecution/requestApproval", nil)
	if ev != nil || ar != nil {
		t.Fatal("expected nil for nil params")
	}
}

func TestInformationalEvents(t *testing.T) {
	params := json.RawMessage(`{"threadId":"th5","turnId":"t5"}`)

	tests := []struct {
		method string
		want   string
	}{
		{"turn/started", "avenor.turn.start"},
		{"item/started", "avenor.item.start"},
		{"item/completed", "avenor.item.end"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			ev := translateNotification(tt.method, params)
			if ev == nil {
				t.Fatal("expected event")
			}
			if ev.Event != tt.want {
				t.Errorf("event = %q, want %q", ev.Event, tt.want)
			}
			if ev.SessionID != "th5" {
				t.Errorf("session = %q, want th5", ev.SessionID)
			}
		})
	}
}
