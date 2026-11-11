**Status**: implemented, off by default. Stages 1 to 5 are built and
tested: discover, digest, score, ledger, the two-tier gate, the
adversarial review, the write, snapshot/revert, prune, the `[dreams]`
config table, the embedded briefs, the mint, and `spore dream
digest|gate|reviewer-brief|write|runs|revert|rewind`. A project runs it
only when its `watch.toml` sets `[dreams] enabled = true` and a timer
calls `spore dream digest`; see
[nightly-dreaming-deploy.md](nightly-dreaming-deploy.md). Open items
are under "Follow-ups" below.

**Amendment (2026-09-07)**: the write stage's `lesson` tier (which
appended a heading and body to a project's `state.md`) is retired; every
automated write now goes to the project's memory directory, the same
durable destination the `memory` tier always used. This was a live
correctness fix: a dream-written lesson landed in a consumer project's
`state.md` and was gone by that project's next coordinator wrap-up,
because not every project protects a lesson block from its own
size-cap trim the way spore's own `state.md` does. This changes only
what the automated dream pipeline writes. `state.md`'s own hand-written
`CRITICAL LESSON`/`RULE` convention, and `spore coordinator state-debt`
which scans for it, are both untouched.

# nightly dreaming: learn from spore sessions

## Problem

Agents on this host produce a large volume of session transcripts every
day, and everything learned inside a session dies with it. Lessons reach
the harness only when a human or a coordinator happens to notice a
pattern and write it down by hand. That is unreliable and it does not
scale: 460 transcripts totalling 433 MB exist today, 23 files and
19.4 MB of them touched in a single 24-hour window (measured
2026-08-31).

The result is repeated mistakes. Three concrete examples found while
adopting the `state-debt` convention on the same day:

- The `gh pr create` fork-base gotcha was rediscovered more than once
  before it reached memory.
- The done-gate false negative bit two separate deliveries before it
  was written down at all.
