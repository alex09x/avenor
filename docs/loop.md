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

A loop exits when any of the following fires (evaluated after each full
iteration, i.e. after the last loop phase completes):

| Condition | Trigger |
|---|---|
| **Marker** | Any phase agent emits `[loop: exit]` or `[loop: exit \| label]` |
| **Max iterations** | `iteration_count >= max_iterations` |
| **Phase failure** | Any phase exits with a non-clean stop reason (`refusal`, `max_tokens`, etc.) |
| **Cancellation / timeout** | Existing signal/timer path, unchanged |

"Clean" is `stop_reason == "end_turn"`. All other terminal stop reasons
propagate immediately and abort the loop.

The `[loop: exit]` marker is the primary mechanism: phases are prompted to
emit it when their exit criterion is met, as in the example above. Avenor
checks for this flag after each phase ends (not mid-stream) so the phase
always runs to completion before the loop is cut short.

---

## `[loop: exit]` Marker

Extends the existing `[status: ...]` marker convention in
`internal/digest/marker.go`.

```
[loop: exit]
[loop: exit | tests green]
[loop: continue]   ← explicit no-op, for readability in prompts
```

`ExtractLoopMarker(text string) (exit bool, label string, ok bool)`:

- `ok=true` only for `exit` or `continue` directives.
- `exit=true` → Avenor sets a flag on the current iteration after the phase ends.
- Unknown directives → `ok=false`, ignored.

Extraction is done inside `waitForSession` on the same chunk events already
scanned for `[status: ...]`. The marker is **not** stripped from the forwarded
event — raw text consumers still see it.

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

`exit_marker` is present and `true` only when a `[loop: exit]` marker fired
in this phase.

### `avenor.loop.end`

Emitted once after the loop (or pre sequence) finishes.

```json
{
  "event": "avenor.loop.end",
  "run_id": "a3f9...",
  "ts": 1715000000000,
  "iterations_completed": 2,
  "exit_reason": "marker",
  "exit_label": "tests green"
}
```

`exit_reason` is one of: `marker`, `max_iterations`, `phase_failure`,
`cancelled`, `timeout`.

### Classify / digest

| Event | Class | Excerpt |
|---|---|---|
| `avenor.loop.start` | MILESTONE | `"loop start, max_iterations=N"` |
| `avenor.phase.start` | ACTIVITY | `"phase: <name> (iter N)"` |
| `avenor.phase.end` | MILESTONE | `"phase: <name> → <stop_reason>"` |
| `avenor.loop.end` | MILESTONE | `"loop end: <exit_reason>"` |

---

## Sentinel Behaviour

The existing sentinel mechanism is unchanged. `writeSentinel` fires after the
entire loop finishes (or aborts), not after each phase. The exit code reflects
the loop's overall outcome:

- Loop exits cleanly (marker or max_iterations reached normally) → `0`
- Any phase non-clean stop → that phase's exit code propagates
- Timeout / cancellation → `124` / `130` as today

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
| `internal/digest/loopmarker.go` | `ExtractLoopMarker(text string) (exit bool, label string, ok bool)` |

### Files to modify

| File | Change |
|---|---|
| `internal/cli/cli.go` | `--loop-file` flag; dispatch to `runLoop` when set |
| `internal/cli/cli.go` | `waitForSession` gains a `loopMarkerSeen *bool` out-param (or returned in result) so the loop runner can read it after the session ends |
| `internal/digest/marker.go` | Optional: share regex infrastructure with `loopmarker.go` |
| `internal/digest/classify.go` | Add cases for the four new event types |
| `internal/digest/digest.go` | Add excerpts for the four new event types |

### Suggested order

1. **`loopmarker.go`** — pure function, no dependencies, easy to test first.
2. **`loop.go` config structs + validation** — JSON decode, template expansion,
   error cases; no execution yet.
3. **`loop.go` runLoop orchestrator** — calls `runSingleAttempt` per phase,
   emits lifecycle events, evaluates exit conditions.
4. **`cli.go` wiring** — `--loop-file` flag + dispatch.
5. **`waitForSession` loop marker extraction** — hook into existing chunk scan.
6. **`classify.go` + `digest.go`** — mechanical additions after events are defined.

### Testing

- `internal/digest/loopmarker_test.go` — covers `[loop: exit]`, `[loop: exit | label]`,
  `[loop: continue]`, unknown directive, malformed.
- `internal/cli/loop_test.go` — config load/validate (table-driven), template
  rendering, `backoffDelay` reuse.
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
