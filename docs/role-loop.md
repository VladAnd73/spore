# Role-based fleet: operator guide

The role loop runs one task through an engineer pane and two reviewer
panes. Roles never see each other; they talk through files under
`.spore/<slug>/`. The driver derives all state from that tree, so
every command here is safe to re-run.

## Minting a task

Create `tasks/<slug>.md` with the role opt-in:

    spore task new "<title>" --role

The frontmatter is the standard task set (`status`, `slug`, `title`,
`created`, `project`) plus `loop: role`; the body below the
frontmatter becomes the spec. When editing a task file by hand, add
`loop: role` to the frontmatter yourself.

Then flip the frontmatter to `status: active` by editing the file,
not via `spore task start` (start spawns a homogeneous worker
session, and refuses on a `loop: role` task). The key makes the
opt-in atomic with the status flip: the reconciler pass that reacts
to the file write already sees `loop: role`, never mints a
homogeneous worker for the slug, and drives the role loop instead.
No pre-seeding is needed; the first drive tick creates
`.spore/<slug>/`.

`loop: role` is the only opt-in. A `.spore/<slug>/` dir on disk does
not mark the slug role-looped: coordinators stage briefs and inputs
there for generic tasks too, and those tasks keep their single
homogeneous worker. A task seeded by hand with only the dir needs
`loop: role` added to enter the role loop.

`role-drive` is also the manual driver: each run advances the loop by
one idempotent tick, and refuses a task whose frontmatter lacks
`loop: role`. The reconciler calls the same drive for
role-looped slugs on every pass, so an active task keeps moving
without manual ticks.

The first tick:

- caches the task body (frontmatter stripped) at
  `.spore/<slug>/spec.md`; the cache is immutable for the run
- creates the `wt/<slug>` branch and worktree at `.worktrees/<slug>/`
- spawns the engineer pane, tmux session
  `spore-role/<project>/<slug>/engineer`

## The phase machine

Phases derive from artifact counts on disk; writing a file causes the
transition. Verdict files hold `approve` or `request_changes`.

| Phase | Meaning | Entered by writing |
|---|---|---|
| `engineer-initial` | no engineer response yet | (start state) |
| `review-A` | A owns the next verdict | `responses/engineer-round-N.json` |
| `engineer-revise-A` | A requested changes | `reviews/A/round-N.json` (request_changes) |
| `review-B` | A approved; B owns the verdict | `reviews/A/round-N.json` (approve) |
| `engineer-revise-B` | B requested changes | `reviews/B/round-N.json` (request_changes) |
| `done` | B approved; branch ready | `reviews/B/round-N.json` (approve) |
| `escalated` | cap hit; operator decides | request_changes at round 3 |

A revise phase flips back to the review phase once the engineer
writes the response to the latest verdict. Each reviewer phase is
capped at 3 rounds (`MaxReviewerRounds`): a `request_changes` verdict
at round 3 escalates instead of looping. On escalation the driver
leaves all panes alive; the operator can clarify the spec,
force-approve, kill the task, or hand it back for another loop.

Per-phase side effects of a drive tick:

- engineer phases: keep the engineer pane alive (see Pane liveness);
  on revise phases, nudge it (tmux send-keys) with the latest verdict
  path
- `review-A`: keep engineer and reviewer A panes alive
- `review-B`: reap reviewer A's pane, keep reviewer B alive; the
  engineer pane stays alive across the handover
- `done`: write `summary.md`, clear `escalated-*` markers, write the
  `ready` marker, reap all three panes
- `escalated`: write the `escalated-*` marker, touch nothing else
- every phase: bounce invalid role files (see below)
- non-terminal phases: check the active role for a stall (see Pane
  liveness)

## Pane liveness

Phase completion comes only from files; a pane exiting never advances
or stalls the loop by itself. Each tick probes every pane the phase
expects (tmux `pane_dead`, `pane_current_command`, `session_created`):

- missing session: spawn it, silently. This covers a pane missing
  after a phase transition and a token-monitor wrap (which kills its
  own session on purpose); neither counts toward the respawn cap.
- agent gone (pane dead, or a shell in the foreground once the
  session is 30s old, e.g. a hand-started role whose agent exited):
  kill the session and spawn a fresh one.

