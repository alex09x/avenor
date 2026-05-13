# Control Protocol

Avenor accepts JSON-RPC 2.0 requests over a Unix domain socket when `--control-socket` is passed. An optional `--http-debug` adapter exposes the same state over HTTP for rapid debugging.

The control protocol is additive to fire-and-wait mode. When `--control-socket` is absent, the process behaves exactly as before.

## Quick Start

### One-Shot Mode (single prompt, live monitoring)

```sh
# Start avenor with a control socket
avenor \
  --control-socket /tmp/avenor.sock \
  --http-debug :8080 \
  --prompt "List the files in this directory and exit." \
  --on-event /tmp/events.ndjson \
  --sentinel-file /tmp/done.env

# In another terminal, inspect status
avenor control --socket /tmp/avenor.sock status

# Tail live events
avenor control --socket /tmp/avenor.sock tail

# Cancel the run
avenor control --socket /tmp/avenor.sock cancel
```

### Stable Mode (multiplexed supervisor)

```sh
# Start the supervisor
avenor stable --control-socket /tmp/avenor-stable.sock

# Spawn a child runtime
avenor control --socket /tmp/avenor-stable.sock prompt "Review PR #42" -- spawn

# List all runtimes
avenor control --socket /tmp/avenor-stable.sock list

# Cancel a specific runtime
avenor control --socket /tmp/avenor-stable.sock cancel rt_1

# Shut down the supervisor
avenor control --socket /tmp/avenor-stable.sock shutdown graceful
```

## Protocol

### Transport

Line-delimited JSON over Unix domain socket. One JSON object per line.

### Requests

```json
{"jsonrpc":"2.0","id":1,"method":"status","params":{}}
```

- `id` — string or number. Echoed in the response.
- `method` — one of the methods below.
- `params` — method-specific JSON object.

### Success Responses

```json
{"jsonrpc":"2.0","id":1,"result":{"phase":"working","session_id":"ses_1"}}
```

### Error Responses

```json
{"jsonrpc":"2.0","id":1,"error":{"code":-32010,"message":"permission_denied"}}
```

Error codes:
| Code | Meaning |
|---|---|
| -32700 | Parse error |
| -32600 | Invalid request |
| -32601 | Method not found |
| -32602 | Invalid params |
| -32000 | Server error (with message) |
| -32001 | No pending permission |
| -32010 | Permission denied (not owner) |
| -32020 | Backend prompt unsupported |

### Notifications (Server → Client)

```json
{"jsonrpc":"2.0","method":"event","params":{"event":"agent.status","phase":"working",...}}
```

Events match `--on-event` NDJSON format exactly. The `subscribe` method enables event delivery on the controlling connection. Subscribers receive live events only (from `subscribe` onward). Read the `--on-event` log for history.

Slow subscribers (256-event buffer full): the oldest pending event is dropped and one `subscriber.lagged` notification is emitted with `dropped_count`. Coalesced while still lagging.

## Methods

### One-Shot Methods

These work when `avenor` is started with `--control-socket` (no `runtime_id` in params):

#### `status`

```json
{"jsonrpc":"2.0","id":1,"method":"status"}
```

Returns:
```json
{
  "session_id": "ses_1",
  "run_id": "abc123",
  "run_label": "phase-1",
  "phase": "working",
  "phase_label": "go test ./...",
  "last_event": "tool.call",
  "retry_attempt": 1,
  "max_retries": 3,
  "pending_permission": false,
  "permission": null,
  "started_at": 1700000000000,
  "updated_at": 1700000001000,
  "turn_state": "running"
}
```

#### `subscribe`

```json
{"jsonrpc":"2.0","id":1,"method":"subscribe"}
```

Returns `{"subscribed":true}`. After this, event notifications arrive on this connection.

#### `cancel`

```json
{"jsonrpc":"2.0","id":1,"method":"cancel"}
```

Cancels the run (equivalent to SIGINT). Writes `STOP_REASON=cancelled` to the sentinel.

#### `prompt`

```json
{"jsonrpc":"2.0","id":1,"method":"prompt","params":{"text":"Continue with the next step."}}
```

