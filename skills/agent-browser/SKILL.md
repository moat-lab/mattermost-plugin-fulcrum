---
name: mattermost-plugin-fulcrum/agent-browser
description: Project-local agent-browser CLI configuration for mattermost-plugin-fulcrum. Tool-config only — selects the remote moat-browser controller. End-to-end test identity / credentials live in `docs/e2e-mattermost.md`.
---

# agent-browser — tool config

Selects which `agent-browser` controller iter / review / audit runs in this repo talk to. It does **not** describe what to test, who to test as, or where credentials live — that is the e2e-test concern, documented in `docs/e2e-mattermost.md`.

## Controller

`MOAT_CONTROLLER=ws://browser.hb.lan:3000` (loaded from `./.env`).

Pre-flight before any browser run:

```bash
moat init --json | jq .
# expect: { "data": { "sessionId": "..." }, "success": true }
```

If JSON `success: false`, the moat-browser stack on the `browser` Komodo server needs operator attention; file a blocker against `Mouriya-Emma/fulcrum#221` and stop. Do not retry / fall back.

## What this file is NOT

- Not a Mattermost auth contract — see `docs/e2e-mattermost.md`
- Not a place for credentials — sops in `pve-vctcn` is the canonical store
- Not coupled to any specific test identity — `agent-browser` (the CLI) is the **driver**, the user being driven is a separate concern