Each agent-gone respawn claims `state/respawn-<role>-r<round>-<n>`
and tells the coordinator (`<slug>: respawned <role> (<reason>; round
R, respawn K/3)`). When the agent dies again after 3 respawns of one
role in one round, the driver stops, leaves the dead pane for
inspection, and tells the coordinator once (`<slug>: escalated:
<role> agent died again ...`, marker
`state/respawn-capped-<role>-r<round>`). The cap re-arms when the
role's round advances; delete the role's `respawn-<role>-r<round>-*`
markers and the capped marker to re-arm it by hand.

Stall: the role that owns the next file write is stalled when its
expected file is absent and neither the role files under
`.spore/<slug>/` (outside `state/`) nor the pane transcript (the text
above the input prompt; status line and input box excluded) changed
for `[fleet] role_stall_minutes` (default 20, 0 disables). The
coordinator is told once per quiet spell (`<slug>: stalled: ...`);
any activity re-arms it. Stalls never kill the pane: the agent may be
waiting on a dialog only the operator can answer. The activity clock
lives in `state/activity-<role>-r<round>`.

## Roles

All panes run in the task worktree with `SPORE_TASK_DIR` pointing at
the absolute `.spore/<slug>/` tree, plus `SPORE_TASK_SLUG`,
`SPORE_PROJECT_ROOT`, `WT_PROJECT`, and `SPORE_ROLE` in the
environment. Role bodies come from `bootstrap/roles/<role>.md`.
When `[fleet] account_tier` is set in spore.toml, panes also get
`SPORE_ACCOUNT_TIER`, and `spore worker token-monitor` meters them
with the same three bands as workers (soft warn, finish-unit, force)
at the same tier-keyed caps. The wrap instructions are role-shaped:
commit in-flight work to the task branch, write the phase artifact
only if it is complete, then kill the pane session; the drive loop
respawns the phase's pane on its next tick and it resumes from the
branch plus artifacts. The soft warn fires once per session via a
marker at `.spore/<slug>/state/token-monitor/<session_id>.soft`.

### Pre-start hook

`[fleet] pre_start = "<shell command>"` runs a project command inside
each role pane and each worker session, before the agent starts (not
the coordinator). Typical use is bringing services up and loading a
test schema, e.g. marketer:

    [fleet]
    pre_start = "bin/with-services up && <schema load>"

The command runs in the session cwd (the worktree) with the session's
`SPORE_*` env, via `sh -c`, under `flock .spore/<slug>/state/pre-start.lock`.
The lock is per slug and shared by the worker, engineer, and both
reviewers, so make the command idempotent: later sessions wait for
the first, then should return quickly. The pane shell runs it, not
the reconciler, so spawning never blocks on it. The hook runs under
`timeout`, default 900s, set by `[fleet] pre_start_timeout_seconds`
(0 runs it unbounded), so a hung bring-up cannot hold the slug lock
forever.

While a session waits on the lock or runs the hook, the pane shows
the launch shell, which liveness would otherwise read as a dead
agent once spawnGrace passes. The prelude therefore holds
`state/pre-start-<role>.running` (`engineer`, `reviewer-A`,
`reviewer-B`, or `worker`) from just before the lock until just
before exec. Liveness leaves a shell-showing pane alone while that
marker exists and is no older than the session; a marker left by a
pane killed mid-hook is older than the respawned session and is
ignored.

