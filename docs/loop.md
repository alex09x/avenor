# Avenor: Phase Loop

An optional multi-phase execution model where Avenor runs a sequence of agent
sessions — some once, some repeatedly — until a loop exit condition is met.

---

## Problem

A single-prompt run is sufficient for simple tasks but inadequate for workflows
that naturally alternate between phases: build once, then test → review →
fix until clean. Today this requires an external shell script that calls
`avenor run` repeatedly and glues the results together with bespoke logic.
Avenor is already the right place to own that loop because it holds the
run ID, event log, sentinel, and retry machinery.

---

## Design Overview

A **loop config file** (JSON) defines a sequence of named phases, split into:

- **`pre`**: phases that run once before the loop starts (e.g. build).
- **`loop`**: phases that repeat until an exit condition fires.

Each phase is one Avenor agent session. Phases run serially. The same event
log, run ID, and permission handler are shared across all phases.

### Example config

```json
{
  "max_iterations": 5,
  "pre": [
    {
      "name": "build",
      "prompt": "Build the project. Fix any compilation errors until the build succeeds."
    }
  ],
  "loop": [
    {
      "name": "test",
      "prompt": "Run the test suite. If all tests pass, emit [loop: exit | tests green]. Otherwise report the failures."
    },
    {
      "name": "review",
      "prompt": "Review all changes made since the initial task. Write a concise findings list to REVIEW_FINDINGS.md."
    },
    {
      "name": "verify",
      "prompt": "Read REVIEW_FINDINGS.md. If all items are resolved emit [loop: exit | verified clean]. Otherwise leave the file updated with remaining issues."
    },
    {
      "name": "fix",
      "prompt": "Read REVIEW_FINDINGS.md and address each remaining item."
    }
  ]
}
```

### Minimal invocation

```bash
avenor run \
  --loop-file loop.json \
  --auto-approve \
  --sentinel-file run.done
```

`--prompt` / `--prompt-file` are **not required** when `--loop-file` is set.
They remain valid and, when provided, run as an implicit pre-phase before
any phases defined in the config.

---

## Exit Conditions

A loop stops when any of the following fires:

| Condition | Evaluated | Trigger |
|---|---|---|
| **Exit marker** | After phase ends | Any phase agent emits `[loop: exit]` or `[loop: exit \| label]` |
| **Abort marker** | After phase ends | Any phase agent emits `[loop: abort]` or `[loop: abort \| reason]` |
| **Max iterations** | After last loop phase | `iteration_count >= max_iterations` |
| **Phase failure** | Immediately | Any phase exits with a non-clean stop reason |
| **Cancellation / timeout** | Immediately | Existing signal/timer path, unchanged |

"Clean" is `stop_reason == "end_turn"`. All other terminal stop reasons
propagate immediately and stop the loop.

Both `exit` and `abort` are evaluated at phase-end (not mid-stream): the
phase always runs to the natural end of its session before the loop acts on
the marker. This gives the agent time to write findings, explain its
reasoning, or clean up before control returns to Avenor. The distinction
between the two is in the outcome — exit is a success signal, abort is an
escalation signal.

---

## Loop Markers

Extends the existing `[status: ...]` marker convention in
`internal/digest/marker.go`.

```
[loop: exit]                    ← clean completion, stop iterating
[loop: exit | tests green]      ← with label
[loop: continue]                ← explicit no-op, for readability in prompts
[loop: abort]                   ← blocked, needs escalation
[loop: abort | architectural issue: layering violation in pkg/db]
```

`ExtractLoopMarker(text string) (directive, label string, ok bool)`:

- `directive` is one of `"exit"`, `"continue"`, `"abort"`.
- `ok=true` only for known directives; unknown words → `ok=false`, ignored.
- If multiple markers appear in one chunk the first wins (consistent with
  status marker behaviour).

Extraction is done inside `waitForSession` on the same chunk events already
scanned for `[status: ...]`. The marker is **not** stripped from the forwarded
event — raw text consumers still see it.

