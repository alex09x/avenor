# Avenor: Near-Term Development Plan

Three workstreams in priority order.

---

## 1. Agent Status Events

### Problem

Harnesses have no structured way to observe agent progress during a run. The
event stream contains low-level ACP events (tool calls, message chunks) but
nothing that says "the agent is planning" or "the agent is 40% through this
task". Two downstream needs share the same gap:

- **Progress UI**: a harness watching the event log needs a signal it can
  render as a status indicator without parsing prose.
- **Inter-agent signalling**: a jockey watching a mule's event log needs a
  stable checkpoint event to gate on, not just `session.end`.

### Proposed Event: `agent.status`

A new first-class event type written to the NDJSON stream by Avenor itself
(not passed through from OpenCode ACP).

```json
{
  "event": "agent.status",
  "session_id": "ses_abc123",
  "phase": "working",
  "label": "Running test suite",
  "source": "avenor"
}
```

Fields:

| Field | Type | Notes |
|---|---|---|
| `phase` | string | One of: `thinking`, `working`, `waiting`, `done`. |
| `label` | string | Optional human-readable description of current activity. |
| `source` | string | `"avenor"` for synthesized events, `"agent"` for explicit markers. |

`phase` values map to consumer intent:

- `thinking` — agent is reasoning; no tool calls yet this turn.
- `working` — at least one tool call is active or has completed this turn.
- `waiting` — a `permission.request` is pending a human response.
- `done` — session ended (`session.end` received). Mirrors `session.end` so
  status-only consumers don't need to handle two event types.

### Generation: Synthesized (State Machine)

Avenor observes the event stream and emits `agent.status` on phase
transitions. The state machine lives in `internal/cli/` alongside `cli.go`.

```
idle ──► thinking  on: agent.thought_chunk
     ──► working   on: tool.call

thinking ──► working   on: tool.call
         ──► thinking  on: agent.message_chunk (re-enters; no event emitted)

working ──► thinking   on: agent.thought_chunk (new reasoning after tool)
        ──► working    on: tool.call (label update only if title changed)

any ──► waiting   on: permission.request
any ◄── working   on: permission answered (internal; fileHandler completes)
any ──► done      on: session.end
```

A transition only emits an `agent.status` event when the phase *changes* or
when the label changes within the same phase (e.g. a new tool call with a
different title while already `working`). Repeated `agent.thought_chunk`
events in `thinking` state do not produce repeated `agent.status` events.

The label is populated from the most recent tool call `title` field when
entering `working`, or cleared when entering `thinking`.

Implementation: add a `statusTracker` type to `internal/cli/` with a
`Transition(event events.Event) (events.Event, bool)` method that returns the
synthesized status event and whether one should be emitted. Call it inside
`waitForSession` before writing each ACP event, emit the status event first if
one is produced.

### Generation: Explicit Agent Markers

Agents can embed structured markers in their output text to override or
supplement synthesized status. Avenor extracts these from `agent.message_chunk`
and `agent.thought_chunk` events.

Convention: `[status: <phase>]` or `[status: <phase> | <label>]` anywhere
in the content text, case-insensitive.

Examples:

```
[status: working | Analysing failing tests]
[status: thinking]
[status: working | Writing patch for src/foo.go]
```

Extraction lives in a `extractStatusMarker(text string) (phase, label string,
ok bool)` function in `internal/digest/` (alongside `classify.go`). When a
marker is found, Avenor emits `agent.status` with `"source": "agent"` and
suppresses the synthesized event that would otherwise fire for that same ACP
event.

The marker is extracted but **not** stripped from the emitted ACP event —
consumers that want the raw text still see it.

### Classify and Watch Integration

`internal/digest/classify.go`:

- `agent.status` with `phase == "done"` → `MILESTONE`
- `agent.status` with `phase == "waiting"` → `MILESTONE`
- `agent.status` otherwise → `ACTIVITY`

`internal/digest/digest.go` — add a case to `excerpt()`:

```go
case "agent.status":
    phase := stringField(event, "phase")
    if label := stringField(event, "label"); label != "" {
        return phase + " | " + label
    }
    return phase
```

`avenor watch` plain output becomes:

