package codexappserver

import (
	"encoding/json"

	"github.com/sdougbrown/avenor/internal/events"
)

// translateNotification converts an app-server notification to an events.Event.
// Returns nil for notifications that should be silently dropped.
func translateNotification(method string, params json.RawMessage) *events.Event {
	switch method {
	case "turn/completed":
		return translateTurnCompleted(params)
	case "turn/started":
		return informationalEvent("avenor.turn.start", params)
	case "item/started":
		return informationalEvent("avenor.item.start", params)
	case "item/completed":
		return informationalEvent("avenor.item.end", params)
	default:
		return nil // silently dropped
	}
}

// translateApprovalRequest converts a server-initiated approval request to
// a permission.request event. Returns the parsed params for diagnostics.
func translateApprovalRequest(method string, params json.RawMessage) (*events.Event, *approvalRequestParams) {
	kind, ok := approvalKind(method)
	if !ok {
		return nil, nil
	}
	if params == nil {
		return nil, nil
	}
	var ar approvalRequestParams
	if err := json.Unmarshal(params, &ar); err != nil {
		return nil, nil
	}

	description := approvalDescription(kind, ar)

	fields := map[string]any{
		"kind":        kind,
		"description": description,
		"tool":        method,
		"options": []any{
			map[string]any{"optionId": "reject", "kind": "reject"},
			map[string]any{"optionId": "allow", "kind": "allow"},
		},
	}
	if ar.Command != "" {
		fields["command"] = ar.Command
	}
	if ar.Path != "" {
		fields["path"] = ar.Path
	}
	if ar.Summary != "" {
		fields["summary"] = ar.Summary
	}
	if ar.Message != "" {
		fields["message"] = ar.Message
	}

	return &events.Event{
		Event:     "permission.request",
		SessionID: ar.ThreadID,
		Fields:    fields,
	}, &ar
}

func translateTurnCompleted(params json.RawMessage) *events.Event {
	var n turnCompletedNotification
	if err := json.Unmarshal(params, &n); err != nil {
		return nil
	}

	fields := map[string]any{
		"turn_id": n.Turn.ID,
		"reason":  n.Turn.Status,
	}

	switch n.Turn.Status {
	case "completed":
		fields["stop_reason"] = "end_turn"
	case "interrupted":
		fields["stop_reason"] = "cancelled"
	case "failed":
		fields["stop_reason"] = "error"
		if n.Turn.Error != nil {
			fields["error_message"] = n.Turn.Error.Message
		}
	default:
		return nil // unknown status, drop
	}

	return &events.Event{
		Event:     "session.end",
		SessionID: n.ThreadID,
		Fields:    fields,
	}
}

func informationalEvent(eventType string, params json.RawMessage) *events.Event {
	var n itemNotification
	if err := json.Unmarshal(params, &n); err != nil {
		return nil
	}
	return &events.Event{
		Event:     eventType,
		SessionID: n.ThreadID,
		Fields: map[string]any{
			"turn_id": n.TurnID,
		},
	}
}

func approvalKind(method string) (string, bool) {
	switch method {
	case "item/commandExecution/requestApproval":
		return "command", true
	case "item/fileChange/requestApproval":
		return "file", true
	case "item/permissions/requestApproval":
		return "permissions", true
	default:
		return "", false
	}
}

func approvalDescription(kind string, ar approvalRequestParams) string {
	switch kind {
	case "command":
		return firstNonEmpty(ar.Command, ar.Summary, ar.Message)
	case "file":
		return firstNonEmpty(ar.Path, ar.Summary, ar.Message)
	case "permissions":
		return firstNonEmpty(ar.Summary, ar.Message)
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