- A lesson recorded in `state.md` ("tell does not wake an idle
  coordinator") stayed there for seven weeks after its own fix had
  merged, actively misinforming every session that read it.

Anthropic ships an analogous feature for hosted agents, Dreams
(`https://platform.claude.com/docs/en/managed-agents/dreams`): it reads
a memory store plus past session transcripts and produces a curated
replacement. It does not apply here (it operates on Managed Agents
memory stores, which this host does not use), but the shape is right and
this design borrows it.

## Goal

A job that runs every night, reads the spore-managed sessions that are
new since the last run, and turns what it finds into durable harness
improvements: lessons, rules, memory entries, and candidate skills. Per
project, because lessons and skills are per project.

## Non-goals

- Reading non-spore sessions. The pilot's own panes and any ad-hoc
  `claude` session on this host are out of scope.
- Curating any hosted memory store. This is entirely local files.
- Installing skills automatically. Skills are proposed, never written
  into place (see Decisions).
- Editing project source. The job writes lessons, memory, and skill
  proposals only.

## Decisions locked (operator, 2026-08-31)

1. **Input scope**: spore-managed sessions only, meaning coordinator and
   worker sessions. Partitioned per project; every output is
   project-scoped.
2. **Authority**: memory entries (facts, rules, and preferences alike)
   are written automatically. Skills are proposed to a review file and
   never installed by the job.
3. **Evidence bar**: two-tier. An explicit operator correction is
   sufficient evidence on a single occurrence. Anything the job infers
   on its own must recur in at least two independent sessions.
4. **Read depth**: mechanical digest of every in-scope session, plus a
   full read of a small capped number of flagged sessions per project.
5. **Execution**: the deterministic half is Go (`spore dream`); the
   judgement half is a normal spore worker spawned by the fleet.
6. **Adversarial review**: no change is written until an independent
   reviewer confirms it against real documentation, code, or live host
   state. The reviewer is barred from answering from model knowledge and
   defaults to reject.

## Architecture

Five stages. Stages 1 and 2 are deterministic Go. Stages 3 to 5 run
inside one worker session per project.

    03:00 timer
      |
      1. discover     Go   find + classify new sessions
      2. digest       Go   extract high-signal slices, score, flag,
      |                    update ledger, mint one task per project
      |
      fleet spawns a worker per project
      |
      3. propose      worker    read digests, deep-read flagged,
      |                         emit evidence packets
      4. review       subagent  one per packet, fresh context,
      |                         adversarial, default reject
      5. write        worker    survivors only; snapshot first
      |
      report -> tell the project coordinator

### Stage 1: discover

Scan `~/.claude/projects/*/*.jsonl` for files modified since the
per-project watermark. Classify each session by two markers, both
verified live on 2026-08-31:

- **worker**: the entry `cwd` matches
  `^<home>/(?P<project>[^/]+)/\.worktrees/(?P<slug>[^/]+)$`.
- **coordinator**: the first user message begins with
  `# Coordinator role`. The project is the basename of `cwd`.
- **everything else**: dropped. This is what excludes the pilot pane and
  ad-hoc sessions, which share a `cwd` with the coordinator but carry no
  role marker.
- **dream workers** (the minted slug `dream-<YYYYMMDD>-<4 hex>`, with
  an optional `-N` collision suffix): dropped. Their own session is new
  every night, so digesting it would mint a proposer on an otherwise
  quiet project forever.

Subagent activity is already inline in the parent transcript (entries
carry `isSidechain`), so it needs no separate discovery.

Output: a list of `(project, kind, slug, path, first_ts, last_ts)`.

### Stage 2: digest

Per session, extract only high-signal slices:

- every message from the operator, verbatim, with session id and
  timestamp (this is tier-one evidence for the two-tier bar)
- tool calls that returned an error, plus the following few turns, so
  the recovery is visible alongside the failure
- commands issued more than a threshold number of times in one session
- denied permissions and blocked hook results
- for workers: the task brief and the final report
- terminal state: done, blocked, or token exhaustion

Everything else is dropped. Successful reads, routine edits, and
assistant reasoning are the bulk of the bytes and hold the least signal.

Each session is then scored for deep-read priority on error density,
operator turn count, length, and whether it ended blocked or failed. The
top `deep_read_cap` sessions per project (default 5, see Configuration)
are flagged. The cap is enforced in Go so the worker cannot widen it.

Layout per run:

    ~/.local/state/spore/<project>/dreams/<run-id>/
      digest.md        human and model readable, all in-scope sessions
      sessions.json    machine index: paths, scores, flags
      backup/          pre-write copies of every file the run touches
      report.md        written at the end of stage 5
    ~/.local/state/spore/<project>/dreams/ledger.json
    ~/.local/state/spore/<project>/dreams/watermark

`<run-id>` is the UTC date plus a short random suffix, and is stamped
into every artifact the run writes.

Stage 2 ends by minting one task per project that has any in-scope
session, via the normal `spore task new` / `spore task start` path, so
the existing fleet spawns the worker. A project with nothing new gets no
task and no pane.

### Stage 3: propose

The worker reads `digest.md`, deep-reads the flagged sessions in full,
and emits candidate findings. It writes nothing else. Each candidate is
an evidence packet:

    claim:      one sentence
    type:       operator-preference | tool-behavior | host-state |
                code-behavior | process-pattern
    evidence:   pointers, not quotes - session id + timestamp for an
                operator message, file:line for code, the exact command
                that would prove a host claim, the doc URL for an API
    tier:       memory-entry | skill
    target:     the file it would change
    text:       the literal proposed content

The ledger gates which candidates may proceed. An
`operator-preference` candidate proceeds on first sighting. Every other
type must have `occurrences >= 2` across independent sessions; a first
sighting is recorded as a candidate and the run moves on.

### Stage 4: adversarial review

Every packet that clears the ledger goes to a reviewer in a **fresh
context** that receives the packet and the target file and nothing else.
It never sees the proposer's reasoning, so it cannot inherit the
proposer's confidence.

Three rules define the review:

1. **Re-derive, never trust.** Citations in the packet are pointers, not
   proof. The reviewer runs the command itself, opens the file at that
   line, reads the actual `--help` output or documentation. Text quoted
   in the packet carries no weight; a hallucinated quote must not be
   able to pass.
2. **Documentation over recall.** For any claim about how a tool, flag,
   command, or API behaves, the reviewer consults the real source:
   `--help`, the man page, the code in this repo, or the official
   documentation fetched over the network. Answering from model
   knowledge is prohibited. For a claim about this host, the standard is
   higher: run the command and quote the output.
3. **Default to reject.** Uncertain, unevidenced, or unverifiable is a
   refusal. The burden is entirely on the proposal.

Beyond truth, the reviewer must also refute:

- **stale claims**: the fix already landed, the flag was renamed, the
  gap is closed. This is the failure mode that produced the seven-week
  wrong lesson, so it is a required check, not an optional one.
- **overgeneralisation**: true of one odd session, written as a general
  rule.
- **duplicates**: already covered by an existing memory entry, lesson,
  rule, or skill.
- **leaks**: operator-machine paths, internal hostnames, or personal
  addresses in content bound for a committed file.

Verdicts:

- `confirmed` proceeds to stage 5.
- `refuted` means the reviewer positively established the claim is
  false. Recorded against the fingerprint and permanently dead: the
  finding is dropped before review on every later run.
- `unevidenced` means the reviewer could neither confirm nor refute.
  Drops back to candidate so a later night with better evidence may
  retry. A fingerprint that comes back `unevidenced` twice is also
  marked permanently dead, so an unprovable claim cannot be re-reviewed
  forever.

### Stage 5: write

Survivors only, and only after the run has copied every target file,
and each memory target's `MEMORY.md`, into `backup/`.

"That project's memory dir" is Claude Code's per-project directory:
`<transcripts>/<dir>/memory`, where `<dir>` is the one whose transcripts
ran in the project root (Claude Code truncates and hashes names past 200
bytes), falling back to the root with every non-alphanumeric byte as `-`.
`digest` records it in the run directory (`memory-dir`), the minted task
names it under "This run", and `write` refuses the whole batch if any
memory target is not a file directly inside it.

| Tier | Destination | Mode |
| --- | --- | --- |
| memory entry (fact, rule, or preference) | that project's memory dir, plus its `MEMORY.md` index line | auto |
| skill | `<run-dir>/skill-proposals/<name>.md` | proposed only |

A project missing a target is not an error: if it has no memory
directory yet, the run creates one holding only the entry it just wrote.

A memory entry's `text` carries its own frontmatter (`name:`,
`description:`, `metadata: {type: ...}`), exactly as a hand-written
memory file does; the write stage reads `name` and `description` back
out of it to build the `MEMORY.md` index line, and fails the packet
outright if either is missing rather than silently indexing it under
the filename. Every written item carries its run id so it can be traced
back and reverted.