The most severe marker seen during a phase wins: `abort` > `exit` > `continue`.
If a phase emits `[loop: exit]` and later `[loop: abort]` in the same session,
the phase is treated as aborted.

---

## Phase Abort

An agent emits `[loop: abort | reason]` when it has discovered something it
cannot resolve on its own — an architectural constraint violation, a decision
that requires human judgement, or a dependency on another agent's output that
isn't available.

The abort path diverges from the exit path in three ways:

**1. Outcome: blocked, not success.**
The loop stops and Avenor writes a `BLOCKED` sentinel (exit code `5`). The
harness or orchestrating agent reads the sentinel and decides next steps —
route to a human, invoke a different agent, or re-invoke Avenor with a
modified prompt. Avenor does not attempt another iteration or retry.

**2. Reason is preserved.**
The abort label from the marker is carried through the `avenor.phase.end`
event and the `avenor.loop.end` event, and into the sentinel as a `REASON=`
line. Harnesses can gate on this without parsing event logs.

**3. Exit code `5` (blocked).**
Added to `internal/runtime/exit.go` alongside the existing stop-reason map:
- `StopReasonForExitCode(5) → "blocked"`
- `ExitCodeForStopReason("blocked") → 5`

Sentinel format for a blocked run:
```
BLOCKED
SESSION=ses_abc123
STOP_REASON=blocked
REASON=architectural issue: layering violation in pkg/db
RUN=a3f9...
```

The `REASON` line is omitted when the marker had no label (`[loop: abort]`
with no pipe).

### Inter-agent escalation pattern

A jockey watching a mule's event log via `--on-event` sees the
`avenor.loop.end` event with `exit_reason: "abort"` and `exit_label` carrying
the reason. The jockey can then:
- Surface the reason to a human via a tool call or message.
- Invoke a specialist agent with the abort label as its prompt context.
- Re-invoke the original mule with an amended prompt that addresses the blocker.

No new Avenor mechanism is required for the jockey side — reading `exit_reason`
from the event stream is sufficient.

---

## Phase Execution Model

Each phase is one call to `runSingleAttempt` with a fresh provider (matching
the existing retry model). No session is resumed between phases — accumulated
context risk outweighs the benefit for typical loop workloads.

Sessions are independent. The phase prompt is the sole input. Context flows
through agent-managed artefacts (files the agent writes and subsequent agents
read), which is both simpler and more reliable than Avenor trying to
summarise or inject prior session content.

### Prompt templates

Phase prompts support a small set of template variables (Go `text/template`
syntax):

| Variable | Value |
|---|---|
| `{{.RunID}}` | The run's correlation ID |
| `{{.Phase}}` | Current phase name |
| `{{.Iteration}}` | Current loop iteration (1-indexed; 0 for pre-phases) |
| `{{.MaxIterations}}` | Value of `max_iterations` |
| `{{.WorkDir}}` | Working directory |

```json
{
  "name": "fix",
  "prompt": "Iteration {{.Iteration}} of {{.MaxIterations}}. Read REVIEW_FINDINGS.md and address each item."
}
```

---

## Synthetic Events

All loop events carry `run_id`, `ts`, and `phase` fields.

### `avenor.loop.start`

Emitted once before the first pre-phase (or loop phase if no pre).

```json
{
  "event": "avenor.loop.start",
  "run_id": "a3f9...",
  "ts": 1715000000000,
  "max_iterations": 5,
  "phase_count": 4
}
```

### `avenor.phase.start`

Emitted immediately before each phase's session begins.

```json
{
  "event": "avenor.phase.start",
  "run_id": "a3f9...",
  "ts": 1715000000000,
  "phase": "test",
  "iteration": 2,
  "kind": "loop"
}
```

`kind` is `"pre"` or `"loop"`.

### `avenor.phase.end`

Emitted after each phase's session ends (before any backoff/retry).

