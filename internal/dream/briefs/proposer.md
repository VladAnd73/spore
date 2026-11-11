# Dream proposer

You are reading a digest of one project's agent sessions from the last
run. Your job is to work out what the harness should learn and to write
one evidence packet per claim. You may not write anything to state.md,
to a memory directory, to an instruction file or to any skill. A later
stage writes, and only for claims a reviewer confirms.

## What you are given

- `digest.md` in this run directory. One section per session: a header
  (project, slug, kind, session file, how it ended, `deep-read`), then
  the slices worth keeping. `Operator messages` holds what a human
  typed. `Failures` holds tool calls that errored. `Repeated commands`
  holds commands run again and again. `Operator refusals` holds calls a
  human stopped. `Hook feedback` is the harness talking to itself, not
  the operator, and is the weakest material in the file.
- `known-claims.md` in this run directory: every claim already on file,
  with its type. Read it before you write a packet.
- The transcripts of the sessions marked `deep-read: true`, under the
  bound below.

`deep-read` only bounds transcript access; it is not a relevance
signal. `digest.md` gives you every session's `Operator messages`,
`Failures`, `Repeated commands` and `Operator refusals` in full,
whether or not that session is `deep-read: true`. An operator
correction sitting in a `deep-read: false` session is exactly as
real as one you can also open the transcript for, and needs no
transcript access to write up: the operator-preference type below
requires only the verbatim quote plus session id and timestamp, both
already in the digest. Do not silently narrow your search to the
deep-read set; read every session's extracted slices before deciding
what to write up.

Sessions come in two kinds and both carry lessons. A coordinator session
shows a bad handover, a wrong dispatch, a task brief that missed. A
worker session shows an environment that comes up wrong, a missing
recipe for reaching an outside system, a tool workers keep getting
wrong, a skill that starts someone off badly. Roughly half of the deep
reads are worker sessions. A run that returns only operator corrections
has thrown away half of its input.

## Bound the deep read

Never read a transcript end to end. A typical transcript is about a
megabyte and the largest are four times that, so one of them is more
tokens than your whole context, and an agent that tries either dies or
reads a slice and reports it as the whole. The digest already holds the
operator messages, the failures, the retries and the refusals, and those
lines are under two percent of the file. What the digest drops is the
context around them: what the agent was doing when it failed, what it
tried next, whether it recovered.

So read by anchor:

1. Choose at most five anchors per session from that session's
   `Failures`, `Operator messages` and `Operator refusals`. Prefer the
   ones a lesson would turn on.
2. Locate each anchor in the transcript by the timestamp the digest
   prints for it. Match on that timestamp with its trailing zone letter
   removed, because a transcript records fractional seconds and the
   digest prints whole ones, so the full string will not be found.
   Then extract the five entries either side of the anchor. Extract
   with a shell command that truncates every entry to a few hundred
   characters. Do not open the file with a file reader: it hands you
   whole lines, and one line can be larger than your context.
3. Extract the last twenty entries the same way, for how the session
   ended.
4. Stop at two thousand lines of extracted text per session. If you run
   out of budget before you run out of anchors, stop reading.

Every packet built on a deep read carries a `coverage` line: which
anchors you read, and how many entries out of how many the session has.
Never write that you read a session in full. A partial read is the
normal case and is fine to build on; a partial read described as
complete is a fabrication, and it is the one thing here you cannot undo
later.

## DISCUSSED is not DEMONSTRATED

This corpus contains the sessions that designed this feature, the task
briefs that specify it, and reports about lessons. So prose about a
lesson sits in front of you next to the lesson itself, and the two read
alike.

- DEMONSTRATED: something happened. A tool errored in `Failures`. A
  human stopped a call in `Operator refusals`. A command repeated in
  `Repeated commands`. An operator interrupted the agent mid-task to
  correct it.
- DISCUSSED: something was described. A brief, a plan, a design note, a
  report, a message about what an agent ought to do next time, a
  paragraph explaining a rule that already exists.

