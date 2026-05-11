package events

type Event struct {
	Event     string         `json:"event"`
	SessionID string         `json:"session_id,omitempty"`
	Fields    map[string]any `json:"-"`
}
