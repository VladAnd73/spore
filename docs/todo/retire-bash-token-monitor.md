**Status**: kernel-change-landed, per-project swap pending

# retire the per-project bash token-monitor in favor of the kernel hook

> Update 2026-11-11: sessions run with a 1M context window, so the
> max-tier bands moved up 100k. Coordinator: soft 250k, hard 290k,
> force 300k. Max-tier workers and role panes: soft 250k, wrap 280k,
> force 300k. Sub-max caps are unchanged.

> Update 2026-07-28: the kernel monitors now run three bands. The old
> hard/wrap cap means "finish the in-flight unit, start nothing new";
> a higher force cap (coordinator 200k; worker 195k max / 140k sub)
> demands an immediate wrap; workers also get a one-time soft warning
> 30k under the wrap cap. Workers resolve their tier from the
> `[fleet] account_tier` spore.toml knob, injected at spawn as
> SPORE_ACCOUNT_TIER (unset = sub-max caps). Projects still on the
> bash shim (marketer, crm-gateway) are unaffected until they swap;
> when they do, set the knob to match the host's account tier.

## Problem

Two of three projects on this host still ship a 224-line bash
`bin/dev/spore-token-monitor` Stop hook (marketer and crm-gateway;
crm-gateway-ruby-client does not). Spore itself uses the kernel
hook directly in its own `.claude/settings.json`:

```
spore coordinator token-monitor
spore worker token-monitor
spore fleet replenish-hook
spore hooks watch-inbox
```

The bash shim and the kernel command overlap in purpose (context-budget
soft / hard caps on Stop) but they were authored independently. One
real workflow delta blocks the swap, plus a parallel event-ledger
system the bash hook builds out that the kernel covers a different
(better) way.

## The blocking delta (RESOLVED)

### Coordinator hard-cap action

- **Bash** hands the LLM a literal respawn-pane command that
  preserves the tmux session AND any attached SSH client:

  ```
  tmux respawn-pane -k -t spore/<project>/coordinator:0 \
    'exec claude... "$(cat <project>/bootstrap/coordinator/role.md)"'
  ```

- **Kernel before fix** (`internal/coordinator/tokenmonitor/tokenmonitor.go`,
  hard-cap path at line 126-132 and soft-cap at 141-147 in the prior
  shape): the wrap-up message instructed the LLM to run `tmux
  kill-session`, which drops the tmux session AND any attached SSH
  client. Real regression for SSH-attached operators on this host:
  their terminal closes when the coordinator caps.

The earlier draft of this spec pointed at `internal/fleet/coordinator.go`
`ReapCoordinator`, which is the wrong site: that function is only
called on fleet `Disable()` (true shutdown), where `kill-session` is
semantically correct. The LLM-runnable wrap-up command lives in
`tokenmonitor.Check`'s `result.Message`. Confirmed during Phase A
implementation 2026-05-25.

**Fix landed**: kernel commit on branch
`coordinator-tokenmonitor-respawn-pane` replaces the `tmux
kill-session` line in both hard- and soft-cap messages with:

```
tmux respawn-pane -k -t "$(tmux display-message -p '#S'):0" 'exec /usr/local/bin/spore-coordinator-launch'
```

Self-targets via `display-message -p '#S'` so external-session-pattern
coordinators work without configuration. The exec target is the host
shim that `spore infect` installs (`internal/infect/infect.go:469`).

## Non-blocking notes

### Hook placement + gating

- **Bash**: registered in the host-global
  `~/.claude/settings.json`. Each project's shim self-gates on
  `transcript_path` prefix.
- **Kernel**: registered in the per-project `.claude/settings.json`
  (Claude Code's hook layering handles project gating). Worker /
  non-coordinator gating is via `$SPORE_TASK_INBOX` presence.

Mechanical swap: copy `spore/.claude/settings.json`'s hook block
into each project, remove the per-project entry from the host-global
file.

### Env tunables (dropped from scope)

Audit on 2026-05-25 grepped every project tree, shell rc,
`/etc/spore/coordinator.env`, and systemd user-unit env for
`SPORE_TOKEN_SOFT` / `SPORE_TOKEN_HARD`. Zero external exporters.
Only the bash hooks themselves reference the names, with hardcoded
defaults `150000 / 190000`.

The kernel's defaults now sit 100k higher (`250000 / 290000`, force
`300000`) to match 1M-context sessions; a project still on the shim
wraps earlier until it swaps. If someone later
wants to tune caps per project, adding the env reads is a small
follow-up; not in scope here.

