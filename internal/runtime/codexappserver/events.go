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
	if params == nil {
		return nil, nil
	}
	var ar approvalRequestParams
	if err := json.Unmarshal(params, &ar); err != nil {
		return nil, nil
	}

	kind, description := extractApprovalKind(method, ar)

	fields := map[string]any{
		"kind":        kind,
		"description": description,
		"tool":        method,
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
		"turn_id":  n.Turn.ID,
		"reason":   n.Turn.Status,
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
	_ = json.Unmarshal(params, &n)
	return &events.Event{
		Event:     eventType,
		SessionID: n.ThreadID,
		Fields: map[string]any{
			"turn_id": n.TurnID,
		},
	}
}

func extractApprovalKind(method string, ar approvalRequestParams) (string, string) {
	switch method {
	case "item/commandExecution/requestApproval":
		desc := firstNonEmpty(ar.Command, ar.Summary, ar.Message)
		return "command", desc
	case "item/fileChange/requestApproval":
		desc := firstNonEmpty(ar.Path, ar.Summary, ar.Message)
		return "file", desc
	case "item/permissions/requestApproval":
		desc := firstNonEmpty(ar.Summary, ar.Message)
		return "permissions", desc
	default:
		return "unknown", ""
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
