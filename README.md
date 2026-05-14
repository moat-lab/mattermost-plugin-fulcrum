# mattermost-plugin-fulcrum

Mattermost server-side plugin for the Fulcrum CLI. Sub-deliverable of the
plugin + gRPC remote-exec umbrella [Mouriya-Emma/fulcrum#221](https://github.com/Mouriya-Emma/fulcrum/issues/221) §5.3.

Loads into the Mattermost server process and:

- registers the `/f` slash command with a structured `AutocompleteData` tree
  mirroring the CLI verb catalogue (`fulcrum/cli/JSON_SCHEMA.md`);
- forwards each invocation as `argv` to a remote `rexecd` over gRPC via
  [`github.com/Mouriya-Emma/rexec-go`](https://github.com/Mouriya-Emma/rexec-go);
- parses the CLI JSON envelope and posts the result as a real bot post
  (`post.UserId = botUserID`) so subsequent interactive button clicks can
  `UpdatePost` the original card.

## Repository layout

```
.
├── plugin.json                # manifest (id=fulcrum, 5 platforms, no settings_schema)
├── server/
│   ├── main.go                # plugin.ClientMain entry point
│   ├── plugin.go              # OnActivate / OnDeactivate / EnsureBot wiring
│   ├── configuration.go       # REXECD_ADDR env resolution
│   ├── command.go             # AutocompleteData tree + ExecuteCommand
│   ├── http.go                # ServeHTTP for /action interactive callbacks
│   └── render.go              # CLI envelope → SlackAttachment
├── build/pluginctl/           # minimal deploy CLI (REST upload + enable)
├── Makefile                   # crossbuild 5 platforms + bundle
└── README.md
```

## Configuration

The plugin reads one network coordinate from the Mattermost server process
environment:

| Env var | Required | Example |
|---|---|---|
| `REXECD_ADDR` | yes | `dns:///rexecd.vctcn.internal:50051` |

`plugin.json` deliberately has **no `settings_schema`**: business knobs live
in the Fulcrum CLI, and the only deployment-side knob (`REXECD_ADDR`) is
provisioned by IaC (`pve-vctcn` Mattermost compose template) as a process
env var.

## Build

```sh
make tidy       # go mod tidy
make test       # go test ./server/...
make dist       # crossbuild 5 platforms and bundle dist/fulcrum-<version>.tar.gz
```

The bundle is consumed verbatim by the Mattermost server's plugin upload
endpoint or by `mmctl plugin add`.

## Deploy

```sh
MM_SERVICESETTINGS_SITEURL=https://mattermost.237575.xyz \
MM_ADMIN_TOKEN=<admin-token> \
  ./build/pluginctl/pluginctl deploy fulcrum dist/fulcrum-0.1.0.tar.gz
```

The IaC-friendly path is to commit the bundle URL into the
`pve-vctcn` Mattermost stack and let Terraform `null_resource` +
`mmctl plugin add` upload it during `tofu apply`.

After deploy, set `REXECD_ADDR` on the Mattermost server container and
restart so `OnActivate` can dial the rexecd sidecar.

## Slash command surface

```
/f                              # dashboard
/f dashboard
/f tasks list [--status=...] [--priority=...] [--project=...] [--tag=...] [--page=...]
/f tasks get <id>
/f tasks create --title=<title> [--project=...] [--priority=...]
/f tasks set-status <id> <status>
/f tasks set-priority <id> <priority>
/f tasks diff <id>
/f tasks start-agent <id>
/f tasks kill-agent <id>
/f apps list
/f apps get|deploy|stop <id>
/f apps rollback <id> <deployment-id>
/f apps logs <id> [--service=...] [--tail=...]
/f search <query> [--limit=...]
/f monitor
/f jobs [--scope=all|user|system]
/f projects
```

Each verb is wired through `AutocompleteData` so a Tab-press in Mattermost
guides the user through the same argument shape the plugin forwards to the
CLI.

## Interactive callback contract

Buttons emitted by render layers must point at `/plugins/fulcrum/action`
with a JSON `context.argv` array. The action handler:

1. validates `context.argv` is a `[]string`;
2. runs `fulcrum <argv...> --json` over `rexec-go`;
3. renders the envelope;
4. `UpdatePost`s the original post (which must be owned by the bot —
   the handler refuses otherwise so legacy user-owned posts surface a
   clear error).

## Dependencies

| Module | Why |
|---|---|
| `github.com/mattermost/mattermost/server/public` | plugin SDK + `pluginapi` |
| `github.com/Mouriya-Emma/rexec-go` | gRPC client for the rexecd sidecar |

`rexec-go` is a Go module under `Mouriya-Emma/`; running `make tidy` will
resolve it from GOPROXY.
