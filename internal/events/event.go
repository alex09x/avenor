package events

import "encoding/json"

type Event struct {
	Event     string         `json:"event"`
	SessionID string         `json:"session_id,omitempty"`
	Fields    map[string]any `json:"-"`
}

func (e Event) MarshalJSON() ([]byte, error) {
	fields := map[string]any{}
	for k, v := range e.Fields {
		fields[k] = v
	}
	fields["event"] = e.Event
	if e.SessionID != "" {
		fields["session_id"] = e.SessionID
	}
	return json.Marshal(fields)
}

func (e *Event) UnmarshalJSON(data []byte) error {
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	if event, ok := fields["event"].(string); ok {
		e.Event = event
		delete(fields, "event")
	}
	if sessionID, ok := fields["session_id"].(string); ok {
		e.SessionID = sessionID
		delete(fields, "session_id")
	}
	e.Fields = fields
	return nil
}