The run then writes `report.md` (written, refuted with reasons, held as
candidates, skills awaiting review) and tells the project's coordinator.

## Safety

Neither `~/.claude/` nor any memory directory is a git repository
(verified 2026-08-31), so auto-writes have no version control to fall
back on. Three mechanisms compensate:

- **Snapshot before write.** Every file a run will touch is copied to
  `<run-dir>/backup/` first.
- **`spore dream revert <run-id>`.** Restores that run's snapshots and
  marks its ledger entries back to candidate.
- **Hard caps in Go**: sessions deep-read per project, and writes per
  run. A runaway night cannot rewrite the harness wholesale.

## Configuration

Per project, in the existing watch config
(`~/.config/spore/<project>/watch.toml`):

    [dreams]
    enabled = true
    deep_read_cap = 5
    max_writes_per_run = 10
    recurrence_threshold = 2

The values shown are the defaults. `deep_read_cap` is 5 rather than the
3 this spec first named: the bar for an inferred claim is two
independent sessions, and three deep reads make that count the binding
constraint rather than the evidence. The number lives in two places
(`internal/dream.DefaultDeepReadCap` and the `[dreams]` default in
`internal/watch/config.go`) because the CLI passes the config value
straight through, and
`TestDefaultDeepReadCapMatchesTheWatchConfigDefault` fails if they ever
disagree again.

Kernel defaults must name no project and no skill.

## Deploy

See [nightly-dreaming-deploy.md](nightly-dreaming-deploy.md). It was
verified on a live host on 2026-09-03 by installing the units, firing
them from a real timer, and reading the journal.

Summary: one hand-installed user oneshot per project,
`spore-dream-<project>.{service,timer}`, `OnCalendar=*-*-* 03:00:00`
with `RandomizedDelaySec=15min` and `Persistent=false`, no `Restart=`,
no path unit, running `spore dream digest`. No sudo and no host
rebuild. The worker half needs no deploy, since it goes through the
normal fleet.