Queues a follow-up prompt. If the session is idle, starts immediately; otherwise starts after the current turn ends. Requires ownership.

#### `answer_permission`

```json
{"jsonrpc":"2.0","id":1,"method":"answer_permission","params":{"request_id":"req_17","option_id":"allow"}}
```

Answers the currently pending permission request. Requires ownership.

### Stable Methods

These work with `avenor stable` and require `runtime_id` for scoped operations:

#### `spawn`

```json
{
  "jsonrpc":"2.0","id":1,"method":"spawn",
  "params":{
    "prompt":"Review PR #42",
    "dir":"/repo/A",
    "label":"review-42",
    "auto_approve":true
  }
}
```

Returns:
```json
{
  "runtime_id": "rt_1",
  "session_id": "ses_xyz",
  "on_event": "/tmp/avenor-stable/abc123/rt_1/events.ndjson",
  "sentinel_file": "/tmp/avenor-stable/abc123/rt_1/sentinel.env"
}
```

Required: `prompt` or `prompt_file`, `dir`. Optional: `agent`, `label`, `model`, `server_url`, `on_event`, `sentinel_file`, `permission_handler`, `auto_approve`, `timeout`, `max_retries`.

If `on_event` or `sentinel_file` is omitted, stable mode creates per-runtime files under `$TMPDIR/avenor-stable/<supervisor_run_id>/<runtime_id>/`.

#### `list`

```json
{"jsonrpc":"2.0","id":1,"method":"list"}
```

Returns all active and recently-completed runtimes with status summaries. No ownership required.

#### `shutdown`

```json
{"jsonrpc":"2.0","id":1,"method":"shutdown","params":{"mode":"graceful"}}
```

Shuts down the supervisor. `graceful` cancels children and waits up to `--shutdown-timeout` (default 10s). `kill` cancels children immediately. Requires ownership.

#### Runtime-scoped methods (require `runtime_id`)

- `status {"runtime_id":"rt_1"}` — runtime-specific status
- `cancel {"runtime_id":"rt_1"}` — cancel one runtime
- `prompt {"runtime_id":"rt_1","text":"Continue"}` — send a prompt to one runtime
- `answer_permission {"runtime_id":"rt_1","request_id":"req_1","option_id":"allow"}` — answer permission for a runtime

## Permission Resolution Precedence

When `--control-socket` is active, permission requests are resolved by trying these sources in order:

1. **Auto-approve** (`--auto-approve` flag) — resolves immediately.
2. **Control owner** — the connected owner may answer within a 1s claim window.
3. **File handler** (`--permission-handler file:<path>`) — writes `.req`, polls `.req.response`.
4. **No resolver** — `permission.request` is emitted, backend waits until context cancellation or backend timeout.

`permission.response` events are emitted for all resolution paths (auto-approve, control, file).

## Owner Semantics

The first connection to issue a mutating method (cancel, prompt, answer_permission, spawn, shutdown) becomes the owner. Non-owner mutating commands fail with error code `-32010` (`permission_denied`). Ownership is released when the owner connection closes.

Multiple connections may observe (subscribe, status, list). Event subscriptions from non-owners continue to receive notifications.

## HTTP Debug Adapter

When `--http-debug :8080` is passed, the process starts an HTTP debug adapter bound to localhost:

```
GET  /status            — current snapshot JSON
GET  /events            — Server-Sent Events stream (SSE)
POST /cancel            — cancel the run
POST /answer-permission — answer a permission request
```

The HTTP adapter is an in-process debug tool. It binds only to localhost by default (`127.0.0.1`). The Unix socket remains the source of truth for ownership, lifecycles, and permission state.

## Socket Lifecycle

- Parent directory created with `0700` if needed.
- Fails fast if another listener is active on the path.
- Stale socket unlinked only after a failed dial proves no process is listening.
- Socket file chmodded to `0600`.
- Socket unlinked on clean shutdown.

## Fire-and-Wait Compatibility

A caller that passes only `--on-event` and `--sentinel-file` without `--control-socket` gets exactly today's behavior. No socket is created, no goroutines are started, and the hot path is unchanged.
