# Backends

Stage 2 ships one backend: `opencode-acp`.

## Discovery

The default CLI resolves the requested opencode ACP source in this order:

1. `--server-url <url>`
2. `AVENOR_OPENCODE_URL`
3. Spawn `opencode acp --pure` for this Avenor invocation

The flag wins over the environment variable. If neither is set, Avenor uses the subprocess fallback.

The Stage 2 opencode provider implements the documented ACP stdio transport. If a URL is selected, the provider fails cleanly because no network ACP transport is defined by the current opencode ACP documentation.

## opencode-acp

The backend uses OpenCode's ACP JSON-RPC protocol and the Stage 1 event mapper. Subprocess mode starts `opencode acp --pure --log-level WARN --cwd <dir>` and communicates over stdin/stdout.

Resumability depends on server lifetime:

External server mode is intended for long-lived harnesses. The backend can resume a persisted session ID when the same OpenCode data store is available.

Subprocess fallback mode is a convenience path for direct CLI use. It may not survive process exit or server restart unless OpenCode has persisted enough session state for a later process to resume it.

## Capabilities

`opencode-acp` supports:

- New sessions
- Session resume
- Prompt execution
- Client-side cancellation
- Event streaming
- Permission request relay when ACP emits `session/request_permission`

Stage 2 does not add a second backend. `--backend` accepts only `opencode-acp`.
