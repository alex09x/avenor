# Changelog

## v0.5.0 — 2026-05-11

### Added

- `avenor answer <perm-base>` subcommand: reads `<perm-base>.req`, validates
  `--option` against the offered set, and atomically writes
  `<perm-base>.req.response`. Flags: `--option <id>` (required),
  `--message <text>` (optional), `--outcome selected|cancelled` (default
  `selected`), `--force` (overwrite existing response).
- Replaces the printf/jq response-write block in `opencode/skills/answer-jockey`
  and `agents/groom.md` (Stage 3 of the avenor-subsume-consumer-prose refactor).

## v0.4.1 — 2026-05-11

### Fixed

- `avenor watch` plain mode now emits nothing for JSON lines lacking an `event` field. Previously it emitted `EVENT   ` noise for legacy text-protocol input that had no event key.

## v0.4.0 — 2026-05-11

### Added

- `avenor watch --since-cursor <path>`: persist byte offset to a cursor file, atomically rewrite the cursor on EOF, rewrite every 10 events in follow mode.

## v0.3.0 — 2026-05-11

### Added

- `avenor watch <log>` subcommand: plain digest format (`EVENT name session_id excerpt`), per-event excerpt mapping (`.content.text` for chunks, `kind:title [status]` for tools, etc.), `--follow` and `--format json` flags.

## v0.2.0 — 2026-05-11

### Added

- Documented the `--permission-handler file:<path>` flow end to end (`docs/permission-handler.md` was added in v0.1.0; v0.2.0 promotes it to README + consumer integration story).
- `templates/opencode/skills/answer-jockey/SKILL.md` is now the reference consumer skill for responding to permission requests (installed downstream in `.botfiles`).

### Notes

- Version tag is now injected via ldflags (`-X main.Version={{ .Version }}`), so `avenor --version` reports the release tag instead of a hardcoded constant.

## v0.1.1 — 2026-05-11

### Changed

- `cmd/avenor/main.go`: replaced `const Version` with `var Version` so goreleaser's `-ldflags -X main.Version=...` injects the real tag. Previously `avenor --version` reported `v0.0.1` regardless of the release.

## v0.1.0 — 2026-05-11

### Added

- Initial MVP CLI: `avenor`, `avenor probe`, `--prompt-file`, `--on-event`, `--dir`, `--model`, `--timeout`, `--agent`.
- `opencode-acp` runtime adapter (`internal/runtime/opencodeacp/`).
- File permission handler (`--permission-handler file:<path>`, `internal/permission/`).
- NDJSON event vocabulary (`agent.message_chunk`, `agent.thought_chunk`, `tool.call`, `tool.call_update`, `user.message_chunk`, `session.plan`, `permission.request`, `session.end`); `docs/events.md`.
- Stop reasons: `end_turn` / `cancelled` / `max_tokens` / `max_turn_requests` / `refusal` / `timeout`. Exit map: `end_turn=0`, `refusal=2`, `max_tokens=3`, `max_turn_requests=4`, `cancelled=130`, `timeout=124`.

## v0.0.1 — 2026-05-11

Initial scaffolding.