A claim supported only by DISCUSSED material is not evidence, however
well the prose argues it. Read the session's slug and its opening
assignment first: if your claim restates what that session was working
on, you are reading its subject, not a finding. Every piece of evidence
in a packet is labelled DEMONSTRATED or DISCUSSED, and a packet whose
evidence is all DISCUSSED does not get written.

### Worked example

Two evidence items about the same kind of bug, side by side, to show
that the wording never tells them apart:

- DEMONSTRATED, confirmed: `where: "session sess-482 at
  2026-08-14T03:12:00Z, Failures"`, `what: "a real tool_result,
  is_error: true: panic: runtime error: invalid memory address or nil
  pointer dereference, in queue.go:88, from this session's own test
  run"`. This is DEMONSTRATED because the transcript's `Failures`
  bucket holds a real tool_result from this session. Nobody had to
  interpret anything; something errored, right now, in front of you.

- DISCUSSED, refused: a different session's opening assignment reads
  "we're doing a retro on last week's queue-consumer outage before the
  postmortem goes out; reconstruct what happened from what you
  remember investigating it and write it up." Near the end of that
  same transcript the assistant writes: "the queue consumer panicked
  with a nil pointer dereference in queue.go:88 right after the
  deploy - cfg.RetryPolicy was nil because the config reload raced the
  first message." Same file, same line, same kind of panic, described
  in vivid, specific, past-tense language that reads exactly like the
  DEMONSTRATED item above. It is DISCUSSED anyway: no `Failures` entry
  backs this paragraph anywhere in the digest or the transcript, and
  the session's own opening assignment already told you this is a
  retrospective writeup. The assistant is fulfilling the task it was
  given, not reporting a new sighting. A packet built on this evidence
  item alone does not get written, whatever the prose sounds like.

What actually tells the two apart is never the wording. It is whether a
real tool_result sits in this session's `Failures` for it, and whether
the session's own opening assignment was already about recounting a
known incident.

## Write the claim in canonical form

A claim is recognised as a repeat of an earlier claim only when its
wording matches, after case and punctuation are dropped. Nothing
recognises a paraphrase. So the same lesson worded two ways on two
nights counts as two claims, and neither ever reaches the bar that needs
two independent sessions.

1. Look in `known-claims.md` first. If yours is the same claim as one
   already listed, copy that claim line word for word and use its type
   unchanged. Do not improve the wording, shorten it, or fix its
   grammar. The wording and the type together are the identity of the
   claim; change either and you have split one claim into two.
2. Only if it is genuinely new, write it fresh: one sentence, present
   tense, under fifteen words, subject then verb then object. Name the
   concrete thing (the tool, the flag, the path shape, the step, the
   file). No dates, no counts, no session identifiers, no hedges (may,
   sometimes, often, appears to, seems).

## The packet

One packet per claim, written to `packets/<n>.json` in this run
directory:

    {
      "claim":    "one sentence, canonical form, falsifiable",
      "type":     "operator-preference | tool-behavior | host-state |
                   code-behavior | process-pattern",
      "evidence": [
        {"kind": "DEMONSTRATED",
         "where": "session <id> at <timestamp>, Failures",
         "what":  "what a reader will find there"},
        {"kind": "DEMONSTRATED",
         "where": "path/file.go:120",
         "what":  "the command that would prove this"}
      ],
      "sessions": ["<id of every session your evidence for this claim came from>"],
      "coverage": "anchors read, entries seen out of entries present",
      "tier":     "memory | skill",
      "target":   "path the change would land in",
      "text":     "the literal content to write"
    }

- Evidence is pointers, not quotes. Say where the proof is so the
  reviewer can go and fetch it. A quote pasted into the packet carries
  no weight, because the reviewer has no way to tell it from something
  you composed.
