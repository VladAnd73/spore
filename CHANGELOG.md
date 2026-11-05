# Changelog

## Unreleased

- Role loop on a visible draft PR (`docs/github-app.md`). With
  `pr_flow = "draft"` in the new host-side
  `$XDG_STATE_HOME/spore/<project>/github.toml` (0600), `DriveRoleLoop`
  mirrors the loop to GitHub as a per-org GitHub App: engineer round 1
  pushes the branch and opens a draft PR (base: new optional task
  frontmatter `base:`, else the remote default branch) assigned to
  `operator_login`; each verdict becomes a `COMMENT` review headed
  `Review #1` / `Review #2`; later engineer responses become PR
  comments; `PhaseDone` marks the PR ready and the coordinator notice
  becomes `<slug>: done, PR #N ready for operator approval: <url>`;
  an escalation adds one PR comment. Mirror state sits in
  `.spore/<slug>/github.json`; GitHub errors never block the loop
  (every git/gh call and the token mint is capped at 60s, prompts
  off) and reach the coordinator once per distinct error. The PR body
  is the brief's first prose paragraph only. Engineer panes commit
  as `commit_name` / `commit_email`. New `spore gh config [--set k=v]`,
  `spore gh token` (installation token, cached 0600 until 5 min before
  expiry), and `spore gh exec -- <cmd>` (GH_TOKEN and
  GH_ENTERPRISE_TOKEN plus a GIT_CONFIG_* credential helper so gh and https git push act as the app; ssh
  origins refused). Engineer, reviewer, and coordinator role text name
  the flow and `base:`. Consumer impact: off by default and
  byte-identical when off; no consumer repo change. Turning it on needs
  an org-owned GitHub App and an https `origin`; the role and
  coordinator text lands on the next `spore install` / coordinator
  respawn.

- `spore coordinator state ls` / `state archive <id>... --slug NAME`
  keep `state.md` under the 8KB SessionStart budget. `ls` lists `## `
  sections and, inside them, `### ` subsections and bold-lead
  paragraphs with ids and byte sizes. `archive` moves the selected
  units verbatim to host-side
  `$XDG_STATE_HOME/spore/<project>/coordinator/state-archive/<date>-<slug>.md`
  and leaves one link line per archive under `## Archive` in
  `state.md` (archive written and fsynced first, `state.md` replaced
  atomically, refuses an existing archive file, `--dry-run`). The
  `load-state-md.pl` SessionStart hook and the codex start context now
  lead with a size warning when `state.md` exceeds 8KB. The shared
  coordinator role gains a wrap-up step: archive verifiably done blocks
  before respawn. Consumer impact: new subcommand, hook warning above
  8KB, `role.md` wrap-up step; no consumer repo change.

- Fleet workers and role-loop panes can run codex without touching the
  consumer repo. New host-side overlay
  `$XDG_STATE_HOME/spore/<project>/workers.toml` (0600) with the
  `spore.toml` `[fleet.workers]` keys, layered over `spore.toml` (a key
  or table set there wins); `spore fleet worker-agent [claude|codex |
  --clear]` prints or sets its `default`, never writing `spore.toml`.
  `SPORE_AGENT_BINARY` (the claude `spore-worker-brief` launcher on
  hosts) now applies to claude workers only: `agent: codex` workers run
  codex and start on the brief (`codex ... -- "$(cat tasks/<slug>.md)"`).
  Role panes take the task's `agent:`, else the effective worker
  `default`, else claude (never ratio or rules). Codex launches now
  pass `--dangerously-bypass-hook-trust` so the user hooks run, and
  role-body launches end in `--` so a `---` body is not parsed as a
  flag. `SPORE_CODEX_BIN` replaces the codex executable for workers and
  role panes too. Consumer impact: `internal/fleet`, `internal/task`,
  `codexpolicy` only; no consumer repo change. Workers already pinned
  to `agent: codex` on hosts with `SPORE_AGENT_BINARY` set switch from
  claude to codex on their next spawn.

