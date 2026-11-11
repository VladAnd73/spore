**Status**: reference. The unit layout, timer fields, failure modes and
journal lines below were verified on a live host on 2026-09-03 by
installing the units, firing them from a real timer, and reading the
journal. The reviewer, write, revert and prune behaviour described here
landed after that run; it is checked against the code and its tests,
not against a live deploy.

# Deploying the nightly dream job

`spore dream digest` is the deterministic half of nightly dreaming. It
reads this machine's own session transcripts, writes a digest, and mints
one proposer task that the fleet picks up. This note is how to put it on
a timer, and what it costs you when you get a step wrong.

## Before you deploy anything

**1. The `spore` binary the unit runs must have the `dream` command.**
Check with `spore dream --help`. The unit below runs whichever `spore`
is first on the user's `PATH` (the host package on a NixOS host). If you
point it at a hand-built binary instead, build that binary from `main`:
every other unit that runs the same path moves with it.

The digest mints a task file; the fleet that reads that file and spawns
a worker for it runs the fleet's own `spore`, which may be older. The
seam is safe because the mint writes only the frontmatter fields every
spore task already carries (`status`, `slug`, `title`, `created`,
`project`). If a later change to the mint adds a field, the fleet's
binary is what has to understand it.

**2. `$HOME/.claude/projects` must be readable by the unit.** That is
the session corpus. A user unit inherits it; a system unit does not.

## Cost of one run

Measured on the host this was written on, 2026-09-03, with a corpus of
476 transcripts totalling 483 MB:

    real  0m4.1s     user  0m4.0s     sys  0m0.25s

The work is CPU, not disk: `classify` parses every transcript in the
corpus on every run, whatever the watermark says, and only then filters
by project. One run is therefore about 4 seconds of one core, on an
8-core host. Two projects at 03:00 do not contend for anything that
matters.

Disk growth per run is small. A first run over a whole corpus wrote
33,494 bytes; an incremental one-session night wrote 1,449 bytes. Each
`spore dream digest` run also prunes run directories older than seven
days whose minted task is done or gone (`dream.Prune`).

## Step 1: check the binary

    spore dream --help

To try an unmerged change, build to a scratch path and run it by hand
rather than pointing the unit at it:

    nix develop -c go build -o /tmp/spore-probe ./cmd/spore
    /tmp/spore-probe dream digest --project-root <project root> --dry-run

`--dry-run` writes nothing and mints nothing.

## Step 2: create the config, left disabled

Per project, in `~/.config/spore/<project>/watch.toml`:

    [dreams]
    enabled = false
    deep_read_cap = 5
    max_writes_per_run = 10
    recurrence_threshold = 2

Leave `enabled = false` until you have read "When to turn a project on".
A disabled project is a complete no-op: it prints one line naming this
file and exits 0 without taking the lock or writing anything.

All three numbers may be omitted; the values above are the defaults.
`deep_read_cap` is 5 because the bar for an inferred claim is two
independent sessions, and three deep reads make that count the binding
constraint rather than the evidence.

## Step 3: install the unit

A hand-installed user unit under `~/.config/systemd/user/` needs no
sudo and no host rebuild.

`~/.config/systemd/user/spore-dream-<project>.service`:

    [Unit]
    Description=Nightly dream [<project>]

    [Service]
    Type=oneshot
    WorkingDirectory=<project root>
    ExecStart=/usr/bin/env spore dream digest --project-root <project root>

`~/.config/systemd/user/spore-dream-<project>.timer`:

    [Unit]
    Description=Nightly dream timer [<project>]

    [Timer]
    OnCalendar=*-*-* 03:00:00
    RandomizedDelaySec=15min
    Persistent=false

    [Install]
    WantedBy=timers.target

Then:

    systemctl --user daemon-reload
    systemctl --user enable --now spore-dream-<project>.timer
    systemctl --user list-timers spore-dream-<project>.timer

**Never hand-edit a home-manager-managed unit in place.** Both kinds
can live in this directory: home-manager units are symlinks into
`/nix/store` and are read-only, hand-installed units are plain files.
Check before you edit:

    ls -l ~/.config/systemd/user/spore-dream-<project>.service

If that is ever a symlink, the unit has become module-managed and the
edit belongs at the module level.

### Why each field, and what is deliberately absent

`--project-root` must be the **main repo path, never a worktree**. A
worktree is rejected at the mint, exit 1:

    dream-error: spore dream digest: dream: mint: tasks dir
    <root>/.worktrees/probe/tasks belongs to project "probe", not
    "<project>"; a task minted here would be picked up by the wrong
    project's fleet

