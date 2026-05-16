// Package opencodehttp implements a runtime.Provider that communicates with
// opencode serve over its HTTP API (SSE event stream + REST endpoints).
//
// # Transport contract
//
// The opencode serve HTTP API uses:
//
//   - Server-Sent Events (SSE) over HTTP for the event stream (GET /event).
//   - Standard JSON REST for all other operations.
//
// SSE framing is `data: <JSON>\n\n`. The connection is a long-lived HTTP GET
// that stays open for the lifetime of the provider. Heartbeat/keepalive events
// (`server.heartbeat`) are emitted periodically with empty properties.
//
// Reconnect behavior: the server does not appear to support Last-Event-ID cursor
// resumption; a reconnecting client will receive a fresh stream starting from the
// current point. The provider should close the stream on disconnect and
// re-establish it, filtering events by sessionID locally.
//
// # Session correlation
//
// The event stream is global — it delivers events for ALL sessions on the
// server. Each session-scoped event carries a `sessionID` field inside
// `properties`. Server-level events (`server.connected`, `server.heartbeat`)
// have no sessionID.
//
// Concurrent sessions are safe: events for multiple sessions interleave in
// the stream, and the provider filters by sessionID when delivering to event
// channels. There is no cross-delivery risk because each session ID is unique
// and the provider holds its own event channel per session.
//
// # Session lifecycle events
//
// A message execution produces this event sequence:
//
//	session.status {type:"busy"}
//	session.diff (empty diff)
//	session.status {type:"busy"} (duplicate on message dispatch)
//	message.part.delta × N (streaming text chunks)
//	session.status {type:"idle"}
//	session.idle
//
// For cancelled messages, a `session.error` event is inserted before the
// idle transition:
//
//	session.error {name:"MessageAbortedError", data:{message:"Aborted"}}
//	session.status {type:"idle"}
//	session.idle
//
// # session.end source
//
// The synchronous POST /message response is the authoritative source for
// session.end. SSE idle transitions are deliberately ignored as terminal
// signals because they can arrive late for a previous turn on the same
// session.
//
// # Reconnect behavior
//
// The SSE stream reconnects with exponential backoff (100ms → 1s cap). On
// reconnect, the per-session busy/error state tracked by the SSE reader is
// reset. If a session was mid-execution during disconnect, the post-reconnect
// idle event will not produce a session.end. The response-derived session.end
// from Prompt() covers this gap in practice.
//
// # Stop-reason mapping
//
// The HTTP API uses `info.finish` on the POST /session/:id/message response
// to signal completion status.
//
//	finish value     | avenor stop_reason
//	-----------------|--------------------
//	"stop"           | "end_turn"
//	(error present)  | (error name)
//
// The `session.error` properties contain `{name, data}` where name identifies
// the error class (e.g., "MessageAbortedError" for cancellation).
//
// # Model format
//
// The HTTP API expects `model` as an object `{providerID, modelID}`, not a
// plain string. The provider maps StartOptions.Model (a string) to this
// format. If the string contains a "/", it is split as "providerID/modelID".
// Otherwise the providerID is inferred from agent defaults.
//
// # Working directory
//
// The HTTP API does not currently support binding a new session to an
// arbitrary directory. Start rejects non-default StartOptions.Dir values; run
// opencode serve from the target project directory instead.
//
// # Permissions
//
// Permission request/response behavior in HTTP mode was not observed during
// Phase 0 live testing. The opencode serve may handle permissions internally
// (auto-approve) or may not emit them through the SSE event stream. The
// provider sets Capabilities.Permissions = false until this is confirmed and
// mapped in Phase D.
//
// # Resume decision
//
// Resume is implemented: GET /session/:id returns the session metadata (200)
// if the session exists, or an error if not found. On success, the provider
// returns a Session with the existing sessionID. Messages can then be sent to
// that session via POST /session/:id/message.
//
// Capabilities.Resume is set to true.
//
// # Auth and security
//
// The provider supports HTTP basic auth via the URL (http://user:pass@host)
// or explicit headers in provider options. Credentials are masked in
// error/log output. The plan recommends localhost-only for v1 with an
// explicit warning for non-local bindings.
package opencodehttp
