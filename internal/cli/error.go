package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/sdougbrown/avenor/internal/events"
)

// emitErrorEvent logs message to stderr and writes an avenor.error event to
// the event log. The write is best-effort — if it fails the error is silently
// dropped so we don't recurse on a broken writer.
func emitErrorEvent(writer *eventWriter, sessionID, runID, source, message string, stderr io.Writer) {
	fmt.Fprintf(stderr, "avenor: %s\n", message)
	if writer == nil {
		return
	}
	fields := map[string]any{
		"message": message,
		"source":  source,
		"ts":      time.Now().UnixMilli(),
	}
	if runID != "" {
		fields["run_id"] = runID
	}
	_ = writer.Write(events.Event{
		Event:     "avenor.error",
		SessionID: sessionID,
		Fields:    fields,
	})
}
