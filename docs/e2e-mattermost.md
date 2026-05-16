# End-to-end testing identity for prod Mattermost

This document defines **who** an autonomous iter / review / audit run logs in as when it needs to exercise the plugin on the production Mattermost instance (`mattermost.237575.xyz`). The browser-driver tool (`agent-browser`) is configured separately in `skills/agent-browser/SKILL.md`.

## Identity: `fulcrum-e2e`

A dedicated SSO user exists in Keycloak `moat` realm + Mattermost `vctcn` team for fulcrum's e2e flows. **Always use this identity**, never a human account.

| Field | Value | Source |
|---|---|---|
| Keycloak realm | `moat` | `pve-vctcn/apps/vctcn-app1/main.tf` |
| Keycloak username | `fulcrum-e2e` | runtime; IaC commit pending `pve-vctcn#126` |
| Keycloak user uuid | `5ca0a2f7-7f12-44a1-a962-363f8d5aa68b` | created 2026-05-16 |
| Mattermost username | `fulcrum-e2e` (auto-created on first SSO) | `auth_service=gitlab`, `auth_data=<keycloak uuid>` |
| Mattermost team | `vctcn` | `mmctl --local team users add vctcn fulcrum-e2e` |
| Mattermost channels | `vctcn/fulcrum` (and any future channel a test needs) | `mmctl --local channel users add vctcn:fulcrum fulcrum-e2e` |
| Credential | sops `mattermost_e2e_username` / `mattermost_e2e_password` | `pve-vctcn/_shared/secrets/secrets.yml` |

## Reading the credential

```bash
USER=$(sops -d /Users/mouriya/Ext/code/pve-vctcn/_shared/secrets/secrets.yml \
       | awk -F': ' '/^mattermost_e2e_username:/{print $2}')
PW=$(sops -d /Users/mouriya/Ext/code/pve-vctcn/_shared/secrets/secrets.yml \
     | awk -F': ' '/^mattermost_e2e_password:/{print $2}')
```

Use as transient env values only. Do **not** echo / commit / paste them into PR bodies, GH comments, evidence files, or trace logs.

## Login flow (standard SSO, no bypass)

```text
agent-browser open https://mattermost.237575.xyz
→ Mattermost local login form renders; the SSO entry "Keycloak" is below it.
→ click the "Keycloak" link
→ redirected to https://keycloak.237575.xyz/realms/moat/protocol/openid-connect/auth?...
→ fill input[name="username"] with $USER
→ fill input[name="password"] with $PW
→ submit "Sign In"
→ land on https://mattermost.237575.xyz/vctcn/channels/<channel>
```

Verified working 2026-05-16 by supervisor; screenshot of post-login Town Square at `/tmp/fulcrum-e2e-verified.jpg` during that run.

## Failure modes and the right next step

- **`moat init` fails / browser controller down** → file blocker against `fulcrum#221`, stop. Do not try alternate identities.
- **Keycloak rejects credentials** → sops password drifted from Keycloak. Reset via Keycloak admin API + `sops set _shared/secrets/secrets.yml '["mattermost_e2e_password"]' '"<new>"'`. Track under `pve-vctcn#126`.
- **Login lands at `/select_team` (no channels)** → Mattermost membership lost. Re-add: `mmctl --local team users add vctcn fulcrum-e2e` + `mmctl --local channel users add vctcn:fulcrum fulcrum-e2e`. Document in `pve-vctcn#126` followup.
- **A test needs a different channel** → first `mmctl --local channel users add vctcn:<channel> fulcrum-e2e`, then proceed. Membership is a per-channel ACL, not implicit.

## What you must NOT do

- Use the human operator `mouriya`'s SSO credentials. That account is for the real person, not for autonomous agents.
- Bypass SSO by creating a Mattermost Personal Access Token (PAT) and routing API calls around Keycloak. The contract is "SSO via a dedicated SSO user", not "skip SSO".
- Create per-run throwaway accounts. The identity is stable and shared — there is one `fulcrum-e2e`, persistent.
- Persist cookies into the moat-browser long-term profile in a way that other autonomous flows or humans would inherit.

## Why this file is here (and not under `skills/agent-browser/`)

`skills/agent-browser/` is for **tool configuration** — which controller, which browser session — not for **test identity**. The identity belongs with the testing concern (e2e of fulcrum's Mattermost integration), so it lives under `docs/`. The driver tool and the driven user are intentionally decoupled: tomorrow a different driver could log in as `fulcrum-e2e` against the same Mattermost.

## IaC followup

`Mouriya-Emma/pve-vctcn#126` tracks committing this user to tofu so it survives a Keycloak rebuild. Until that lands, the user exists only as runtime state created via Keycloak admin API.