### events.jsonl + scan-worker-completions.pl

The bash hook writes a cross-project ledger at
`~/.local/state/spore/events.jsonl` with `{ts, kind, session_id,
slug, tokens}` per Stop event. crm-gateway (only) ships a perl
consumer, `bin/dev/scan-worker-completions.pl`, registered in the
host-global `~/.claude/settings.json` as a UserPromptSubmit hook
that tails the ledger and surfaces worker events as additionalContext
to the coordinator.

This entire writer + reader pair duplicates the kernel's inbox flow:

- Worker -> coordinator pokes go through
  `internal/hooks/notifycoordinator.go` (`spore hooks
  notify-coordinator`), which drops a file into the coordinator's
  project inbox.
- Coordinator side runs `spore hooks watch-inbox`
  (`internal/hooks/watchinbox.go`,
  `cmd/spore/hooks_cmd.go:35`), already wired in
  `spore/.claude/settings.json` as a Stop hook with
  `asyncRewake: true`. It blocks on inotify, exits 2 on message,
  Claude Code surfaces the rewake as additionalContext.
- The worker spawn (`internal/fleet/coordinator.go:127`,
  `internal/task/lifecycle.go:511`) already sets
  `SPORE_TASK_INBOX=<inbox>` for both coordinator and worker
  sessions, which is what the kernel's gating reads.

Decision (operator-confirmed 2026-05-25): retire the perl alongside
the bash hook. No compat write from the kernel to
`~/.local/state/spore/events.jsonl`; the kernel inbox flow is the
load-bearing path going forward. See `feedback_prefer_kernel_flow.md`
in project memory.

## Plan

### A. Kernel change (LANDED)

Edit to `internal/coordinator/tokenmonitor/tokenmonitor.go`:

1. Hard- and soft-cap `result.Message` strings now embed the
   respawn-pane command instead of `tmux kill-session`. The
   command self-targets via `tmux display-message -p '#S'` so
   external-session-pattern coordinators work without extra config.
2. `coordinatorRespawnCommand` is a package-level constant so both
   messages share one source of truth; if `host-shims-via-nix.md`
   later changes the shim path, this is the one spot to update.
3. `assertRespawnPaneMessage` test helper added to
   `tokenmonitor_test.go`; both `TestCheckHardCap` and
   `TestCheckSoftCap` now assert that the message contains
   `tmux respawn-pane -k`, the `display-message` self-target, the
   correct shim path, and NOT `tmux kill-session`.

No changes needed in `internal/fleet/coordinator.go`,
`spore.toml` schema, or `cmd/spore/coordinator_cmd.go`. Scope dropped
versus the earlier draft:

- `ReapCoordinator` stays as `kill-session` (fleet `Disable()` is a
  true shutdown; `kill-session` is semantically right there).
- `[coordinator].respawn_template` config plumbing was unnecessary;
  the constant suffices and the message is self-targeting.
- `SPORE_TOKEN_SOFT/HARD` env reads stay out of scope: the
  2026-05-25 audit found zero exporters anywhere on this host.

### B. Per-project swap (one PR per project, NEXT)

For marketer and crm-gateway, in either order, after A merges:

1. Add the kernel Stop-hook block to project `.claude/settings.json`
   (copy from `spore/.claude/settings.json` verbatim, minus the
   `gopls-lsp` plugin entry which is project-specific).
2. Delete `bin/dev/spore-token-monitor`.
3. crm-gateway only: delete `bin/dev/scan-worker-completions.pl`,
   the `~/.local/state/spore/scan-worker-completions/` cursor dir,
   and the doc references in `CLAUDE.md:149` and `AGENTS.md:95`
   ("Events are appended to `~/.local/state/spore/events.jsonl`...").
