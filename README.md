# Avenor

Avenor is a single-binary ACP (Agent Client Protocol) client designed to orchestrate AI agents in structured, event-driven workflows. It acts as a stable intermediary—the chief stable officer—between orchestration harnesses (like `.botfiles`) and ACP-speaking runtimes (like OpenCode).

## Installation

Download the latest binary for your platform from [GitHub Releases](https://github.com/sdougbrown/avenor/releases):

```bash
curl -fsSL https://github.com/sdougbrown/avenor/releases/download/v0.2.0/avenor_darwin_arm64 -o avenor
chmod +x avenor
./avenor --version
```

## Permission handling

Use `--permission-handler file:<path>` when the backend forwards tool approval through ACP `session/request_permission`. Avenor writes the pending request to `<path>.req`, waits for the operator response at `<path>.req.response`, then relays the answer back to the backend. See [docs/permission-handler.md](docs/permission-handler.md) for the request and response JSON shapes.

## Consumer integration

`.botfiles`, the reference consumer harness, auto-derives the permission base from its sentinel path and keeps approval in the orchestration loop: OpenCode `/dispatch-jockey` surfaces permission events to `/answer-jockey`, and Claude Code grooming handles the same `<perm-base>.req` / `.req.response` round-trip. See [sdougbrown/.botfiles](https://github.com/sdougbrown/.botfiles).

## Name

*Avenor* is the chief stable officer of a king—a reference to the horse/mule/groom/jockey dispatch vocabulary already in use within agent orchestration frameworks.