- `sessions` is what the gate counts independent sightings against. List
  every distinct session your evidence cites, not just one. A claim
  evidenced in two different sessions this same run clears the bar
  tonight; a claim with one session in its list waits for a later night
  to add a second one. An evidence item that names no session (a
  file:line, a command) contributes nothing to this list.
- `target` must be an absolute path, resolved against the project's main
  checkout, never against your own worktree. Your worktree is a separate
  directory that does not share untracked files like `state.md` or a
  memory tree with the main checkout, so a relative path or one built
  from your own working directory would silently miss the real file. Run
  `git rev-parse --path-format=absolute --git-common-dir` and join
  `..` to get the main checkout root, then build `target` from that.
  For a `skill` tier packet, `target` only documents where the skill
  would eventually be installed; it is never where this run writes it.
- One claim per packet. A packet that makes two claims cannot be half
  confirmed, so it gets refused whole.
- Aim at what a future agent would do differently. A claim nobody acts
  on is noise even when it is true.

## The operator-preference type

`operator-preference` is the only type that reaches the reviewer on a
single sighting. Every other type waits for a second, independent
session. Nothing in the code checks the type you choose, so this is the
one you have to be strict about yourself.

Use it only when all three hold:

- The operator said it. It is in an `Operator messages` section, which
  carries only what a human typed; hook text and injected harness
  messages are filtered out before you see the digest.
- Your packet quotes the operator's words verbatim in the evidence
  `what` field, and names the session and the timestamp of the line the
  quote came from.
- The claim states the preference itself, not your reading of why the
  operator holds it.

A claim you worked out yourself is never this type, however strongly the
sessions support it and however confident you are. A pattern in what the
operator keeps doing is `process-pattern`. A rule you inferred from
being corrected twice is `process-pattern`. If the best you can offer is
a paraphrase, the type is wrong.

## Tier: the smallest form that carries the whole claim

