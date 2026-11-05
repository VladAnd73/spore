**Status**: implemented in v0.29.0 (`internal/ghapp`,
`internal/fleet/rolegithub.go`, `docs/github-app.md`); live
verification against a real app pending the org owner creating it.

# github-app-pr-flow: role loop on a visible draft PR

Move the role loop's feedback from files only onto a GitHub draft PR
the team can watch. A GitHub App opens and updates the PR; reviewer
verdicts are posted on it; the operator gives the only approval and
merges.

## Motivation

Today the loop is invisible until the end: engineer and reviewers
talk through `.spore/<slug>/` files, the coordinator pushes and opens
the PR after `PhaseDone`, and every GitHub action runs as the
operator's own account (`GH_TOKEN` in the project vault). Two
problems:

- The team never sees the review rounds, only the final PR.
- The PR author is the operator, and GitHub does not let an author
  approve their own PR, so the operator's "final approval" is only
  the merge.

## Decisions (operator, do not re-litigate)

1. **One GitHub App per org**, created once by an org owner, shared
   by every teammate's spore. The app opens the PR, pushes the
   branch, and posts the reviews.
2. **Commits are authored as the dispatching operator** (their name
   and email, linked to their GitHub account). The app is the pusher
   and the PR author, so the operator can approve.
3. **Reviewers use the same app.** Reviews are posted as `COMMENT`
   reviews whose body starts with `Review #1` (instance A) or
   `Review #2` (instance B). The app never submits `APPROVE` or
   `REQUEST_CHANGES` (it is the PR author, and the operator must stay
   the only real gate).
4. **The loop's outcome flips the PR from draft to ready.** On
   `PhaseDone` the PR is marked ready for review and the coordinator
   tells the operator to check it.
5. **The operator approves and merges.** No teammate approval is
   requested unless the operator says so for that task.

## Flow

```
engineer round 1 done  -> push branch (app), open DRAFT PR
review-A verdict       -> post "Review #1 (round N): <verdict>" on PR
engineer revises       -> push, post engineer response as PR comment
review-B verdict       -> post "Review #2 (round N): <verdict>" on PR
PhaseDone              -> gh pr ready; coordinator: "PR #N ready for your approval"
operator               -> approves + merges on GitHub
PhaseEscalated         -> PR comment naming the cap hit; stays draft
```

## Design

### Who does the GitHub side: the driver, not the agents

`DriveRoleLoop` (`internal/fleet/roledrive.go`) already reacts to every
new round file. It mirrors them to GitHub deterministically. Agents
keep writing only their files; they never push, open, or comment.
This keeps `derivePhase` a pure function of files on disk (GitHub is
a mirror, never read back for phase), keeps reviewer B's fresh-eyes
isolation (B reads files, never the PR thread), and keeps prompt
drift out of the GitHub side.

Idempotence: per-task mirror state in `.spore/<slug>/github.json`
(PR number + url, last pushed sha, set of posted file names, ready
flag). Each tick posts only what is missing. A GitHub failure never
blocks the loop: log, retry next tick, notify the coordinator once
per distinct error.

### Mirror steps

- **Engineer response `engineer-round-N.json` appears:** push the task
  branch via the app (`--force-with-lease` only if the branch was
  rewritten). Round 1 with no PR yet: `gh pr create --draft`, base =
  the task's base (below), title = task title, body = spec summary +
  `Dispatched by @<operator_login>` + a note that reviews arrive as
  `Review #1` / `Review #2` comments. Assign `operator_login`. Round
  2+: post the response (`addressed`, `pushback`, `notes`,
  `followups`) as one PR comment headed `Engineer (round N)`.
- **Reviewer verdict `reviews/<A|B>/round-N.json` appears:** post one
  `COMMENT` review: `Review #1 (round N): request changes` /
  `approve`, the summary, then the comments as a list.
- **`PhaseDone`:** `gh pr ready`, then notify the coordinator with the
  PR url (extend the existing `notifyRoleLoop` message:
  `<slug>: done, PR #N ready for operator approval: <url>`).
- **`PhaseEscalated`:** one PR comment with the cap and reviewer; PR
  stays draft.

### PR base

New optional task frontmatter `base:` (stack parent branch). Default:
the remote's default branch (`git symbolic-ref
refs/remotes/origin/HEAD`). Engineer and reviewer role files keep
their "real parent" rule and name the `base:` field.

### App auth: `internal/ghapp`

- Mint an installation token: RS256 JWT from the app private key
  (stdlib `crypto/rsa`, no new dependency), `POST
  /app/installations/<id>/access_tokens`. Cache it host-side until
  ~5 min before expiry (0600).
- `spore gh token` prints a token; `spore gh exec -- <cmd...>` runs a
  command with `GH_TOKEN` set and a git credential helper injected via
  `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_n`/`GIT_CONFIG_VALUE_n`
  (username `x-access-token`), so `gh` and `git push` over https both
  act as the app. The driver uses the library directly.
- Remote must be https; an ssh remote is an error with a clear
  message (it would push as the operator's key).

### Host-side config, never in the consumer repo

`$XDG_STATE_HOME/spore/<project>/github.toml` (0600):

```toml
pr_flow = "draft"            # absent or "off" keeps today's behaviour
app_id = 123456
installation_id = 7890123
private_key = "~/.config/spore/<project>/github-app.pem"   # 0600
operator_login = "<github login>"
commit_name = "<operator name>"
commit_email = "<email linked to that login>"
```

`spore gh config [--set k=v]` prints/edits it. With `pr_flow` off,
behaviour is exactly today's (regression-tested).

### Commit identity

When `pr_flow = "draft"`, engineer panes get `GIT_AUTHOR_NAME`,
`GIT_AUTHOR_EMAIL`, `GIT_COMMITTER_NAME`, `GIT_COMMITTER_EMAIL` from
`commit_name` / `commit_email`. This overrides the repo's git config
for that pane only.

### Role and coordinator text

- `bootstrap/roles/engineer.md`, `reviewer.md`: the driver publishes
  your files to the PR; still never push, open, comment, or approve
  yourself. Reviewer: write comments the team can read (they are
  public on the PR).
- `bootstrap/coordinator/role.md`: on a `done, PR #N ready` notice,
  tell the operator in one line to approve and merge; do not request
  teammate review unless the operator asked for it on that task.

## Out of scope (v1)

- Line-anchored review comments (`Review.Comments` are free strings).
- Separate reviewer app / real `REQUEST_CHANGES` states.
- Reading PR comments from humans back into the loop.
- Signed commits (if a repo requires them, agent commits fail that
  rule; document, do not solve).

## Operator setup (document in `docs/github-app.md`)

1. Org owner: Settings -> Developer settings -> GitHub Apps -> New.
   Permissions: Contents RW, Pull requests RW, Metadata R. No webhook.
2. Install on the target repos; note app id and installation id.
3. Generate a private key per teammate; store at the configured path,
   mode 0600.
4. `spore gh config --set ...`, then `spore gh token` to verify.

## Verification

- Unit: JWT signing and token request against `httptest`; token cache
  expiry; credential-helper env shape; config parsing; mirror
  idempotence (each file posted once across repeated ticks) with a
  fake `gh`/git remote; `pr_flow` off is byte-identical behaviour.
- Live (needs the app; operator-bound until it exists): a scratch
  task on a throwaway repo goes draft PR -> Review #1 / #2 comments ->
  ready, authored by the app with operator commits.

## Consumer impact

New `spore gh` command, new host-side `github.toml`, role and
coordinator text changes, optional `base:` frontmatter. Off by
default; no consumer repo change.
