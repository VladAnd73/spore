# GitHub App draft-PR flow

With `pr_flow = "draft"`, the role loop runs on a visible draft PR. A
GitHub App pushes the task branch, opens the PR, and posts every
round; the operator gives the only approval and merges. Design and
locked decisions: `docs/todo/github-app-pr-flow.md`. For ad-hoc use
from a coordinator (detect, `spore gh exec`, App vs personal token),
see `spore recipes show github`.

## What happens

```
engineer round 1 done  -> push branch (app), open DRAFT PR, assign operator
review verdict         -> COMMENT review "Review #1 (round N): <verdict>" (A) / "Review #2" (B)
engineer round 2+      -> push, PR comment "Engineer (round N)"
PhaseDone              -> gh pr ready; coordinator: "<slug>: done, PR #N ready for operator approval: <url>"
PhaseEscalated         -> one PR comment naming the cap and reviewer; PR stays draft
```

`DriveRoleLoop` does all of it (`internal/fleet/rolegithub.go`). Agents
still only write their `.spore/<slug>/` files; GitHub is a mirror,
never read back for phase. The app never submits `APPROVE` or
`REQUEST_CHANGES`: it is the PR author, and the operator stays the
only real gate.

Mirror state lives in `.spore/<slug>/github.json` (PR number and url,
last pushed sha, posted file names, ready flag, last error). Each tick
posts only what is missing, in loop order. A GitHub failure never
blocks the loop: it logs to stderr, retries next tick, and tells the
coordinator once per distinct error (`<slug>: github mirror failed,
retrying each tick: ...`); a git/gh error counts as the same error
when only its stderr differs. If the loop reaches done while the
mirror is failing, the coordinator also gets the plain `<slug>: done`
once. Every git/gh call and the token mint is capped at 60 seconds,
with git and gh prompts disabled, so a hung connection delays a tick
by about a minute at most. Deleting `github.json` re-posts every
comment; an open PR for the branch is adopted, not duplicated.

Every gh call runs with `GH_REPO` set from `origin`'s URL, so gh and
`git push` always target the same repo; without it gh prefers an
`upstream` remote in a fork-style clone.

The PR body is the first prose paragraph of `.spore/<slug>/spec.md`
(the task brief, skipping headings and the `**Status**` line) plus a
fixed footer; a paragraph naming an absolute path (`/...` or `~/...`)
is replaced by the task title. Everyone with repo access reads it, so
open the brief with a plain summary: no host paths, no
coordinator-only notes. The rest of the brief stays off GitHub.

The PR base is the task's `base:` frontmatter (a stack parent branch),
else the remote default branch (`git symbolic-ref
refs/remotes/origin/HEAD`; run `git remote set-head origin --auto` if
it is missing).

A branch whose last pushed commit is no longer an ancestor of HEAD
(rebased, amended) goes up with `--force-with-lease` pinned to that
commit; otherwise pushes are fast-forward only.

## One-time setup

