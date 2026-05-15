package opencodehttp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/sdougbrown/avenor/internal/events"
)

// readSSEEvents reads SSE frames from r and sends parsed events to out.
// It returns when ctx is cancelled or r reaches EOF/error.
func readSSEEvents(ctx context.Context, r io.Reader, out chan<- events.Event) {
	defer close(out)
	scanner := bufio.NewScanner(r)
	// SSE frames can be large; bump the buffer.
	scanner.Buffer(make([]byte, 4096), 1<<20)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[6:]
		var raw struct {
			Type       string         `json:"type"`
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal([]byte(payload), &raw); err != nil {
			continue
		}
		evt := mapHTTPEvent(raw.Type, raw.Properties)
		if evt.Event != "" {
			out <- evt
		}
	}
}

// mapHTTPEvent converts a raw opencode HTTP event into an avenor events.Event.
// Phase D will expand this with full event mapping.
func mapHTTPEvent(eventType string, props map[string]any) events.Event {
	switch eventType {
	case "server.connected", "server.heartbeat":
		// Server-level events — skip for now.
		return events.Event{}
	default:
		// Pass through unknown events for debugging.
		sessionID, _ := props["sessionID"].(string)
		return events.Event{
			Event:     "http." + eventType,
			SessionID: sessionID,
			Fields:    props,
		}
	}
}