```json
{
  "event": "avenor.phase.end",
  "run_id": "a3f9...",
  "ts": 1715000000000,
  "phase": "test",
  "iteration": 2,
  "stop_reason": "end_turn",
  "exit_marker": true,
  "exit_marker_label": "tests green"
}
```

Marker fields present only when a marker fired in this phase:

| Field | Present when |
|---|---|
| `exit_marker: true` | `[loop: exit]` was seen |
| `exit_marker_label` | exit marker had a label |
| `abort_marker: true` | `[loop: abort]` was seen |
| `abort_marker_label` | abort marker had a label |

`abort_marker` takes priority: if both appear in the same phase session,
only the abort fields are set and the loop exits with the blocked outcome.

### `avenor.loop.end`

Emitted once after the loop (or pre sequence) finishes, regardless of how it
ended — success, abort, failure, or limit.

```json
{
  "event": "avenor.loop.end",
  "run_id": "a3f9...",
  "ts": 1715000000000,
  "iterations_completed": 2,
  "exit_reason": "abort",
  "exit_label": "architectural issue: layering violation in pkg/db"
}
```

`exit_reason` is one of: `marker`, `abort`, `max_iterations`, `phase_failure`,
`cancelled`, `timeout`.

`exit_label` carries the label from the winning marker when `exit_reason` is
`marker` or `abort`; absent otherwise.

### Classify / digest

| Event | Class | Excerpt |
|---|---|---|
| `avenor.loop.start` | MILESTONE | `"loop start, max_iterations=N"` |
| `avenor.phase.start` | ACTIVITY | `"phase: <name> (iter N)"` |
| `avenor.phase.end` | MILESTONE | `"phase: <name> → <stop_reason>"` |
| `avenor.loop.end` | MILESTONE | `"loop end: <exit_reason>"` (e.g. `"loop end: abort"`) |

---

## Sentinel Behaviour

`writeSentinel` fires once after the entire loop finishes, not after each
phase. The exit code reflects the loop's overall outcome:

| Outcome | Status | Exit code |
|---|---|---|
| Clean exit (marker or max_iterations) | `DONE` | `0` |
| Abort marker | `BLOCKED` | `5` |
| Phase non-clean stop | `FAILED` | phase exit code |
| Timeout | `TIMEOUT` | `124` |
| Cancellation | `KILLED` | `130` |

The `BLOCKED` sentinel includes a `REASON=` line when the abort marker carried
a label:

```
BLOCKED
SESSION=ses_abc123
STOP_REASON=blocked
REASON=architectural issue: layering violation in pkg/db
RUN=a3f9...
```

Exit code `5` is added to `internal/runtime/exit.go`:
- `StopReasonForExitCode(5) → "blocked"`
- `ExitCodeForStopReason("blocked") → 5`
- `sentinelContent` gains a `case 5:` branch producing the `BLOCKED` prefix.

If a sentinel is wanted per phase (e.g. for external monitoring), the calling
harness can subscribe to `avenor.phase.end` events via `--on-event` and write
its own markers. Avenor does not provide per-phase sentinels.

---

## Config Validation

On load, before any phase runs:

- `loop` and `pre` may each be empty but both absent is an error.
- `max_iterations` must be ≥ 1 when `loop` is non-empty. Default: `10`.
- Each phase must have a non-empty `name` and a non-empty `prompt`.
- Phase names must be unique within the config.
- `--prompt` / `--prompt-file` with `--loop-file` inserts an unnamed pre-phase
  at index 0 with no special name (emitted as `phase: "(initial)"`).

---

## CLI Changes

```
--loop-file <path>   path to loop config JSON (optional; enables multi-phase mode)
```

Mutual exclusions when `--loop-file` is set:

- `--resume` is rejected (loop manages session lifecycle).

All other flags (`--agent`, `--dir`, `--model`, `--timeout`, `--max-retries`,
`--auto-approve`, `--permission-handler`, `--sentinel-file`, `--on-event`,
`--run-id`) apply uniformly to all phases.

