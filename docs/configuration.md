# Configuration

Optional `spore.toml` and NixOS module settings. The README covers the
defaults; read this when you need more than one worker agent, an
external work source, or deploy-time draining.

## Matter plugins

Matter plugins pull work from external sources (Linear, GitHub
Issues, ad-hoc adapters) and mirror local task transitions back to
them. Configure one or more in `spore.toml` under
`[matter.<name>]`, or via `SPORE_MATTER_<NAME>__<KEY>` env vars
when the NixOS module is the source of truth (the loader merges
both).

The fleet reconciler runs `Sync` against every enabled matter
before it enumerates tasks, so new upstream tickets land as
`tasks/<slug>.md` files automatically on the next pass. When a
task carrying matter metadata flips to done, an `OnDone` hook
mirrors the close back upstream; a fallback sweep on the next
`Sync` covers misses (reconciler off, adapter down, task edited
out of band).

Tasks created by a matter carry three frontmatter keys: `matter`
(adapter name), `matter_id` (upstream id), and `matter_url`
(deep link, optional).

### Linear

The bundled `linear` adapter polls a configured team for issues in
a ready state, projects each one to `tasks/<slug>.md`, pushes the
issue to in-progress, and mirrors `status: done` flips back to a
configured done state.

```toml
# spore.toml
[matter.linear]
enabled = true
team = "MAR"                       # team key, required
ready_state = "Ready"              # default
in_progress_state = "In Progress"  # default
done_state = "Done"                # default
api_key_env = "LINEAR_API_KEY"
# or, when the NixOS module supplies the secret via systemd:
# api_key_file = "linear-api-key"
```

For NixOS deployments, configure the same shape under
`services.spore-fleet.matters.linear`. Secrets ride systemd
`LoadCredential` and never enter Nix evaluation or `/nix/store`.
See [nixosModules/spore-fleet.nix](../nixosModules/spore-fleet.nix).

## Worker mix

The fleet reconciler picks a worker agent (Claude, Codex, or any
binary on PATH) for each task it spawns. Default behaviour without
`spore.toml` is "all Claude". To run a mix, declare a default,
optionally a ratio, and optionally rules keyed on a task's
`complexity:` extra:

```toml
# spore.toml
[fleet.workers]
default = "claude"

[fleet.workers.ratio]
claude = 70
codex = 30

[fleet.workers.rules]
mechanical = "codex"
deep = "claude"
```

Selection precedence:

1. An explicit `agent:` already in the task frontmatter wins.
2. A rule whose key matches the task's `complexity:` value wins next.
3. Otherwise the ratio balancer picks the agent whose currently
   spawned share is furthest below its target share, so the active
   fleet converges on the configured split.
4. Otherwise `default` (or `claude` when omitted) is used.

The picked agent is written back into the task's frontmatter
`agent:` when reconcile spawns the session, so downstream tools see
the same answer the launcher used. Coordinator agent selection lives
under the separate `[coordinator]` section and is never subject to
the worker ratio.

A project without `spore.toml` (or an operator who must not edit it)
sets the worker default host-side instead:

```
spore fleet worker-agent            # print the effective default and its source
spore fleet worker-agent codex      # write it to <state dir>/workers.toml
spore fleet worker-agent --clear    # drop the host-side key
```

`<state dir>` is `$XDG_STATE_HOME/spore/<project>`. The file takes the
same `[fleet.workers]` tables; a key or table set there wins over
`spore.toml` (a ratio or rules table replaces the whole map). Role-loop
panes (`loop: role`) take the task's `agent:`, else this effective
`default`, else `claude`; they never use ratio or rules, so one task's
engineer and reviewers stay on one agent. `SPORE_AGENT_BINARY` is the
claude worker launcher and is ignored for `agent: codex`, which starts
codex on the brief directly.

## Editor

The coordinator can connect to the operator's VS Code window (Claude
Code's `/ide`) so the editor selection and diagnostics reach it. Off
by default; opt in per project:

```toml
# spore.toml
[editor]
auto_connect_ide = true
```

When on, the kernel spawns the coordinator session with
`CLAUDE_CODE_AUTO_CONNECT_IDE=1`. Claude Code then connects to the
window whose workspace folders contain the project root; with no such
window it stays unconnected. An operator-set
`CLAUDE_CODE_AUTO_CONNECT_IDE` wins over the knob. Workers and role
panes never get it: one Claude Code instance holds a window at a time.
See [todo/editor-integration.md](todo/editor-integration.md).

## Graceful deployment

When the NixOS module is enabled with `gracefulDeploy.enable =
true` (the default), pre and post activation hooks drain active
workers around `nixos-rebuild switch` and colmena deploys:

- pre-activation disables the kill-switch, drops a wrap-up
  message into every active worker's inbox, waits up to
  `gracefulDeploy.timeout` seconds for them to flush, then kills
  any holdouts;
- post-activation re-enables the kill-switch so the next
  reconcile pass repopulates the fleet.

The `preScript` and `postScript` paths are also exposed as
read-only options so a colmena `deployment.preActivation` /
`postActivation` hook can drive the same drain remotely. Set
`gracefulDeploy.enable = false` for one-off worker tiers whose
tasks should never see a wrap-up signal.
