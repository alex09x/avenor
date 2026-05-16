# opencode-http fixtures

Live-captured JSON fixtures from opencode serve v1.14.50, used for unit tests
of the HTTP provider mapping layer.

## Files

| File | Description |
|------|-------------|
| `session_create.json` | POST /session response — session creation |
| `session_get.json` | GET /session/:id response — session info (resume check) |
| `message_complete.json` | POST /session/:id/message response — normal prompt completion (finish: "stop") |
| `events_complete.json` | SSE event stream capture during a normal prompt completion |
| `message_cancel.json` | POST /session/:id/message response — cancelled message with error info |
| `events_cancel.json` | SSE event stream capture during a cancelled prompt |

## Missing

- **permission.request**: Permission events were not observed in HTTP mode during
  Phase 0 testing. The opencode serve may auto-approve tools internally or may use
  a different permission mechanism than the ACP `session/request_permission` event.
  These fixtures will be added in Phase D when the permission path is confirmed.
- **permission.response**: Same as above — dependent on confirming the HTTP permission
  model.

## Regeneration

To regenerate these fixtures, start opencode serve and run manual prompts against it.
See `internal/runtime/opencodehttp/doc.go` for the endpoint contracts.
