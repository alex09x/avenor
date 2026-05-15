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

## Control sockets

Avenor can expose a Unix-domain control socket so another process can inspect status, tail live events, answer permissions, cancel work, and send follow-up prompts while a run is active:

```bash
avenor \
  --control-socket /tmp/avenor.sock \
  --prompt "List the files in this directory and exit." \
  --on-event /tmp/events.ndjson \
  --sentinel-file /tmp/done.env

avenor control --socket /tmp/avenor.sock status
avenor control --socket /tmp/avenor.sock tail
avenor control --socket /tmp/avenor.sock prompt "Continue with the next step"
avenor control --socket /tmp/avenor.sock cancel
```

For long-lived orchestration, `avenor stable` starts a supervisor that can spawn and manage multiple child runtimes:

```bash
avenor stable --control-socket /tmp/avenor-stable.sock

avenor control --socket /tmp/avenor-stable.sock spawn \
  --prompt "Review PR #42" \
  --dir /repo/A \
  --label review-42

avenor control --socket /tmp/avenor-stable.sock list
avenor control --socket /tmp/avenor-stable.sock prompt "Continue" rt_1
avenor control --socket /tmp/avenor-stable.sock cancel rt_1
avenor control --socket /tmp/avenor-stable.sock shutdown graceful
```

The socket also speaks newline-delimited JSON-RPC 2.0 directly, and `--http-debug` can expose loopback-only HTTP/SSE endpoints for debugging. See [docs/control-protocol.md](docs/control-protocol.md) for the full method list, event stream, ownership rules, permission precedence, and HTTP debug surface.

## Phase loops

When a single prompt isn't enough — build once, then test → review → fix until clean — define a loop config and let Avenor run the phases:

```bash
avenor run --loop-file loop.json --auto-approve --sentinel-file run.done
```

Phases emit `[loop: exit]` to finish clean or `[loop: abort | reason]` to escalate. Pre phases run once. Loop phases repeat until exit, abort, or `max_iterations`. The same `avenor stable` supervisor spawns loop runs via `loop_file` in the spawn params. See [docs/loop.md](docs/loop.md) for the full config reference, prompt templates, lifecycle events, and abort mechanics.

## Consumer integration

If you want to see Avenor from the consumer side, [sdougbrown/.botfiles](https://github.com/sdougbrown/.botfiles) is the reference harness. For the surrounding event model and loop mechanics, see [docs/events.md](docs/events.md), [docs/loop.md](docs/loop.md), [docs/plan.md](docs/plan.md), and [docs/backends.md](docs/backends.md).

## Name

*Avenor* is the chief stable officer of a king, a nod to the horse/mule/groom/jockey vocabulary already used in agent orchestration frameworks.

Someone still has to clean out the stables, but at least the naming keeps the chore list from bolting.