---

## Implementation Plan

### Files to create

| File | Purpose |
|---|---|
| `internal/cli/loop.go` | `LoopConfig`, `Phase` structs; JSON loading; template rendering; `runLoop` orchestrator |
| `internal/digest/loopmarker.go` | `ExtractLoopMarker(text string) (directive, label string, ok bool)` |

### Files to modify

| File | Change |
|---|---|
| `internal/cli/cli.go` | `--loop-file` flag; dispatch to `runLoop` when set |
| `internal/cli/retry.go` | Extend `attemptResult` with `loopDirective string` and `loopLabel string`; `waitForSession` populates these from the chunk scan |
| `internal/runtime/exit.go` | Exit code `5` for `"blocked"`; `StopReasonForExitCode` and `ExitCodeForStopReason` cases |
| `internal/cli/sentinel.go` | `sentinelContent` case 5: `BLOCKED` prefix + optional `REASON=` line; `writeSentinel` accepts optional reason string or the loop runner sets stopReason to `"blocked"` and passes the label separately |
| `internal/digest/marker.go` | Optional: share regex infrastructure with `loopmarker.go` |
| `internal/digest/classify.go` | Add cases for the four new event types |
| `internal/digest/digest.go` | Add excerpts for the four new event types |

### Key data flow for abort

```
waitForSession  ← sees [loop: abort | reason] in chunk
    │
    └─► sets attemptResult.loopDirective = "abort"
            attemptResult.loopLabel    = "reason"

runSingleAttempt returns attemptResult

runLoop checks result.loopDirective == "abort"
    │
    ├─► emit avenor.phase.end { abort_marker: true, abort_marker_label: "..." }
    ├─► emit avenor.loop.end  { exit_reason: "abort", exit_label: "..." }
    └─► return exit code 5

exitWithSentinel(5) → writeSentinel writes BLOCKED sentinel
```

### Suggested order

1. **`loopmarker.go`** — pure function, no dependencies, easy to test first.
2. **`runtime/exit.go`** — add exit code 5; update `sentinel.go` `case 5:`.
3. **`retry.go`** — extend `attemptResult`; hook `waitForSession` chunk scan.
4. **`loop.go` config structs + validation** — JSON decode, template expansion,
   error cases; no execution yet.
5. **`loop.go` runLoop orchestrator** — calls `runSingleAttempt` per phase,
   emits lifecycle events, evaluates exit/abort conditions.
6. **`cli.go` wiring** — `--loop-file` flag + dispatch.
7. **`classify.go` + `digest.go`** — mechanical additions after events are defined.

### Testing

- `internal/digest/loopmarker_test.go` — covers `[loop: exit]`, `[loop: exit | label]`,
  `[loop: continue]`, `[loop: abort]`, `[loop: abort | reason]`, abort wins over
  exit when both appear, unknown directive, malformed.
- `internal/runtime/exit_test.go` — add `blocked`/`5` to existing tables.
- `internal/cli/sentinel_test.go` — add `BLOCKED` sentinel case with and without reason.
- `internal/cli/loop_test.go` — config load/validate (table-driven), template
  rendering, abort-wins-over-exit priority logic.
- Integration: the existing `runSingleAttempt` mock surface in `retry_test.go`
  can be extended to exercise phase sequencing without a live OpenCode process.

---

## Out of Scope

- **Parallel phases** — serial execution is sufficient; parallelism creates
  coordination problems (shared file writes, event ordering) with unclear
  benefit for the primary use case.
- **Conditional phase skipping** — phases always run in order; skip logic
  belongs in the phase prompt ("if X is already clean, emit [loop: exit]").
- **Per-phase `--max-retries`** — the existing retry flag applies to each
  phase individually; that's enough.
- **Cross-session context injection** — agent-managed files are the handoff
  mechanism; Avenor does not summarise or inject prior session output.
- **Loop nesting** — one level only.
- **Non-JSON config formats** — YAML would require a new dependency.