4. Remove the per-project Stop hook entry AND the
   UserPromptSubmit hook entry from the host-global
   `~/.claude/settings.json`. If no other project still depends on
   that global file for spore reasons, leave the file untouched
   beyond removing the spore-specific entries.
5. Smoke: trigger a soft cap (one large turn) and a hard cap
   (sequence of large turns); confirm wrap-up message + respawn-pane
   behavior; confirm SSH client survives. Dispatch a worker and
   confirm worker events still reach the coordinator via the inbox
   flow.
6. Stale ledger cleanup: `rm
   ~/.local/state/spore/events.jsonl` after both projects swap, so
   there is no orphan file giving the impression the system is still
   writing.

## Acceptance

1. (DONE) `tokenmonitor.Check`'s hard- and soft-cap messages
   instruct the agent to run `tmux respawn-pane -k -t
   "$(tmux display-message -p '#S'):0" 'exec
   /usr/local/bin/spore-coordinator-launch'` rather than
   `tmux kill-session`. SSH clients attached to the session
   survive a cap. Test coverage in place
   (`assertRespawnPaneMessage`).
2. `bin/dev/spore-token-monitor` is deleted from marketer and
   crm-gateway. Their `.claude/settings.json` carries the kernel
   hook block.
3. `bin/dev/scan-worker-completions.pl` is deleted from crm-gateway
   along with its cursor dir and the AGENTS.md / CLAUDE.md
   references.
4. The host-global `~/.claude/settings.json` no longer references
   either project's bash hook or the perl scan tool.
5. `~/.local/state/spore/events.jsonl` is gone (no kernel-side
   writer; no remaining reader).
6. Worker events still reach coordinator sessions via the kernel
   inbox flow; verified by a multi-worker smoke.

## Sequencing vs. host-shims-via-nix

The two specs touch overlapping surfaces (shims + hooks) but the
work is independent:

- The kernel PR (A) does not depend on shim delivery; it edits
  Go source and Go tests.
- The per-project swap (B) depends on the kernel PR but not on
  shim delivery.
- The respawn-pane template in A points at
  `/usr/local/bin/spore-coordinator-launch`, which is exactly the
  path `host-shims-via-nix` is migrating. As long as both specs
  agree to keep that path stable (symlink farm into the nix store
  is fine), neither blocks the other.

Pick whichever is cheapest to land first; the other slots in behind.

## Cross-references

- `internal/coordinator/tokenmonitor/tokenmonitor.go` (Phase A
  call site: `Check`'s hard- and soft-cap `result.Message`).
- `internal/fleet/coordinator.go:239` (`ReapCoordinator`, retained
  as `kill-session` for the fleet `Disable()` shutdown path; NOT
  the call site the spec earlier pointed at).
- `internal/fleet/coordinator.go:127` (worker spawn,
  `SPORE_TASK_INBOX=` env).
- `internal/task/lifecycle.go:511` (worker spawn env, same
  variable).
- `internal/hooks/notifycoordinator.go` (worker -> coordinator
  poke).
- `internal/hooks/watchinbox.go`, `cmd/spore/hooks_cmd.go:35`
  (`spore hooks watch-inbox`).
- `cmd/spore/coordinator_cmd.go:385` (`runCoordinatorTokenMonitor`,
  no env reads today; intentionally out of scope here).
- `cmd/spore/worker_cmd.go:46` (`runWorkerTokenMonitor`).
- `.claude/settings.json` (canonical kernel-hook layout).
- crm-gateway `bin/dev/scan-worker-completions.pl` and
  `bin/dev/spore-token-monitor`; marketer `bin/dev/spore-token-monitor`.
- Project memory: `feedback_prefer_kernel_flow.md` (the operator
  call: kernel mechanism over bash compat).
- Stale assumption from the bash shim: "workers must exit 0
  because `claude -p` hangs on injected stderr turns" is outdated;
  `spore-worker-brief` now spawns interactive claude (commit
  `fix(handover): spore-worker-brief uses interactive claude, drops
  -p`), so the kernel `exit 2` shape is safe.
- Related todo: `docs/todo/host-shims-via-nix.md` (overlapping shim
  delivery surface; sequenceable independently).
