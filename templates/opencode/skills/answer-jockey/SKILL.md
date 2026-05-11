# answer-jockey

Use this skill when the operator needs to answer an Avenor `permission.request` handled through `--permission-handler file:<path>`.

## Inputs

You need the base handler path. Avenor writes the request to:

```text
<path>.req
```

You must write the response to:

```text
<path>.req.response
```

## Workflow

1. Read `<path>.req`.
2. Present the question, tool, and options to the operator.
3. Ask the operator to choose one option.
4. Write `<path>.req.response` as JSON.

## Request Shape

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
  "payload": {}
}
```

## Response Shape

```json
{
  "outcome": "selected",
  "option_id": "allow",
  "message": "Approved by operator"
}
```

Use the exact `option_id` from the selected request option. `message` is optional. If there is no safe answer, choose the deny/reject option when present.

Do not write any other file format. Do not use `QUESTION:` prose markers.
