# Avenor

Avenor is a small, single-binary ACP (Agent Client Protocol) client for coordinating AI agents in event-driven workflows. It sits between orchestration harnesses, like `.botfiles`, and ACP-speaking runtimes, like OpenCode, so the two can talk without getting in each other's way.

## Installation

Download the latest binary for your platform from [GitHub Releases](https://github.com/sdougbrown/avenor/releases):

```bash
curl -fsSL https://github.com/sdougbrown/avenor/releases/latest/download/avenor_darwin_arm64 -o avenor
chmod +x avenor
./avenor --version
```

## Permission handling

Use `--permission-handler file:<path>` when your backend forwards tool approval through ACP `session/request_permission`. Avenor keeps the request in a file-based handshake so operators can review it and send back a response without breaking the flow. See [docs/permission-handler.md](docs/permission-handler.md) for the request and response JSON shapes.

## Consumer integration

`.botfiles`, the reference consumer harness, uses a sentinel path to derive the permission base and keep approvals connected to the right run. OpenCode `/dispatch-jockey` surfaces permission events to `/answer-jockey`, and Claude Code grooming uses the same `<perm-base>.req` / `.req.response` pair. See [sdougbrown/.botfiles](https://github.com/sdougbrown/.botfiles).

## Name

*Avenor* is the chief stable officer of a king, a nod to the horse/mule/groom/jockey vocabulary already used in agent orchestration frameworks.

Someone still has to clean out the stables, but at least the naming makes the chores easier to remember.
