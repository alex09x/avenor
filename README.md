# Avenor

Avenor is a small, single-binary ACP (Agent Client Protocol) client that helps orchestration harnesses and ACP-speaking backends work together without stepping on each other. Think of it as the bit that sits between a harness like `.botfiles` and a backend like OpenCode.

## Installation

Grab the latest binary for your platform from [GitHub Releases](https://github.com/sdougbrown/avenor/releases), make it executable, and check that it runs:

```bash
curl -fsSL https://github.com/sdougbrown/avenor/releases/latest/download/avenor_darwin_arm64 -o avenor
chmod +x avenor
./avenor --version
```

If you want a deeper tour, the docs cover the permission handler, event flow, plan, loop, and backend support in more detail.

## Permission handling

When your backend forwards tool approval through ACP `session/request_permission`, point `--permission-handler` at a file path:

```bash
--permission-handler file:<path>
```

Avenor writes the request there and reads the response back from the same handshake. See [docs/permission-handler.md](docs/permission-handler.md) for the request and response JSON shapes.

## Consumer integration

If you want to see Avenor from the consumer side, [sdougbrown/.botfiles](https://github.com/sdougbrown/.botfiles) is the reference harness. For the surrounding event model and loop mechanics, see [docs/events.md](docs/events.md), [docs/loop.md](docs/loop.md), [docs/plan.md](docs/plan.md), and [docs/backends.md](docs/backends.md).

## Name

*Avenor* is the chief stable officer of a king, a nod to the horse/mule/groom/jockey vocabulary already used in agent orchestration frameworks.

Someone still has to clean out the stables, but at least the naming keeps the chore list from bolting.