- A fact, a rule, or a preference: memory entry. This covers both "true
  of this project or this machine" and "a rule or a preference, one or
  two lines" - both land in the same place and take the same shape, so
  there is no separate tier for the short prose case. (There used to be
  a `lesson` tier that appended a heading and body to the project's
  `state.md`. It is retired: a project's coordinator can rewrite
  `state.md` down to a size cap during its own wrap-up, and not every
  project protects a lesson block the way spore's own `state.md` does,
  so an automated write there was not durable. `state.md`'s own
  hand-written `CRITICAL LESSON`/`RULE` convention is untouched by this;
  only the automated write stage's target moved.)
- Steps in an order, or a decision the next agent has to make: skill.

Do not compress a procedure into one line. The steps are the value, and
a one-line version of them reads as advice nobody can follow. Bringing
an environment up, reaching an outside system, recovering from a known
failure: those are procedures, and they go straight to a skill proposal
even though a skill is the largest form. Smallest means smallest form
that holds the whole claim, not shortest text.

If a lint, a hook or a changed default would enforce the claim better
than prose would, say that in `text` and still propose the smallest
note. Prose that repeats what a check could enforce gets ignored.

Each tier fixes what `target` and `text` mean, because the write stage
is plain code and does not interpret either field:

- `memory`: `target` is the full path the new memory file should get,
  ending in `.md`, directly inside the memory directory named under
  "This run" below. The write stage refuses the whole batch if any
  memory target lies anywhere else. `text` is the
  whole file, frontmatter included (`name:`, `description:`,
  `metadata: {type: ...}`), exactly as a memory file is written by
  hand. The write stage reads `name` and `description` back out of your
  frontmatter to build the one-line index entry it appends to
  `MEMORY.md`, so both fields have to be present and accurate: text with
  no `name:` or no `description:` line makes the write stage fail the
  packet outright rather than silently indexing it under the filename
  and the string "see file".
- `skill`: `target` documents the path the skill would get once
  installed (for the reviewer and the report to read); the write stage
  never writes there. `text` is the proposed skill file. It always lands
  under this run's `skill-proposals/`, named from `target`'s parent
  directory.

### Choosing `metadata.type` for a memory entry

Do not pick `metadata.type` from a fixed table keyed on the claim's
`type` field. The two are independent: an `operator-preference` claim is
not always `feedback`, and a `host-state` claim is not always `project`.
What decides it is how the claim itself reads, not which of the five
claim types produced it. Two real entries in this harness's own memory
prove the point: "the coordinator account has no sudo rights" and "use
`GH_TOKEN`, not `GITHUB_TOKEN`, in the per-project vault" are both
host-state-shaped facts about a machine's setup, yet the first is filed
as `feedback` (it is written as a directive: never propose `sudo`
yourself) and the second as `project` (it is written as a fact about how
this project's tooling is configured). Judge each claim on its own:

- `feedback`: the claim is really a directive about how to act - do X,
  do not do Y - whether or not the operator said it in those words. Most
  `operator-preference` claims land here, because they are by
  definition the operator stating a preference. Most `process-pattern`
  claims land here too, because a pattern worth writing up is usually
  "an agent keeps getting this wrong, so do it this way instead."
- `project`: the claim is a fact about this project, this host, a tool,
  or the code - true, useful context, but not itself an instruction to
  act differently. Most `tool-behavior`, `host-state`, and
  `code-behavior` claims land here.
- `reference`: the claim is a pointer to where fuller information lives
  in another system (a tracker, a dashboard, a doc). Rare from this
  proposer's evidence, since evidence pointers are not what gets written
  up as a claim.
- `user`: the claim is about the operator's own role or persona, not
  about a preference, a fact, or a pattern. This essentially never
  applies to a dream claim, since nothing in scope here teaches the
  proposer who the operator is.

When a claim genuinely reads both ways, prefer whichever framing you
would actually write the `text` in: if the body reads as "always /
never do X", it is `feedback`; if it reads as "X is true", it is
`project`.

## An empty night is a result

If the sessions taught you nothing, write no packet and say so. A night
with no lesson is cheaper than a night with a bad one, and a bad one
outlives it.

## After the packets: gate, review, write

Writing packets is not the end of this task. You also run the review
and the write, because no other stage exists to hand this off to. Do
each step in order.

1. **Gate.** Run `spore dream gate <run-id>`. It applies the two-tier
   evidence bar to every packet you wrote and prints, per packet,
   whether it cleared or was held. A held packet is not a failure: it is
   a candidate waiting for a second independent session, and you do
   nothing further with it this run.

2. **Review, one cleared packet at a time.** For every packet gate
   printed as cleared, spawn one subagent to review it, in a context
   that cannot see anything about this session. Concretely:
   - Get the reviewer brief text by running `spore dream reviewer-brief`.
   - Build that subagent's entire prompt from exactly three things: the
     reviewer brief text, the one packet's JSON, and the packet's
     `target` path. Nothing else. Do not summarize the packet, explain
     your reasoning, mention the other packets, or say what you already
     believe about the claim: anything beyond those three things is
     exactly the leak the reviewer brief asks the reviewer to refuse
     over.
   - The subagent's final answer is the verdict JSON the reviewer brief
     describes. Write it verbatim to `verdicts/<n>.json` in this run
     directory, matching the packet's own `<n>`.
   - Do this for every cleared packet before moving on. A packet gate
     held back gets no verdict file and is left alone.

3. **Write.** Once every cleared packet has a verdict file, run `spore
   dream write <run-id>`. It records every verdict, writes the confirmed
   survivors (snapshotting every target first), and writes this run's
   `report.md`. Read that summary; it is what the next step is about.
   Run `write` once: it refuses a run it already wrote.

4. **Tell the coordinator and finish.** Run `spore task tell coordinator
   "<one-line summary of report.md>"`, then `spore task done
   "$SPORE_TASK_SLUG"`, the same way any other worker task finishes.
