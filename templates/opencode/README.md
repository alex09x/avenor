# Avenor OpenCode Template Pack

This pack is a starting point for consumers that run OpenCode agents through Avenor.

The pieces are meant to be used together:

- `prompts/jockey.md` defines the lead agent contract.
- `prompts/mule.md` defines a literal executor for small mechanical work.
- `prompts/horse.md` defines a bounded implementation executor.
- `skills/answer-jockey/SKILL.md` answers Avenor file permission requests.

In the Avenor world, jockey asks operator questions through ACP permission requests, not prose markers like `QUESTION:`. When ACP emits `session/request_permission`, Avenor writes the file format documented in `docs/permission-handler.md` and emits a `permission.request` event. The answer-jockey skill reads `<path>.req`, presents the question to the operator, and writes `<path>.req.response`.

These files are templates. They are not runtime policy, and consumers should adapt them to their own permission model, agent names, and verification standards.