- `spore coordinator swap` (and `/select-agent`) no longer writes the
  project tree. The swapped `driver` / `model` go to a host-side
  override, `$XDG_STATE_HOME/spore/<project>/coordinator/driver.toml`
  (0600), which `LoadCoordinatorConfig` layers over `spore.toml`
  `[coordinator]`: an override driver replaces driver and model
  together; only those two keys are allowed. Precedence: session env,
  then the override, then `spore.toml`, then defaults; a reconciler
  respawn picks the override up. Consumer impact: none in the repo; a
  consumer without `spore.toml` no longer gets an untracked one. A
  `spore.toml` driver flipped by an earlier swap stays as written.

- `select-agent` skill: swap the coordinator agent from inside the
  coordinator session. `/select-agent` (Claude Code) or the skill
  mention (Codex) prompts with the available agents, or takes
  `<agent> [model]` as an argument; on a pick it refuses unless
  `SPORE_TASK_SLUG=coordinator`, flushes `state.md` (noting the swap
  for the incoming agent), and runs `spore coordinator swap --to
  <agent> [--model m] --no-wrap`. New `spore coordinator agents`
  prints each swap driver as `current`, `available` (binary on PATH),
  or `missing`. Consumer impact: the skill lands in every consumer's
  `.claude/skills/` and `.agents/skills/` on the next `spore install`;
  nothing runs until invoked.

- tmux baseline sets `detach-on-destroy off`. With `spore-attach` as
  the login shell the tmux client is sshd's direct child, so a
  `kill-session` on the session the operator was viewing (task done,
  merge, reap) closed the SSH connection. Clients now switch to
  another session. Consumer impact: hosts pick it up on the next
  `nixos-rebuild`; live servers need `tmux set -g detach-on-destroy
  off` or a server restart.