`spore dream revert` undoes a run that wrote; a digest-only run
snapshots nothing, so the undo for a bad night that has not written is
to clear the minted task and then `spore dream rewind`.

## Follow-ups

These came out of building stages 1 and 2. Most are now closed in code;
the list keeps the ones that are not.

1. **Discovery still depends on a literal role marker.** Missing
   directories and unreadable transcripts are now reported as
   `TranscriptError`s instead of reading as a quiet night, but the
   coordinator marker is still the seed file's literal first line
   `# Coordinator role` (`internal/dream/discover.go`). The run warns
   when it finds no coordinator at all, which is the only signal that
   the marker has drifted.
2. **Nothing caps the minted-task backlog.** `MintTask` writes an active
   task unconditionally, so consecutive nights whose tasks are not yet
   done accumulate one active dream task each and the fleet works
   through all of them.

Closed since the first draft: prune (`dream.Prune`, run from `spore
dream digest`), the duplicated watermark/run/config formats in the CLI
(`dream.Rewind`, `dream.ListRuns`, `watch.TomlPath`), revert creating a
run directory for an unknown id, the one-step `previous` (now a
`History`), the missing reviewer stage, the Go caller for the two-tier
bar (`GateRun`), and `deep_read_cap = 0` (now means zero deep reads).

## Acceptance scenarios

Each must be a test that fails before
implementation and passes after.

1. **Discovery excludes non-spore sessions.** Given a transcript
   directory holding one worker session, one coordinator session, and
   one ad-hoc session at a project root with no role marker, when
   discovery runs, then only the first two are returned, attributed to
   the right project and kind.
2. **Watermark advances.** Given a completed run, when the job runs
   again with no new transcripts, then it returns no sessions, mints no
   task, and leaves the ledger unchanged.
3. **Digest drops noise and keeps signal.** Given a session containing
   an operator correction, a failed tool call, and fifty successful
   reads, when the digest is built, then it contains the correction and
   the failure and none of the reads.
4. **Deep-read cap is enforced.** Given ten sessions all scoring above
   the flag threshold and `deep_read_cap = 3`, when digesting, then
   exactly three are flagged.
5. **Two-tier evidence bar.** Given an operator-preference candidate
   seen once and an inferred candidate seen once, when the ledger gates
   them, then the first proceeds and the second is held; when the
   inferred one appears in a second independent session, then it
   proceeds.
6. **Refuted findings never return.** Given a fingerprint refuted by the
   reviewer, when the same finding is proposed on a later run, then it
   is dropped before review.
7. **Nothing is written without a confirmed verdict.** Given a run whose
   every packet is refuted, when it completes, then no target file is
   modified and the report lists the refusals with reasons.
8. **Revert restores exactly.** Given a run that wrote three memory
   files, when `spore dream revert <run-id>` is run, then all three
   files are byte-identical to their pre-run state and the ledger
   entries are back to candidate.
9. **Skills are never installed.** Given a confirmed skill candidate,
   when the run completes, then a file exists under `skill-proposals/`
   and no file was created in any `.claude/skills/` directory.
10. **Full flow, end to end.** Given a fixture transcript directory for
    one project containing a repeated operator correction and a
    genuinely stale claim, when the whole pipeline runs, then the
    correction is written as a memory file with its `MEMORY.md` index
    entry, the stale claim is refuted with a recorded reason, a backup
    exists for every written file, `state.md` is never created, and the
    coordinator receives one report envelope.

## Risks and open questions

- **Reviewer network access.** Rule 2 requires fetching real
  documentation. Interactively-authenticated MCP servers may be
  unavailable to a job started by a timer. Implementation must confirm
  what the worker can actually reach at 03:00 and fall back to a direct
  fetch for known URLs. If nothing is reachable, an API-behavior claim
  is `unevidenced`, not confirmed.
- **Self-reference.** The dream worker's own transcript becomes input on
  a later night. Harmless but worth watching for a feedback loop where
  the job keeps rediscovering its own output; the fingerprint ledger
  should absorb it.
- **Fingerprint stability.** If fingerprints are too literal, the same
  lesson phrased differently reads as new and the recurrence counter
  never advances. This is the most likely thing to need tuning after
  the first week.
- **Cost.** Roughly 0.5 to 1M tokens per night before review, plus one
  small agent per surviving candidate. Worth re-measuring after the
  first week against a real run rather than this estimate.