```
ACTIVITY   EVENT agent.status ses_abc thinking
ACTIVITY   EVENT agent.status ses_abc working | Running test suite
MILESTONE  EVENT agent.status ses_abc waiting | Allow file write to /etc/hosts?
MILESTONE  EVENT agent.status ses_abc done
```

### Inter-Agent Use

A jockey watching a mule's event log polls with `avenor watch --follow
--classify` and gates on `agent.status` events. The `phase` field gives
coarse progress; explicit markers from the mule give task-level checkpoints.
No new Avenor mechanism is needed — the shared event log is the channel.

---

## 2. Retry / Error Recovery

### Problem

A subprocess crash or transient RPC error leaves Avenor with a `FAILED`
sentinel and no recovery. The harness must restart from scratch. With session
resumability already implemented (`provider.Resume`), Avenor can do better.

### Proposed Flags

```
--max-retries N       Attempt up to N restarts on retryable failure (default 0).
```

Delay between attempts: fixed exponential backoff starting at 2 s, doubling
each attempt, capped at 30 s. Not configurable for now.

### Retryable vs Non-Retryable

Retryable: OpenCode subprocess exits unexpectedly, `readLoop` closes with a
non-nil `readErr`, JSON-RPC transport errors.

Non-retryable: `stop_reason` of `refusal`, `max_tokens`, `max_turn_requests`;
context cancellation; client-initiated timeout.

### Synthetic Event: `avenor.retry`

Emitted to the event log before each retry attempt:

```json
{
  "event": "avenor.retry",
  "session_id": "ses_abc123",
  "attempt": 2,
  "max_retries": 3,
  "reason": "opencode acp exited: signal: killed"
}
```

`classify.go`: `avenor.retry` → `MILESTONE`.

### Retry Loop

In `internal/cli/cli.go`, wrap `waitForSession` in a retry loop. On
retryable failure:

1. Emit `avenor.retry` event to the open writer.
2. Sleep the backoff duration.
3. Create a new provider (`opencodeacp.NewWithOptions`).
4. Call `provider.Resume(ctx, sessionID)` if a session ID was established;
   otherwise `provider.Start`.
5. Re-subscribe to events and restart the prompt goroutine.

The session ID from the first successful `Start` is preserved across retries
so OpenCode can reconnect to persisted state.

---

## 3. Observability

### Problem

Debugging a multi-step workflow means grepping stderr and event logs with no
common correlation key. There are no timestamps on events.

### Run ID

Generate a `run_id` (random hex, 16 bytes) at `avenor run` startup. Accept
`--run-id` to allow the harness to supply one for end-to-end correlation.

Propagate to:

- All Avenor-synthesized events (`agent.status`, `avenor.retry`) as a
  top-level `run_id` field.
- Sentinel file as `RUN=<run_id>`.
- Stderr log lines as a prefix: `[run_id] avenor: ...`.

ACP passthrough events (from OpenCode) are not modified — we don't own their
schema.

### Timestamps on Synthetic Events

Add `ts` (Unix milliseconds, integer) to all Avenor-emitted events. ACP
passthrough events are left as-is; if OpenCode adds timestamps in future those
flow through naturally.

### Structured Error Event: `avenor.error`

When Avenor encounters a non-fatal error during a session (e.g., a permission
handler timeout, a failed cursor write), instead of only writing to stderr,
also emit:

```json
{
  "event": "avenor.error",
  "session_id": "ses_abc123",
  "run_id": "a3f9...",
  "ts": 1715000000000,
  "message": "permission handler timed out after 10m",
  "source": "permission"
}
```

`classify.go`: `avenor.error` → `MILESTONE`.

Fatal errors (those that cause immediate exit) continue to write only to
stderr and the sentinel file — there may be no open writer to emit to.

---

## Implementation Order

1. `agent.status` synthesis (state machine only, no explicit markers yet) —
   highest value, self-contained.
2. Run ID + timestamps on synthetic events — adds almost no complexity, makes
   status events immediately more useful.
3. `agent.status` explicit markers — depends on (1).
4. Retry/recovery — depends on having stable status events to emit during
   retry.
5. `avenor.error` structured events — clean-up pass once (1)–(4) are stable.