- `spore coordinator swap --to claude|codex [--model M] [--project N]`
  (unit 4 of `docs/todo/codex-coordinator-swap.md`). Asks the live
  coordinator through its inbox to flush `state.md` and waits for the
  write (`--no-wrap` skips, `--force` swaps after `--wrap-timeout`),
  sets `[coordinator]` `driver` / `model` in `spore.toml` keeping
  comments and other keys, sets `SPORE_COORDINATOR_PROVIDER` /
  `SPORE_COORDINATOR_MODEL` in the tmux session env, and respawns the
  session's first pane with the coordinator agent. A driver change
  without `--model` clears the model. From inside the coordinator
  pane, flush `state.md` yourself and pass `--no-wrap`. The respawned
  agent comes from `SPORE_COORDINATOR_AGENT` in the tmux session env,
  then the tmux global env, then `/usr/local/bin/spore-coordinator-launch`
  when installed (never the caller's env); new coordinator
  sessions record it in their session env. Consumer impact: none
  until invoked; the command rewrites `spore.toml`, so commit the
  flip if the project tracks that file. Hosts whose
  `/etc/spore/coordinator.env` sets `SPORE_COORDINATOR_PROVIDER` /
  `SPORE_COORDINATOR_MODEL`: the reconciler env still beats
  `spore.toml`, so a reconciler respawn reverts a swap until those
  lines are cleared or updated.

- Codex context bridge (unit 3 of `docs/todo/codex-coordinator-swap.md`).
  `spore hooks codex-session-start` now also prints SessionStart JSON
  (`hookSpecificOutput.additionalContext`) carrying the main repo's
  `state.md` (only when `SPORE_TASK_SLUG` is unset or `coordinator`,
  as the Claude `load-state-md.pl` hook) and the Claude memory index
  `~/.claude/projects/<key>/memory/MEMORY.md` (every session, as
  claude-code does). The rendered Codex `hooks.json` sets
  `additionalContextLimit: 0` on that handler so Codex does not spill
  context past ~2,500 tokens to a temp file. `spore install` (and the
  `repo-mapped` bootstrap stage) now also copies the skills into
  `.agents/skills/`, the project skill root Codex scans. Consumer
  impact: a Codex session in any repo with a `state.md` or a Claude
  memory dir now gets that text injected at startup, resume, clear
  and compact; commit `.agents/skills/` next to `.claude/skills/`
  (or gitignore it, as spore does), and regenerate `~/.codex/hooks.json`
  (NixOS: rebuild; otherwise `spore hooks codex-config`) or the
  2,500-token spill applies. Memory dir keys now replace every
  non-alphanumeric byte with `-` (claude-code's rule), which also
  fixes the transcript fallback for paths with `.` or `_`.

- Codex: `spore fleet replenish-hook` prints its status line to stderr
  instead of stdout. Codex parses Stop-hook stdout as JSON, so every
  Codex coordinator turn ended with "hook returned invalid stop hook
  JSON output". Claude Code behavior is unchanged.

- Codex config render (unit 2 of `docs/todo/codex-coordinator-swap.md`).
  New `spore hooks codex-config [--spore PATH]` prints the user-level
  Codex `hooks.json`: SessionStart `spore hooks codex-session-start`
  (the thread record the unit-1 inbox sidecar waits on) and the Stop
  token monitors plus `fleet replenish-hook`. The Claude
  `watch-inbox` asyncRewake hook is left out (Codex would run it
  synchronously; the sidecar replaces it). New NixOS option
  `services.spore-fleet.codex.enable` (default off) with
  `codex.package` (default `pkgs.codex`): installs codex system-wide
  and on the reconciler PATH, and renders `~/.codex/hooks.json` for
  the fleet user through home-manager. The `spore infect` host flake
  sets it. Consumer impact: Codex coordinators require
  `codex.enable = true`; on a host without it a Codex session gets no
  Stop hooks at all (no token monitor, no replenish) and nothing
  warns. A pre-existing `~/.codex/hooks.json` fails the whole
  home-manager activation for the fleet user (no
  `home-manager.backupFileExtension` set): the fleet reconcile units
  stop deploying and `nixos-rebuild switch` exits non-zero. Move the
  file aside first, or set `home-manager.backupFileExtension` so
  home-manager renames it. `config.toml` and `auth.json` stay
  unmanaged. The launch shim is unchanged (`hooks.json`
  is already in its link list). The tracked repo-level
  `.codex/hooks.json` is removed: Codex loads project and user hooks
  without dedupe, so it double-fired the Stop hooks in trusted
  checkouts and ran `watch-inbox` as a blocking Stop hook. Consumers
  carrying a copy should drop it for the same reason.

- Fleet: `[fleet] pre_start = "<shell command>"` runs a project hook
  in worker sessions and role panes (not the coordinator) before the
  agent starts, in the worktree with the session's `SPORE_*` env,
  serialized per slug by `flock .spore/<slug>/state/pre-start.lock`.
  The hook is bounded by `timeout` (default 900s,
  `[fleet] pre_start_timeout_seconds`, 0 unbounded). A failing or
  timed-out hook still starts the agent and prepends a one-line
  failure note (exit code, command) to its first prompt. Role
  liveness skips a pane while its `state/pre-start-<role>.running`
  marker is fresh, so a slow bring-up or lock wait is not respawned
  as a dead agent. Consumer
  impact: opt-in, no change unless set; marketer and crm-gateway can
  adopt with `pre_start = "bin/with-services up && <schema load>"`.
- Codex hook parity (unit 1 of `docs/todo/codex-coordinator-swap.md`).
  Token monitors (coordinator and worker) read Codex rollouts: the
  live context is `last_token_usage.total_tokens` of the latest
  `token_count` event; `.jsonl.zst` rollouts are skipped. Hook entry
  points accept the Codex payload fields. The transcript fallback and
  `spore coordinator verify-done` resolve the project root from
  `SPORE_PROJECT_ROOT`, then `CLAUDE_PROJECT_DIR`, then cwd. New
  `spore hooks codex-session-start` (records the Codex thread id per
  agent session next to the inbox) and `spore hooks watch-inbox
  --codex [--pid N]` (long-running sidecar that claims inbox envelopes
  and delivers them with `codex queue --thread`; `SPORE_CODEX_BIN`
  overrides the binary). Consumer impact: the coordinator launch shim's
  codex branch now passes `--dangerously-bypass-hook-trust`, gives
  each coordinator session its own `CODEX_HOME`
  (`<coordinator state dir>/<project>/codex-home/<session>/`, with
  `auth.json`, `config.toml`, `hooks.json`, `AGENTS.md` symlinked from
  `~/.codex`), restarts that home's app-server daemon, exports
  `SPORE_COORDINATOR_SESSION` and runs the sidecar for the pane's
  lifetime (log:
  `<coordinator state dir>/<project>/codex-watch-inbox.log`). The
  claude branch is unchanged. Wiring the SessionStart hook into Codex
  config is unit 2; until then the sidecar idles.
- Fix: `spore task tell coordinator` wrote
  `<state>/spore/<project>/coordinator/inbox`, which nothing drains. It
  now writes the coordinator inbox
  `<state>/spore/coordinator/<project>/inbox`. Consumer impact: worker
  `tell coordinator` messages now reach the coordinator.
- Role loop: pane liveness. Each drive tick probes the panes the phase
  expects: a missing session is spawned silently (phase transitions,
  token-monitor wraps; never capped), and a session whose agent died
  (pane dead, or a shell in the foreground) is killed and respawned.
  Those respawns notify the coordinator; after 3 of them for one role
  in one round the driver stops and escalates once instead. The
  active role is also watched for stalls (no expected file, no role
  file or pane transcript change for `[fleet] role_stall_minutes`,
  default 20, 0 disables): the coordinator is told once per quiet
  spell, nothing is killed. Consumer impact: projects running role
  loops get new `role-loop` coordinator notices (`respawned`,
  `escalated: ... died again`, `stalled`) and the new spore.toml
  knob.
- Role loop: strict validation of `responses/engineer-round-N.json`
  and `reviews/<A|B>/round-N.json`. Unknown keys, trailing data, a
  bad verdict, or a missing / mismatched `author` make a file
  invalid. Invalid files no longer wedge `DeriveSnapshot` or skew
  round math: they are skipped, the coordinator is told once, and the
  authoring pane is woken with the error; after the wake lands the
  file moves to `<name>.invalid`. New required `author` field
  (`engineer`, `A`, or `B`) and optional engineer `followups`.
  Consumer impact: projects that symlink `bootstrap/roles/*.md` pick
  up the new prompts (Write tool for JSON, no heredoc / bare `cat`,
  base-branch definition, PR-size blocker evidence, fix-round scope).
  In-flight loops: files present at the first drive after upgrade
  are grandfathered via `.spore/<slug>/state/legacy-files` (lenient
  decode, no `author` needed); later files are strict, and panes
  still on the old prompt are bounced once with the missing field
  named.
- flake: bump nixpkgs to pick up go 1.27.3 (GO-2026-6809).
- `bootstrap/handover/spore-coordinator-launch.sh`: support a host-wide
  shared role file in addition to the per-project `$SPORE_COORDINATOR_ROLE`.
  The launcher now reads `$SPORE_COORDINATOR_ROLE_SHARED` (default
  `${XDG_CONFIG_HOME:-$HOME/.config}/spore/coordinator-role.md`) and
  concatenates the shared file and the project role file with a blank
  line between them before passing the result as the agent's first user
  message. Each part is optional; missing parts are skipped, and when
  both are missing the agent launches unseeded as before. Operators can
  factor common respawn / wrap-up / operating-regime boilerplate out of
  per-project role files and keep only project-specific identity and
  deltas in each `bootstrap/coordinator/role.md`. Backward compatible:
  hosts without a shared file behave exactly as today.
- `nixosModules/spore-fleet`: replaced single-project
  `services.spore-fleet.projectRoot` with the multi-project
  `services.spore-fleet.projects` attrset
  (`name → { path }`). The module now generates one
  `spore-fleet-reconcile-<name>` service + timer + path watchers
  per entry, so a single host can reconcile several project trees
  under the same `user` without external glue. Tmux session naming
  (`spore/<name>/coordinator`, `spore/<name>/<slug>`) is already
  namespaced by project, so coordinator and worker sessions stay
  isolated.

  `projectRoot` is kept as a deprecated alias: when set (and
  `projects` is empty), it surfaces as
  `projects.${baseNameOf projectRoot}.path`, with a deprecation
  warning. Setting both is an assertion error; setting neither
  with `enable = true` is also an assertion error.

  The kill-switch flag at `~/.local/state/spore/fleet-enabled`
  remains host-wide; flipping it triggers reconciles across every
  project. The pre/post graceful-deploy scripts now drain workers
  for every configured project sequentially.
- `matter.linear`: Sync now projects new Linear comments per active
  ticket as tell-envelope JSON files in the matching rover slug's
  spore inbox dir, so an operator commenting on a Linear ticket
  reaches the rover working it via the existing watch-inbox Stop
  hook. Per-ticket cursor lives at
  `<StateDirForProject>/<slug>/linear-comments.cursor`; first
  observation seeds at "now" so historical comments do not flood
  the inbox.
- Renamed the per-task inbox env var from `SKYBOT_INBOX` to
  `SPORE_TASK_INBOX` to drop a foreign prefix that had leaked in
  from a consumer harness. Spore sets and reads the new name only;
  consumers that exported `SKYBOT_INBOX` for operator-side hooks
  must rename on their side. No backward-compat alias.

## 0.4.2 - 2026-05-06

- Fixed `spore coordinator start` lying about success when the agent
  binary failed to exec. `driverToBinary("claude")` returned the
  package name `claude-code` instead of the actual binary name
  `claude`, so the inner shell command died on `exec` and tmux tore
  the session down before any `has-session` check ever ran. Two
  changes: (1) "claude" now maps to `claude` (the kernel default
  fallback also moves to `claude`); (2) `EnsureCoordinator` now
  waits a short settle window after `tmux new-session -d` and
  re-checks the session, returning a real error if the spawn died.
- Synced `VERSION` with the git tag scheme (was stuck at `0.0.3`
  while tags advanced to `v0.4.x`). New `just release X.Y.Z` recipe
  validates clean+green, bumps `VERSION`, commits, and tags so the
  two stay in step from now on.
- Refactored `embed_test.go` to read `VERSION` at test time instead
  of hardcoding the expected string, so a release no longer requires
  a paired test edit.

## 0.0.3 - 2026-05-05

Spore 0.0.3 lands the universal coordinator entry point. "How do I
start the coordinator?" now has the same answer for every consumer:
`spore coordinator start`.

- Added `spore coordinator start [--wait] [--poll-sec N]`,
  `stop`, `restart`, and `status` over the existing fleet
  coordinator helpers. `start` is idempotent; `--wait` blocks
  until the session exits (helm-spawn / skyhelm-spawn shape).
- Added the `[coordinator]` section in `spore.toml` with `driver`,
  `model`, and `brief` keys. Env vars still win; driver "claude"
  maps to `claude-code`, "codex" to `codex`, and any other value
  passes through (so a project can point at a launcher script).
- Injected `SPORE_COORDINATOR_PROVIDER` and
  `SPORE_COORDINATOR_MODEL` into the session env when configured,
  matching the bundled `spore-coordinator-launch` dispatch shape.
- `status` prints up/down, the configured driver/model/brief, and
  exits 3 when the coordinator session is down (0 when up).

## 0.0.2 - 2026-05-04

Spore 0.0.2 is a focused release: it can now run Codex-backed workers
as a first-class task option while keeping the
existing Claude Code path intact. Feedback and rough edges are welcome.

- Added Codex worker support through `agent: codex`, with task-level
  model and reasoning-effort selection.
- Kept mixed Claude Code and Codex workflows on the same task
  frontmatter, tmux worker sessions, inbox/tell protocol, and merge
  close path.
- Bumped the package and CLI version to `0.0.2` and added
  `spore version`.
- Smoothed the README opening flow by removing the "Choose a Path"
  table.
