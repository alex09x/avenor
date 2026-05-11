# Changelog

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
