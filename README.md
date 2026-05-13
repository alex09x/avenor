# Avenor

Avenor helps an orchestration harness and an ACP-speaking backend coordinate tool calls and approvals without stepping on each other. ACP (Agent Client Protocol) is the wire protocol that lets those two sides talk; if you've got a harness like `.botfiles` on one side and a backend like OpenCode on the other, Avenor is the bit in the middle that keeps the handoff clean.

## Installation

Grab the release asset for your platform from [GitHub Releases](https://github.com/sdougbrown/avenor/releases), make it executable, and check that it runs. On macOS arm64, the direct download looks like this:

```bash
curl -fsSL https://github.com/sdougbrown/avenor/releases/latest/download/avenor_darwin_arm64 -o avenor
chmod +x avenor
./avenor --version
```

If you want a deeper tour, the docs cover the permission handler, event flow, plan, loop, and backend support in more detail.

## Permission handling

Permission handling matters because a backend can ask for approval mid-run, and Avenor's job is to broker that request without turning the harness into a blocking human-in-the-loop primitive. When your backend forwards tool approval through ACP `session/request_permission`, point `--permission-handler` at a file path:

```bash
--permission-handler file:<path>
```

Avenor writes the request there and reads the response back from the same handshake. See [docs/permission-handler.md](docs/permission-handler.md) for the request and response JSON shapes.

## Consumer integration

If you want to see Avenor from the consumer side, [sdougbrown/.botfiles](https://github.com/sdougbrown/.botfiles) is the reference harness. For the surrounding event model and loop mechanics, see [docs/events.md](docs/events.md), [docs/loop.md](docs/loop.md), [docs/plan.md](docs/plan.md), and [docs/backends.md](docs/backends.md).

## Name

*Avenor* is the chief stable officer of a king, a nod to the horse/mule/groom/jockey vocabulary already used in agent orchestration frameworks.

Someone still has to clean out the stables, but at least the naming keeps the chore list from bolting.