Org-owner path: create the app once per org. Teammates joining an
existing app skip to [Teammate onboarding](#teammate-onboarding).

1. Org owner: Settings -> Developer settings -> GitHub Apps -> New.
   Permissions: Contents read/write, Pull requests read/write,
   Metadata read. No webhook. One app per org, shared by every
   teammate.
2. Install it on the target repos. Note the app id (app settings
   page) and the installation id (the number at the end of the
   installation's settings URL).
3. Generate a private key per teammate (see
   [Teammate onboarding](#teammate-onboarding)). Store it outside the
   repo, mode 0600, e.g. `~/.config/spore/<project>/github-app.pem`.
4. Point the project's `origin` at https (an ssh remote is refused:
   it would push as your own key):
   `git remote set-url origin https://github.com/<owner>/<repo>.git`.
5. Configure, then verify a token mints:

   ```
   spore gh config --set app_id=123456 --set installation_id=7890123 \
     --set private_key=~/.config/spore/<project>/github-app.pem \
     --set operator_login=<github login> \
     --set "commit_name=<your name>" --set commit_email=<email linked to that login> \
     --set pr_flow=draft
   spore gh token
   ```

## Teammate onboarding

Per-teammate path: the org already has its app (today
`marketer-spore`). You share that app and bring your own private key.
Below, `<pem>` is `~/.config/spore/<project>/github-app.pem`.

1. Generate a key: app settings -> "Private keys" -> "Generate a
   private key". The browser downloads a `.pem`. This is a private
   key, NOT a client secret; client secrets are for OAuth user login,
   which spore does not use.
2. On the spore host, create the secrets dir (it may not exist):
   `install -d -m 0700 ~/.config/spore/<project>`.
3. Get the `.pem` onto the host. Never paste the key into an agent
   chat or run it through the `!` prefix.
   - Paste (works over plain SSH, recommended). Copy the key on your
     laptop: `pbcopy < key.pem` (Mac), `xclip -selection clipboard <
     key.pem` or `wl-copy < key.pem` (Linux). In a fresh tmux window on
     the host run `umask 077; cat > <pem>`, paste the full PEM (BEGIN
     through END line), press Enter, then Ctrl-D, and close the window.
     Clear the clipboard after.
   - scp from the laptop:
     `scp <file>.pem <user>@<host>:~/.config/spore/<project>/github-app.pem`.
     Replace every placeholder fully: zsh reads a leftover `<` or `>`
     as a redirection. If your ssh config auto-attaches tmux
     (`RemoteCommand` / `RequestTTY`), non-interactive ssh fails with
     "open terminal failed: not a terminal"; use the paste route.
4. Verify without printing the key:
   `chmod 600 <pem>; stat -c %a <pem>; openssl pkey -in <pem> -noout && echo OK`.
   Expect `600` and `OK`.
5. Find the ids.
   - App id: the app settings page shows an "App ID" (plain integer,
     e.g. `4071579`; this is `app_id`) and a "Client ID" (starts with
     `Iv`). spore does not use the Client ID; ignore it.
   - Installation id: an installation is the app added to an org or
     account, with its own separate id. Org Settings -> GitHub Apps ->
     Configure on the app opens
     `github.com/organizations/<org>/settings/installations/<id>`;
     the trailing number is `installation_id`. On that page, confirm
     the app is installed on the target repos.
6. Continue with [One-time setup](#one-time-setup) steps 4-5: https
   `origin`, `spore gh config --set ...` with your own
   `private_key`, `operator_login`, `commit_name`, `commit_email`, then
   `spore gh token`.

## Config

`$XDG_STATE_HOME/spore/<project>/github.toml` (0600), host-side, never
in the consumer repo:

| key | meaning |
|---|---|
| `pr_flow` | `draft` turns the flow on; absent or `off` keeps the files-only loop exactly as before |
| `app_id`, `installation_id` | integers from step 2 |
| `private_key` | path to the PEM; a leading `~/` expands |
| `operator_login` | your GitHub login: PR assignee and the only approver |
| `commit_name`, `commit_email` | engineer commit identity (see below) |
| `api_url` | optional REST root for the token mint; default `https://api.github.com`. Untested against GitHub Enterprise Server: `GH_ENTERPRISE_TOKEN` is set for gh, but no GHES run has verified the flow |

`spore gh config` prints the file; `--set key=` deletes a key.

## Commit identity

With the flow on, engineer panes start with `GIT_AUTHOR_NAME`,
`GIT_AUTHOR_EMAIL`, `GIT_COMMITTER_NAME`, `GIT_COMMITTER_EMAIL` from
`commit_name` / `commit_email`, overriding the repo's git config for
that pane only. The app is the pusher and PR author; you are the
commit author, so GitHub lets you approve. Panes spawned before the
flow was turned on keep their old identity until respawned.

## `spore gh`

- `spore gh token` prints an installation token. Tokens are cached in
  `$XDG_STATE_HOME/spore/<project>/github-token.json` (0600) until 5
  minutes before expiry; changing `app_id` or `installation_id`
  invalidates the cache.
- `spore gh exec -- <cmd...>` runs a command as the app: `GH_TOKEN`
  and `GH_ENTERPRISE_TOKEN` are set, and a git credential helper
  (username `x-access-token`, password from `$GH_TOKEN`) is injected
  through `GIT_CONFIG_COUNT` / `GIT_CONFIG_KEY_n` /
  `GIT_CONFIG_VALUE_n`, after an empty `credential.helper` that
  clears your own helpers. Both `gh` and `git push` over https then
  act as the app.

## Limits (v1)

- Review comments are not line-anchored.
- Human PR comments are not read back into the loop.
- Signed commits: if a repo requires them, agent commits fail that
  rule. Not solved.
- A task already mid-loop when the flow is turned on gets its
  existing rounds posted in order on the next tick.
