# Rule: e2e Mattermost identity

End-to-end runs in this repo that drive the prod Mattermost (`mattermost.237575.xyz`) must log in as the dedicated SSO user **`fulcrum-e2e`**. No human credentials, no PAT bypass, no per-run throwaway accounts.

## Credential

Encrypted at `.claude/secrets/e2e-mattermost.enc.yml` (sops + age, committable to git):

```bash
USER=$(sops -d .claude/secrets/e2e-mattermost.enc.yml | yq -r .username)
PW=$(sops -d .claude/secrets/e2e-mattermost.enc.yml | yq -r .password)
```

The age recipient is the operator's existing key (same one used by `pve-vctcn/.sops.yaml`); any host with that age private key configured decrypts it directly — no separate key distribution needed.

## SSO login flow (no bypass)

```text
agent-browser open https://mattermost.237575.xyz
→ Mattermost login page; SSO entry is the "Keycloak" link below the local form
→ scroll into view, click "Keycloak"
→ redirected to https://keycloak.237575.xyz/realms/moat/protocol/openid-connect/auth?...
→ fill input[name="username"] with $USER
→ fill input[name="password"] with $PW
→ click "Sign In"
→ land at https://mattermost.237575.xyz/vctcn/channels/<channel>
```

## What you must NOT do

- Use the human operator `mouriya`'s credentials.
- Generate a Mattermost PAT to skip SSO.
- Create per-run throwaway users in Keycloak / Mattermost.
- Persist cookies into the moat-browser long-term profile beyond the run's lifetime.
- Echo / commit / paste the decrypted password into PR bodies, GH comments, evidence files, or trace logs.

## Membership

`fulcrum-e2e` is a member of Mattermost team `vctcn` and channel `vctcn/fulcrum`. If a test needs another channel, add membership first via mmctl:

```bash
ssh root@vctcn-app1.mouriya.lan \
  "docker exec vctcn-app1-mattermost-1 mmctl --local channel users add vctcn:<channel> fulcrum-e2e"
```

## Failure-mode triage

- **`moat init` fails (controller down)** → file blocker against `Mouriya-Emma/fulcrum#221`, stop.
- **Keycloak rejects credential** → sops password drifted from Keycloak. Rotate via Keycloak admin API + re-encrypt this file with `sops --in-place .claude/secrets/e2e-mattermost.enc.yml`.
- **Login lands at `/select_team`** → Mattermost team membership lost. Re-add via mmctl (above).

## IaC followup

`Mouriya-Emma/pve-vctcn#126` tracks committing the `fulcrum-e2e` Keycloak user to tofu so it survives a Keycloak rebuild. Until that lands, the user exists only as runtime state created via Keycloak admin API on 2026-05-16.