Note the failure only happens once the run reaches the mint. A
**disabled** project pointed at a worktree prints the disabled line and
exits 0, so a bad `--project-root` can sit unnoticed until the day you
enable the project. Check it while the project is still disabled by
running the command by hand with `--dry-run` from the main repo.

`OnCalendar`, not `OnUnitInactiveSec`. A fixed wall clock time is the
point: `OnUnitInactiveSec` drifts, and every drift moves the run further
from the hour nobody is working.

`RandomizedDelaySec=15min`. The measured digest cost above does not
justify this on its own; 4 seconds of one core on an 8-core host is not
contention. It is here for what happens *after* the digest: the mint
creates a task and the fleet spawns a worker for it about 0.4 seconds
later. N projects firing at the same second means N agents starting at
once, all competing for the same worker slots. That is worth spreading.
Verified in action: with the delay set, `list-timers` showed the next
elapse at `03:00:00` plus the roll, and the service ran at the rolled
time.

`Persistent=false`. A missed night is skipped, not caught up. Two
reasons. First, skipping is free: the window is the watermark, not the
calendar, so the next run digests every session since the last
successful run however many nights that spans. Verified: a run whose
watermark said `since=2026-09-03T02:05:00Z` picked up a session
timestamped `04:05:00Z` on the following run with no gap. Second, a
catch-up run fires at whatever hour the host next boots, mints a
proposer task, and the fleet spawns an agent for it within a second, in
the middle of somebody's working session.

**No `Restart=`.** A failed digest waits for tomorrow. Every retry that
gets as far as the mint produces another proposer task for the same
night, and nothing caps that backlog: consecutive runs each mint a new
task file and no code refuses a second one.

**No path unit, and no trigger other than the timer.** Every digest that
gets past the mint creates a task. Anything that fires on file changes
mints one task per change.

**No secrets wrapper.** `dream digest` reaches no outside system.
Verified: the unit above ran to completion, minted a task, and advanced
the watermark with no wrapper and no secrets in its environment.

**No locking to add.** `digest`, `revert` and `rewind` already take an
flock at `$XDG_STATE_HOME/spore/<project>/dreams/lock`, and a second
caller exits 0 with a warning:

    dream-warn: <project>: another dream run holds the lock:
    <state>/spore/<project>/dreams/lock, so this run did nothing

One oneshot per project would serialise the timer against itself, but
not against a human typing `spore dream digest` while the timer fires,
which is the case the flock covers.

### Optional: sandbox the unit

The unit above runs unsandboxed. To bound what a mistyped `--project-root` can touch, add:

    ProtectSystem=strict
    ReadWritePaths=%S/spore/<project>
    ReadWritePaths=<project root>

Both write paths must exist before the unit starts, so create them in
this step:

    mkdir -p ${XDG_STATE_HOME:-$HOME/.local/state}/spore/<project>

Getting this wrong fails loudly, which is the only reason it is worth
writing down. Both failure modes were verified on the host.

A `ReadWritePaths` entry that does not exist stops the unit before the
binary runs. `systemctl --user start` exits non-zero and the journal
names the path:

    Failed to set up mount namespacing: <root>/tasks-typo:
      No such file or directory
    Failed at step NAMESPACE spawning <binary>: No such file or directory
    Main process exited, code=exited, status=226/NAMESPACE

A write path that exists but is not listed fails at the first write,
exit 1, with the file named:

    dream-error: spore dream digest: open
      <root>/tasks/dream-20260903-f4d4.md: read-only file system

The run directory is discarded when the mint fails, so a failure like
this leaves nothing behind to clean up.

## Step 4: verify without waiting for 03:00

With the project still disabled, start the service once:

    systemctl --user start spore-dream-<project>.service
    journalctl --user -u spore-dream-<project>.service -n 20

Expected, and verified verbatim on the host this was written on:

    Starting Nightly dream [<project>]...
    dream <project>: disabled, nothing to do; set enabled = true under
      [dreams] in <config>/spore/<project>/watch.toml
    Finished Nightly dream [<project>].

That exercises everything the wiring can get wrong: the binary path, the
working directory, the environment, and the write paths. It writes
nothing. If the state directory does not exist beforehand, it still does
not exist afterwards.

If the binary predates the `dream` command, this is what you see, and
`systemctl start` exits non-zero:

    spore: unknown command "dream"
    ...
    Main process exited, code=exited, status=2/INVALIDARGUMENT