A non-zero exit (including a missing `flock`, or 124 on timeout) never stops the agent.
The agent's first prompt gets one leading line: `spore: pre-start
hook failed (exit N): <command>. Services may be down.` Role panes
put it above the role body; a role pane with no role body, and a
worker (which starts with no prompt), get that line alone as the
prompt. Unset or empty leaves the spawn argv byte-identical to
before.

Engineer (`spore-role/<project>/<slug>/engineer`): reads the spec and
the latest verdict, commits on the task branch, and ends each round
by writing `responses/engineer-round-N.json`:

    {
      "author": "engineer",
      "addressed": ["comment 1 -> commit X"],
      "pushback": ["comment 3 -> disagree because Z"],
      "notes": "free-form context for the reviewer",
      "followups": ["out-of-scope idea for a separate PR"]
    }

Reviewer (`spore-role/<project>/<slug>/reviewer-<instance>`): one
body, two instances, selected by `SPORE_REVIEWER_INSTANCE` (A or B).
A persists across its rounds and reads the engineer's responses. B
spawns fresh after A approves; round 1 is fresh eyes (no A history,
no engineer responses), rounds 2+ read only the engineer's replies to
B's own verdicts. Each round a reviewer writes
`reviews/<instance>/round-N.json`:

    {
      "author": "A",
      "verdict": "approve" | "request_changes",
      "summary": "one-line overall take",
      "comments": ["one string per concern"]
    }

## File validation

Every drive tick validates each role file before deriving the phase
(`internal/task/rolefile.go`). A file is invalid when it is not one
JSON object, carries a key outside its schema, lacks a required field
(`author` everywhere; `verdict` and `summary` on reviews), has a
verdict outside `approve` / `request_changes`, or has an `author`
that does not match its location: `reviews/A/` needs `"A"`,
`reviews/B/` needs `"B"`, `responses/` needs `"engineer"`. Panes fill
it from `$SPORE_REVIEWER_INSTANCE` / `$SPORE_ROLE`.

An invalid file never counts toward the phase, so it cannot wedge or
skew the loop. The drive tells the coordinator once per bad file and
wakes the authoring pane with the validation error. Once the wake is
confirmed, the file moves to `<name>.invalid` (then `.invalid.2`, ...)
for debugging and the pane rewrites it. If the authoring pane is not
running, the file moves aside at once and the coordinator notice is
the only signal.

Upgrade path: the first drive by a validating binary writes
`state/legacy-files`, listing the role files already on disk. Those
are read leniently (unknown keys and a missing `author` pass; the
verdict enum still applies), so an in-flight loop does not rewind.
Files written after that point are strict. Panes spawned before the
upgrade still run the old role body; their next file without
`author` is bounced, and the wake message names the missing field.

## Observing a running loop

`spore task status <slug>` prints the derived snapshot: phase,
engineer round, current reviewer, reviewer round, last verdict.

Markers under `.spore/<slug>/state/`:

- `woken-<reviewer>-<round>-<stamp>`: the engineer was nudged for
  this verdict. The stamp is the pane's tmux `session_created`, so a
  respawned engineer pane gets re-nudged on the next tick.
- `escalated-<reviewer>-<round>`: the loop escalated. Cleared when
  the loop later reaches done (operator force-approve).
- `ready`: the loop reached done; the branch awaits the operator.
  Cleared by `spore task done`.
- `legacy-files`: role files grandfathered from before validation.
- `respawn-<role>-r<round>-<n>`, `respawn-capped-<role>-r<round>`,
  `activity-<role>-r<round>`, `stalled-<role>-r<round>-<since>`: pane
  liveness (see above).
- `invalid-<file>-<n>-<hash>`: the coordinator was told about this
  invalid file; `bounced-<file>-<n>-<hash>-<stamp>` marks the
  delivered wake.

`spore task waybar` renders `d/a/p/b/e/r` counts (draft, active,
paused, blocked, escalated, ready). Escalated and ready count marker
files regardless of frontmatter status. The chip CSS class is the
highest-priority nonzero bucket: `escalated`, then `blocked`, then
`ready`, then `active`, else `idle`.

`summary.md` lands in `.spore/<slug>/` when the loop hits done: both
review threads (verdict, summary, comments per round) plus every
engineer response. It embeds a fingerprint of the artifact counts and
regenerates if the tree changes under it.

## Finishing

When the loop is done, review the branch (`summary.md` is the digest)
and merge with `spore task merge <slug>`. Then run:

    spore task done <slug>

Done flips the status, kills the homogeneous worker session, removes
the worktree and the `wt/<slug>` branch, deletes `tasks/<slug>.md`,
and clears the `state/ready` marker. It refuses while the branch has
unmerged commits (`--force` discards them). It does not kill role
panes: its session cleanup matches `<project>/<slug>` at a name
boundary, which `spore-role/...` names never hit. On the normal path
that is moot (the done tick already reaped all three panes), but when
you abandon an escalated task, kill the surviving panes yourself with
`tmux kill-session -t spore-role/<project>/<slug>/<role>`.

The rest of `.spore/<slug>/` is preserved: `spec.md`, `responses/`,
`reviews/`, `summary.md`, and the remaining state markers stay on
disk as the task's audit trail.
