# File Permission Handler

Stage 2 supports one permission handler:

```sh
--permission-handler file:/tmp/avenor-permission
```

When the ACP backend emits `session/request_permission`, Avenor writes:

```text
/tmp/avenor-permission.req
```

Then Avenor polls for:

```text
/tmp/avenor-permission.req.response
```

The default timeout is 10 minutes. The default polling cadence is 500 ms.

## Request File

`<path>.req` is JSON:

```json
{
  "request_id": "17",
  "session_id": "ses_123",
  "tool": "bash",
  "question": "Run command?",
  "options": [
    {"optionId": "allow", "kind": "allow"},
    {"optionId": "deny", "kind": "reject"}
  ],
  "payload": {
    "request_id": "17",
    "tool": "bash",
    "question": "Run command?",
    "options": []
  }
}
```

`request_id` is required. `session_id`, `tool`, `question`, and `options` are best-effort normalized fields for consumers. `payload` preserves the full normalized Avenor event fields.

Avenor also emits a `permission.request` NDJSON event after writing the request file.

## Response File

The answer process writes `<path>.req.response`:

```json
{
  "outcome": "selected",
  "option_id": "allow",
  "message": "Approved by operator"
}
```

`outcome` defaults to `selected` when omitted. `option_id` should match one of the request option IDs when the backend provided options. `message` is optional.

After reading the response, Avenor relays it to the ACP backend with `Provider.AnswerPermission`.

## Semantics

Avenor removes any stale response file before writing a new request. It does not remove the request or response after completion, so surrounding tools can inspect what happened.

MVP supports `file:` only. There is no `fifo:` or `stdio:` handler in Stage 2.