Go back to step 1.

## Reading the journal

One stdout line per run, starting `dream <project> <run-id>:`. A real
line from a real run:

    dream <project> 20260903-29d5: sessions=1/1 digested=1 deep-read=1
    cap=2 digest=402B omitted=0 claims=0 unreadable=0
    task=dream-20260903-29d5 since=2026-09-03T02:05:00Z
    watermark=2026-09-03T04:05:00Z

Anything a human should act on carries a token. `dream-warn:` is a run
that finished with something worth knowing. `dream-error:` is a run that
failed. The grep for a week of journal:

    journalctl --user -u 'spore-dream-*' | grep -E 'dream-(warn|error):'

The tokens are the filter, not the stream. The summary goes to stdout
and the warnings go to stderr, but journald gives both the same
`PRIORITY=6` and the same `_TRANSPORT=stdout`, so no journalctl flag
separates them. Verified by reading the journal in JSON.

**Do not alarm on exit 0.** A quiet night, a disabled project and a
contended lock all exit 0 by design. Only a non-zero exit is a fault:

- exit 0: the run did what it could, including doing nothing.
- exit 1: the run failed. Look for `dream-error:`.
- exit 2: usage error. The unit's `ExecStart` is wrong.

Warnings are capped at 20 per run, followed by a count of the rest, so
one lost corpus cannot bury the summary line.

## Undoing a bad night

What to undo depends on how far the night got.

**The proposer task is minted but nothing was written yet.** Stop the
proposer: mark the minted task done, or delete its file from
`<project root>/tasks/`. Then put its sessions back in scope:

    spore dream rewind --project <project>

Verified round trip:

    dream rewind <project>: last 2026-09-03T04:05:00Z ->
    2026-09-03T02:05:00Z; the sessions run 20260903-29d5 consumed are in
    scope again

and the next run reported `since=2026-09-03T02:05:00Z`, so the rewound
night was read again. The watermark keeps the last ten positions, so
`rewind` can step back over several unjudged nights, one step per call.
After the first-ever run it rewinds to "the beginning", so the whole
corpus is read again.

**The worker ran `spore dream write` and changed files.** `write`
snapshots every target before it touches it and seals a manifest, so:

    spore dream revert <run-id> --project <project>

puts every target back, including the `MEMORY.md` index line, and
returns the run's ledger entries to candidate. `write` runs once per
run: a second `write` on a run it already wrote is refused, because it
would replace the backup of the pre-run state. A full revert retires
the run's manifest (kept as `manifest.reverted.json`), so the run can
be written again; a second revert then says the run was already
reverted. It refuses to touch a file that changed after the run was
sealed, and says which. A digest-only run snapshots nothing, so
reverting one fails with `no manifest for run <id>` and changes
nothing.

To find a run id without knowing it:

    spore dream runs --project <project>

## When to turn a project on

`enabled = true` points a real proposer agent at a real fleet, and the
fleet spawns it about 0.4 seconds after the mint. The proposer runs
`spore dream gate`, spawns one isolated reviewer subagent per packet
that clears the gate, and only then runs `spore dream write`, which
writes confirmed survivors to the project's memory directory (skills
go to `skill-proposals/`, never installed) and sends the coordinator a
report.

Turn a project on only when all of these hold:

1. `spore dream --help` on the unit's `PATH` lists `gate` and `write`.
2. You ran `spore dream digest --project-root <project root> --dry-run`
   by hand and read the summary line.
3. `--project-root` is the main repo, confirmed, not a worktree.
4. You will read the first few `report.md` files yourself.

Then set `enabled = true` in the project's `watch.toml`. No daemon
reload is needed: the config is read at run time.

The first enabled run has no watermark, so it digests the whole corpus
in one go. Do that one by hand and watch it, rather than letting a timer
do it at 03:00.

## Flag names

`--transcripts` is the session corpus, default `$HOME/.claude/projects`.
It is deliberately not called `--projects-root`, because
`--project-root` and `--projects-root` differ by one character and mean
completely different directories: one is a repo, the other is a
transcript store.

## Removing the job

    systemctl --user disable --now spore-dream-<project>.timer
    rm ~/.config/systemd/user/spore-dream-<project>.{service,timer}
    systemctl --user daemon-reload
    systemctl --user list-unit-files 'spore-dream-*'

The last command must list nothing. State under
`$XDG_STATE_HOME/spore/<project>/dreams/` and any minted task files
survive; delete them separately if you want them gone.
