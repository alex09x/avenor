package cli

import "github.com/sdougbrown/avenor/internal/events"

const (
	phaseThink = "thinking"
	phaseWork  = "working"
	phaseWait  = "waiting"
	phaseDone  = "done"
)

// statusTracker observes the ACP event stream and emits synthesized
// agent.status events on phase transitions.
type statusTracker struct {
	sessionID string
	phase     string
	label     string
}

func newStatusTracker(sessionID string) *statusTracker {
	return &statusTracker{sessionID: sessionID}
}

// Observe returns a synthesized agent.status event and true when ev causes a
// phase transition or label change. Returns the zero Event and false otherwise.
func (s *statusTracker) Observe(ev events.Event) (events.Event, bool) {
	switch ev.Event {
	case "agent.thought_chunk":
		if s.phase == phaseThink || s.phase == phaseWait || s.phase == phaseDone {
			return events.Event{}, false
		}
		return s.set(phaseThink, "")

	case "tool.call":
		if s.phase == phaseWait || s.phase == phaseDone {
			return events.Event{}, false
		}
		label := statusField(ev.Fields, "title")
		if s.phase == phaseWork && label == s.label {
			return events.Event{}, false
		}
		return s.set(phaseWork, label)

	case "permission.request":
		if s.phase == phaseDone {
			return events.Event{}, false
		}
		label := statusField(ev.Fields, "question")
		if label == "" {
			label = statusField(ev.Fields, "tool")
		}
		return s.set(phaseWait, label)

	case "session.end":
		return s.set(phaseDone, "")
	}

	return events.Event{}, false
}

// PermissionAnswered returns a synthesized agent.status event transitioning
// back to working after a permission request is resolved.
func (s *statusTracker) PermissionAnswered() (events.Event, bool) {
	if s.phase != phaseWait {
		return events.Event{}, false
	}
	return s.set(phaseWork, "")
}

func (s *statusTracker) set(phase, label string) (events.Event, bool) {
	s.phase = phase
	s.label = label
	fields := map[string]any{
		"phase":  phase,
		"source": "avenor",
	}
	if label != "" {
		fields["label"] = label
	}
	return events.Event{
		Event:     "agent.status",
		SessionID: s.sessionID,
		Fields:    fields,
	}, true
}

func statusField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	v, _ := fields[key].(string)
	return v
}
